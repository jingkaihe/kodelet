// Package openai provides a client implementation for interacting with OpenAI and OpenAI-compatible AI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

var (
	// ReasoningModels lists OpenAI models that support reasoning capabilities.
	// These arrays are now managed by platform defaults but kept for backward compatibility
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

// Thread implements the Thread interface using OpenAI's API.
// It embeds base.Thread to inherit common functionality.
type Thread struct {
	*base.Thread                                   // Embedded base thread for shared functionality
	client          *openai.Client                 // OpenAI API client
	messages        []openai.ChatCompletionMessage // OpenAI-specific message format
	summary         string                         // Conversation summary
	reasoningEffort string                         // Reasoning effort for o1/o3 models
	customModels    *llmtypes.CustomModels         // Custom model configuration
	customPricing   llmtypes.CustomPricing         // Custom pricing configuration
	useCopilot      bool                           // Whether using GitHub Copilot
}

// Provider returns the provider name for this thread.
func (t *Thread) Provider() string {
	return "openai"
}

func resolveClientBaseURL(config llmtypes.Config, useCopilot bool) string {
	if useCopilot {
		if configuredBaseURL := GetConfiguredBaseURL(config); configuredBaseURL != "" {
			return configuredBaseURL
		}
		return "https://api.githubcopilot.com"
	}

	return GetBaseURL(config)
}

// NewOpenAIThread creates a new thread with OpenAI's API
func NewOpenAIThread(config llmtypes.Config) (*Thread, error) {
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
	log := logger.G(ctx)

	// Check if Copilot usage is requested via flag
	if config.UseCopilot {
		// Verify Copilot credentials exist
		copilotCredsExists, _ := auth.GetCopilotCredentialsExists()
		if !copilotCredsExists {
			log.Error("use-copilot flag set but no GitHub Copilot credentials found, run 'kodelet copilot-login'")
			// Fall back to OpenAI API key
			apiKeyEnvVar := GetAPIKeyEnvVar(config)
			apiKey := os.Getenv(apiKeyEnvVar)
			clientConfig = openai.DefaultConfig(apiKey)
			useCopilot = false
		} else {
			copilotToken, err := auth.CopilotAccessToken(ctx)
			if err != nil {
				log.WithError(err).Error("failed to get copilot access token despite credentials existing")
				// Fall back to OpenAI API key
				apiKeyEnvVar := GetAPIKeyEnvVar(config)
				apiKey := os.Getenv(apiKeyEnvVar)
				clientConfig = openai.DefaultConfig(apiKey)
				useCopilot = false
			} else {
				log.Debug("using GitHub Copilot token")
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
		log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key")

		// Validate API key early
		if os.Getenv(apiKeyEnvVar) == "" {
			return nil, errors.Errorf("%s environment variable is required", apiKeyEnvVar)
		}

		apiKey := os.Getenv(apiKeyEnvVar)
		clientConfig = openai.DefaultConfig(apiKey)
		useCopilot = false
	}

	if resolvedBaseURL := resolveClientBaseURL(config, useCopilot); resolvedBaseURL != "" {
		clientConfig.BaseURL = resolvedBaseURL
	}

	client := openai.NewClientWithConfig(clientConfig)

	// Load custom models and pricing if available
	customModels, customPricing := loadCustomConfiguration(config)

	conversationID := convtypes.GenerateID()
	hookTrigger := base.CreateHookTrigger(context.Background(), config, conversationID)

	// Create the base thread with shared functionality
	baseThread := base.NewThread(config, conversationID, hookTrigger)

	thread := &Thread{
		Thread:          baseThread,
		client:          client,
		reasoningEffort: reasoningEffort,
		customModels:    customModels,
		customPricing:   customPricing,
		useCopilot:      useCopilot,
	}

	// Set the LoadConversation callback for provider-specific loading
	baseThread.LoadConversation = thread.loadConversation

	return thread, nil
}

// AddUserMessage adds a user message with optional images to the thread
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	contentParts := []openai.ChatMessagePart{}

	// Validate image count
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), base.MaxImageCount, base.MaxImageCount)
		imagePaths = imagePaths[:base.MaxImageCount]
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

	// Create span with OpenAI-specific attributes
	ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
		attribute.String("reasoning_effort", t.reasoningEffort),
		attribute.Bool("use_copilot", t.useCopilot),
	)
	defer func() {
		t.FinalizeMessageSpan(span, err)
	}()

	var originalMessages []openai.ChatCompletionMessage
	if opt.NoSaveConversation {
		originalMessages = make([]openai.ChatCompletionMessage, len(t.messages))
		copy(originalMessages, t.messages)
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
			if t.State != nil {
				contexts = t.State.DiscoverContexts()
			}
			systemPrompt := sysprompt.SystemPrompt(model, t.Config, contexts)

			// Update system message content
			if len(t.messages) > 0 && t.messages[0].Role == openai.ChatMessageRoleSystem {
				t.messages[0].Content = systemPrompt
			}

			// Check if auto-compact should be triggered before each exchange
			t.TryAutoCompact(ctx, opt.DisableAutoCompact, opt.CompactRatio, t.CompactContext)

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

			base.TriggerTurnEnd(ctx, t.HookTrigger, t, finalOutput, turnCount)

			// If no tools were used, check for hook follow-ups before stopping
			if !toolsUsed {
				if base.HandleAgentStopFollowUps(ctx, t.HookTrigger, t, handler) {
					continue OUTER
				}

				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.messages = originalMessages
	}

	// Save conversation state after completing the interaction
	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		// Skip LLM-based summary generation for subagent runs to avoid unnecessary API calls
		t.SaveConversation(saveCtx, !t.Config.IsSubAgent)
	}

	if !t.Config.IsSubAgent {
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

	if !t.Config.IsSubAgent {
		if err := t.processPendingSteer(ctx, &requestParams, handler); err != nil {
			return "", false, errors.Wrap(err, "failed to process pending steer")
		}
	}

	extraHeaders := t.getPromptCacheHeaders(opt)

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", model),
		attribute.Int("max_tokens", maxTokens),
	)

	// Record start time for usage logging
	apiStartTime := time.Now()

	// Check if handler supports streaming
	streamHandler, isStreamingHandler := handler.(llmtypes.StreamingMessageHandler)

	// Make the API request with retry logic (use streaming if handler supports it)
	response, err := t.createChatCompletionWithRetry(ctx, requestParams, streamHandler, isStreamingHandler, extraHeaders)
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

	// Extract text content (skip if streaming handler already processed it)
	content := assistantMessage.Content
	if content != "" {
		if !isStreamingHandler {
			handler.HandleText(content)
		}
		finalOutput = content
	}

	// Handle reasoning content (skip if streaming handler already processed it)
	thinking := assistantMessage.ReasoningContent
	if thinking != "" {
		if !isStreamingHandler {
			handler.HandleThinking(thinking)
		}
	}

	// Check for tool calls
	toolCalls := assistantMessage.ToolCalls
	if len(toolCalls) == 0 {
		// Log structured LLM usage when no tool calls are made (main agent only)
		if !t.Config.IsSubAgent && !opt.DisableUsageLog {
			usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, response.Usage.CompletionTokens)
		}
		return finalOutput, false, nil
	}

	// Process tool calls
	for _, toolCall := range toolCalls {
		handler.HandleToolUse(toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments)

		// For tracing, add tool execution event
		telemetry.AddEvent(ctx, "tool_execution_start",
			attribute.String("tool_name", toolCall.Function.Name),
		)

		toolExecution := base.ExecuteTool(
			ctx,
			t.HookTrigger,
			t,
			t.State,
			t.GetRecipeHooks(),
			t.RendererRegistry,
			toolCall.Function.Name,
			toolCall.Function.Arguments,
			toolCall.ID,
		)
		output := toolExecution.Result
		structuredResult := toolExecution.StructuredResult

		handler.HandleToolResult(toolCall.ID, toolCall.Function.Name, output)

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
	if !t.Config.IsSubAgent && !opt.DisableUsageLog {
		usage.LogLLMUsage(ctx, t.GetUsage(), model, apiStartTime, response.Usage.CompletionTokens)
	}

	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		t.SaveConversation(ctx, false)
	}
	return finalOutput, true, nil
}

func (t *Thread) processPendingSteer(ctx context.Context, requestParams *openai.ChatCompletionRequest, handler llmtypes.MessageHandler) error {
	steerStore, err := steer.NewSteerStore()
	if err != nil {
		return errors.Wrap(err, "failed to create steer store")
	}

	pendingSteer, err := steerStore.ReadPendingSteer(t.ConversationID)
	if err != nil {
		return errors.Wrap(err, "failed to read pending steer")
	}

	if len(pendingSteer) > 0 {
		logger.G(ctx).WithField("steer_count", len(pendingSteer)).Info("processing pending steer messages")

		for i, steerMsg := range pendingSteer {
			if steerMsg.Content == "" {
				logger.G(ctx).WithField("message_index", i).Warn("skipping empty steer message")
				continue
			}

			userMessage := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: steerMsg.Content,
			}
			requestParams.Messages = append(requestParams.Messages, userMessage)
			handler.HandleText(fmt.Sprintf("🗣️ User steering: %s", steerMsg.Content))
		}

		if err := steerStore.ClearPendingSteer(t.ConversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear pending steer, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared pending steer")
		}
	}

	return nil
}

func (t *Thread) createChatCompletionWithRetry(
	ctx context.Context,
	requestParams openai.ChatCompletionRequest,
	streamHandler llmtypes.StreamingMessageHandler,
	isStreamingHandler bool,
	extraHeaders map[string]string,
) (openai.ChatCompletionResponse, error) {
	var response openai.ChatCompletionResponse
	var originalErrors []error // Store all errors for better context

	retryConfig := t.Config.Retry

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
			client := t.client
			if len(extraHeaders) > 0 {
				client = t.chatClientWithHeaders(extraHeaders)
			}
			if isStreamingHandler {
				response, apiErr = t.createStreamingChatCompletionWithClient(ctx, requestParams, streamHandler, client)
			} else {
				response, apiErr = client.CreateChatCompletion(ctx, requestParams)
			}
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

func (t *Thread) createStreamingChatCompletionWithClient(
	ctx context.Context,
	requestParams openai.ChatCompletionRequest,
	handler llmtypes.StreamingMessageHandler,
	client *openai.Client,
) (openai.ChatCompletionResponse, error) {
	// Enable streaming and request usage info
	requestParams.Stream = true
	requestParams.StreamOptions = &openai.StreamOptions{
		IncludeUsage: true,
	}

	stream, err := client.CreateChatCompletionStream(ctx, requestParams)
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	defer stream.Close()

	// Accumulators for the full response
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var toolCalls []openai.ToolCall
	var usage openai.Usage
	var responseID string
	var model string
	var finishReason openai.FinishReason

	// Track if we've started text/thinking blocks
	textStarted := false
	reasoningStarted := false

	for {
		streamResponse, err := stream.Recv()
		if errors.Is(err, context.Canceled) {
			return openai.ChatCompletionResponse{}, err
		}
		if err != nil {
			// io.EOF indicates the stream has ended normally
			if errors.Is(err, io.EOF) {
				break
			}
			return openai.ChatCompletionResponse{}, err
		}

		// Capture response metadata
		if responseID == "" && streamResponse.ID != "" {
			responseID = streamResponse.ID
		}
		if model == "" && streamResponse.Model != "" {
			model = streamResponse.Model
		}

		// Handle usage from stream (sent at the end with StreamOptions.IncludeUsage)
		if streamResponse.Usage != nil {
			usage = *streamResponse.Usage
		}

		// Process each choice delta
		for _, choice := range streamResponse.Choices {
			delta := choice.Delta

			// Handle text content delta
			if delta.Content != "" {
				if !textStarted {
					// If reasoning was in progress, end it before starting text
					if reasoningStarted {
						handler.HandleThinkingBlockEnd()
					}
					textStarted = true
				}
				handler.HandleTextDelta(delta.Content)
				contentBuilder.WriteString(delta.Content)
			}

			// Handle reasoning content delta (for o1/o3 models)
			if delta.ReasoningContent != "" {
				if !reasoningStarted {
					reasoningStarted = true
					handler.HandleThinkingStart()
				}
				handler.HandleThinkingDelta(delta.ReasoningContent)
				reasoningBuilder.WriteString(delta.ReasoningContent)
			}

			// Handle tool calls - accumulate them
			if len(delta.ToolCalls) > 0 {
				for _, tc := range delta.ToolCalls {
					// Find or create the tool call entry
					if tc.Index == nil {
						logger.G(ctx).WithFields(map[string]any{
							"tool_call_id":  tc.ID,
							"function_name": tc.Function.Name,
						}).Warn("received tool call delta with nil index, skipping")
						continue
					}
					idx := *tc.Index
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, openai.ToolCall{})
					}
					if tc.ID != "" {
						toolCalls[idx].ID = tc.ID
					}
					if tc.Type != "" {
						toolCalls[idx].Type = tc.Type
					}
					if tc.Function.Name != "" {
						toolCalls[idx].Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						toolCalls[idx].Function.Arguments += tc.Function.Arguments
					}
				}
			}

			// Capture finish reason
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}

	// Signal end of content blocks
	// Only end thinking block if text didn't start (which would have already ended it)
	if reasoningStarted && !textStarted {
		handler.HandleThinkingBlockEnd()
	}
	if textStarted {
		handler.HandleContentBlockEnd()
	}

	// Reconstruct the full response
	response := openai.ChatCompletionResponse{
		ID:    responseID,
		Model: model,
		Choices: []openai.ChatCompletionChoice{
			{
				Index: 0,
				Message: openai.ChatCompletionMessage{
					Role:             openai.ChatMessageRoleAssistant,
					Content:          contentBuilder.String(),
					ReasoningContent: reasoningBuilder.String(),
					ToolCalls:        toolCalls,
				},
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}

	return response, nil
}

func (t *Thread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	return base.AvailableTools(t.State, opt.NoToolUse)
}

func (t *Thread) updateUsage(usage openai.Usage, model string) {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	cachedTokens := 0
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
	}

	nonCachedInput := usage.PromptTokens - cachedTokens
	if nonCachedInput < 0 {
		nonCachedInput = usage.PromptTokens
	}

	// Track usage statistics
	t.Usage.InputTokens += nonCachedInput
	t.Usage.OutputTokens += usage.CompletionTokens
	if cachedTokens > 0 {
		t.Usage.CacheReadInputTokens += cachedTokens
	}

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

	if nonCachedInput > 0 {
		t.Usage.InputCost += float64(nonCachedInput) * pricing.Input
	}
	if cachedTokens > 0 {
		t.Usage.CacheReadCost += float64(cachedTokens) * pricing.CachedInput
	}
	t.Usage.OutputCost += float64(usage.CompletionTokens) * pricing.Output

	t.Usage.CurrentContextWindow = usage.PromptTokens + usage.CompletionTokens
	t.Usage.MaxContextWindow = pricing.ContextWindow
}

func (t *Thread) runUtilityPrompt(ctx context.Context, prompt string, useWeakModel bool) (string, error) {
	return base.RunUtilityPrompt(ctx,
		func() (*Thread, error) {
			return NewOpenAIThread(t.GetConfig())
		},
		func(summaryThread *Thread) {
			summaryThread.messages = t.messages
		},
		prompt,
		useWeakModel,
	)
}

// SwapContext replaces the conversation history with a summary message.
// This implements the hooks.ContextSwapper interface.
func (t *Thread) SwapContext(_ context.Context, summary string) error {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	t.messages = []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: summary,
		},
	}

	t.FinalizeSwapContextLocked(summary)

	return nil
}

// CompactContext performs comprehensive context compacting by creating a detailed summary
func (t *Thread) CompactContext(ctx context.Context) error {
	return base.CompactContextWithSummary(ctx, fragments.LoadCompactPrompt, t.runUtilityPrompt, t.SwapContext)
}

// ShortSummary generates a concise summary of the conversation using a faster model.
func (t *Thread) ShortSummary(ctx context.Context) string {
	return base.GenerateShortSummary(
		ctx,
		prompts.ShortSummaryPrompt,
		t.runUtilityPrompt,
		func(err error) {
			logger.G(ctx).WithError(err).Error("failed to generate summary")
		},
	)
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

// processImage converts an image path/URL to an OpenAI ChatMessagePart
func (t *Thread) processImage(imagePath string) (*openai.ChatMessagePart, error) {
	if base.IsInsecureHTTPURL(imagePath) {
		// Explicitly reject HTTP URLs for security
		return nil, fmt.Errorf("only HTTPS URLs are supported for security: %s", imagePath)
	}

	return base.RouteImageInput(
		imagePath,
		t.processImageURL,
		t.processImageDataURL, // Data URLs can be passed directly to OpenAI.
		t.processImageFile,    // Treat remaining inputs as local file paths.
	)
}

// processImageURL creates an image part from an HTTPS URL
func (t *Thread) processImageURL(url string) (*openai.ChatMessagePart, error) {
	// Validate URL format (HTTPS only).
	if err := base.ValidateHTTPSImageURL(url); err != nil {
		return nil, err
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

// processImageDataURL creates an image part from a data URL
func (t *Thread) processImageDataURL(dataURL string) (*openai.ChatMessagePart, error) {
	// Validate data URL format.
	if err := base.ValidateDataURLPrefix(dataURL); err != nil {
		return nil, err
	}

	// OpenAI accepts data URLs directly in the URL field
	part := &openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL:    dataURL,
			Detail: openai.ImageURLDetailAuto,
		},
	}
	return part, nil
}

// processImageFile creates an image part from a local file
func (t *Thread) processImageFile(filePath string) (*openai.ChatMessagePart, error) {
	dataURL, err := base.ReadImageFileAsDataURL(filePath)
	if err != nil {
		return nil, err
	}

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
	return base.ImageMIMETypeFromExtension(ext)
}

func (t *Thread) getPromptCacheHeaders(opt llmtypes.MessageOpt) map[string]string {
	if !opt.PromptCache || t.Config.OpenAI == nil || !t.Config.OpenAI.ManualCache {
		return nil
	}

	if t.ConversationID == "" {
		return nil
	}

	return map[string]string{
		"x-session-affinity": t.ConversationID,
	}
}

func (t *Thread) chatClientWithHeaders(extraHeaders map[string]string) *openai.Client {
	if len(extraHeaders) == 0 {
		return t.client
	}

	clientConfig := t.buildClientConfig()
	if clientConfig.HTTPClient == nil {
		return t.client
	}

	clientConfig.HTTPClient = &headerInjectingHTTPClient{
		base:    clientConfig.HTTPClient,
		headers: extraHeaders,
	}

	return openai.NewClientWithConfig(clientConfig)
}

type headerInjectingHTTPClient struct {
	base    openai.HTTPDoer
	headers map[string]string
}

func (h *headerInjectingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())
	clonedReq.Header = req.Header.Clone()

	for key, value := range h.headers {
		clonedReq.Header.Set(key, value)
	}

	return h.base.Do(clonedReq)
}

func (t *Thread) buildClientConfig() openai.ClientConfig {
	if t.useCopilot {
		ctx := context.Background()
		copilotToken, err := auth.CopilotAccessToken(ctx)
		if err == nil {
			clientConfig := openai.DefaultConfig("")
			clientConfig.HTTPClient = &http.Client{Transport: auth.NewCopilotTransport(copilotToken)}

			clientConfig.BaseURL = resolveClientBaseURL(t.Config, true)

			return clientConfig
		}
	}

	apiKeyEnvVar := GetAPIKeyEnvVar(t.Config)
	apiKey := os.Getenv(apiKeyEnvVar)
	clientConfig := openai.DefaultConfig(apiKey)
	if resolvedBaseURL := resolveClientBaseURL(t.Config, false); resolvedBaseURL != "" {
		clientConfig.BaseURL = resolvedBaseURL
	}

	return clientConfig
}
