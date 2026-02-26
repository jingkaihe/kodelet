// Package responses implements the OpenAI Responses API client.
// The Responses API is OpenAI's next-generation API designed for building AI agents,
// offering native support for multi-turn conversations, built-in tool calling,
// and automatic conversation state management.
package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/xai"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// Thread represents a conversation thread using the OpenAI Responses API.
// It implements the llmtypes.Thread interface with feature parity to the
// Chat Completions implementation.
type Thread struct {
	*base.Thread

	// client is the OpenAI client for making API calls
	client *openai.Client

	// compactFunc allows overriding Responses.Compact in tests.
	compactFunc func(context.Context, responses.ResponseCompactParams, ...option.RequestOption) (*responses.CompactedResponse, error)
	// compactRawFunc allows overriding raw JSON compact requests in tests.
	compactRawFunc func(context.Context, responses.ResponseCompactParams, ...option.RequestOption) (*responses.CompactedResponse, error)
	// compactWithSummaryFunc allows overriding summary-based compaction in tests.
	compactWithSummaryFunc func(context.Context) error

	// inputItems holds the complete conversation history as Responses API input items
	// This is used for persistence and display purposes
	inputItems []responses.ResponseInputItemUnionParam

	// pendingItems holds items that need to be sent in the next API call
	// When using previous_response_id, only these items are sent instead of full history
	pendingItems []responses.ResponseInputItemUnionParam

	// storedItems is the canonical conversation history for persistence
	// It includes all items (messages, function calls, reasoning) in order
	// This mirrors how Anthropic stores thinking blocks inline with messages
	storedItems []StoredInputItem

	// pendingReasoning accumulates reasoning content during streaming
	// It's stored here (not locally in processStream) to persist across API calls
	pendingReasoning strings.Builder

	// reasoningEffort controls the reasoning depth for o-series models
	reasoningEffort shared.ReasoningEffort

	// customModels contains provider-specific model aliases
	customModels map[string]string

	// customPricing contains provider-specific pricing information
	customPricing map[string]llmtypes.ModelPricing

	// lastResponseID stores the ID of the last response for multi-turn conversations
	// Used with previous_response_id parameter for server-side conversation state
	lastResponseID string

	// summary stores a short summary of the conversation for persistence
	summary string

	// isCodex indicates if this thread is using Codex authentication
	// Some API parameters may not be supported by the Codex API
	isCodex bool

	processMessageExchangeFunc func(
		ctx context.Context,
		handler llmtypes.MessageHandler,
		model string,
		maxTokens int,
		systemPrompt string,
		opt llmtypes.MessageOpt,
	) (string, bool, error)
	newStreamingFunc  func(context.Context, responses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
	processStreamFunc func(context.Context, *ssestream.Stream[responses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (bool, error)
}

// NewThread creates a new Responses API thread with the given configuration.
func NewThread(config llmtypes.Config) (*Thread, error) {
	log := logger.G(context.Background())

	log.WithField("model", config.Model).Debug("creating OpenAI Responses API thread")

	conversationID := convtypes.GenerateID()
	hookTrigger := base.CreateHookTrigger(context.Background(), config, conversationID)

	// Create the base thread with shared functionality
	baseThread := base.NewThread(config, conversationID, hookTrigger)

	// Build client options based on authentication mode
	opts, useCodex, err := buildClientOptions(config, log)
	if err != nil {
		return nil, err
	}

	// Create the OpenAI client
	client := openai.NewClient(opts...)

	// Determine reasoning effort from config or default
	reasoningEffort := shared.ReasoningEffort(config.ReasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = shared.ReasoningEffortMedium
	}

	// Load custom models and pricing
	customModels, customPricing := loadCustomConfiguration(config)

	thread := &Thread{
		Thread:          baseThread,
		client:          &client,
		inputItems:      make([]responses.ResponseInputItemUnionParam, 0),
		pendingItems:    make([]responses.ResponseInputItemUnionParam, 0),
		storedItems:     make([]StoredInputItem, 0),
		reasoningEffort: reasoningEffort,
		customModels:    customModels,
		customPricing:   customPricing,
		isCodex:         useCodex,
	}
	thread.processMessageExchangeFunc = thread.processMessageExchange
	thread.newStreamingFunc = thread.client.Responses.NewStreaming
	thread.processStreamFunc = thread.processStream
	thread.compactFunc = thread.client.Responses.Compact
	thread.compactRawFunc = thread.compactRawJSON
	thread.compactWithSummaryFunc = func(ctx context.Context) error {
		return base.CompactContextWithSummary(ctx, fragments.LoadCompactPrompt, thread.runUtilityPrompt, thread.SwapContext)
	}

	// Set the LoadConversation callback for provider-specific loading
	baseThread.LoadConversation = thread.loadConversation

	log.Debug("OpenAI Responses API thread created successfully")
	return thread, nil
}

// Provider returns the provider identifier for this thread.
func (t *Thread) Provider() string {
	return "openai"
}

// AddUserMessage adds a user message with optional images to the thread.
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	// Validate image count
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images",
			len(imagePaths), base.MaxImageCount, base.MaxImageCount)
		imagePaths = imagePaths[:base.MaxImageCount]
	}

	var inputItem responses.ResponseInputItemUnionParam

	// Build content parts if we have images
	if len(imagePaths) > 0 {
		contentParts := responses.ResponseInputMessageContentListParam{}

		// Process images and add them as content parts
		for _, imagePath := range imagePaths {
			imagePart, err := processImage(imagePath)
			if err != nil {
				logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
				continue
			}
			contentParts = append(contentParts, imagePart)
		}

		// Add text content
		contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
			OfInputText: &responses.ResponseInputTextParam{
				Text: message,
			},
		})

		// Create user message input item with content list
		inputItem = responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: contentParts},
			},
		}
	} else {
		// Simple text message
		inputItem = responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(message)},
			},
		}
	}

	// Add to inputItems (for API calls), pendingItems (for next API call), and storedItems (for persistence)
	t.inputItems = append(t.inputItems, inputItem)
	t.pendingItems = append(t.pendingItems, inputItem)
	t.storedItems = append(t.storedItems, StoredInputItem{
		Type:    "message",
		Role:    "user",
		Content: message,
	})
}

// SendMessage sends a message to the LLM and processes the response.
func (t *Thread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	logger.G(ctx).Debug("SendMessage called")
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
		attribute.String("reasoning_effort", string(t.reasoningEffort)),
		attribute.String("api", "responses"),
	)
	defer func() {
		t.FinalizeMessageSpan(span, err)
	}()

	var originalInputItems []responses.ResponseInputItemUnionParam
	var originalPendingItems []responses.ResponseInputItemUnionParam
	var originalLastResponseID string
	if opt.NoSaveConversation {
		originalInputItems = make([]responses.ResponseInputItemUnionParam, len(t.inputItems))
		copy(originalInputItems, t.inputItems)
		originalPendingItems = make([]responses.ResponseInputItemUnionParam, len(t.pendingItems))
		copy(originalPendingItems, t.pendingItems)
		originalLastResponseID = t.lastResponseID
	}

	// Trigger user_message_send hook before adding user message
	if blocked, reason := t.HookTrigger.TriggerUserMessageSend(ctx, t, message, t.GetRecipeHooks()); blocked {
		return "", errors.Errorf("message blocked by hook: %s", reason)
	}

	if len(opt.Images) > 0 {
		t.AddUserMessage(ctx, message, opt.Images...)
	} else {
		t.AddUserMessage(ctx, message)
	}

	// Determine which model to use
	model := t.Config.Model
	maxTokens := t.Config.MaxTokens
	if opt.UseWeakModel && t.Config.WeakModel != "" {
		model = t.Config.WeakModel
		if t.Config.WeakModelMaxTokens > 0 {
			maxTokens = t.Config.WeakModelMaxTokens
		}
	}

	turnCount := 0
	maxTurns := max(opt.MaxTurns, 0)

OUTER:
	for {
		select {
		case <-ctx.Done():
			logger.G(ctx).Info("stopping kodelet.llm.openai.responses")
			break OUTER
		default:
			// Check turn limit
			if maxTurns > 0 && turnCount >= maxTurns {
				logger.G(ctx).WithField("turn_count", turnCount).
					WithField("max_turns", maxTurns).
					Warn("reached maximum turn limit, stopping interaction")
				break OUTER
			}

			// Get relevant contexts from state and regenerate system prompt
			var contexts map[string]string
			if t.State != nil {
				contexts = t.State.DiscoverContexts()
			}

			var systemPrompt string
			if t.Config.IsSubAgent {
				systemPrompt = sysprompt.SubAgentPrompt(model, t.Config, contexts)
			} else {
				systemPrompt = sysprompt.SystemPrompt(model, t.Config, contexts)
			}

			// Check if auto-compact should be triggered
			t.TryAutoCompact(ctx, opt.DisableAutoCompact, opt.CompactRatio, t.CompactContext)

			logger.G(ctx).WithField("model", model).Debug("starting message exchange")
			processExchange := t.processMessageExchangeFunc
			if processExchange == nil {
				processExchange = t.processMessageExchange
			}
			exchangeOutput, toolsUsed, err := processExchange(ctx, handler, model, maxTokens, systemPrompt, opt)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logger.G(ctx).Info("Request cancelled, stopping kodelet.llm.openai.responses")
					break OUTER
				}
				if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
					t.SaveConversation(ctx, false)
				}
				return "", err
			}

			turnCount++
			finalOutput = exchangeOutput

			base.TriggerTurnEnd(ctx, t.HookTrigger, t, finalOutput, turnCount)

			// If no tools were used, check for hook follow-ups before stopping
			if !toolsUsed {
				if base.HandleAgentStopFollowUps(ctx, t.HookTrigger, t, handler) {
					continue OUTER
				}

				// Turn completed successfully, clear pending items
				// The server now has the full conversation state via previous_response_id
				t.pendingItems = nil
				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.inputItems = originalInputItems
		t.pendingItems = originalPendingItems
		t.lastResponseID = originalLastResponseID
	}

	// Save conversation state
	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background()
		// Skip LLM-based summary generation for subagent runs to avoid unnecessary API calls
		t.SaveConversation(saveCtx, !t.Config.IsSubAgent)
	}

	if !t.Config.IsSubAgent {
		handler.HandleDone()
	}

	return finalOutput, nil
}

// applyCodexRestrictions modifies request parameters for Codex API compatibility.
// The Codex API doesn't support: store=true, previous_response_id, max_output_tokens.
// This method centralizes all Codex-specific parameter restrictions in one place.
func (t *Thread) applyCodexRestrictions(params *responses.ResponseNewParams) {
	if !t.isCodex {
		return
	}
	// Codex API requires store=false (no server-side conversation state)
	params.Store = param.NewOpt(false)
	// Clear unsupported parameters
	params.PreviousResponseID = param.Opt[string]{}
	params.MaxOutputTokens = param.Opt[int64]{}

	if t.State != nil {
		contexts := t.State.DiscoverContexts()

		promptCtx := sysprompt.NewPromptContext(contexts)
		devMessages := []string{
			promptCtx.FormatSystemInfo(),
			promptCtx.FormatContexts(),
			promptCtx.FormatMCPServers(),
		}

		// prepend dev messages to params' input
		for i := len(devMessages) - 1; i >= 0; i-- {
			params.Input.OfInputItemList = append([]responses.ResponseInputItemUnionParam{
				{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    responses.EasyInputMessageRoleDeveloper,
						Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(devMessages[i])},
					},
				},
			}, params.Input.OfInputItemList...)
		}
	}
}

// processMessageExchange handles a single message exchange with the Responses API.
func (t *Thread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model string,
	maxTokens int,
	systemPrompt string,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	log := logger.G(ctx)
	var finalOutput string

	saveConversation := func() {
		if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
			t.SaveConversation(ctx, false)
		}
	}

	// Build tools
	tools := buildTools(t.State, opt.NoToolUse)
	log.WithField("tool_count", len(tools)).Debug("built tools for request")

	// Determine which input items to send:
	// - If we have a previous response ID (and not Codex), only send pending items (server has history)
	// - For Codex or fresh conversations, send full input items (no server-side state)
	var inputToSend []responses.ResponseInputItemUnionParam
	usePreviousResponseID := t.lastResponseID != "" && len(t.pendingItems) > 0 && !t.isCodex

	if usePreviousResponseID {
		inputToSend = t.pendingItems
	} else {
		inputToSend = t.inputItems
	}

	// Build request parameters with standard settings
	params := responses.ResponseNewParams{
		Model:        model,
		Input:        responses.ResponseNewParamsInputUnion{OfInputItemList: inputToSend},
		Instructions: param.NewOpt(systemPrompt),
		Tools:        tools,
		Store:        param.NewOpt(true), // Enable server-side conversation state storage
	}

	// Set previous_response_id for multi-turn conversations
	if usePreviousResponseID {
		params.PreviousResponseID = param.NewOpt(t.lastResponseID)
	}

	// Set max output tokens if specified
	if maxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(maxTokens))
	}

	// Add reasoning configuration for reasoning models (o-series, gpt-5, etc.)
	if t.isReasoningModelDynamic(model) && t.reasoningEffort != "" {
		reasoningEffort := t.reasoningEffort
		if opt.UseWeakModel {
			reasoningEffort = shared.ReasoningEffortMedium
		}
		params.Reasoning = shared.ReasoningParam{
			Effort:  reasoningEffort,
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	// Apply Codex-specific restrictions (overrides unsupported params)
	t.applyCodexRestrictions(&params)

	log.WithField("model", model).
		WithField("input_items", len(inputToSend)).
		WithField("tool_count", len(tools)).
		WithField("is_codex", t.isCodex).
		Debug("sending request to Responses API")

	newStreaming := t.newStreamingFunc
	if newStreaming == nil {
		newStreaming = t.client.Responses.NewStreaming
	}
	processStream := t.processStreamFunc
	if processStream == nil {
		processStream = t.processStream
	}

	// Use streaming API
	stream := newStreaming(ctx, params)
	log.Debug("stream created, processing events")

	// Process stream events
	toolsUsed, err := processStream(ctx, stream, handler, model, opt)
	if err != nil {
		// Log detailed error information for debugging
		log.WithError(err).
			WithField("model", model).
			WithField("tool_count", len(tools)).
			WithField("input_items", len(inputToSend)).
			Error("API request failed")

		// Check if the error is related to invalid previous_response_id
		// If so, fall back to sending full input items
		if usePreviousResponseID && isInvalidPreviousResponseIDError(err) {
			log.WithError(err).Warn("previous_response_id invalid, falling back to full input items")

			// Clear the invalid response ID and retry with full history
			t.lastResponseID = ""
			t.pendingItems = nil

			// Rebuild params with full input items
			params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: t.inputItems}
			params.PreviousResponseID = param.Opt[string]{} // Clear the param

			// Retry the request
			stream = newStreaming(ctx, params)
			toolsUsed, err = processStream(ctx, stream, handler, model, opt)
			if err != nil {
				saveConversation()
				return "", false, err
			}
		} else {
			saveConversation()
			return "", false, err
		}
	}

	// Extract final text output from the last response
	if len(t.inputItems) > 0 {
		// Get text from the last assistant message
		for i := len(t.inputItems) - 1; i >= 0; i-- {
			item := t.inputItems[i]
			if item.OfMessage != nil && item.OfMessage.Role == responses.EasyInputMessageRoleAssistant {
				if item.OfMessage.Content.OfString.Valid() {
					finalOutput = item.OfMessage.Content.OfString.Value
					break
				}
			}
		}
	}

	saveConversation()

	return finalOutput, toolsUsed, nil
}

// GetMessages returns the messages from the thread in a common format.
func (t *Thread) GetMessages() ([]llmtypes.Message, error) {
	result := make([]llmtypes.Message, 0, len(t.inputItems))

	for _, item := range t.inputItems {
		if item.OfMessage != nil {
			msg := item.OfMessage
			role := string(msg.Role)
			content := ""

			// Extract content
			if msg.Content.OfString.Valid() {
				content = msg.Content.OfString.Value
			} else if len(msg.Content.OfInputItemContentList) > 0 {
				for _, part := range msg.Content.OfInputItemContentList {
					if part.OfInputText != nil {
						content += part.OfInputText.Text
					}
				}
			}

			if content != "" {
				result = append(result, llmtypes.Message{
					Role:    role,
					Content: content,
				})
			}
		}
	}

	return result, nil
}

// SwapContext replaces the conversation history with a summary message.
// This implements the hooks.ContextSwapper interface.
func (t *Thread) SwapContext(_ context.Context, summary string) error {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	t.inputItems = []responses.ResponseInputItemUnionParam{
		{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(summary)},
			},
		},
	}

	// Update storedItems for persistence
	t.storedItems = []StoredInputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: summary,
		},
	}

	// Clear pending items - next request will use the new input
	t.pendingItems = nil

	// Clear the previous response ID
	t.lastResponseID = ""

	t.FinalizeSwapContextLocked(summary)

	return nil
}

// CompactContext compacts the conversation history using the Responses API compact endpoint.
// The Compact API returns user messages plus a single compaction item containing encrypted
// conversation state, which can be used to continue the conversation efficiently.
func (t *Thread) CompactContext(ctx context.Context) error {
	if len(t.inputItems) == 0 {
		return nil
	}

	compactWithSummary := t.compactWithSummaryFunc
	if compactWithSummary == nil {
		compactWithSummary = func(ctx context.Context) error {
			return base.CompactContextWithSummary(ctx, fragments.LoadCompactPrompt, t.runUtilityPrompt, t.SwapContext)
		}
	}

	var contexts map[string]string
	if t.State != nil {
		contexts = t.State.DiscoverContexts()
	}

	systemPrompt := sysprompt.SystemPrompt(t.Config.Model, t.Config, contexts)
	if t.Config.IsSubAgent {
		systemPrompt = sysprompt.SubAgentPrompt(t.Config.Model, t.Config, contexts)
	}

	compactParams := responses.ResponseCompactParams{
		Input: responses.ResponseCompactParamsInputUnion{
			OfResponseInputItemArray: t.inputItems,
		},
		Model:        responses.ResponseCompactParamsModel(t.Config.Model),
		Instructions: param.NewOpt(systemPrompt),
	}

	compactOpts := []option.RequestOption{}
	if t.isCodex {
		compactOpts = append(compactOpts, option.WithHeader("Accept", "application/json"))
	}

	// Use raw JSON compact parsing by default because some backends may omit
	// JSON content-type on compact responses, which can break typed SDK decode.
	compactRawFn := t.compactRawFunc
	if compactRawFn == nil && t.client != nil {
		compactRawFn = t.compactRawJSON
	}

	var (
		resp *responses.CompactedResponse
		err  error
	)
	if compactRawFn != nil {
		resp, err = compactRawFn(ctx, compactParams, compactOpts...)
	} else {
		// Fallback for tests that construct Thread manually without client/raw hook.
		compactFn := t.compactFunc
		if compactFn == nil {
			return errors.New("compact function is not initialized")
		}
		resp, err = compactFn(ctx, compactParams, compactOpts...)
	}

	if err != nil {
		logger.G(ctx).WithError(err).Warn("responses compact endpoint failed, falling back to summary compaction")
		if fallbackErr := compactWithSummary(ctx); fallbackErr != nil {
			return errors.Wrapf(fallbackErr, "failed to compact context (responses endpoint error: %v)", err)
		}
		return nil
	}

	// Update usage metrics
	t.Mu.Lock()
	t.Usage.InputTokens += int(resp.Usage.InputTokens)
	t.Usage.OutputTokens += int(resp.Usage.OutputTokens)
	t.Mu.Unlock()

	// Convert compacted output items to persisted/input items while preserving
	// the original output payload for forward compatibility.
	newInputItems := make([]responses.ResponseInputItemUnionParam, 0, len(resp.Output))
	newStoredItems := make([]StoredInputItem, 0, len(resp.Output))

	for _, output := range resp.Output {
		raw := output.RawJSON()
		if raw == "" {
			rawJSON, marshalErr := json.Marshal(output)
			if marshalErr == nil {
				raw = string(rawJSON)
			}
		}

		storedItem := storedItemFromCompactOutput(output, raw)

		if output.Type == "message" {
			msg := output.AsMessage()
			role := strings.TrimSpace(string(msg.Role))
			parsedRole, ok := parseStoredMessageRole(role)
			if !ok {
				logger.G(ctx).WithField("role", role).Debug("Skipping compacted message with unsupported role")
				continue
			}
			if storedItem.Content == "" {
				continue
			}

			newInputItems = append(newInputItems, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    parsedRole,
					Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(storedItem.Content)},
				},
			})
			newStoredItems = append(newStoredItems, storedItem)
			continue
		}

		inputItem, ok := inputItemFromRawItem(json.RawMessage(raw))
		if !ok {
			logger.G(ctx).WithField("type", output.Type).Debug("Skipping unsupported compact output item")
			continue
		}

		newInputItems = append(newInputItems, inputItem)
		newStoredItems = append(newStoredItems, storedItem)
	}

	if len(newInputItems) == 0 {
		logger.G(ctx).Warn("responses compact returned no usable items, falling back to summary compaction")
		if fallbackErr := compactWithSummary(ctx); fallbackErr != nil {
			return errors.Wrap(fallbackErr, "failed to compact context: empty compact output and summary fallback failed")
		}
		return nil
	}

	// Replace input items with compacted output
	t.inputItems = newInputItems
	t.storedItems = newStoredItems

	// Clear pending items - next request will use the new compacted input
	t.pendingItems = nil

	// Clear the previous response ID - the server-side conversation state is now
	// represented by the compaction item, not the response chain
	t.lastResponseID = ""

	t.ResetContextStateLocked()

	return nil
}

func storedItemFromCompactOutput(output responses.ResponseOutputItemUnion, raw string) StoredInputItem {
	item := StoredInputItem{
		Type:    output.Type,
		RawItem: json.RawMessage(raw),
	}

	switch output.Type {
	case "message":
		msg := output.AsMessage()
		item.Role = strings.TrimSpace(string(msg.Role))
		for _, content := range msg.Content {
			if content.Type == "output_text" || content.Type == "input_text" {
				item.Content += content.Text
			}
		}
	case "function_call":
		call := output.AsFunctionCall()
		item.CallID = call.CallID
		item.Name = call.Name
		item.Arguments = call.Arguments
	case "function_call_output":
		item.CallID = output.CallID
		item.Output = output.Output.OfString
	case "reasoning":
		item.Role = "assistant"
		reasoning := output.AsReasoning()
		for _, summary := range reasoning.Summary {
			if item.Content != "" {
				item.Content += "\n"
			}
			item.Content += summary.Text
		}
	case "compaction":
		compaction := output.AsCompaction()
		item.EncryptedContent = compaction.EncryptedContent
		if item.EncryptedContent == "" {
			item.EncryptedContent = output.EncryptedContent
		}
	case "compaction_summary":
		item.EncryptedContent = output.EncryptedContent
	}

	return item
}

func parseStoredMessageRole(role string) (responses.EasyInputMessageRole, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return responses.EasyInputMessageRoleUser, true
	case "assistant":
		return responses.EasyInputMessageRoleAssistant, true
	case "system":
		return responses.EasyInputMessageRoleSystem, true
	case "developer":
		return responses.EasyInputMessageRoleDeveloper, true
	default:
		return "", false
	}
}

func (t *Thread) compactRawJSON(
	ctx context.Context,
	params responses.ResponseCompactParams,
	opts ...option.RequestOption,
) (*responses.CompactedResponse, error) {
	// Parse compact responses via raw bytes to decouple from SDK content-type handling.
	if t.client == nil {
		return nil, errors.New("openai client is not initialized")
	}

	var body []byte
	if err := t.client.Post(ctx, "responses/compact", params, &body, opts...); err != nil {
		return nil, err
	}

	var resp responses.CompactedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to parse responses/compact payload")
	}

	return &resp, nil
}

func (t *Thread) runUtilityPrompt(ctx context.Context, prompt string, useWeakModel bool) (string, error) {
	return base.RunUtilityPrompt(ctx,
		func() (*Thread, error) {
			return NewThread(t.Config)
		},
		func(summaryThread *Thread) {
			// Copy input items to the summary thread.
			summaryThread.inputItems = t.inputItems
		},
		prompt,
		useWeakModel,
	)
}

// ShortSummary generates a short summary of the conversation using an LLM.
func (t *Thread) ShortSummary(ctx context.Context) string {
	if len(t.inputItems) == 0 {
		return ""
	}

	return base.GenerateShortSummary(
		ctx,
		prompts.ShortSummaryPrompt,
		t.runUtilityPrompt,
		func(err error) {
			logger.G(ctx).WithError(err).Error("failed to generate summary")
		},
	)
}

// SaveConversation saves the current thread to the conversation store.
func (t *Thread) SaveConversation(ctx context.Context, summarize bool) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return nil
	}

	// Clean up orphaned messages before saving
	t.cleanupOrphanedItems()

	// Generate a new summary if requested
	if summarize {
		t.summary = t.ShortSummary(ctx)
	}

	// Serialize stored items directly (already built inline during streaming)
	inputItemsJSON, err := json.Marshal(t.storedItems)
	if err != nil {
		return errors.Wrap(err, "error marshaling input items")
	}

	// Build the conversation record
	metadata := map[string]any{
		"model":          t.Config.Model,
		"lastResponseID": t.lastResponseID,
		"api_mode":       "responses",
		"platform":       resolvePlatformName(t.Config),
	}

	record := convtypes.ConversationRecord{
		ID:                  t.ConversationID,
		RawMessages:         inputItemsJSON,
		Provider:            "openai",
		Usage:               *t.Usage,
		Metadata:            metadata,
		Summary:             t.summary,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		FileLastAccess:      t.State.FileLastAccess(),
		ToolResults:         t.GetStructuredToolResults(),
		BackgroundProcesses: t.State.GetBackgroundProcesses(),
	}

	return t.Store.Save(ctx, record)
}

// loadConversation loads a conversation from the store.
// NOTE: This function expects the caller to hold ConversationMu lock.
func (t *Thread) loadConversation(ctx context.Context) {
	if !t.Persisted || t.Store == nil {
		return
	}

	record, err := t.Store.Load(ctx, t.ConversationID)
	if err != nil {
		return
	}

	if record.Provider != "" {
		if record.Provider != "openai-responses" {
			if record.Provider != "openai" {
				return
			}
			if !recordUsesResponsesAPI(record.Metadata) {
				return
			}
		}
	}

	// Deserialize from storage format
	var storedItems []StoredInputItem
	if err := json.Unmarshal(record.RawMessages, &storedItems); err != nil {
		return
	}

	// Store the loaded items directly and convert to SDK format for API calls
	t.storedItems = storedItems
	t.inputItems = fromStoredItems(storedItems)
	t.cleanupOrphanedItems()
	t.Usage = &record.Usage
	t.summary = record.Summary
	t.State.SetFileLastAccess(record.FileLastAccess)
	t.SetStructuredToolResults(record.ToolResults)
	base.RestoreBackgroundProcesses(t.State, record.BackgroundProcesses)

	// Restore lastResponseID from metadata
	if record.Metadata != nil {
		if lastResponseID, ok := record.Metadata["lastResponseID"].(string); ok {
			t.lastResponseID = lastResponseID
		}
	}
}

// cleanupOrphanedItems removes incomplete tool call sequences from the end.
func (t *Thread) cleanupOrphanedItems() {
	// Remove trailing tool calls without results
	for len(t.inputItems) > 0 {
		lastItem := t.inputItems[len(t.inputItems)-1]

		// If last item is a tool call without a result, remove it
		if lastItem.OfFunctionCall != nil {
			t.inputItems = t.inputItems[:len(t.inputItems)-1]
			continue
		}

		break
	}

	// Keep persisted history in sync with cleanup logic.
	for len(t.storedItems) > 0 {
		lastItem := t.storedItems[len(t.storedItems)-1]
		if lastItem.Type == "function_call" {
			t.storedItems = t.storedItems[:len(t.storedItems)-1]
			continue
		}
		break
	}

	// Pending items can also contain an unfinished tail.
	for len(t.pendingItems) > 0 {
		lastItem := t.pendingItems[len(t.pendingItems)-1]
		if lastItem.OfFunctionCall != nil {
			t.pendingItems = t.pendingItems[:len(t.pendingItems)-1]
			continue
		}
		break
	}
}

// Helper functions

const defaultOpenAIPlatform = "openai"

func normalizePlatformName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func resolvePlatformName(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	return defaultOpenAIPlatform
}

func resolvePlatformForLoading(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	if config.OpenAI.Models == nil && config.OpenAI.Pricing == nil {
		return defaultOpenAIPlatform
	}

	return ""
}

func parseAPIMode(raw string) (llmtypes.OpenAIAPIMode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")

	switch normalized {
	case "chat", "chat_completions", "chatcompletions":
		return llmtypes.OpenAIAPIModeChatCompletions, true
	case "responses", "responses_api", "response":
		return llmtypes.OpenAIAPIModeResponses, true
	default:
		return "", false
	}
}

func getPlatformAPIKeyEnvVar(platform string) string {
	switch normalizePlatformName(platform) {
	case "xai":
		return xai.APIKeyEnvVar
	default:
		return openaipreset.APIKeyEnvVar
	}
}

func getPlatformBaseURL(platform string) string {
	switch normalizePlatformName(platform) {
	case "xai":
		return xai.BaseURL
	case "codex":
		return codexpreset.BaseURL
	case "openai":
		return openaipreset.BaseURL
	default:
		return ""
	}
}

func getAPIKeyEnvVar(config llmtypes.Config) string {
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		return config.OpenAI.APIKeyEnvVar
	}
	return getPlatformAPIKeyEnvVar(resolvePlatformName(config))
}

func getBaseURL(config llmtypes.Config) string {
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		return baseURL
	}
	if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		return config.OpenAI.BaseURL
	}
	return getPlatformBaseURL(resolvePlatformName(config))
}

// loadCustomConfiguration loads custom models and pricing from config.
// It processes platform defaults first, then applies custom overrides if provided.
func loadCustomConfiguration(config llmtypes.Config) (map[string]string, map[string]llmtypes.ModelPricing) {
	customModels := make(map[string]string)
	customPricing := make(map[string]llmtypes.ModelPricing)

	platformName := resolvePlatformForLoading(config)
	if platformName != "" {
		platformModels, platformPricing := loadPlatformDefaults(platformName)
		for model, category := range platformModels {
			customModels[model] = category
		}
		for model, pricing := range platformPricing {
			customPricing[model] = pricing
		}
	}

	if config.OpenAI != nil {
		if config.OpenAI.Models != nil {
			for _, model := range config.OpenAI.Models.Reasoning {
				customModels[model] = "reasoning"
			}
			for _, model := range config.OpenAI.Models.NonReasoning {
				customModels[model] = "non-reasoning"
			}
		}

		if config.OpenAI.Pricing != nil {
			for k, v := range config.OpenAI.Pricing {
				customPricing[k] = v
			}
		}
	}

	return customModels, customPricing
}

// buildClientOptions constructs the OpenAI client options based on authentication mode.
// Returns the options, whether Codex auth is being used, and any error.
func buildClientOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, bool, error) {
	useCodex := resolvePlatformName(config) == "codex"

	var opts []option.RequestOption
	var err error

	if useCodex {
		opts, err = buildCodexAuthOptions(log)
	} else {
		opts, err = buildAPIKeyAuthOptions(config, log)
	}
	if err != nil {
		return nil, useCodex, err
	}

	opts = append(opts, errorLoggingMiddleware(log))

	return opts, useCodex, nil
}

// buildCodexAuthOptions returns client options for Codex CLI authentication.
func buildCodexAuthOptions(log *logrus.Entry) ([]option.RequestOption, error) {
	log.Debug("using Codex authentication for Responses API")
	opts, err := auth.CodexHeader()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get Codex credentials")
	}
	return opts, nil
}

// buildAPIKeyAuthOptions returns client options for standard API key authentication.
func buildAPIKeyAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, error) {
	apiKeyEnvVar := getAPIKeyEnvVar(config)
	apiKey := os.Getenv(apiKeyEnvVar)
	if apiKey == "" {
		return nil, errors.Errorf("%s environment variable is required", apiKeyEnvVar)
	}

	log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key for Responses API")

	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return opts, nil
}

// errorLoggingMiddleware returns a middleware that logs error response bodies for debugging.
func errorLoggingMiddleware(log *logrus.Entry) option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil {
			return resp, err
		}

		// Log response body for non-2xx status codes
		if resp != nil && resp.StatusCode >= 400 {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				log.WithError(readErr).Debug("failed to read error response body")
				return resp, err
			}

			log.WithField("status_code", resp.StatusCode).
				WithField("response_body", string(body)).
				Debug("API error response")

			// Restore the body so the SDK can still read it
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}

		return resp, err
	})
}

// loadPlatformDefaults loads built-in defaults for known OpenAI-compatible platforms.
func loadPlatformDefaults(platformName string) (map[string]string, map[string]llmtypes.ModelPricing) {
	switch normalizePlatformName(platformName) {
	case "openai":
		return loadPlatformDefaultsFromConfig(openaipreset.Models, openaipreset.Pricing)
	case "xai":
		return loadPlatformDefaultsFromConfig(xai.Models, xai.Pricing)
	case "codex":
		return loadPlatformDefaultsFromConfig(codexpreset.Models, codexpreset.Pricing)
	default:
		return nil, nil
	}
}

// loadPlatformDefaultsFromConfig converts platform model and pricing defaults into the internal format.
func loadPlatformDefaultsFromConfig(platformModels llmtypes.CustomModels, platformPricing llmtypes.CustomPricing) (map[string]string, map[string]llmtypes.ModelPricing) {
	models := make(map[string]string)
	pricing := make(map[string]llmtypes.ModelPricing)

	for _, model := range platformModels.Reasoning {
		models[model] = "reasoning"
	}
	for _, model := range platformModels.NonReasoning {
		models[model] = "non-reasoning"
	}

	for model, p := range platformPricing {
		pricing[model] = p
	}

	return models, pricing
}

// getPricing returns the pricing information for a model, checking custom pricing first.
func (t *Thread) getPricing(model string) llmtypes.ModelPricing {
	// Check custom pricing first
	if t.customPricing != nil {
		if pricing, ok := t.customPricing[model]; ok {
			return pricing
		}
	}

	// Return default pricing as fallback
	return llmtypes.ModelPricing{
		Input:         0.000002,  // $2.00 per million tokens (GPT-4.1 default)
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.000008,  // $8.00 per million tokens
		ContextWindow: 1047576,
	}
}

// isReasoningModelDynamic checks if a model supports reasoning using loaded platform defaults/config.
func (t *Thread) isReasoningModelDynamic(model string) bool {
	if t.customModels != nil {
		if category, ok := t.customModels[model]; ok {
			return category == "reasoning"
		}
	}
	return false
}

// StreamableMessage contains parsed message data for streaming.
type StreamableMessage struct {
	Kind       string // "text", "tool-use", "tool-result", "thinking"
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
}

const compactedHistoryNotice = "Context compacted"

// StreamMessages parses raw messages into streamable format for conversation streaming.
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var items []StoredInputItem
	if err := json.Unmarshal(rawMessages, &items); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling input items")
	}

	streamable := make([]StreamableMessage, 0, len(items))

	for _, item := range items {
		switch item.Type {
		case "reasoning":
			// Add thinking message
			streamable = append(streamable, StreamableMessage{
				Kind:    "thinking",
				Role:    "assistant",
				Content: item.Content,
			})

		case "message":
			// Skip system/developer messages
			if item.Role == "system" || item.Role == "developer" {
				continue
			}

			if item.Content != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    item.Role,
					Content: item.Content,
				})
			}

		case "function_call":
			streamable = append(streamable, StreamableMessage{
				Kind:       "tool-use",
				Role:       "assistant",
				ToolName:   item.Name,
				ToolCallID: item.CallID,
				Input:      item.Arguments,
			})

		case "function_call_output":
			resultStr := item.Output
			toolName := ""
			if structuredResult, ok := toolResults[item.CallID]; ok {
				toolName = structuredResult.ToolName
				if jsonData, err := structuredResult.MarshalJSON(); err == nil {
					resultStr = string(jsonData)
				}
			}
			streamable = append(streamable, StreamableMessage{
				Kind:       "tool-result",
				Role:       "assistant",
				ToolName:   toolName,
				ToolCallID: item.CallID,
				Content:    resultStr,
			})

		case "compaction", "compaction_summary":
			streamable = append(streamable, StreamableMessage{
				Kind:    "text",
				Role:    "assistant",
				Content: compactedHistoryNotice,
			})
		}
	}

	return streamable, nil
}

// ExtractMessages converts the stored message format to the common format.
func ExtractMessages(data []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	var items []StoredInputItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling input items")
	}

	result := make([]llmtypes.Message, 0, len(items))

	registry := renderers.NewRendererRegistry()

	for _, item := range items {
		switch item.Type {
		case "reasoning":
			// Add thinking message
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("💭 Thinking:\n%s", item.Content),
			})

		case "message":
			// Skip system/developer messages
			if item.Role == "system" || item.Role == "developer" {
				continue
			}

			if item.Content != "" {
				result = append(result, llmtypes.Message{
					Role:    item.Role,
					Content: item.Content,
				})
			}

		case "function_call":
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("🔧 Using tool: %s\n  Arguments: %s", item.Name, item.Arguments),
			})

		case "function_call_output":
			text := item.Output
			if structuredResult, ok := toolResults[item.CallID]; ok {
				text = registry.Render(structuredResult)
			}
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("🔄 Tool result:\n%s", text),
			})

		case "compaction", "compaction_summary":
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: compactedHistoryNotice,
			})
		}
	}

	return result, nil
}

// isInvalidPreviousResponseIDError checks if an error is related to an invalid previous_response_id.
// This can happen when the server-side conversation state has expired or been deleted.
func isInvalidPreviousResponseIDError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	invalidResponseIDPatterns := []string{
		"previous_response_id",
		"response not found",
		"invalid response id",
		"response id not found",
		"no response found",
	}

	for _, pattern := range invalidResponseIDPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	return false
}

func recordUsesResponsesAPI(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}

	if modeRaw, ok := metadata["api_mode"]; ok {
		if mode, ok := modeRaw.(string); ok {
			if parsedMode, parsed := parseAPIMode(mode); parsed {
				return parsedMode == llmtypes.OpenAIAPIModeResponses
			}
		}
	}

	if legacyRaw, ok := metadata["use_responses_api"]; ok {
		if legacy, ok := legacyRaw.(bool); ok {
			return legacy
		}
	}

	return false
}
