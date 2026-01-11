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
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
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
}

// NewThread creates a new Responses API thread with the given configuration.
func NewThread(
	config llmtypes.Config,
	subagentContextFactory llmtypes.SubagentContextFactory,
) (*Thread, error) {
	log := logger.G(context.Background())

	log.WithField("model", config.Model).Debug("creating OpenAI Responses API thread")

	// Initialize hook trigger (zero-value if discovery fails or disabled - hooks disabled)
	var hookTrigger hooks.Trigger
	conversationID := convtypes.GenerateID()
	if !config.IsSubAgent && !config.NoHooks {
		hookManager, err := hooks.NewHookManager()
		if err != nil {
			log.WithError(err).Warn("Failed to initialize hook manager, hooks disabled")
		} else {
			hookTrigger = hooks.NewTrigger(hookManager, conversationID, config.IsSubAgent)
		}
	}

	// Create the base thread with shared functionality
	baseThread := base.NewThread(config, conversationID, subagentContextFactory, hookTrigger)

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

	// Set the LoadConversation callback for provider-specific loading
	baseThread.LoadConversation = thread.loadConversation

	log.Debug("OpenAI Responses API thread created successfully")
	return thread, nil
}

// Provider returns the provider identifier for this thread.
func (t *Thread) Provider() string {
	return "openai-responses"
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
	if blocked, reason := t.HookTrigger.TriggerUserMessageSend(ctx, message); blocked {
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
			if !opt.DisableAutoCompact && t.ShouldAutoCompact(opt.CompactRatio) {
				logger.G(ctx).WithField("context_utilization",
					float64(t.GetUsage().CurrentContextWindow)/float64(t.GetUsage().MaxContextWindow)).
					Info("triggering auto-compact")
				if err := t.CompactContext(ctx); err != nil {
					logger.G(ctx).WithError(err).Error("failed to auto-compact context")
				} else {
					logger.G(ctx).Info("auto-compact completed successfully")
				}
			}

			logger.G(ctx).WithField("model", model).Debug("starting message exchange")
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, model, maxTokens, systemPrompt, opt)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logger.G(ctx).Info("Request cancelled, stopping kodelet.llm.openai.responses")
					break OUTER
				}
				return "", err
			}

			turnCount++
			finalOutput = exchangeOutput

			// If no tools were used, check for hook follow-ups before stopping
			if !toolsUsed {
				logger.G(ctx).Debug("no tools used, checking agent_stop hook")

				if messages, err := t.GetMessages(); err == nil {
					if followUps := t.HookTrigger.TriggerAgentStop(ctx, messages); len(followUps) > 0 {
						logger.G(ctx).WithField("count", len(followUps)).
							Info("agent_stop hook returned follow-up messages, continuing conversation")
						for _, msg := range followUps {
							t.AddUserMessage(ctx, msg)
							handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
						}
						continue OUTER
					}
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
		t.SaveConversation(saveCtx, true)
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
			Summary: shared.ReasoningSummaryDetailed,
		}
	}

	// Apply Codex-specific restrictions (overrides unsupported params)
	t.applyCodexRestrictions(&params)

	log.WithField("model", model).
		WithField("input_items", len(inputToSend)).
		WithField("tool_count", len(tools)).
		WithField("is_codex", t.isCodex).
		Debug("sending request to Responses API")

	// Use streaming API
	stream := t.client.Responses.NewStreaming(ctx, params)
	log.Debug("stream created, processing events")

	// Process stream events
	toolsUsed, err := t.processStream(ctx, stream, handler)
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
			stream = t.client.Responses.NewStreaming(ctx, params)
			toolsUsed, err = t.processStream(ctx, stream, handler)
			if err != nil {
				return "", false, err
			}
		} else {
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

// NewSubAgent creates a new subagent thread with the given configuration.
func (t *Thread) NewSubAgent(ctx context.Context, config llmtypes.Config) llmtypes.Thread {
	config.IsSubAgent = true

	newThread, err := NewThread(config, t.SubagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to create subagent thread")
		return nil
	}

	// Copy custom models and pricing from parent
	newThread.customModels = t.customModels
	newThread.customPricing = t.customPricing

	return newThread
}

// CompactContext compacts the conversation history using the Responses API compact endpoint.
// The Compact API returns user messages plus a single compaction item containing encrypted
// conversation state, which can be used to continue the conversation efficiently.
func (t *Thread) CompactContext(ctx context.Context) error {
	if len(t.inputItems) == 0 {
		return nil
	}

	resp, err := t.client.Responses.Compact(ctx, responses.ResponseCompactParams{
		Input: responses.ResponseCompactParamsInputUnion{
			OfResponseInputItemArray: t.inputItems,
		},
		Model: responses.ResponseCompactParamsModel(t.Config.Model),
	})
	if err != nil {
		return errors.Wrap(err, "failed to compact context")
	}

	// Update usage metrics
	t.Mu.Lock()
	t.Usage.InputTokens += int(resp.Usage.InputTokens)
	t.Usage.OutputTokens += int(resp.Usage.OutputTokens)
	t.Mu.Unlock()

	// Convert output items to input items
	// The Compact API returns: user messages + a single compaction item
	newInputItems := make([]responses.ResponseInputItemUnionParam, 0, len(resp.Output))
	for _, output := range resp.Output {
		switch output.Type {
		case "message":
			// Convert output message to input message
			msg := output.AsMessage()
			if msg.Role == "user" {
				// Extract text content from the message
				var textContent string
				for _, content := range msg.Content {
					if content.Type == "output_text" {
						textPart := content.AsOutputText()
						textContent += textPart.Text
					}
				}
				if textContent != "" {
					newInputItems = append(newInputItems, responses.ResponseInputItemUnionParam{
						OfMessage: &responses.EasyInputMessageParam{
							Role:    responses.EasyInputMessageRoleUser,
							Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(textContent)},
						},
					})
				}
			}

		case "compaction":
			// Convert compaction item using the SDK helper
			compaction := output.AsCompaction()
			newInputItems = append(newInputItems, responses.ResponseInputItemParamOfCompaction(compaction.EncryptedContent))
		}
	}

	// Replace input items with compacted output
	t.inputItems = newInputItems

	// Clear pending items - next request will use the new compacted input
	t.pendingItems = nil

	// Clear the previous response ID - the server-side conversation state is now
	// represented by the compaction item, not the response chain
	t.lastResponseID = ""

	// Clear stale tool results - they reference tool calls that no longer exist
	t.ToolResults = make(map[string]tooltypes.StructuredToolResult)

	// Clear file access tracking to start fresh with context retrieval
	if t.State != nil {
		t.State.SetFileLastAccess(make(map[string]time.Time))
	}

	return nil
}

// ShortSummary generates a short summary of the conversation using an LLM.
func (t *Thread) ShortSummary(ctx context.Context) string {
	if len(t.inputItems) == 0 {
		return ""
	}

	// Create a new summary thread
	summaryThread, err := NewThread(t.Config, nil)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to create summary thread")
		return "Could not generate summary."
	}

	// Copy input items to the summary thread
	summaryThread.inputItems = t.inputItems
	summaryThread.EnablePersistence(ctx, false)
	summaryThread.HookTrigger = hooks.Trigger{} // disable hooks for summary

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = summaryThread.SendMessage(ctx, prompts.ShortSummaryPrompt, handler, llmtypes.MessageOpt{
		UseWeakModel:       true,
		NoToolUse:          true,
		DisableAutoCompact: true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	})
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to generate summary")
		return "Could not generate summary."
	}

	return handler.CollectedText()
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
	record := convtypes.ConversationRecord{
		ID:                  t.ConversationID,
		RawMessages:         inputItemsJSON,
		Provider:            "openai-responses",
		Usage:               *t.Usage,
		Metadata:            map[string]interface{}{"model": t.Config.Model, "lastResponseID": t.lastResponseID},
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

	// Check if this is a Responses API conversation
	if record.Provider != "" && record.Provider != "openai-responses" {
		return
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
	t.restoreBackgroundProcesses(record.BackgroundProcesses)

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
}

// restoreBackgroundProcesses restores background processes from the conversation record.
func (t *Thread) restoreBackgroundProcesses(processes []tooltypes.BackgroundProcess) {
	for _, process := range processes {
		if osutil.IsProcessAlive(process.PID) {
			if restoredProcess, err := osutil.ReattachProcess(process); err == nil {
				t.State.AddBackgroundProcess(restoredProcess)
			}
		}
	}
}

// Helper functions

// loadCustomConfiguration loads custom models and pricing from config.
// It processes presets first, then applies custom overrides if provided.
func loadCustomConfiguration(config llmtypes.Config) (map[string]string, map[string]llmtypes.ModelPricing) {
	customModels := make(map[string]string)
	customPricing := make(map[string]llmtypes.ModelPricing)

	// Determine which preset to use
	presetName := ""
	if config.OpenAI == nil {
		// No OpenAI config at all, use default preset
		presetName = "openai"
	} else if config.OpenAI.Preset != "" {
		// Explicit preset specified
		presetName = config.OpenAI.Preset
	} else {
		// OpenAI config exists but no preset
		// Check if we have custom models/pricing, if not, use default preset
		if config.OpenAI.Models == nil && config.OpenAI.Pricing == nil {
			presetName = "openai" // Default preset when no custom config
		}
	}

	// Load preset if one was determined
	if presetName != "" {
		presetModels, presetPricing := loadPreset(presetName)
		for model, category := range presetModels {
			customModels[model] = category
		}
		for model, pricing := range presetPricing {
			customPricing[model] = pricing
		}
	}

	// Override with custom configuration if provided
	if config.OpenAI != nil {
		if config.OpenAI.Models != nil {
			// Map reasoning models
			for _, model := range config.OpenAI.Models.Reasoning {
				customModels[model] = "reasoning"
			}
			// Map non-reasoning models
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
	useCodex := config.OpenAI != nil && config.OpenAI.Preset == "codex"

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

	// Add error logging middleware
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
	apiKeyEnvVar := "OPENAI_API_KEY"
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		apiKeyEnvVar = config.OpenAI.APIKeyEnvVar
	}

	apiKey := os.Getenv(apiKeyEnvVar)
	if apiKey == "" {
		return nil, errors.Errorf("%s environment variable is required", apiKeyEnvVar)
	}

	log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key for Responses API")

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	// Add base URL if configured
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.OpenAI.BaseURL))
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

// loadPreset loads a built-in preset configuration for popular providers.
func loadPreset(presetName string) (map[string]string, map[string]llmtypes.ModelPricing) {
	switch presetName {
	case "openai":
		return loadPresetFromConfig(openaipreset.Models, openaipreset.Pricing)
	case "codex":
		return loadPresetFromConfig(codexpreset.Models, codexpreset.Pricing)
	default:
		return nil, nil
	}
}

// loadPresetFromConfig converts preset model and pricing configurations into the internal format.
func loadPresetFromConfig(presetModels llmtypes.CustomModels, presetPricing llmtypes.CustomPricing) (map[string]string, map[string]llmtypes.ModelPricing) {
	models := make(map[string]string)
	pricing := make(map[string]llmtypes.ModelPricing)

	// Map reasoning models
	for _, model := range presetModels.Reasoning {
		models[model] = "reasoning"
	}
	// Map non-reasoning models
	for _, model := range presetModels.NonReasoning {
		models[model] = "non-reasoning"
	}

	// Load pricing
	for model, p := range presetPricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         p.Input,
			CachedInput:   p.CachedInput,
			Output:        p.Output,
			ContextWindow: p.ContextWindow,
		}
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

// isReasoningModelDynamic checks if a model supports reasoning using the preset configuration.
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

// StreamMessages parses raw messages into streamable format for conversation streaming.
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var items []StoredInputItem
	if err := json.Unmarshal(rawMessages, &items); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling input items")
	}

	var streamable []StreamableMessage

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
		}
	}

	return result, nil
}

// EnablePersistence enables or disables conversation persistence.
func (t *Thread) EnablePersistence(ctx context.Context, enabled bool) {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	t.Persisted = enabled

	if enabled && t.Store == nil {
		store, err := conversations.GetConversationStore(ctx)
		if err != nil {
			logger.G(ctx).WithError(err).Error("Error initializing conversation store")
			t.Persisted = false
			return
		}
		t.Store = store
	}

	if enabled && t.Store != nil && t.LoadConversation != nil {
		t.LoadConversation(ctx)
	}
}

// isInvalidPreviousResponseIDError checks if an error is related to an invalid previous_response_id.
// This can happen when the server-side conversation state has expired or been deleted.
func isInvalidPreviousResponseIDError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for common error messages related to invalid previous_response_id
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
