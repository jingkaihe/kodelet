// Package openai provides a client implementation for interacting with OpenAI and OpenAI-compatible AI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing.
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/ide"
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
	// ReasoningModels lists OpenAI models that support reasoning capabilities.
	// These arrays are now managed by the preset system but kept for backward compatibility
	// with the IsReasoningModel and IsOpenAIModel functions.
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
	// NonReasoningModels lists standard OpenAI models without reasoning capabilities.
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

// IsReasoningModel checks if the given model supports reasoning capabilities.
func IsReasoningModel(model string) bool {
	return slices.Contains(ReasoningModels, model)
}

// IsOpenAIModel checks if the given model is a valid OpenAI model (reasoning or non-reasoning).
func IsOpenAIModel(model string) bool {
	return slices.Contains(ReasoningModels, model) || slices.Contains(NonReasoningModels, model)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		statusCode := apiErr.HTTPStatusCode
		return statusCode >= 400 && statusCode < 600
	}

	var httpErr *openai.RequestError
	if errors.As(err, &httpErr) {
		return true
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return false
}

// isReasoningModelDynamic checks if a model supports reasoning using custom configuration
func (t *Thread) isReasoningModelDynamic(model string) bool {
	// Use custom models if configured
	if t.customModels != nil {
		return slices.Contains(t.customModels.Reasoning, model)
	}

	// Fall back to hardcoded check
	return IsReasoningModel(model)
}

// getPricing returns the pricing information for a model, checking custom pricing first
func (t *Thread) getPricing(model string) (llmtypes.ModelPricing, bool) {
	// Check custom pricing first
	if t.customPricing != nil {
		if pricing, ok := t.customPricing[model]; ok {
			return pricing, true
		}
	}

	// No custom pricing found, return empty pricing
	return llmtypes.ModelPricing{}, false
}

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package
type ConversationStore = conversations.ConversationStore

// Thread implements the Thread interface using OpenAI's API
type Thread struct {
	client                 *openai.Client
	config                 llmtypes.Config
	reasoningEffort        string
	state                  tooltypes.State
	messages               []openai.ChatCompletionMessage
	usage                  *llmtypes.Usage
	conversationID         string
	summary                string
	isPersisted            bool
	store                  ConversationStore
	mu                     sync.Mutex
	conversationMu         sync.Mutex
	toolResults            map[string]tooltypes.StructuredToolResult
	customModels           *llmtypes.CustomModels
	customPricing          llmtypes.CustomPricing
	useCopilot             bool
	subagentContextFactory llmtypes.SubagentContextFactory
	ideStore               *ide.Store // IDE context store (nil if IDE mode disabled)
}

// Provider returns the provider name for this thread.
func (t *Thread) Provider() string {
	return "openai"
}

// NewOpenAIThread creates a new thread with OpenAI's API
func NewOpenAIThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*Thread, error) {
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

		// Validate API key early
		if os.Getenv(apiKeyEnvVar) == "" {
			return nil, errors.Errorf("%s environment variable is required", apiKeyEnvVar)
		}

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

	var ideStore *ide.Store
	if config.IDE && !config.IsSubAgent {
		store, err := ide.NewIDEStore()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create IDE store")
		}
		ideStore = store
	}

	return &Thread{
		client:                 client,
		config:                 config,
		reasoningEffort:        reasoningEffort,
		conversationID:         convtypes.GenerateID(),
		isPersisted:            false,
		usage:                  &llmtypes.Usage{},
		toolResults:            make(map[string]tooltypes.StructuredToolResult),
		customModels:           customModels,
		customPricing:          customPricing,
		useCopilot:             useCopilot,
		subagentContextFactory: subagentContextFactory,
		ideStore:               ideStore,
	}, nil
}

// SetState sets the state for the thread
func (t *Thread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *Thread) GetState() tooltypes.State {
	return t.state
}

// AddUserMessage adds a user message with optional images to the thread
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
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
func (t *Thread) SendMessage(
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

	if !t.config.IsSubAgent && t.ideStore != nil {
		if err := t.processIDEContext(ctx, handler); err != nil {
			return "", errors.Wrap(err, "failed to process IDE context")
		}
	}

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

	// Add initial system message if it doesn't exist
	if len(t.messages) == 0 || t.messages[0].Role != openai.ChatMessageRoleSystem {
		systemMessage := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "", // Will be set in OUTER loop
		}

		// Insert system message at the beginning
		t.messages = append([]openai.ChatCompletionMessage{systemMessage}, t.messages...)
	}

	turnCount := 0
	maxTurns := max(opt.MaxTurns, 0)

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

			// Get relevant contexts from state and regenerate system prompt
			var contexts map[string]string
			if t.state != nil {
				contexts = t.state.DiscoverContexts()
			}
			var systemPrompt string
			if t.config.IsSubAgent {
				systemPrompt = sysprompt.SubAgentPrompt(model, t.config, contexts)
			} else {
				systemPrompt = sysprompt.SystemPrompt(model, t.config, contexts)
			}

			// Update system message content
			if len(t.messages) > 0 && t.messages[0].Role == openai.ChatMessageRoleSystem {
				t.messages[0].Content = systemPrompt
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
func (t *Thread) processMessageExchange(
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

	if !t.config.IsSubAgent {
		if err := t.processPendingFeedback(ctx, &requestParams, handler); err != nil {
			return "", false, errors.Wrap(err, "failed to process pending feedback")
		}
	}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", model),
		attribute.Int("max_tokens", maxTokens),
	)

	// Record start time for usage logging
	apiStartTime := time.Now()

	// Make the API request with retry logic
	response, err := t.createChatCompletionWithRetry(ctx, requestParams)
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

		runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)
		output := tools.RunTool(runToolCtx, t.state, toolCall.Function.Name, toolCall.Function.Arguments)

		// Use CLI rendering for consistent output formatting
		structuredResult := output.StructuredData()
		registry := renderers.NewRendererRegistry()
		renderedOutput := registry.Render(structuredResult)
		handler.HandleToolResult(toolCall.Function.Name, renderedOutput)

		t.SetStructuredToolResult(toolCall.ID, structuredResult)

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

func (t *Thread) processIDEContext(ctx context.Context, handler llmtypes.MessageHandler) error {
	ideContext, err := t.ideStore.ReadContext(t.conversationID)
	if err != nil && !errors.Is(err, ide.ErrContextNotFound) {
		return errors.Wrap(err, "failed to read IDE context")
	}

	if ideContext != nil {
		logger.G(ctx).WithFields(map[string]any{
			"open_files_count":  len(ideContext.OpenFiles),
			"has_selection":     ideContext.Selection != nil,
			"diagnostics_count": len(ideContext.Diagnostics),
		}).Info("processing IDE context")

		ideContextPrompt := ide.FormatContextPrompt(ideContext)
		if ideContextPrompt != "" {
			t.AddUserMessage(ctx, ideContextPrompt)
			handler.HandleText(fmt.Sprintf("📋 IDE Context: %d files, %d diagnostics",
				len(ideContext.OpenFiles), len(ideContext.Diagnostics)))
		}

		if err := t.ideStore.ClearContext(t.conversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear IDE context, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared IDE context")
		}
	}

	return nil
}

func (t *Thread) processPendingFeedback(ctx context.Context, requestParams *openai.ChatCompletionRequest, handler llmtypes.MessageHandler) error {
	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		return errors.Wrap(err, "failed to create feedback store")
	}

	pendingFeedback, err := feedbackStore.ReadPendingFeedback(t.conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to read pending feedback")
	}

	if len(pendingFeedback) > 0 {
		logger.G(ctx).WithField("feedback_count", len(pendingFeedback)).Info("processing pending feedback messages")

		for i, fbMsg := range pendingFeedback {
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

		if err := feedbackStore.ClearPendingFeedback(t.conversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear pending feedback, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared pending feedback")
		}
	}

	return nil
}

func (t *Thread) createChatCompletionWithRetry(ctx context.Context, requestParams openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var response openai.ChatCompletionResponse
	var originalErrors []error // Store all errors for better context

	retryConfig := t.config.Retry

	initialDelay := time.Duration(retryConfig.InitialDelay) * time.Millisecond
	maxDelay := time.Duration(retryConfig.MaxDelay) * time.Millisecond

	var delayType retry.DelayTypeFunc
	switch retryConfig.BackoffType {
	case "fixed":
		delayType = retry.FixedDelay
	case "exponential":
		fallthrough
	default:
		delayType = retry.BackOffDelay
	}

	err := retry.Do(
		func() error {
			var apiErr error
			response, apiErr = t.client.CreateChatCompletion(ctx, requestParams)
			if apiErr != nil {
				originalErrors = append(originalErrors, apiErr)
			}
			return apiErr
		},
		retry.RetryIf(isRetryableError),
		retry.Attempts(uint(retryConfig.Attempts)),
		retry.Delay(initialDelay),
		retry.DelayType(delayType),
		retry.MaxDelay(maxDelay),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			logger.G(ctx).WithError(err).WithField("attempt", n+1).WithField("max_attempts", retryConfig.Attempts).Warn("retrying OpenAI API call")
		}),
	)

	if err != nil && len(originalErrors) > 0 {
		return response, errors.Wrapf(err, "all %d retry attempts failed, original errors: %v", len(originalErrors), originalErrors)
	}

	return response, err
}

func (t *Thread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

func (t *Thread) updateUsage(usage openai.Usage, model string) {
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

// getLastAssistantMessageText extracts text content from the most recent assistant message
func (t *Thread) getLastAssistantMessageText() (string, error) {
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

// shouldAutoCompact checks if auto-compact should be triggered based on context window utilization
func (t *Thread) shouldAutoCompact(compactRatio float64) bool {
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
func (t *Thread) CompactContext(ctx context.Context) error {
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
func (t *Thread) GetUsage() llmtypes.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.usage
}

// NewSubAgent creates a new subagent thread that shares the parent's client and configuration.
func (t *Thread) NewSubAgent(_ context.Context, config llmtypes.Config) llmtypes.Thread {
	// Create subagent thread reusing the parent's client instead of creating a new one
	thread := &Thread{
		client:                 t.client, // Reuse parent's client
		config:                 config,
		reasoningEffort:        config.ReasoningEffort, // Use config's reasoning effort
		conversationID:         convtypes.GenerateID(),
		isPersisted:            false,                    // subagent is not persisted
		usage:                  t.usage,                  // Share usage tracking with parent
		customModels:           t.customModels,           // Share custom models configuration
		customPricing:          t.customPricing,          // Share custom pricing configuration
		useCopilot:             t.useCopilot,             // Share Copilot usage with parent
		subagentContextFactory: t.subagentContextFactory, // Propagate the injected function
	}

	return thread
}

// ShortSummary generates a concise summary of the conversation using a faster model.
func (t *Thread) ShortSummary(ctx context.Context) string {
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

// GetConfig returns the configuration of the thread
func (t *Thread) GetConfig() llmtypes.Config {
	return t.config
}

// GetConversationID returns the current conversation ID
func (t *Thread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *Thread) SetConversationID(id string) {
	t.conversationID = id
}

// IsPersisted returns whether this thread is being persisted
func (t *Thread) IsPersisted() bool {
	return t.isPersisted
}

// GetMessages returns the current messages in the thread
func (t *Thread) GetMessages() ([]llmtypes.Message, error) {
	result := make([]llmtypes.Message, 0, len(t.messages))

	for _, msg := range t.messages {
		// Skip system messages
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}

		role := msg.Role
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
func (t *Thread) EnablePersistence(ctx context.Context, enabled bool) {
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
	if enabled && t.store != nil {
		t.loadConversation(ctx)
	}
}

// createMessageSpan creates and configures a tracing span for message handling
func (t *Thread) createMessageSpan(
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
func (t *Thread) finalizeMessageSpan(span trace.Span, err error) {
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
func (t *Thread) processImage(imagePath string) (*openai.ChatMessagePart, error) {
	// Only allow HTTPS URLs for security
	if strings.HasPrefix(imagePath, "https://") {
		return t.processImageURL(imagePath)
	}
	if strings.HasPrefix(imagePath, "http://") {
		// Explicitly reject HTTP URLs for security
		return nil, fmt.Errorf("only HTTPS URLs are supported for security: %s", imagePath)
	}
	if filePath, ok := strings.CutPrefix(imagePath, "file://"); ok {
		// Remove file:// prefix and process as file
		return t.processImageFile(filePath)
	}
	// Treat as a local file path
	return t.processImageFile(imagePath)
}

// processImageURL creates an image part from an HTTPS URL
func (t *Thread) processImageURL(url string) (*openai.ChatMessagePart, error) {
	// Validate URL format (HTTPS only)
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("only HTTPS URLs are supported for security: %s", url)
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
func (t *Thread) processImageFile(filePath string) (*openai.ChatMessagePart, error) {
	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("image file not found: %s", filePath)
	}

	// Determine media type from file extension first
	mediaType, err := getImageMediaType(filepath.Ext(filePath))
	if err != nil {
		return nil, fmt.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", filepath.Ext(filePath))
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file info")
	}
	if fileInfo.Size() > MaxImageFileSize {
		return nil, fmt.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), MaxImageFileSize)
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
func (t *Thread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.toolResults[toolCallID] = result
}

// GetStructuredToolResults returns all structured tool results
func (t *Thread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		return make(map[string]tooltypes.StructuredToolResult)
	}
	result := make(map[string]tooltypes.StructuredToolResult)
	maps.Copy(result, t.toolResults)
	return result
}

// SetStructuredToolResults replaces all structured tool results with the provided map.
func (t *Thread) SetStructuredToolResults(results map[string]tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if results == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	} else {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
		maps.Copy(t.toolResults, results)
	}
}
