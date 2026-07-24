package responses

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
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
// Returns stream processing outcome and any error.
func (t *Thread) processStream(
	ctx context.Context,
	stream *ssestream.Stream[responses.ResponseStreamEventUnion],
	handler llmtypes.MessageHandler,
	model string,
	opt llmtypes.MessageOpt,
) (processStreamResult, error) {
	telemetry.AddEvent(ctx, "stream_processing_started")
	log := logger.G(ctx)
	log.Debug("starting stream processing")
	apiStartTime := time.Now()

	// Track current state
	var currentText strings.Builder
	var toolsUsed bool
	var contentBlockEnded bool // Track if we've signaled end of content block
	var thinkingStarted bool   // Track if thinking block has started
	var responseCompleted bool
	var responseIncompleteReason string
	var responseID string
	var serverKnownItems []responses.ResponseInputItemUnionParam

	// Track pending tool calls
	pendingToolCalls := make(map[string]*toolCallState)
	var functionCalls []functionCallInvocation
	var streamProcessingErr error

	// Track completed response
	var finalResponse *responses.Response

	// Check if handler supports streaming
	streamHandler, isStreaming := handler.(llmtypes.StreamingMessageHandler)
	var contentFinalized bool

	flushPendingReasoning := func() {
		if strings.TrimSpace(t.pendingReasoning.String()) == "" {
			t.pendingReasoning.Reset()
			return
		}

		t.storedItems = append(t.storedItems, StoredInputItem{
			Type:    "reasoning",
			Role:    "assistant",
			Content: t.pendingReasoning.String(),
		})
		t.pendingReasoning.Reset()
	}

	result := func() processStreamResult {
		return processStreamResult{
			toolsUsed:         toolsUsed,
			responseCompleted: responseCompleted,
			responseID:        responseID,
			serverKnownItems:  cloneResponsesInputItems(serverKnownItems),
		}
	}

	finalizeContentBlocks := func() {
		if contentFinalized {
			return
		}

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

		contentFinalized = true
	}

	// Process stream events
	log.Debug("waiting for stream events")
streamLoop:
	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.created":
			// Response created, store the response ID
			telemetry.AddEvent(ctx, "response_created",
				attribute.String("response_id", event.Response.ID),
			)

		case "response.output_text.delta":
			// Text content delta
			if event.Delta != "" {
				if isStreaming && thinkingStarted {
					streamHandler.HandleThinkingBlockEnd()
					thinkingStarted = false
				}
				if contentBlockEnded {
					currentText.Reset()
					contentBlockEnded = false
				}
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
			flushPendingReasoning()

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
			case "web_search_call":
				if isStreaming && thinkingStarted {
					streamHandler.HandleThinkingBlockEnd()
					thinkingStarted = false
				}

				if isStreaming && !contentBlockEnded && currentText.Len() > 0 {
					streamHandler.HandleContentBlockEnd()
					contentBlockEnded = true
				}

				webSearch := item.AsWebSearchCall()
				callID := webSearch.ID
				if callID == "" {
					callID = item.ID
				}

				toolInput := webSearchInputJSON(webSearch.Action, string(webSearch.Status))
				handler.HandleToolUse(callID, openAISearchToolName, toolInput)

				rawItem := []byte(webSearch.RawJSON())
				if len(rawItem) == 0 {
					if marshaled, err := json.Marshal(webSearch); err == nil {
						rawItem = marshaled
					}
				}

				flushPendingReasoning()

				storedItem := StoredInputItem{
					Type:    "web_search_call",
					CallID:  callID,
					Status:  string(webSearch.Status),
					Action:  webSearch.Action.Type,
					RawItem: rawItem,
				}
				details := webSearchDetailsFromAction(webSearch.Action)
				switch webSearch.Action.Type {
				case "open_page":
					storedItem.Content = details.url
				case "find_in_page":
					storedItem.Content = details.url
					storedItem.Arguments = details.pattern
				default:
					storedItem.Content = strings.Join(details.queries, ", ")
				}
				t.storedItems = append(t.storedItems, storedItem)
				if inputItems := fromStoredItems([]StoredInputItem{storedItem}); len(inputItems) > 0 {
					t.inputItems = append(t.inputItems, inputItems[0])
					serverKnownItems = append(serverKnownItems, inputItems[0])
				}

				result := webSearchStructuredResult(callID, webSearch)
				if meta, ok := result.Metadata.(tooltypes.OpenAIWebSearchMetadata); ok {
					extendWebSearchMetadataFromRawItem(&meta, rawItem)
					result.Metadata = meta
				}
				t.SetStructuredToolResult(callID, result)
				handler.HandleToolResult(callID, openAISearchToolName, structuredToolResultToToolResult(result))

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

				// Flush pending reasoning before collecting the function call so replay
				// preserves the reasoning-before-tools boundary.
				flushPendingReasoning()

				functionCalls = append(functionCalls, functionCallInvocation{
					outputIndex: event.OutputIndex,
					callID:      funcCall.CallID,
					name:        funcCall.Name,
					arguments:   funcCall.Arguments,
				})

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
						if isStreaming && !contentBlockEnded && currentText.Len() > 0 {
							streamHandler.HandleContentBlockEnd()
							contentBlockEnded = true
						} else if !isStreaming {
							handler.HandleText(textContent)
						}

						// Flush pending reasoning to storedItems before adding message
						flushPendingReasoning()

						rawItem := json.RawMessage(item.RawJSON())
						if len(rawItem) == 0 {
							if marshaled, err := json.Marshal(item); err == nil {
								rawItem = marshaled
							}
						}
						storedItem := StoredInputItem{
							Type:    "message",
							Role:    "assistant",
							Content: textContent,
							RawItem: rawItem,
						}
						t.storedItems = append(t.storedItems, storedItem)
						if inputItems := fromStoredItems([]StoredInputItem{storedItem}); len(inputItems) > 0 {
							t.inputItems = append(t.inputItems, inputItems[0])
							serverKnownItems = append(serverKnownItems, inputItems[0])
						}

						currentText.Reset()
						contentBlockEnded = false
					}
				}
			}

		case "response.completed":
			// Response completed
			finalResponse = &event.Response
			responseCompleted = true
			responseID = event.Response.ID
			telemetry.AddEvent(ctx, "response_completed",
				attribute.String("response_id", event.Response.ID),
				attribute.String("status", string(event.Response.Status)),
			)

			finalizeContentBlocks()
			flushPendingReasoning()

		case "response.incomplete":
			// Response ended but is incomplete (e.g. max_output_tokens/content_filter)
			finalResponse = &event.Response
			responseIncompleteReason = event.Response.IncompleteDetails.Reason
			telemetry.AddEvent(ctx, "response_incomplete",
				attribute.String("response_id", event.Response.ID),
				attribute.String("reason", responseIncompleteReason),
			)

			finalizeContentBlocks()

		case "response.failed", "error":
			// Handle errors
			errMsg := responseStreamEventErrorMessage(event)
			if !isRetryableResponseStreamEventError(event) {
				streamProcessingErr = retry.Unrecoverable(errors.New(errMsg))
			} else {
				streamProcessingErr = errors.New(errMsg)
			}
			break streamLoop

		case "response.in_progress", "response.queued":
			// Status updates - no action needed
			continue
		}
	}

	// Check for stream errors
	if err := stream.Err(); streamProcessingErr == nil && err != nil {
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
		streamProcessingErr = errors.Wrap(err, "stream error")
	}
	if err := ctx.Err(); err != nil {
		streamProcessingErr = err
	}
	if errors.Is(streamProcessingErr, context.Canceled) || errors.Is(streamProcessingErr, context.DeadlineExceeded) {
		return result(), streamProcessingErr
	}

	sort.SliceStable(functionCalls, func(i, j int) bool {
		return functionCalls[i].outputIndex < functionCalls[j].outputIndex
	})
	for _, functionCall := range functionCalls {
		handler.HandleToolUse(functionCall.callID, functionCall.name, functionCall.arguments)

		// The function call itself is already present in the server's response
		// state; only its locally produced output is incremental input.
		functionCallItem := responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID:    functionCall.callID,
				Name:      functionCall.name,
				Arguments: functionCall.arguments,
			},
		}
		t.inputItems = append(t.inputItems, functionCallItem)
		serverKnownItems = append(serverKnownItems, functionCallItem)
		t.storedItems = append(t.storedItems, StoredInputItem{
			Type:      "function_call",
			CallID:    functionCall.callID,
			Name:      functionCall.name,
			Arguments: functionCall.arguments,
		})
	}

	toolExecutions := t.executeFunctionCallsParallel(ctx, functionCalls, handler)
	for i, toolExecution := range toolExecutions {
		functionCall := functionCalls[i]
		toolResult := toolExecution.Result
		t.SetStructuredToolResult(functionCall.callID, toolExecution.StructuredResult)

		outputUnion, storedOutput, rawOutput := buildStoredFunctionCallOutput(toolResult)
		t.inputItems = append(t.inputItems, responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: functionCall.callID,
				Output: outputUnion,
			},
		})
		t.storedItems = append(t.storedItems, StoredInputItem{
			Type:      "function_call_output",
			CallID:    functionCall.callID,
			Output:    storedOutput,
			RawOutput: rawOutput,
		})
	}
	if err := ctx.Err(); err != nil {
		return result(), err
	}

	if streamProcessingErr != nil {
		return result(), streamProcessingErr
	}

	// Update usage from final response
	if finalResponse != nil {
		t.updateUsage(finalResponse.Usage, model, llmtypes.OpenAIServiceTier(finalResponse.ServiceTier))
		if usageHandler, ok := handler.(llmtypes.UsageMessageHandler); ok {
			usageHandler.HandleUsage(t.GetUsage())
		}

		if !opt.DisableUsageLog {
			usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, int(finalResponse.Usage.OutputTokens))
		}
	}

	if responseIncompleteReason != "" {
		return result(), errors.Errorf("response incomplete: %s", responseIncompleteReason)
	}

	if !responseCompleted {
		return result(), errors.New("response stream ended before response.completed event")
	}

	return result(), nil
}

type processStreamResult struct {
	toolsUsed         bool
	responseCompleted bool
	responseID        string
	serverKnownItems  []responses.ResponseInputItemUnionParam
}

type functionCallInvocation struct {
	outputIndex int64
	callID      string
	name        string
	arguments   string
}

func responseStreamEventErrorMessage(event responses.ResponseStreamEventUnion) string {
	if strings.TrimSpace(event.Message) != "" {
		return event.Message
	}
	if strings.TrimSpace(event.Response.Error.Message) != "" {
		return event.Response.Error.Message
	}
	if event.Response.Error.Code != "" {
		return string(event.Response.Error.Code)
	}
	return "Unknown error"
}

func isRetryableResponseStreamEventError(event responses.ResponseStreamEventUnion) bool {
	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		event.Code,
		string(event.Response.Error.Code),
	)))

	if code == "invalid_prompt" {
		return false
	}
	if code == "context_length_exceeded" {
		return false
	}
	if code == "insufficient_quota" || code == "usage_not_included" {
		return false
	}
	if code == "cyber_policy" {
		return false
	}
	if code == "server_is_overloaded" || code == "slow_down" {
		return false
	}

	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// toolCallState tracks the state of a pending tool call during streaming.
type toolCallState struct {
	callID    string
	name      string
	arguments strings.Builder
}

func buildStoredFunctionCallOutput(result tooltypes.ToolResult) (
	responses.ResponseInputItemFunctionCallOutputOutputUnionParam,
	string,
	json.RawMessage,
) {
	resultStr := result.AssistantFacing()
	outputUnion := responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
		OfString: param.NewOpt(resultStr),
	}
	if rich, ok := result.(tooltypes.MultiModalToolResult); ok {
		if outputItems := responseFunctionCallOutputItems(rich.ContentParts()); len(outputItems) > 0 {
			outputUnion = responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: outputItems,
			}
			if rawOutput, err := json.Marshal(outputItems); err == nil {
				return outputUnion, resultStr, rawOutput
			}
		}
	}

	return outputUnion, resultStr, nil
}

type structuredResultToolResult struct {
	result tooltypes.StructuredToolResult
}

func (r structuredResultToolResult) AssistantFacing() string {
	registry := renderers.NewRendererRegistry()
	return tooltypes.StringifyToolResult(registry.Render(r.result), r.result.Error)
}

func (r structuredResultToolResult) IsError() bool {
	return !r.result.Success
}

func (r structuredResultToolResult) GetError() string {
	return r.result.Error
}

func (r structuredResultToolResult) GetResult() string {
	registry := renderers.NewRendererRegistry()
	return registry.Render(r.result)
}

func (r structuredResultToolResult) StructuredData() tooltypes.StructuredToolResult {
	return r.result
}

func structuredToolResultToToolResult(result tooltypes.StructuredToolResult) tooltypes.ToolResult {
	return structuredResultToolResult{result: result}
}

func responseFunctionCallOutputItems(parts []tooltypes.ToolResultContentPart) responses.ResponseFunctionCallOutputItemListParam {
	result := make(responses.ResponseFunctionCallOutputItemListParam, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case tooltypes.ToolResultContentPartTypeText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			result = append(result, responses.ResponseFunctionCallOutputItemParamOfInputText(part.Text))
		case tooltypes.ToolResultContentPartTypeImage:
			if strings.TrimSpace(part.ImageURL) == "" {
				continue
			}
			detail := responses.ResponseInputImageContentDetailAuto
			if strings.TrimSpace(part.Detail) == "original" {
				detail = responses.ResponseInputImageContentDetailOriginal
			}
			result = append(result, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputImage: &responses.ResponseInputImageContentParam{
					Detail:   detail,
					ImageURL: param.NewOpt(part.ImageURL),
				},
			})
		}
	}
	return result
}

// executeFunctionCallsParallel executes a response's function calls concurrently,
// streams results as they complete, and returns them in response order.
func (t *Thread) executeFunctionCallsParallel(
	ctx context.Context,
	functionCalls []functionCallInvocation,
	handler llmtypes.MessageHandler,
) []base.ToolExecution {
	toolExecutions := make([]base.ToolExecution, len(functionCalls))
	type completedToolExecution struct {
		index     int
		execution base.ToolExecution
	}
	completed := make(chan completedToolExecution, len(functionCalls))

	var wg sync.WaitGroup
	wg.Add(len(functionCalls))

	for i, functionCall := range functionCalls {
		go func() {
			defer wg.Done()

			if err := ctx.Err(); err != nil {
				toolResult := tooltypes.BaseToolResult{Error: err.Error()}
				structuredResult := toolResult.StructuredData()
				structuredResult.ToolName = functionCall.name
				completed <- completedToolExecution{
					index: i,
					execution: base.ToolExecution{
						Input:            functionCall.arguments,
						Result:           toolResult,
						StructuredResult: structuredResult,
						RenderedOutput:   t.RendererRegistry.Render(structuredResult),
					},
				}
				return
			}

			completed <- completedToolExecution{
				index: i,
				execution: base.ExecuteToolWithHandler(
					ctx,
					t,
					t.State,
					t.RendererRegistry,
					functionCall.name,
					functionCall.arguments,
					functionCall.callID,
					handler,
				),
			}
		}()
	}

	go func() {
		wg.Wait()
		close(completed)
	}()

	for result := range completed {
		toolExecutions[result.index] = result.execution
		functionCall := functionCalls[result.index]
		handler.HandleToolResult(functionCall.callID, functionCall.name, result.execution.Result)
	}

	return toolExecutions
}

// updateUsage updates the thread's usage statistics from a response.
func (t *Thread) updateUsage(usage responses.ResponseUsage, model string, serviceTier llmtypes.OpenAIServiceTier) {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	inputTokens := int(usage.InputTokens)
	outputTokens := int(usage.OutputTokens)
	cacheWriteTokens := int(usage.InputTokensDetails.CacheWriteTokens)

	// Cache-write tokens are included in the response's input token count, but
	// Usage.TotalTokens treats cache creation as a separate category.
	t.Usage.InputTokens += max(inputTokens-cacheWriteTokens, 0)
	t.Usage.OutputTokens += outputTokens

	// Update cached tokens if available
	if usage.InputTokensDetails.CachedTokens > 0 {
		t.Usage.CacheReadInputTokens += int(usage.InputTokensDetails.CachedTokens)
	}
	if cacheWriteTokens > 0 {
		t.Usage.CacheCreationInputTokens += cacheWriteTokens
	}

	// Calculate costs based on model pricing
	pricing := t.getPricingForServiceTier(model, serviceTier)

	// Calculate individual costs
	cachedTokens := int(usage.InputTokensDetails.CachedTokens)
	pricing = pricing.ForPromptTokens(inputTokens)

	// Non-cached input tokens
	nonCachedInput := inputTokens - cachedTokens - cacheWriteTokens
	if nonCachedInput > 0 {
		t.Usage.InputCost += float64(nonCachedInput) * pricing.Input
	}

	// Cached input tokens (typically cheaper)
	if cachedTokens > 0 {
		t.Usage.CacheReadCost += float64(cachedTokens) * pricing.CachedInput
	}

	// Cache write tokens are billed separately from regular input when pricing
	// provides a cache-write rate.
	if cacheWriteTokens > 0 {
		t.Usage.CacheCreationCost += float64(cacheWriteTokens) * pricing.CacheWriteInput
	}

	// Output tokens
	t.Usage.OutputCost += float64(outputTokens) * pricing.Output

	// Update context window
	t.Usage.CurrentContextWindow = inputTokens + outputTokens
	t.Usage.MaxContextWindow = pricing.ContextWindow
}
