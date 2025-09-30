package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	openai_v2 "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/responses"
	"github.com/openai/openai-go/v2/shared"
	"github.com/pkg/errors"
)

// ConversationItem represents a single item in the Response API conversation
// This can be either an input item (user message, tool result) or output item (assistant message, tool call, reasoning)
type ConversationItem struct {
	Type      string          `json:"type"`      // "input" or "output"
	Item      json.RawMessage `json:"item"`      // The actual item (serialized to preserve structure)
	Timestamp time.Time       `json:"timestamp"` // When this item was added
}

// needsResponsesAPI checks if a model requires the Response API
func needsResponsesAPI(model string) bool {
	// Models that only support Response API
	responsesOnlyModels := []string{
		"gpt-5-codex",
		// Add other Response API-only models as they're released
	}
	for _, m := range responsesOnlyModels {
		if model == m {
			return true
		}
	}
	return false
}

// sendMessageResponseAPI handles message sending using the Response API
func (t *OpenAIThread) sendMessageResponseAPI(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (string, error) {
	logger.G(ctx).Debug("using Response API for message sending")

	// Build the request
	params := t.buildResponseRequest(ctx, message, opt)

	// Use streaming or non-streaming based on configuration
	// For now, always use streaming for better UX
	resp, err := t.sendMessageResponseAPIStreaming(ctx, params, handler, opt)
	if err != nil {
		return "", errors.Wrap(err, "error sending message via Response API")
	}

	// Process the response and extract output
	output, toolsUsed, err := t.processResponseOutput(ctx, resp, handler, opt)
	if err != nil {
		return "", errors.Wrap(err, "error processing response output")
	}

	// If tools were used, recursively continue the conversation
	if toolsUsed {
		// Update previousResponseID for next request
		t.previousResponseID = resp.ID

		// Continue with next turn
		return t.sendMessageResponseAPI(ctx, "", handler, opt)
	}

	// Update previousResponseID for conversation continuity
	t.previousResponseID = resp.ID

	return output, nil
}

// buildResponseRequest constructs a Response API request from current state
func (t *OpenAIThread) buildResponseRequest(
	ctx context.Context,
	message string,
	opt llmtypes.MessageOpt,
) responses.ResponseNewParams {
	// Build input items for this request
	inputItems := t.buildInputItems(message, opt.Images)

	// Append input items to conversation history
	for _, item := range inputItems {
		itemJSON, _ := json.Marshal(item)
		t.conversationItems = append(t.conversationItems, ConversationItem{
			Type:      "input",
			Item:      itemJSON,
			Timestamp: time.Now(),
		})
	}

	// Determine which model to use
	model := t.config.Model
	maxTokens := t.config.MaxTokens
	if opt.UseWeakModel && t.config.WeakModel != "" {
		model = t.config.WeakModel
		if t.config.WeakModelMaxTokens > 0 {
			maxTokens = t.config.WeakModelMaxTokens
		}
	}

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		},
		Model:           openai_v2.ChatModel(model),
		MaxOutputTokens: openai_v2.Int(int64(maxTokens)),
	}

	// Add system instructions
	if systemPrompt := t.getSystemPrompt(ctx); systemPrompt != "" {
		params.Instructions = openai_v2.String(systemPrompt)
	}

	// Add conversation continuity if we have a previous response
	if t.previousResponseID != "" {
		params.PreviousResponseID = openai_v2.String(t.previousResponseID)
		params.Store = openai_v2.Bool(true) // Required for chaining
	}

	// Configure storage if requested
	if t.config.OpenAI != nil && t.config.OpenAI.StoreResponses {
		params.Store = openai_v2.Bool(true)
	}

	// Configure reasoning for reasoning models
	if t.isReasoningModelDynamic(model) && t.reasoningEffort != "none" {
		params.Reasoning = shared.ReasoningParam{
			Effort: t.convertReasoningEffort(t.reasoningEffort),
		}
	}

	// Add tools if enabled
	if !opt.NoToolUse {
		params.Tools = t.buildResponseAPITools(opt)
	}

	return params
}

// buildInputItems creates Response API input items from message and images
func (t *OpenAIThread) buildInputItems(message string, images []string) []responses.ResponseInputItemUnionParam {
	var inputItems []responses.ResponseInputItemUnionParam

	// First, include any pending input items (e.g., tool results from previous turn)
	for _, pendingItem := range t.pendingInputItems {
		// Type assert to the correct type
		if item, ok := pendingItem.(responses.ResponseInputItemUnionParam); ok {
			inputItems = append(inputItems, item)
		}
	}
	
	// Clear pending items after including them
	t.pendingInputItems = nil

	// If we have a message or images, create an input message
	if message != "" || len(images) > 0 {
		var contentList responses.ResponseInputMessageContentListParam

		// Add text content if present
		if message != "" {
			contentList = append(contentList, responses.ResponseInputContentUnionParam{
				OfInputText: &responses.ResponseInputTextParam{
					Text: message,
				},
			})
		}

		// Add image content
		for _, imagePath := range images {
			imageItem, err := t.buildResponseAPIImage(imagePath)
			if err != nil {
				logger.G(context.Background()).WithError(err).Warnf("Failed to process image: %s", imagePath)
				continue
			}
			contentList = append(contentList, *imageItem)
		}

		// Create the input message item
		inputItems = append(inputItems, responses.ResponseInputItemParamOfInputMessage(
			contentList,
			"user",
		))
	}

	return inputItems
}

// buildResponseAPIImage creates an image input item for Response API
func (t *OpenAIThread) buildResponseAPIImage(imagePath string) (*responses.ResponseInputContentUnionParam, error) {
	// Process the image (reuse existing logic)
	imagePart, err := t.processImage(imagePath)
	if err != nil {
		return nil, err
	}

	// Convert to Response API format
	return &responses.ResponseInputContentUnionParam{
		OfInputImage: &responses.ResponseInputImageParam{
			ImageURL: openai_v2.String(imagePart.ImageURL.URL),
			Detail:   t.convertImageDetail(string(imagePart.ImageURL.Detail)),
		},
	}, nil
}

// convertImageDetail converts sashabaranov image detail to Response API detail
func (t *OpenAIThread) convertImageDetail(detail string) responses.ResponseInputImageDetail {
	switch detail {
	case "low":
		return responses.ResponseInputImageDetailLow
	case "high":
		return responses.ResponseInputImageDetailHigh
	default:
		return responses.ResponseInputImageDetailAuto
	}
}

// buildResponseAPITools converts tools to Response API format
func (t *OpenAIThread) buildResponseAPITools(opt llmtypes.MessageOpt) []responses.ToolUnionParam {
	availableTools := t.tools(opt)
	var responseAPITools []responses.ToolUnionParam

	for _, tool := range availableTools {
		// Generate JSON schema
		schema := tool.GenerateSchema()
		
		// Convert to map for API
		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			logger.G(context.Background()).WithError(err).Warnf("Failed to marshal schema for tool: %s", tool.Name())
			continue
		}
		
		var schemaMap map[string]any
		if err := json.Unmarshal(schemaJSON, &schemaMap); err != nil {
			logger.G(context.Background()).WithError(err).Warnf("Failed to parse schema for tool: %s", tool.Name())
			continue
		}

		responseAPITools = append(responseAPITools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name(),
				Description: openai_v2.String(tool.Description()),
				Parameters:  schemaMap,
				// Note: Strict mode disabled to support optional parameters in tool schemas
			},
		})
	}

	return responseAPITools
}

// convertReasoningEffort converts kodelet reasoning effort to OpenAI SDK format
func (t *OpenAIThread) convertReasoningEffort(effort string) shared.ReasoningEffort {
	switch effort {
	case "low":
		return shared.ReasoningEffortLow
	case "medium":
		return shared.ReasoningEffortMedium
	case "high":
		return shared.ReasoningEffortHigh
	default:
		return shared.ReasoningEffortMedium
	}
}

// getSystemPrompt generates the system prompt based on current context
func (t *OpenAIThread) getSystemPrompt(_ context.Context) string {
	var contexts map[string]string
	if t.state != nil {
		contexts = t.state.DiscoverContexts()
	}

	var systemPrompt string
	model := t.config.Model
	if t.config.IsSubAgent {
		systemPrompt = sysprompt.SubAgentPrompt(model, t.config, contexts)
	} else {
		systemPrompt = sysprompt.SystemPrompt(model, t.config, contexts)
	}

	return systemPrompt
}

// sendMessageResponseAPIStreaming handles streaming Response API requests
func (t *OpenAIThread) sendMessageResponseAPIStreaming(
	ctx context.Context,
	params responses.ResponseNewParams,
	handler llmtypes.MessageHandler,
	_ llmtypes.MessageOpt,
) (*responses.Response, error) {
	logger.G(ctx).Debug("starting Response API streaming")

	stream := t.responsesClient.Responses.NewStreaming(ctx, params)

	var fullText string
	var currentResponse *responses.Response
	
	// Buffer for streaming text (accumulate before sending to handler)
	var textBuffer strings.Builder
	var reasoningBuffer strings.Builder

	for stream.Next() {
		event := stream.Current()

		// Use AsAny() to switch on event type
		switch e := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			// Accumulate text deltas
			textBuffer.WriteString(e.Delta)
			fullText += e.Delta

		case responses.ResponseReasoningTextDeltaEvent:
			// Accumulate reasoning deltas
			reasoningBuffer.WriteString(e.Delta)

		case responses.ResponseFunctionCallArgumentsDoneEvent:
			// Function call complete - will handle in output processing
			logger.G(ctx).WithField("item_id", e.ItemID).Debug("function call arguments complete")

		case responses.ResponseCompletedEvent:
			// Response complete
			logger.G(ctx).Debug("response completed")
			currentResponse = &e.Response

		case responses.ResponseErrorEvent:
			return nil, errors.New(fmt.Sprintf("Response API error: code=%s, message=%s", e.Code, e.Message))

		case responses.ResponseFailedEvent:
			return nil, errors.New(fmt.Sprintf("Response failed: %+v", e.Response.Error))
		}
	}

	if err := stream.Err(); err != nil {
		return nil, errors.Wrap(err, "streaming error")
	}

	if currentResponse == nil {
		return nil, errors.New("stream ended without completion event")
	}
	
	// Send accumulated text to handler (after streaming completes)
	if textBuffer.Len() > 0 {
		text := strings.TrimRight(textBuffer.String(), "\n")
		if text != "" {
			handler.HandleText(text)
		}
	}
	
	// Send accumulated reasoning to handler
	if reasoningBuffer.Len() > 0 {
		reasoning := strings.TrimRight(reasoningBuffer.String(), "\n")
		if reasoning != "" {
			handler.HandleThinking(reasoning)
		}
	}

	return currentResponse, nil
}

// processResponseOutput processes the output items from a Response API response.
// Note: Error return is currently always nil but kept for future compatibility when
// additional output types may require error handling.
func (t *OpenAIThread) processResponseOutput(
	ctx context.Context,
	resp *responses.Response,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	var textOutput string
	var toolsUsed bool

	// Append all output items to conversation history for persistence
	for _, item := range resp.Output {
		itemJSON, _ := json.Marshal(item)
		t.conversationItems = append(t.conversationItems, ConversationItem{
			Type:      "output",
			Item:      itemJSON,
			Timestamp: time.Now(),
		})

		switch item.Type {
		case "message":
			// Extract text content
			msg := item.AsMessage()
			for _, content := range msg.Content {
				if content.Type == "output_text" {
					text := content.Text
					textOutput += text
				}
			}

		case "function_call":
			// Execute function tool
			toolsUsed = true
			call := item.AsFunctionCall()

			handler.HandleToolUse(call.Name, call.Arguments)

			runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)
			output := tools.RunTool(runToolCtx, t.state, call.Name, call.Arguments)

			// Use CLI rendering for consistent output formatting
			structuredResult := output.StructuredData()
			registry := renderers.NewRendererRegistry()
			renderedOutput := registry.Render(structuredResult)
			handler.HandleToolResult(call.Name, renderedOutput)

			// Store structured result for web UI
			t.SetStructuredToolResult(call.CallID, structuredResult)

			// Create tool result input item for next request
			toolResultItem := responses.ResponseInputItemParamOfFunctionCallOutput(
				call.CallID,
				output.AssistantFacing(),
			)

			// Store in pending items to be included in next request
			t.pendingInputItems = append(t.pendingInputItems, toolResultItem)

		case "reasoning":
			// Handle reasoning content (for o-series models)
			reasoning := item.AsReasoning()
			if len(reasoning.Content) > 0 {
				// Reasoning was already streamed, don't need to handle again
				logger.G(ctx).Debug("reasoning content received")
			}
		}
	}

	// Update usage tracking
	t.updateResponseAPIUsage(resp.Usage, resp.Model)

	return textOutput, toolsUsed, nil
}

// updateResponseAPIUsage updates usage statistics from Response API usage data
func (t *OpenAIThread) updateResponseAPIUsage(usage responses.ResponseUsage, model shared.ResponsesModel) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Track usage statistics (convert int64 to int)
	t.usage.InputTokens += int(usage.InputTokens)
	t.usage.OutputTokens += int(usage.OutputTokens)

	// Calculate costs based on model pricing
	pricing, found := t.getPricing(string(model))
	if !found {
		// Fall back to default pricing
		pricing = llmtypes.ModelPricing{
			Input:         0.000002,
			CachedInput:   0.0000005,
			Output:        0.000008,
			ContextWindow: 400_000,
		}
	}

	// Calculate individual costs
	t.usage.InputCost += float64(usage.InputTokens) * pricing.Input
	t.usage.OutputCost += float64(usage.OutputTokens) * pricing.Output

	// Update context window tracking
	t.usage.CurrentContextWindow = int(usage.InputTokens + usage.OutputTokens)
	t.usage.MaxContextWindow = pricing.ContextWindow
}
