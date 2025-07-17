package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

var (
	// These arrays are now managed by the preset system but kept for backward compatibility
	// with the IsReasoningModel and IsOpenAIModel functions
	ReasoningModels = []string{
		"o1",
		"o1-pro",
		"o1-mini",
		"o3",
		"o3-pro",
		"o3-mini",
		"o3-deep-research",
		"o4-mini",
		"o4-mini-deep-research",
	}
	NonReasoningModels = []string{
		"gpt-4.1",
		"gpt-4.1-mini",
		"gpt-4.1-nano",
		"gpt-4.5-preview",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4o-audio-preview",
		"gpt-4o-realtime-preview",
		"gpt-4o-mini-audio-preview",
		"gpt-4o-mini-realtime-preview",
		"gpt-4o-mini-search-preview",
		"gpt-4o-search-preview",
		"computer-use-preview",
		"gpt-image-1",
		"codex-mini-latest",
	}
)

// Constants for image processing
const (
	MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
	MaxImageCount    = 10              // Maximum 10 images per message
)

func IsReasoningModel(model string) bool {
	return slices.Contains(ReasoningModels, model)
}

func IsOpenAIModel(model string) bool {
	return slices.Contains(ReasoningModels, model) || slices.Contains(NonReasoningModels, model)
}

// isReasoningModelDynamic checks if a model supports reasoning using custom configuration
func (o *OpenAIThread) isReasoningModelDynamic(model string) bool {
	// Use custom models if configured
	if o.customModels != nil {
		return slices.Contains(o.customModels.Reasoning, model)
	}

	// Fall back to hardcoded check
	return IsReasoningModel(model)
}

// getPricing returns the pricing information for a model, checking custom pricing first
func (o *OpenAIThread) getPricing(model string) (llmtypes.ModelPricing, bool) {
	// Check custom pricing first
	if o.customPricing != nil {
		if pricing, ok := o.customPricing[model]; ok {
			return pricing, true
		}
	}

	// No custom pricing found, return empty pricing
	return llmtypes.ModelPricing{}, false
}

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package
type ConversationStore = conversations.ConversationStore

// OpenAIThread implements the Thread interface using OpenAI's API
type OpenAIThread struct {
	client          *openai.Client
	config          llmtypes.Config
	reasoningEffort string // low, medium, high to determine token allocation
	state           tooltypes.State
	messages        []openai.ChatCompletionMessage
	usage           *llmtypes.Usage
	conversationID  string
	summary         string
	isPersisted     bool
	store           ConversationStore
	mu              sync.Mutex
	conversationMu  sync.Mutex
	toolResults     map[string]tooltypes.StructuredToolResult // Maps tool_call_id to structured result
	customModels    *llmtypes.CustomModels                    // Custom model configuration
	customPricing   llmtypes.CustomPricing                    // Custom pricing configuration
	useCopilot      bool                                      // Whether this thread uses GitHub Copilot
}

func (t *OpenAIThread) Provider() string {
	return "openai"
}

// NewOpenAIThread creates a new thread with OpenAI's API
func NewOpenAIThread(config llmtypes.Config) *OpenAIThread {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = "gpt-4.1" // Default to GPT-4.1
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	reasoningEffort := config.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "medium" // Default reasoning effort
	}

	// Validate custom configuration
	if err := validateCustomConfiguration(config); err != nil {
		// For now, we'll log the error and continue with defaults
		// In the future, we could return an error from this function
		fmt.Printf("Warning: OpenAI configuration validation failed: %v\n", err)
	}

	// Initialize client configuration
	var clientConfig openai.ClientConfig
	var useCopilot bool
	ctx := context.Background()
	logger := logger.G(ctx)

	// Check if Copilot usage is requested via flag
	if config.UseCopilot {
		// Verify Copilot credentials exist
		copilotCredsExists, _ := auth.GetCopilotCredentialsExists()
		if !copilotCredsExists {
			logger.Error("use-copilot flag set but no GitHub Copilot credentials found, run 'kodelet copilot-login'")
			// Fall back to OpenAI API key
			apiKeyEnvVar := GetAPIKeyEnvVar(config)
			apiKey := os.Getenv(apiKeyEnvVar)
			clientConfig = openai.DefaultConfig(apiKey)
			useCopilot = false
		} else {
			copilotToken, err := auth.CopilotAccessToken(ctx)
			if err != nil {
				logger.WithError(err).Error("failed to get copilot access token despite credentials existing")
				// Fall back to OpenAI API key
				apiKeyEnvVar := GetAPIKeyEnvVar(config)
				apiKey := os.Getenv(apiKeyEnvVar)
				clientConfig = openai.DefaultConfig(apiKey)
				useCopilot = false
			} else {
				logger.Debug("using GitHub Copilot token")
				// Create custom HTTP client with Copilot transport
				copilotTransport := auth.NewCopilotTransport(copilotToken)
				clientConfig = openai.DefaultConfig("") // No API key needed
				clientConfig.HTTPClient = &http.Client{
					Transport: copilotTransport,
				}
				useCopilot = true
			}
		}
	} else {
		// Use OpenAI API key
		apiKeyEnvVar := GetAPIKeyEnvVar(config)
		logger.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key")
		apiKey := os.Getenv(apiKeyEnvVar)
		clientConfig = openai.DefaultConfig(apiKey)
		useCopilot = false
	}

	// Check for custom base URL (environment variable takes precedence)
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		clientConfig.BaseURL = baseURL
	} else if config.OpenAI != nil {
		// Check preset first, then custom base URL
		if config.OpenAI.Preset != "" {
			if presetBaseURL := getPresetBaseURL(config.OpenAI.Preset); presetBaseURL != "" {
				clientConfig.BaseURL = presetBaseURL
			}
		}
		if config.OpenAI.BaseURL != "" {
			clientConfig.BaseURL = config.OpenAI.BaseURL // Override preset
		}
	} else if useCopilot {
		// Only set Copilot base URL if no other base URL is configured
		clientConfig.BaseURL = "https://api.githubcopilot.com"
	}

	client := openai.NewClientWithConfig(clientConfig)

	// Load custom models and pricing if available
	customModels, customPricing := loadCustomConfiguration(config)

	return &OpenAIThread{
		client:          client,
		config:          config,
		reasoningEffort: reasoningEffort,
		conversationID:  convtypes.GenerateID(),
		isPersisted:     false,
		usage:           &llmtypes.Usage{}, // must be initialized to avoid nil pointer dereference
		toolResults:     make(map[string]tooltypes.StructuredToolResult),
		customModels:    customModels,
		customPricing:   customPricing,
		useCopilot:      useCopilot,
	}
}

// SetState sets the state for the thread
func (t *OpenAIThread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *OpenAIThread) GetState() tooltypes.State {
	return t.state
}

// AddUserMessage adds a user message with optional images to the thread
func (t *OpenAIThread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	contentParts := []openai.ChatMessagePart{}

	// Validate image count
	if len(imagePaths) > MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), MaxImageCount, MaxImageCount)
		imagePaths = imagePaths[:MaxImageCount]
	}

	// Process images and add them as content parts
	for _, imagePath := range imagePaths {
		imagePart, err := t.processImage(imagePath)
		if err != nil {
			logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
			continue
		}
		contentParts = append(contentParts, *imagePart)
	}
	contentParts = append(contentParts, openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeText,
		Text: message,
	})

	t.messages = append(t.messages, openai.ChatCompletionMessage{
		Role:         openai.ChatMessageRoleUser,
		MultiContent: contentParts,
	})
}

// SendMessage sends a message to the LLM and processes the response
func (t *OpenAIThread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	// Check if tracing is enabled and wrap the handler
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.createMessageSpan(ctx, tracer, message, opt)
	defer t.finalizeMessageSpan(span, err)

	var originalMessages []openai.ChatCompletionMessage
	if opt.NoSaveConversation {
		originalMessages = make([]openai.ChatCompletionMessage, len(t.messages))
		copy(originalMessages, t.messages)
	}

	// Add user message with images if provided
	if len(opt.Images) > 0 {
		t.AddUserMessage(ctx, message, opt.Images...)
	} else {
		t.AddUserMessage(ctx, message)
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

	// Add system message if it doesn't exist
	if len(t.messages) == 0 || t.messages[0].Role != openai.ChatMessageRoleSystem {
		var systemPrompt string
		if t.config.IsSubAgent {
			systemPrompt = sysprompt.SubAgentPrompt(model, t.config)
		} else {
			systemPrompt = sysprompt.SystemPrompt(model, t.config)
		}

		systemMessage := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		}

		// Insert system message at the beginning
		t.messages = append([]openai.ChatCompletionMessage{systemMessage}, t.messages...)
	}

	// Main interaction loop for handling tool calls
	turnCount := 0
	maxTurns := opt.MaxTurns
	if maxTurns < 0 {
		maxTurns = 0 // treat negative as no limit
	}

OUTER:
	for {
		select {
		case <-ctx.Done():
			logger.G(ctx).Info("stopping kodelet.llm.openai")
			break OUTER
		default:
			// Check turn limit (0 means no limit)
			logger.G(ctx).WithField("turn_count", turnCount).WithField("max_turns", maxTurns).Debug("checking turn limit")

			if maxTurns > 0 && turnCount >= maxTurns {
				logger.G(ctx).
					WithField("turn_count", turnCount).
					WithField("max_turns", maxTurns).
					Warn("reached maximum turn limit, stopping interaction")
				break OUTER
			}

			// Check if auto-compact should be triggered before each exchange
			if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
				logger.G(ctx).WithField("context_utilization", float64(t.GetUsage().CurrentContextWindow)/float64(t.GetUsage().MaxContextWindow)).Info("triggering auto-compact")
				err := t.CompactContext(ctx)
				if err != nil {
					logger.G(ctx).WithError(err).Error("failed to auto-compact context")
				} else {
					logger.G(ctx).Info("auto-compact completed successfully")
				}
			}

			var exchangeOutput string
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, model, maxTokens, opt)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logger.G(ctx).Info("Request to OpenAI cancelled, stopping kodelet.llm.openai")
					// Remove the last tool message from the messages if it exists
					if len(t.messages) > 0 && isToolResultMessage(t.messages[len(t.messages)-1]) {
						t.messages = t.messages[:len(t.messages)-1]
					}
					break OUTER
				}
				return "", err
			}

			// Increment turn count after each exchange
			turnCount++

			// Update finalOutput with the most recent output
			finalOutput = exchangeOutput

			// If no tools were used, we're done
			if !toolsUsed {
				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.messages = originalMessages
	}

	// Save conversation state after completing the interaction
	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		t.SaveConversation(saveCtx, true)
	}

	if !t.config.IsSubAgent {
		// only main agent can signal done
		handler.HandleDone()
	}
	return finalOutput, nil
}

// isToolResultMessage checks if a message is a tool result message
func isToolResultMessage(msg openai.ChatCompletionMessage) bool {
	return msg.Role == openai.ChatMessageRoleTool
}

// processMessageExchange handles a single message exchange with the LLM, including
// preparing message parameters, making the API call, and processing the response
func (t *OpenAIThread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model string,
	maxTokens int,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	var finalOutput string

	// Prepare completion parameters
	requestParams := openai.ChatCompletionRequest{
		Model:     model,
		Messages:  t.messages,
		MaxTokens: maxTokens,
	}

	if t.isReasoningModelDynamic(model) {
		if t.reasoningEffort != "none" {
			requestParams.ReasoningEffort = t.reasoningEffort
		}
		requestParams.MaxTokens = 0
	}

	// Add tool definitions if tool use is enabled
	if !opt.NoToolUse {
		availableTools := t.tools(opt)
		if len(availableTools) > 0 {
			requestParams.Tools = tools.ToOpenAITools(availableTools)
			requestParams.ToolChoice = "auto"
		}
	}

	// Check for pending feedback messages if this is not a subagent
	if !t.config.IsSubAgent && t.conversationID != "" {
		func() {
			// Use a separate function to ensure feedback processing doesn't break the main flow
			defer func() {
				if r := recover(); r != nil {
					logger.G(ctx).WithField("panic", r).Error("panic occurred while processing feedback")
				}
			}()

			feedbackStore, err := feedback.NewFeedbackStore()
			if err != nil {
				logger.G(ctx).WithError(err).Warn("failed to create feedback store, continuing without feedback")
				return
			}

			pendingFeedback, err := feedbackStore.ReadPendingFeedback(t.conversationID)
			if err != nil {
				logger.G(ctx).WithError(err).Warn("failed to read pending feedback, continuing without feedback")
				return
			}

			if len(pendingFeedback) > 0 {
				logger.G(ctx).WithField("feedback_count", len(pendingFeedback)).Info("processing pending feedback messages")

				// Convert feedback messages to OpenAI messages and append to requestParams
				for i, fbMsg := range pendingFeedback {
					// Add some basic validation
					if fbMsg.Content == "" {
						logger.G(ctx).WithField("message_index", i).Warn("skipping empty feedback message")
						continue
					}

					userMessage := openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: fbMsg.Content,
					}
					requestParams.Messages = append(requestParams.Messages, userMessage)
					handler.HandleText(fmt.Sprintf("🗣️ User feedback: %s", fbMsg.Content))
				}

				// Clear the feedback now that we've processed it
				if err := feedbackStore.ClearPendingFeedback(t.conversationID); err != nil {
					logger.G(ctx).WithError(err).Warn("failed to clear pending feedback, may be processed again")
				} else {
					logger.G(ctx).Debug("successfully cleared pending feedback")
				}
			}
		}()
	}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", model),
		attribute.Int("max_tokens", maxTokens),
	)

	// Record start time for usage logging
	apiStartTime := time.Now()

	// Make the API request
	response, err := t.client.CreateChatCompletion(ctx, requestParams)
	if err != nil {
		return "", false, errors.Wrap(err, "error sending message to OpenAI")
	}

	// Record API call completion
	telemetry.AddEvent(ctx, "api_call_complete",
		attribute.Int("prompt_tokens", response.Usage.PromptTokens),
		attribute.Int("completion_tokens", response.Usage.CompletionTokens),
	)

	// Update usage tracking
	t.updateUsage(response.Usage, model)

	// Process the response
	if len(response.Choices) == 0 {
		return "", false, errors.New("no response choices returned from OpenAI")
	}

	// Add the assistant response to history
	assistantMessage := response.Choices[0].Message
	t.messages = append(t.messages, assistantMessage)

	// Extract text content
	content := assistantMessage.Content
	if content != "" {
		handler.HandleText(content)
		finalOutput = content
	}

	thinking := assistantMessage.ReasoningContent
	if thinking != "" {
		handler.HandleThinking(thinking)
	}

	// Check for tool calls
	toolCalls := assistantMessage.ToolCalls
	if len(toolCalls) == 0 {
		// Log structured LLM usage when no tool calls are made (main agent only)
		if !t.config.IsSubAgent && !opt.DisableUsageLog {
			usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, response.Usage.CompletionTokens)
		}
		return finalOutput, false, nil
	}

	// Process tool calls
	for _, toolCall := range toolCalls {
		handler.HandleToolUse(toolCall.Function.Name, toolCall.Function.Arguments)

		// For tracing, add tool execution event
		telemetry.AddEvent(ctx, "tool_execution_start",
			attribute.String("tool_name", toolCall.Function.Name),
		)

		// Execute the tool
		runToolCtx := t.WithSubAgent(ctx, handler, opt.CompactRatio, opt.DisableAutoCompact)
		output := tools.RunTool(runToolCtx, t.state, toolCall.Function.Name, toolCall.Function.Arguments)

		// Use CLI rendering for consistent output formatting
		structuredResult := output.StructuredData()
		registry := renderers.NewRendererRegistry()
		renderedOutput := registry.Render(structuredResult)
		handler.HandleToolResult(toolCall.Function.Name, renderedOutput)

		// Store the structured result for this tool call
		t.SetStructuredToolResult(toolCall.ID, structuredResult)

		// For tracing, add tool execution completion event
		telemetry.AddEvent(ctx, "tool_execution_complete",
			attribute.String("tool_name", toolCall.Function.Name),
			attribute.String("result", output.AssistantFacing()),
		)

		// Add tool result to messages for next API call
		logger.G(ctx).
			WithField("tool_name", toolCall.Function.Name).
			WithField("result", output.AssistantFacing()).
			Debug("Adding tool result to messages")

		t.messages = append(t.messages, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    output.AssistantFacing(),
			ToolCallID: toolCall.ID,
		})
	}

	// Log structured LLM usage after all content processing is complete (main agent only)
	if !t.config.IsSubAgent && !opt.DisableUsageLog {
		usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, response.Usage.CompletionTokens)
	}

	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		t.SaveConversation(ctx, false)
	}
	return finalOutput, true, nil
}

func (t *OpenAIThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

func (t *OpenAIThread) updateUsage(usage openai.Usage, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Track usage statistics
	t.usage.InputTokens += usage.PromptTokens
	t.usage.OutputTokens += usage.CompletionTokens

	// Calculate costs based on model pricing (use dynamic pricing method)
	pricing, found := t.getPricing(model)
	if !found {
		// If no pricing found, use default GPT-4.1 pricing as fallback
		pricing = llmtypes.ModelPricing{
			Input:         0.000002,
			CachedInput:   0.0000005,
			Output:        0.000008,
			ContextWindow: 1047576,
		}
	}

	// Calculate individual costs
	t.usage.InputCost += float64(usage.PromptTokens) * pricing.Input
	t.usage.OutputCost += float64(usage.CompletionTokens) * pricing.Output

	t.usage.CurrentContextWindow = usage.PromptTokens + usage.CompletionTokens
	t.usage.MaxContextWindow = pricing.ContextWindow
}

func (t *OpenAIThread) NewSubAgent(ctx context.Context) llmtypes.Thread {
	config := t.config
	config.IsSubAgent = true

	// Create subagent thread reusing the parent's client instead of creating a new one
	thread := &OpenAIThread{
		client:          t.client, // Reuse parent's client
		config:          config,
		reasoningEffort: t.reasoningEffort, // Reuse parent's reasoning effort
		conversationID:  convtypes.GenerateID(),
		isPersisted:     false,           // subagent is not persisted
		usage:           t.usage,         // Share usage tracking with parent
		customModels:    t.customModels,  // Share custom models configuration
		customPricing:   t.customPricing, // Share custom pricing configuration
		useCopilot:      t.useCopilot,    // Share Copilot usage with parent
	}

	thread.SetState(tools.NewBasicState(ctx, tools.WithSubAgentTools(), tools.WithExtraMCPTools(t.state.MCPTools())))

	return thread
}

func (t *OpenAIThread) WithSubAgent(ctx context.Context, handler llmtypes.MessageHandler, compactRatio float64, disableAutoCompact bool) context.Context {
	subAgent := t.NewSubAgent(ctx)
	ctx = context.WithValue(ctx, llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:             subAgent,
		MessageHandler:     handler,
		CompactRatio:       compactRatio,
		DisableAutoCompact: disableAutoCompact,
	})
	return ctx
}

// getLastAssistantMessageText extracts text content from the most recent assistant message
func (t *OpenAIThread) getLastAssistantMessageText() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.messages) == 0 {
		return "", errors.New("no messages found")
	}

	// Find the last assistant message
	var messageText string
	for i := len(t.messages) - 1; i >= 0; i-- {
		msg := t.messages[i]
		if msg.Role == openai.ChatMessageRoleAssistant {
			messageText = msg.Content
			break
		}
	}

	if messageText == "" {
		return "", errors.New("no text content found in assistant message")
	}

	return messageText, nil
}

func (t *OpenAIThread) ShortSummary(ctx context.Context) string {
	// Temporarily disable persistence during summarization
	t.isPersisted = false
	defer func() {
		t.isPersisted = true
	}()

	// Use a faster model for summarization as it's a simpler task
	_, err := t.SendMessage(ctx, prompts.ShortSummaryPrompt, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
		UseWeakModel:       true,
		NoToolUse:          true,
		DisableAutoCompact: true, // Prevent auto-compact during summarization
		DisableUsageLog:    true, // Don't log usage for internal summary operations
		// Note: Not using NoSaveConversation so we can access the assistant response
	})
	if err != nil {
		return err.Error()
	}

	// Get the summary from the last assistant message
	summary, err := t.getLastAssistantMessageText()
	if err != nil {
		return err.Error()
	}

	return summary
}

// shouldAutoCompact checks if auto-compact should be triggered based on context window utilization
func (t *OpenAIThread) shouldAutoCompact(compactRatio float64) bool {
	if compactRatio <= 0.0 || compactRatio > 1.0 {
		return false
	}

	usage := t.GetUsage()
	if usage.MaxContextWindow == 0 {
		return false
	}

	utilizationRatio := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
	return utilizationRatio >= compactRatio
}

// CompactContext performs comprehensive context compacting by creating a detailed summary
func (t *OpenAIThread) CompactContext(ctx context.Context) error {
	// Temporarily disable persistence during compacting
	wasPersistedOriginal := t.isPersisted
	t.isPersisted = false
	defer func() {
		t.isPersisted = wasPersistedOriginal
	}()

	// Use the strong model for comprehensive compacting (opposite of ShortSummary)
	_, err := t.SendMessage(ctx, prompts.CompactPrompt, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
		UseWeakModel:       false, // Use strong model for comprehensive compacting
		NoToolUse:          true,
		DisableAutoCompact: true, // Prevent recursion
		DisableUsageLog:    true, // Don't log usage for internal compact operations
		// Note: Not using NoSaveConversation so we can access the assistant response
	})
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	// Get the compact summary from the last assistant message
	compactSummary, err := t.getLastAssistantMessageText()
	if err != nil {
		return errors.Wrap(err, "failed to get compact summary from assistant message")
	}

	// Replace the conversation history with the compact summary
	t.mu.Lock()
	defer t.mu.Unlock()

	t.messages = []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: compactSummary,
		},
	}

	// Clear stale tool results - they reference tool calls that no longer exist
	t.toolResults = make(map[string]tooltypes.StructuredToolResult)

	// Get state reference while under mutex protection
	state := t.state

	// Clear file access tracking to start fresh with context retrieval
	if state != nil {
		state.SetFileLastAccess(make(map[string]time.Time))
	}

	return nil
}

// GetUsage returns the current token usage for the thread
func (t *OpenAIThread) GetUsage() llmtypes.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.usage
}

// GetConversationID returns the current conversation ID
func (t *OpenAIThread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *OpenAIThread) SetConversationID(id string) {
	t.conversationID = id
}

// IsPersisted returns whether this thread is being persisted
func (t *OpenAIThread) IsPersisted() bool {
	return t.isPersisted
}

// GetMessages returns the current messages in the thread
func (t *OpenAIThread) GetMessages() ([]llmtypes.Message, error) {
	result := make([]llmtypes.Message, 0, len(t.messages))

	for _, msg := range t.messages {
		// Skip system messages
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}

		role := string(msg.Role)
		content := msg.Content

		// Handle tool calls
		if len(msg.ToolCalls) > 0 {
			toolCallContent, _ := json.Marshal(msg.ToolCalls)
			content = string(toolCallContent)
		}

		result = append(result, llmtypes.Message{
			Role:    role,
			Content: content,
		})
	}

	return result, nil
}

// EnablePersistence enables conversation persistence for this thread
func (t *OpenAIThread) EnablePersistence(ctx context.Context, enabled bool) {
	t.isPersisted = enabled

	// Initialize the store if enabling persistence and it's not already initialized
	if enabled && t.store == nil {
		store, err := conversations.GetConversationStore(ctx)
		if err != nil {
			// Log the error but continue without persistence
			fmt.Printf("Error initializing conversation store: %v\n", err)
			t.isPersisted = false
			return
		}
		t.store = store
	}

	// If enabling persistence and there's an existing conversation ID,
	// try to load it from the store
	if enabled && t.conversationID != "" && t.store != nil {
		t.loadConversation(ctx)
	}
}

// createMessageSpan creates and configures a tracing span for message handling
func (t *OpenAIThread) createMessageSpan(
	ctx context.Context,
	tracer trace.Tracer,
	message string,
	opt llmtypes.MessageOpt,
) (context.Context, trace.Span) {
	attributes := []attribute.KeyValue{
		attribute.String("model", t.config.Model),
		attribute.Int("max_tokens", t.config.MaxTokens),
		attribute.Int("weak_model_max_tokens", t.config.WeakModelMaxTokens),
		attribute.Bool("use_weak_model", opt.UseWeakModel),
		attribute.Bool("is_sub_agent", t.config.IsSubAgent),
		attribute.String("conversation_id", t.conversationID),
		attribute.Bool("is_persisted", t.isPersisted),
		attribute.Int("message_length", len(message)),
		attribute.String("reasoning_effort", t.reasoningEffort),
		attribute.Bool("use_copilot", t.useCopilot),
	}

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// finalizeMessageSpan records final metrics and status to the span before ending it
func (t *OpenAIThread) finalizeMessageSpan(span trace.Span, err error) {
	// Record usage metrics after completion
	usage := t.GetUsage()
	span.SetAttributes(
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
		attribute.Float64("cost.total", usage.TotalCost()),
		attribute.Int("context_window.current", usage.CurrentContextWindow),
		attribute.Int("context_window.max", usage.MaxContextWindow),
	)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetStatus(codes.Ok, "")
		span.AddEvent("message_processing_completed")
	}
	span.End()
}

// processImage converts an image path/URL to an OpenAI ChatMessagePart
func (t *OpenAIThread) processImage(imagePath string) (*openai.ChatMessagePart, error) {
	// Only allow HTTPS URLs for security
	if strings.HasPrefix(imagePath, "https://") {
		return t.processImageURL(imagePath)
	} else if strings.HasPrefix(imagePath, "http://") {
		// Explicitly reject HTTP URLs for security
		return nil, errors.New(fmt.Sprintf("only HTTPS URLs are supported for security: %s", imagePath))
	} else if strings.HasPrefix(imagePath, "file://") {
		// Remove file:// prefix and process as file
		filePath := strings.TrimPrefix(imagePath, "file://")
		return t.processImageFile(filePath)
	} else {
		// Treat as a local file path
		return t.processImageFile(imagePath)
	}
}

// processImageURL creates an image part from an HTTPS URL
func (t *OpenAIThread) processImageURL(url string) (*openai.ChatMessagePart, error) {
	// Validate URL format (HTTPS only)
	if !strings.HasPrefix(url, "https://") {
		return nil, errors.New(fmt.Sprintf("only HTTPS URLs are supported for security: %s", url))
	}

	part := &openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL:    url,
			Detail: openai.ImageURLDetailAuto, // Use auto detail as default
		},
	}
	return part, nil
}

// processImageFile creates an image part from a local file
func (t *OpenAIThread) processImageFile(filePath string) (*openai.ChatMessagePart, error) {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, errors.New(fmt.Sprintf("image file not found: %s", filePath))
	}

	// Determine media type from file extension first
	mediaType, err := getImageMediaType(filepath.Ext(filePath))
	if err != nil {
		return nil, errors.New(fmt.Sprintf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", filepath.Ext(filePath)))
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file info")
	}
	if fileInfo.Size() > MaxImageFileSize {
		return nil, errors.New(fmt.Sprintf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), MaxImageFileSize))
	}

	// Read and encode the file
	imageData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read image file")
	}

	// Encode to base64
	base64Data := base64.StdEncoding.EncodeToString(imageData)

	// Create data URL with proper MIME type
	dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)

	part := &openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL:    dataURL,
			Detail: openai.ImageURLDetailAuto, // Use auto detail as default
		},
	}
	return part, nil
}

// getImageMediaType returns the MIME type for supported image formats
func getImageMediaType(ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".png":
		return "image/png", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", errors.New("unsupported format")
	}
}

// SetStructuredToolResult stores the structured result for a tool call
func (t *OpenAIThread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.toolResults[toolCallID] = result
}

// GetStructuredToolResults returns all structured tool results
func (t *OpenAIThread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		return make(map[string]tooltypes.StructuredToolResult)
	}
	// Return a copy to avoid race conditions
	result := make(map[string]tooltypes.StructuredToolResult)
	for k, v := range t.toolResults {
		result[k] = v
	}
	return result
}

// SetStructuredToolResults sets all structured tool results (for loading from conversation)
func (t *OpenAIThread) SetStructuredToolResults(results map[string]tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if results == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	} else {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
		for k, v := range results {
			t.toolResults[k] = v
		}
	}
}
