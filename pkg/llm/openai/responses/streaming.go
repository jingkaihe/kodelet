package responses

import (
	"context"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
)

// processStream processes the streaming response from the Responses API.
// It handles text deltas, tool calls, and response completion.
// Returns whether tools were used and any error.
func (t *Thread) processStream(
	ctx context.Context,
	stream *ssestream.Stream[responses.ResponseStreamEventUnion],
	handler llmtypes.MessageHandler,
	model string,
	opt llmtypes.MessageOpt,
) (bool, error) {
	telemetry.AddEvent(ctx, "stream_processing_started")
	log := logger.G(ctx)
	log.Debug("starting stream processing")
	apiStartTime := time.Now()

	// Track current state
	var currentText strings.Builder
	var toolsUsed bool
	var contentBlockEnded bool // Track if we've signaled end of content block
	var thinkingStarted bool   // Track if thinking block has started

	// Track pending tool calls
	pendingToolCalls := make(map[string]*toolCallState)

	// Track completed response
	var finalResponse *responses.Response

	// Check if handler supports streaming
	streamHandler, isStreaming := handler.(llmtypes.StreamingMessageHandler)

	// Process stream events
	log.Debug("waiting for stream events")
	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.created":
			// Response created, store the response ID
			telemetry.AddEvent(ctx, "response_created",
				attribute.String("response_id", event.Response.ID),
			)
			t.lastResponseID = event.Response.ID

		case "response.output_text.delta":
			// Text content delta
			if event.Delta != "" {
				currentText.WriteString(event.Delta)
				if isStreaming {
					streamHandler.HandleTextDelta(event.Delta)
				}
			}

		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			// Reasoning content delta - stored in thread to persist across API calls
			if event.Delta != "" {
				t.pendingReasoning.WriteString(event.Delta)
				if isStreaming {
					// Signal start of thinking block before first delta
					if !thinkingStarted {
						streamHandler.HandleThinkingStart()
						thinkingStarted = true
					}
					streamHandler.HandleThinkingDelta(event.Delta)
				}
			}

		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			// Reasoning content complete - end thinking block for streaming handlers
			if isStreaming && thinkingStarted {
				streamHandler.HandleThinkingBlockEnd()
				thinkingStarted = false
			}

		case "response.function_call_arguments.delta":
			// Function call arguments delta
			callID := event.ItemID
			if pendingToolCalls[callID] == nil {
				pendingToolCalls[callID] = &toolCallState{}
			}
			pendingToolCalls[callID].arguments.WriteString(event.Delta)

		case "response.function_call_arguments.done":
			// Function call arguments complete
			callID := event.ItemID
			if pendingToolCalls[callID] == nil {
				pendingToolCalls[callID] = &toolCallState{}
			}
			pendingToolCalls[callID].name = event.Name
			pendingToolCalls[callID].callID = callID
			pendingToolCalls[callID].arguments.Reset()
			pendingToolCalls[callID].arguments.WriteString(event.Arguments)

		case "response.output_item.added":
			// New output item added - check if it's a function call
			if item := event.Item; item.Type == "function_call" {
				toolsUsed = true
			}

		case "response.output_item.done":
			// Output item complete
			item := event.Item

			switch item.Type {
			case "function_call":
				// Complete function call
				toolsUsed = true

				// Signal end of thinking block before first tool use (adds line break)
				if isStreaming && thinkingStarted {
					streamHandler.HandleThinkingBlockEnd()
					thinkingStarted = false
				}

				// Signal end of text content block before first tool use (adds line break)
				if isStreaming && !contentBlockEnded && currentText.Len() > 0 {
					streamHandler.HandleContentBlockEnd()
					contentBlockEnded = true
				}

				funcCall := item.AsFunctionCall()
				handler.HandleToolUse(funcCall.CallID, funcCall.Name, funcCall.Arguments)

				// Flush pending reasoning to storedItems before adding function call
				if t.pendingReasoning.Len() > 0 {
					t.storedItems = append(t.storedItems, StoredInputItem{
						Type:    "reasoning",
						Role:    "assistant",
						Content: t.pendingReasoning.String(),
					})
					t.pendingReasoning.Reset()
				}

				// Add to inputItems (for API) and storedItems (for persistence)
				t.inputItems = append(t.inputItems, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    funcCall.CallID,
						Name:      funcCall.Name,
						Arguments: funcCall.Arguments,
					},
				})
				t.storedItems = append(t.storedItems, StoredInputItem{
					Type:      "function_call",
					CallID:    funcCall.CallID,
					Name:      funcCall.Name,
					Arguments: funcCall.Arguments,
				})

				// Execute the tool
				result := t.executeToolCall(ctx, funcCall.CallID, funcCall.Name, funcCall.Arguments, handler)

				// Get the string representation for API response
				resultStr := result.AssistantFacing()

				// Create tool result item
				toolResultItem := responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: funcCall.CallID,
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: param.NewOpt(resultStr),
						},
					},
				}

				// Add the tool result to inputItems, storedItems, and pendingItems
				t.inputItems = append(t.inputItems, toolResultItem)
				t.storedItems = append(t.storedItems, StoredInputItem{
					Type:   "function_call_output",
					CallID: funcCall.CallID,
					Output: resultStr,
				})
				t.pendingItems = append(t.pendingItems, toolResultItem)

				handler.HandleToolResult(funcCall.CallID, funcCall.Name, result)

			case "message":
				// Complete message - add to input items if assistant
				msg := item.AsMessage()
				if msg.Role == "assistant" {
					// Extract text content
					var textContent string
					for _, content := range msg.Content {
						if content.Type == "output_text" {
							textPart := content.AsOutputText()
							textContent += textPart.Text
						}
					}
					if textContent != "" {
						// Flush pending reasoning to storedItems before adding message
						if t.pendingReasoning.Len() > 0 {
							t.storedItems = append(t.storedItems, StoredInputItem{
								Type:    "reasoning",
								Role:    "assistant",
								Content: t.pendingReasoning.String(),
							})
							t.pendingReasoning.Reset()
						}

						t.inputItems = append(t.inputItems, responses.ResponseInputItemUnionParam{
							OfMessage: &responses.EasyInputMessageParam{
								Role:    responses.EasyInputMessageRoleAssistant,
								Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(textContent)},
							},
						})
						t.storedItems = append(t.storedItems, StoredInputItem{
							Type:    "message",
							Role:    "assistant",
							Content: textContent,
						})
					}
				}
			}

		case "response.completed":
			// Response completed
			finalResponse = &event.Response
			telemetry.AddEvent(ctx, "response_completed",
				attribute.String("response_id", event.Response.ID),
				attribute.String("status", string(event.Response.Status)),
			)

			// Signal end of thinking block for streaming handlers (if not already done)
			if isStreaming && thinkingStarted {
				streamHandler.HandleThinkingBlockEnd()
				thinkingStarted = false
			}

			// Signal end of text content block for streaming handlers (if not already done)
			if isStreaming && !contentBlockEnded && currentText.Len() > 0 {
				streamHandler.HandleContentBlockEnd()
				contentBlockEnded = true
			}

			// For non-streaming handlers, send the complete text
			if !isStreaming && currentText.Len() > 0 {
				handler.HandleText(currentText.String())
			}

		case "response.failed", "error":
			// Handle errors
			errMsg := event.Message
			if errMsg == "" {
				errMsg = "Unknown error"
			}
			return toolsUsed, errors.New(errMsg)

		case "response.in_progress", "response.queued":
			// Status updates - no action needed
			continue
		}
	}

	// Check for stream errors
	if err := stream.Err(); err != nil {
		// Log detailed error information for debugging API failures
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			log.WithField("status_code", apiErr.StatusCode).
				WithField("error_code", apiErr.Code).
				WithField("error_message", apiErr.Message).
				WithField("error_type", apiErr.Type).
				WithField("error_param", apiErr.Param).
				WithField("raw_json", apiErr.RawJSON()).
				Debug("API error details")
		}
		return toolsUsed, errors.Wrap(err, "stream error")
	}

	// Update usage from final response
	if finalResponse != nil {
		t.updateUsage(finalResponse.Usage)

		if !t.Config.IsSubAgent && !opt.DisableUsageLog {
			usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, int(finalResponse.Usage.OutputTokens))
		}
	}

	return toolsUsed, nil
}

// toolCallState tracks the state of a pending tool call during streaming.
type toolCallState struct {
	callID    string
	name      string
	arguments strings.Builder
}

// executeToolCall executes a tool call and returns the result.
func (t *Thread) executeToolCall(
	ctx context.Context,
	callID string,
	name string,
	arguments string,
	_ llmtypes.MessageHandler,
) tooltypes.ToolResult {
	toolExecution := base.ExecuteTool(
		ctx,
		t.HookTrigger,
		t,
		t.State,
		t.GetRecipeHooks(),
		t.RendererRegistry,
		name,
		arguments,
		callID,
	)

	t.SetStructuredToolResult(callID, toolExecution.StructuredResult)
	return toolExecution.Result
}

// updateUsage updates the thread's usage statistics from a response.
func (t *Thread) updateUsage(usage responses.ResponseUsage) {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	t.Usage.InputTokens += int(usage.InputTokens)
	t.Usage.OutputTokens += int(usage.OutputTokens)

	// Update cached tokens if available
	if usage.InputTokensDetails.CachedTokens > 0 {
		t.Usage.CacheReadInputTokens = int(usage.InputTokensDetails.CachedTokens)
	}

	// Calculate costs based on model pricing
	pricing := t.getPricing(t.Config.Model)

	// Calculate individual costs
	inputTokens := int(usage.InputTokens)
	outputTokens := int(usage.OutputTokens)
	cachedTokens := int(usage.InputTokensDetails.CachedTokens)

	// Non-cached input tokens
	nonCachedInput := inputTokens - cachedTokens
	if nonCachedInput > 0 {
		t.Usage.InputCost += float64(nonCachedInput) * pricing.Input
	}

	// Cached input tokens (typically cheaper)
	if cachedTokens > 0 {
		t.Usage.CacheReadCost += float64(cachedTokens) * pricing.CachedInput
	}

	// Output tokens
	t.Usage.OutputCost += float64(outputTokens) * pricing.Output

	// Update context window
	t.Usage.CurrentContextWindow = inputTokens + outputTokens
	t.Usage.MaxContextWindow = pricing.ContextWindow
}
