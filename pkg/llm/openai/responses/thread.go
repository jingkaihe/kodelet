// Package responses implements the OpenAI Responses API client.
// The Responses API is OpenAI's next-generation API designed for building AI agents,
// offering native support for multi-turn conversations, built-in tool calling,
// and automatic conversation state management.
package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/xai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
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

	// inputItems holds the conversation history as Responses API input items
	inputItems []responses.ResponseInputItemUnionParam

	// reasoningEffort controls the reasoning depth for o-series models
	reasoningEffort shared.ReasoningEffort

	// customModels contains provider-specific model aliases
	customModels map[string]string

	// customPricing contains provider-specific pricing information
	customPricing map[string]llmtypes.ModelPricing

	// lastResponseID stores the ID of the last response for multi-turn conversations
	lastResponseID string

	// summary stores a short summary of the conversation for persistence
	summary string
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

	// Determine API key
	apiKeyEnvVar := "OPENAI_API_KEY"
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		apiKeyEnvVar = config.OpenAI.APIKeyEnvVar
	}

	apiKey := os.Getenv(apiKeyEnvVar)
	if apiKey == "" {
		return nil, errors.Errorf("%s environment variable is required", apiKeyEnvVar)
	}

	log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key for Responses API")

	// Build client options
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	// Check for custom base URL
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.OpenAI.BaseURL))
	}

	// Create the OpenAI client
	client := openai.NewClient(opts...)

	// Determine reasoning effort from environment or default
	reasoningEffort := shared.ReasoningEffortMedium
	if effort := os.Getenv("OPENAI_REASONING_EFFORT"); effort != "" {
		reasoningEffort = shared.ReasoningEffort(effort)
	}

	// Load custom models and pricing
	customModels, customPricing := loadCustomConfiguration(config)

	thread := &Thread{
		Thread:          baseThread,
		client:          &client,
		inputItems:      make([]responses.ResponseInputItemUnionParam, 0),
		reasoningEffort: reasoningEffort,
		customModels:    customModels,
		customPricing:   customPricing,
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
		t.inputItems = append(t.inputItems, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: contentParts},
			},
		})
	} else {
		// Simple text message
		t.inputItems = append(t.inputItems, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(message)},
			},
		})
	}
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
	if opt.NoSaveConversation {
		originalInputItems = make([]responses.ResponseInputItemUnionParam, len(t.inputItems))
		copy(originalInputItems, t.inputItems)
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

				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.inputItems = originalInputItems
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

	// Build request parameters
	params := responses.ResponseNewParams{
		Model:        model,
		Input:        responses.ResponseNewParamsInputUnion{OfInputItemList: t.inputItems},
		Instructions: param.NewOpt(systemPrompt),
		Tools:        tools,
	}

	// Set max output tokens if specified
	if maxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(maxTokens))
	}

	// Add reasoning configuration for o-series models
	if isReasoningModel(model) && t.reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: t.reasoningEffort,
		}
	}

	log.WithField("model", model).
		WithField("max_tokens", maxTokens).
		WithField("is_reasoning", isReasoningModel(model)).
		Debug("sending request to OpenAI Responses API")

	// Use streaming API
	stream := t.client.Responses.NewStreaming(ctx, params)
	log.Debug("stream created, processing events")

	// Process stream events
	toolsUsed, err := t.processStream(ctx, stream, handler)
	if err != nil {
		return "", false, err
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

	// Replace input items with compacted output
	t.inputItems = make([]responses.ResponseInputItemUnionParam, 0)
	for _, output := range resp.Output {
		// Convert output items back to input items
		outputJSON, err := json.Marshal(output)
		if err != nil {
			continue
		}
		var inputItem responses.ResponseInputItemUnionParam
		if err := json.Unmarshal(outputJSON, &inputItem); err != nil {
			continue
		}
		t.inputItems = append(t.inputItems, inputItem)
	}

	return nil
}

// ShortSummary generates a short summary of the conversation.
func (t *Thread) ShortSummary(_ context.Context) string {
	if len(t.inputItems) == 0 {
		return ""
	}

	// Use first user message as summary
	for _, item := range t.inputItems {
		if item.OfMessage != nil && item.OfMessage.Role == responses.EasyInputMessageRoleUser {
			content := ""
			if item.OfMessage.Content.OfString.Valid() {
				content = item.OfMessage.Content.OfString.Value
			} else if len(item.OfMessage.Content.OfInputItemContentList) > 0 {
				for _, part := range item.OfMessage.Content.OfInputItemContentList {
					if part.OfInputText != nil {
						content += part.OfInputText.Text
					}
				}
			}
			if len(content) > 100 {
				return content[:100] + "..."
			}
			return content
		}
	}

	return ""
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

	// Convert to storage format and serialize
	storedItems := toStoredItems(t.inputItems)
	inputItemsJSON, err := json.Marshal(storedItems)
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

	// Convert to SDK format
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

// loadPreset loads a built-in preset configuration for popular providers.
func loadPreset(presetName string) (map[string]string, map[string]llmtypes.ModelPricing) {
	switch presetName {
	case "openai":
		return loadOpenAIPreset()
	case "xai":
		return loadXAIGrokPreset()
	default:
		return nil, nil
	}
}

// loadOpenAIPreset loads the complete OpenAI configuration.
func loadOpenAIPreset() (map[string]string, map[string]llmtypes.ModelPricing) {
	models := make(map[string]string)
	pricing := make(map[string]llmtypes.ModelPricing)

	// Map reasoning models
	for _, model := range openaipreset.Models.Reasoning {
		models[model] = "reasoning"
	}
	// Map non-reasoning models
	for _, model := range openaipreset.Models.NonReasoning {
		models[model] = "non-reasoning"
	}

	// Load pricing
	for model, p := range openaipreset.Pricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         p.Input,
			CachedInput:   p.CachedInput,
			Output:        p.Output,
			ContextWindow: p.ContextWindow,
		}
	}

	return models, pricing
}

// loadXAIGrokPreset loads the complete xAI Grok configuration.
func loadXAIGrokPreset() (map[string]string, map[string]llmtypes.ModelPricing) {
	models := make(map[string]string)
	pricing := make(map[string]llmtypes.ModelPricing)

	// Map reasoning models
	for _, model := range xai.Models.Reasoning {
		models[model] = "reasoning"
	}
	// Map non-reasoning models
	for _, model := range xai.Models.NonReasoning {
		models[model] = "non-reasoning"
	}

	// Load pricing
	for model, p := range xai.Pricing {
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

// isReasoningModel checks if the model is an o-series reasoning model.
func isReasoningModel(model string) bool {
	// Check for o-series models (o1, o3, o4, etc.)
	if len(model) >= 2 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9' {
		return true
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
