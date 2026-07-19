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

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/copilotdefaults"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/steer"
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

	// summary stores a short summary of the conversation for persistence
	summary string

	// isCodex indicates if this thread is using Codex authentication
	// Some API parameters may not be supported by the Codex API
	isCodex bool
	// useCopilot indicates if this thread authenticates through GitHub Copilot.
	useCopilot bool
	// useWebSocket indicates whether Responses API websocket transport is enabled.
	useWebSocket          bool
	authorizer            auth.HTTPAuthorizer
	webSocket             responsesWebSocketStreamer
	webSocketContinuation responsesWebSocketContinuation

	processMessageExchangeFunc func(
		ctx context.Context,
		handler llmtypes.MessageHandler,
		model string,
		maxTokens int,
		systemPrompt string,
		opt llmtypes.MessageOpt,
	) (string, bool, bool, error)
	newStreamingFunc  func(context.Context, responses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
	processStreamFunc func(context.Context, *ssestream.Stream[responses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error)
}

// NewThread creates a new Responses API thread with the given configuration.
func NewThread(config llmtypes.Config) (*Thread, error) {
	log := logger.G(context.Background())
	if err := llmtypes.NormalizeReasoningConfig(&config); err != nil {
		return nil, err
	}
	if err := llmtypes.NormalizeOpenAITextVerbosity(&config); err != nil {
		return nil, err
	}

	if config.Provider == "" {
		config.Provider = "openai"
	}
	if config.Model == "" {
		config.Model = "gpt-5.5"
	}

	log.WithField("model", config.Model).Debug("creating OpenAI Responses API thread")

	conversationID := convtypes.GenerateID()

	// Create the base thread with shared functionality
	baseThread := base.NewThread(config, conversationID)

	// Build client options based on authentication mode
	opts, authInfo, err := buildClientOptions(config, log)
	if err != nil {
		return nil, err
	}

	// Create the OpenAI client
	client := openai.NewClient(opts...)

	// Determine reasoning effort from config or default
	reasoningEffort := shared.ReasoningEffort(strings.ToLower(strings.TrimSpace(config.ReasoningEffort)))
	if reasoningEffort == "" {
		reasoningEffort = shared.ReasoningEffortMedium
	}
	// Load custom models and pricing
	customModels, customPricing := loadCustomConfiguration(config)

	thread := &Thread{
		Thread:          baseThread,
		client:          &client,
		inputItems:      make([]responses.ResponseInputItemUnionParam, 0),
		storedItems:     make([]StoredInputItem, 0),
		reasoningEffort: reasoningEffort,
		customModels:    customModels,
		customPricing:   customPricing,
		isCodex:         authInfo.useCodex,
		useCopilot:      authInfo.useCopilot,
		useWebSocket:    shouldUseResponsesWebSocket(config),
		authorizer:      authInfo.authorizer,
	}
	if thread.useWebSocket && supportsResponsesWebSocket(config) {
		thread.webSocket = newResponsesWebSocketTransport(authInfo.baseURL)
	}
	thread.processMessageExchangeFunc = thread.processMessageExchange
	thread.newStreamingFunc = thread.client.Responses.NewStreaming
	thread.processStreamFunc = thread.processStream
	thread.compactFunc = thread.client.Responses.Compact
	thread.compactRawFunc = thread.compactRawJSON
	thread.compactWithSummaryFunc = func(ctx context.Context) error {
		return base.CompactContextWithSummary(ctx, thread.runUtilityPrompt, thread.SwapContext)
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

// Close releases the persistent Responses API WebSocket, if one was opened.
func (t *Thread) Close() error {
	if t == nil || t.webSocket == nil {
		return nil
	}
	t.webSocketContinuation.reset()
	return t.webSocket.Close()
}

// AddUserMessage adds a user message with optional images to the thread.
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	if goals.IsContextText(message) {
		if imageItem, ok := userImageInputItem(ctx, imagePaths); ok {
			t.addInputItem(imageItem, "")
		}
		inputItem := responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(message)},
			},
		}
		t.addInputItem(inputItem, message)
		return
	}

	var inputItem responses.ResponseInputItemUnionParam

	// Build content parts if we have images
	if len(imagePaths) > 0 {
		contentParts := userImageContentParts(ctx, imagePaths)

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

	t.addInputItem(inputItem, message)
}

func userImageInputItem(ctx context.Context, imagePaths []string) (responses.ResponseInputItemUnionParam, bool) {
	contentParts := userImageContentParts(ctx, imagePaths)
	if len(contentParts) == 0 {
		return responses.ResponseInputItemUnionParam{}, false
	}

	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: contentParts},
		},
	}, true
}

func userImageContentParts(ctx context.Context, imagePaths []string) responses.ResponseInputMessageContentListParam {
	// Validate image count
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images",
			len(imagePaths), base.MaxImageCount, base.MaxImageCount)
		imagePaths = imagePaths[:base.MaxImageCount]
	}

	contentParts := responses.ResponseInputMessageContentListParam{}
	for _, imagePath := range imagePaths {
		imagePart, err := processImage(imagePath)
		if err != nil {
			logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
			continue
		}
		contentParts = append(contentParts, imagePart)
	}
	return contentParts
}

func (t *Thread) addInputItem(inputItem responses.ResponseInputItemUnionParam, content string) {
	t.inputItems = append(t.inputItems, inputItem)
	rawItem, err := json.Marshal(inputItem)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to marshal OpenAI Responses user input item for persistence")
	}
	t.storedItems = append(t.storedItems, StoredInputItem{
		Type:    "message",
		Role:    "user",
		Content: content,
		RawItem: rawItem,
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
		attribute.String("platform", resolvePlatformName(t.Config)),
	)
	defer func() {
		t.FinalizeMessageSpan(span, err)
	}()

	var originalInputItems []responses.ResponseInputItemUnionParam
	if opt.NoSaveConversation {
		originalInputItems = make([]responses.ResponseInputItemUnionParam, len(t.inputItems))
		copy(originalInputItems, t.inputItems)
	}

	message, err = base.ProcessUserMessage(ctx, t, message)
	if err != nil {
		return "", err
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
	base.DispatchAgentStart(ctx, t)

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

			base.DispatchTurnStart(ctx, t, turnCount+1)

			// Get relevant contexts from state and regenerate system prompt
			var contexts map[string]string
			if t.State != nil {
				contexts = t.State.DiscoverContexts()
			}

			systemPrompt := base.ProcessSystemPrompt(ctx, t, sysprompt.SystemPrompt(model, t.Config, contexts))

			// Check if auto-compact should be triggered
			t.TryAutoCompact(ctx, t.CompactRatioOrDefault(opt.CompactRatio), t.CompactContext)

			exchangeOpt := opt.WithTurnInitiator(turnCount)

			logger.G(ctx).WithField("model", model).Debug("starting message exchange")
			processExchange := t.processMessageExchangeFunc
			if processExchange == nil {
				processExchange = t.processMessageExchange
			}
			exchangeOutput, toolsUsed, responseCompleted, err := processExchange(ctx, handler, model, maxTokens, systemPrompt, exchangeOpt)
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

			if !responseCompleted {
				return "", errors.New("response stream ended without response.completed event")
			}

			turnCount++
			finalOutput = exchangeOutput

			base.TriggerTurnEnd(ctx, t, finalOutput, turnCount)

			// If no tools were used, check for queued continuations before stopping
			if !toolsUsed {
				if base.HandleAgentStopFollowUps(ctx, t, handler) {
					continue OUTER
				}
				if (maxTurns == 0 || turnCount < maxTurns) && base.HandleGoalAutoContinuation(ctx, t, base.AvailableToolsForThread(t, t.State, opt.NoToolUse)) {
					continue OUTER
				}
				if (maxTurns == 0 || turnCount < maxTurns) && base.HasPendingSteer(ctx, t.ConversationID) {
					continue OUTER
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

	handler.HandleDone()

	return finalOutput, nil
}

// applyCodexRestrictions modifies request parameters for Codex API compatibility.
// The Codex API doesn't support max_output_tokens on this path, and Codex also
// expects runtime sections as developer messages rather than top-level
// instructions only.
// This method centralizes all Codex-specific parameter restrictions in one place.
func (t *Thread) applyCodexRestrictions(params *responses.ResponseNewParams) {
	if !t.isCodex {
		return
	}
	// Codex uses prompt_cache_key plus full input replay on this path, not
	// server-side stored conversation state.
	params.Store = param.NewOpt(false)
	params.MaxOutputTokens = param.Opt[int64]{}

	if t.State != nil {
		contexts := t.State.DiscoverContexts()
		promptCtx := sysprompt.BuildRuntimeContext(t.Config, contexts)

		renderer, err := sysprompt.ResolveRendererForConfig(t.Config)
		if err != nil {
			logger.G(context.Background()).WithError(err).Warn("failed to load custom sysprompt template for codex runtime sections, using default")
		}

		devMessages := sysprompt.RenderRuntimeSections(promptCtx, renderer)

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
) (string, bool, bool, error) {
	log := logger.G(ctx)
	textVerbosity, sendTextVerbosity, err := llmtypes.ConfiguredOpenAITextVerbosity(t.Config)
	if err != nil {
		return "", false, false, err
	}

	saveConversation := func() {
		if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
			t.SaveConversation(ctx, false)
		}
	}

	if err := t.processPendingSteer(ctx, handler); err != nil {
		return "", false, false, errors.Wrap(err, "failed to process pending steer")
	}

	// Build tools
	tools := buildToolsForThread(t, t.State, opt.NoToolUse)
	log.WithField("tool_count", len(tools)).Debug("built tools for request")

	// Keep a complete local input history for persistence, HTTP prompt caching, and
	// WebSocket reconnect recovery. The WebSocket path derives an incremental input
	// from this full request only while its connection-local continuation is valid.
	params := responses.ResponseNewParams{
		Model:          model,
		Input:          responses.ResponseNewParamsInputUnion{OfInputItemList: t.inputItems},
		Instructions:   param.NewOpt(systemPrompt),
		Tools:          tools,
		Store:          param.NewOpt(false),
		PromptCacheKey: param.NewOpt(t.ConversationID),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		},
	}
	if sendTextVerbosity {
		params.Text = responses.ResponseTextConfigParam{
			Verbosity: responses.ResponseTextConfigVerbosity(textVerbosity),
		}
	}
	applyGPT56PromptCacheOptions(&params, t.Config, model)

	if serviceTier := normalizeServiceTier(t.Config).WireValue(); serviceTier != "" {
		params.ServiceTier = responses.ResponseNewParamsServiceTier(serviceTier)
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
		reasoningEffort = openAIReasoningEffortForRequest(reasoningEffort)
		params.Reasoning = shared.ReasoningParam{
			Effort:  reasoningEffort,
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	// Apply Codex-specific restrictions (overrides unsupported params)
	t.applyCodexRestrictions(&params)

	requestOpts := t.requestOptions(opt)

	log.WithField("model", model).
		WithField("input_items", len(t.inputItems)).
		WithField("tool_count", len(tools)).
		WithField("is_codex", t.isCodex).
		Debug("sending request to Responses API")

	useWebSocket := t.useWebSocket && t.webSocket != nil
	var newStreaming func(context.Context, responses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
	if !useWebSocket {
		newStreaming = t.newStreamingFunc
		if newStreaming == nil {
			newStreaming = t.client.Responses.NewStreaming
		}
	}
	processStream := t.processStreamFunc
	processStreamHandlesNilStream := processStream != nil
	if processStream == nil {
		processStream = t.processStream
	}

	var newResponsesStream responsesStreamFactory
	closeResponsesStream := func(stream *ssestream.Stream[responses.ResponseStreamEventUnion]) error {
		if stream != nil {
			return stream.Close()
		}
		return nil
	}
	transportName := "https"
	if useWebSocket {
		transportName = "websocket"
		newResponsesStream = func(ctx context.Context, params responses.ResponseNewParams) (*responsesStreamAttempt, error) {
			stream, generation, err := t.webSocket.Stream(
				ctx,
				func(connectionGeneration uint64) responses.ResponseNewParams {
					return t.webSocketContinuation.prepare(params, connectionGeneration)
				},
				nil,
				t.authorizer,
			)
			if err != nil {
				t.webSocketContinuation.reset()
				return nil, errors.Wrap(err, "failed to create Responses API websocket stream")
			}
			return &responsesStreamAttempt{
				stream:                     stream,
				webSocketGeneration:        generation,
				fullWebSocketRequestParams: params,
			}, nil
		}
	} else {
		newResponsesStream = func(ctx context.Context, params responses.ResponseNewParams) (*responsesStreamAttempt, error) {
			stream := newStreaming(ctx, params, requestOpts...)
			if stream == nil && !processStreamHandlesNilStream {
				return nil, errors.New("failed to create Responses API stream")
			}
			if stream != nil {
				if err := stream.Err(); err != nil {
					return nil, err
				}
			}
			return &responsesStreamAttempt{stream: stream}, nil
		}
	}

	return t.processMessageExchangeWithStreamRetries(ctx, handler, model, params, tools, newResponsesStream, closeResponsesStream, processStream, opt, saveConversation, transportName)
}

func applyGPT56PromptCacheOptions(params *responses.ResponseNewParams, config llmtypes.Config, model string) {
	if resolvePlatformName(config) != defaultOpenAIPlatform || !isGPT56Model(model) {
		return
	}

	params.PromptCacheOptions = responses.ResponseNewParamsPromptCacheOptions{
		Mode: "implicit",
		Ttl:  "30m",
	}
}

func isGPT56Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-")
}

func openAIReasoningEffortForRequest(effort shared.ReasoningEffort) shared.ReasoningEffort {
	return shared.ReasoningEffort(strings.ToLower(strings.TrimSpace(string(effort))))
}

type responsesStreamAttempt struct {
	stream                     *ssestream.Stream[responses.ResponseStreamEventUnion]
	webSocketGeneration        uint64
	fullWebSocketRequestParams responses.ResponseNewParams
}

type responsesStreamFactory func(context.Context, responses.ResponseNewParams) (*responsesStreamAttempt, error)

func (t *Thread) processMessageExchangeWithStreamRetries(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model string,
	params responses.ResponseNewParams,
	tools []responses.ToolUnionParam,
	newResponsesStream responsesStreamFactory,
	closeResponsesStream func(*ssestream.Stream[responses.ResponseStreamEventUnion]) error,
	processStream func(context.Context, *ssestream.Stream[responses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error),
	opt llmtypes.MessageOpt,
	saveConversation func(),
	transportName string,
) (string, bool, bool, error) {
	log := logger.G(ctx)
	retryConfig := responsesStreamRetryConfig(t.Config)
	var finalOutput string
	var finalStreamResult processStreamResult
	pendingReasoningBeforeAttempt := t.pendingReasoning.String()

	err := retry.Do(
		func() error {
			resetPendingReasoning(&t.pendingReasoning, pendingReasoningBeforeAttempt)
			attemptParams := params
			attemptParams.Input = responses.ResponseNewParamsInputUnion{
				OfInputItemList: cloneResponsesInputItems(t.inputItems),
			}
			t.applyCodexRestrictions(&attemptParams)

			attempt, err := newResponsesStream(ctx, attemptParams)
			if err != nil {
				if !isRetryableResponsesStreamError(err) {
					return retry.Unrecoverable(err)
				}
				return err
			}

			log.WithField("transport", transportName).Debug("stream created, processing events")

			streamResult, err := processStream(ctx, attempt.stream, handler, model, opt)
			if closeErr := closeResponsesStream(attempt.stream); err == nil && closeErr != nil {
				err = errors.Wrap(closeErr, "failed to close Responses API stream")
			}
			if err == nil {
				if attempt.webSocketGeneration != 0 {
					t.webSocketContinuation.commit(
						attempt.webSocketGeneration,
						attempt.fullWebSocketRequestParams,
						streamResult,
					)
				}
				finalOutput = t.lastAssistantMessageText()
				finalStreamResult = streamResult
				return nil
			}

			if attempt.webSocketGeneration != 0 {
				t.webSocketContinuation.reset()
			}
			if !isRetryableResponsesStreamError(err) {
				return retry.Unrecoverable(err)
			}
			return err
		},
		retry.RetryIf(retry.IsRecoverable),
		retry.Attempts(uint(retryConfig.Attempts)),
		retry.Delay(time.Duration(retryConfig.InitialDelay)*time.Millisecond),
		retry.DelayType(responsesStreamRetryDelayType(retryConfig)),
		retry.MaxDelay(time.Duration(retryConfig.MaxDelay)*time.Millisecond),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			log.WithError(err).
				WithField("attempt", n+1).
				WithField("max_attempts", retryConfig.Attempts).
				WithField("transport", transportName).
				Warn("retrying Responses API stream request")
		}),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		logResponsesAPIRequestFailure(log, err, model, len(tools), len(t.inputItems))
		saveConversation()
		return "", false, false, err
	}

	saveConversation()
	return finalOutput, finalStreamResult.toolsUsed, finalStreamResult.responseCompleted, nil
}

func (t *Thread) lastAssistantMessageText() string {
	for i := len(t.inputItems) - 1; i >= 0; i-- {
		item := t.inputItems[i]
		if item.OfMessage != nil && item.OfMessage.Role == responses.EasyInputMessageRoleAssistant {
			if item.OfMessage.Content.OfString.Valid() {
				return item.OfMessage.Content.OfString.Value
			}
		}
	}
	return ""
}

func responsesStreamRetryConfig(config llmtypes.Config) llmtypes.RetryConfig {
	retryConfig := config.Retry
	if retryConfig.Attempts == 0 {
		retryConfig = llmtypes.DefaultRetryConfig
	}

	// Keep Responses stream behavior aligned with the OpenAI Go SDK default:
	// one initial attempt plus at most two retries.
	retryConfig.Attempts = min(max(retryConfig.Attempts, 1), 3)

	if retryConfig.InitialDelay <= 0 {
		retryConfig.InitialDelay = llmtypes.DefaultRetryConfig.InitialDelay
	}
	if retryConfig.MaxDelay <= 0 {
		retryConfig.MaxDelay = llmtypes.DefaultRetryConfig.MaxDelay
	}
	if retryConfig.BackoffType == "" {
		retryConfig.BackoffType = llmtypes.DefaultRetryConfig.BackoffType
	}

	return retryConfig
}

func responsesStreamRetryDelayType(retryConfig llmtypes.RetryConfig) retry.DelayTypeFunc {
	if retryConfig.BackoffType == "fixed" {
		return retry.FixedDelay
	}
	return retry.BackOffDelay
}

func resetPendingReasoning(builder *strings.Builder, value string) {
	builder.Reset()
	if value != "" {
		builder.WriteString(value)
	}
}

func isRetryableResponsesStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if !retry.IsRecoverable(err) {
		return false
	}

	var statusErr *websocketHandshakeStatusError
	if errors.As(err, &statusErr) {
		return isRetryableResponsesWebSocketHandshakeStatus(statusErr.statusCode, statusErr.body)
	}

	var eventErr *responsesWebSocketEventError
	if errors.As(err, &eventErr) {
		code := strings.ToLower(strings.TrimSpace(eventErr.code))
		switch code {
		case "websocket_connection_limit_reached", "previous_response_not_found":
			return true
		case "invalid_prompt", "context_length_exceeded", "insufficient_quota", "usage_not_included", "cyber_policy", "server_is_overloaded", "slow_down":
			return false
		}
		if eventErr.statusCode != 0 {
			return isRetryableResponsesHTTPStatus(eventErr.statusCode, code)
		}
		return true
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return isRetryableResponsesHTTPStatus(apiErr.StatusCode, apiErr.Code)
	}

	return true
}

func isRetryableResponsesHTTPStatus(statusCode int, errorCode string) bool {
	switch statusCode {
	case http.StatusBadRequest, http.StatusTooManyRequests:
		return false
	case http.StatusServiceUnavailable:
		code := strings.ToLower(strings.TrimSpace(errorCode))
		return code != "server_is_overloaded" && code != "slow_down"
	case 0:
		return true
	default:
		return statusCode >= http.StatusInternalServerError || statusCode == http.StatusRequestTimeout || statusCode == http.StatusConflict
	}
}

func isRetryableResponsesWebSocketHandshakeStatus(statusCode int, body string) bool {
	switch statusCode {
	case http.StatusBadRequest, http.StatusTooManyRequests:
		return false
	case http.StatusServiceUnavailable:
		code := responsesAPIErrorCodeFromBody(body)
		return code != "server_is_overloaded" && code != "slow_down"
	default:
		return true
	}
}

func responsesAPIErrorCodeFromBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(payload.Error.Code))
}

func logResponsesAPIRequestFailure(log *logrus.Entry, err error, model string, toolCount int, inputItemCount int) {
	log.WithError(err).
		WithField("model", model).
		WithField("tool_count", toolCount).
		WithField("input_items", inputItemCount).
		Error("API request failed")
}

func (t *Thread) processPendingSteer(ctx context.Context, handler llmtypes.MessageHandler) error {
	steerStore, err := steer.NewSteerStore(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create steer store")
	}
	defer steerStore.Close()

	pendingSteer, err := steerStore.Consume(ctx, t.ConversationID)
	if err != nil {
		return errors.Wrap(err, "failed to consume pending steer")
	}

	if len(pendingSteer) == 0 {
		return nil
	}

	logger.G(ctx).WithField("steer_count", len(pendingSteer)).Info("processing pending steer messages")

	for i, steerMsg := range pendingSteer {
		if steerMsg.Content == "" {
			logger.G(ctx).WithField("message_index", i).Warn("skipping empty steer message")
			continue
		}

		inputItem := pendingSteerInputItem(ctx, steerMsg)

		t.inputItems = append(t.inputItems, inputItem)

		rawItem, err := json.Marshal(inputItem)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to marshal steering input item for persistence")
		}

		t.storedItems = append(t.storedItems, StoredInputItem{
			Type:    "message",
			Role:    "user",
			Content: steerMsg.Content,
			RawItem: rawItem,
		})

		if userHandler, ok := handler.(llmtypes.UserMessageHandler); ok {
			userHandler.HandleUserMessage(steerMsg.Content, steerMsg.Images)
		} else {
			handler.HandleText(steer.FormatPendingNotice(steerMsg.Content, len(steerMsg.Images)))
		}
	}

	return nil
}

func pendingSteerInputItem(ctx context.Context, steerMsg steer.Message) responses.ResponseInputItemUnionParam {
	if len(steerMsg.Images) == 0 {
		return responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(steerMsg.Content)},
			},
		}
	}

	imagePaths := steerMsg.Images
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).
			WithField("image_count", len(imagePaths)).
			WithField("max_image_count", base.MaxImageCount).
			Warn("too many steering images provided; truncating")
		imagePaths = imagePaths[:base.MaxImageCount]
	}

	contentParts := make(responses.ResponseInputMessageContentListParam, 0, len(imagePaths)+1)
	for _, imagePath := range imagePaths {
		imagePart, err := processImage(imagePath)
		if err != nil {
			logger.G(ctx).
				WithError(err).
				WithField("image_path", imagePath).
				Warn("failed to process steering image")
			continue
		}
		contentParts = append(contentParts, imagePart)
	}
	contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
		OfInputText: &responses.ResponseInputTextParam{Text: steerMsg.Content},
	})

	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: contentParts},
		},
	}
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

	t.FinalizeSwapContextLocked(summary)

	return nil
}

// CompactContext compacts the conversation history. Only OpenAI and Codex are
// known to support the native Responses compact endpoint; other OpenAI-compatible
// providers use the in-harness summary compactor instead.
func (t *Thread) CompactContext(ctx context.Context) error {
	if len(t.inputItems) == 0 {
		return nil
	}

	compactWithSummary := t.compactWithSummaryFunc
	if compactWithSummary == nil {
		compactWithSummary = func(ctx context.Context) error {
			return base.CompactContextWithSummary(ctx, t.runUtilityPrompt, t.SwapContext)
		}
	}
	if !supportsNativeResponsesCompact(t.Config) {
		return compactWithSummary(ctx)
	}

	var contexts map[string]string
	if t.State != nil {
		contexts = t.State.DiscoverContexts()
	}

	systemPrompt := sysprompt.SystemPrompt(t.Config.Model, t.Config, contexts)

	compactParams := responses.ResponseCompactParams{
		Input: responses.ResponseCompactParamsInputUnion{
			OfResponseInputItemArray: t.inputItems,
		},
		Model:        responses.ResponseCompactParamsModel(t.Config.Model),
		Instructions: param.NewOpt(systemPrompt),
	}
	serviceTier := normalizeServiceTier(t.Config)
	if wireServiceTier := serviceTier.WireValue(); wireServiceTier != "" {
		compactParams.ServiceTier = responses.ResponseCompactParamsServiceTier(wireServiceTier)
	}

	compactOpts := []option.RequestOption{}
	if t.isCodex {
		compactOpts = append(compactOpts, option.WithHeader("Accept", "application/json"))
	}
	compactOpts = append(compactOpts, t.requestOptions(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent})...)

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
		// Fallback for tests that construct Thread manually without a client/raw compact function.
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

	// Account for compaction like any other model response, including cache reads,
	// cache writes, and their associated costs.
	t.updateUsage(resp.Usage, t.Config.Model, serviceTier)

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

	t.ResetContextStateLocked()
	pricing := t.getPricing(t.Config.Model)
	if t.Usage != nil {
		t.Usage.MaxContextWindow = pricing.ContextWindow
	}

	estimatedContext := 0
	for _, item := range newStoredItems {
		switch item.Type {
		case "message", "reasoning":
			estimatedContext += len(item.Content)
		case "function_call":
			estimatedContext += len(item.Name) + len(item.Arguments)
		case "function_call_output":
			estimatedContext += len(item.Output)
		case "web_search_call":
			estimatedContext += len(item.Content) + len(item.Action) + len(item.Status)
		case "compaction", "compaction_summary":
			estimatedContext += len(item.EncryptedContent)
		}
	}

	// Rough token estimate (~4 chars/token) with a small non-zero floor so future
	// auto-compact decisions are based on the compacted thread, not stale pre-compact usage.
	if t.Usage != nil {
		t.Usage.CurrentContextWindow = max(estimatedContext/4, 1)
	}

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
	case "web_search_call":
		search := output.AsWebSearchCall()
		item.CallID = search.ID
		item.Status = string(search.Status)
		item.Action = search.Action.Type
		details := webSearchDetailsFromAction(search.Action)
		switch search.Action.Type {
		case "open_page":
			item.Content = details.url
		case "find_in_page":
			item.Content = details.url
			item.Arguments = details.pattern
		default:
			item.Content = strings.Join(details.queries, ", ")
		}
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
func (t *Thread) ShortSummary(ctx context.Context) (string, error) {
	rawMessages, err := json.Marshal(t.storedItems)
	if err != nil {
		return "", err
	}

	toolResults := t.GetStructuredToolResults()
	messages, err := StreamMessages(rawMessages, toolResults)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		return "", nil
	}

	markdown := base.RenderMarkdownForSummary(conversationsFromResponses(messages), toolResults)

	return base.GenerateShortSummary(
		ctx,
		markdown,
		t.runUtilityPrompt,
	)
}

func conversationsFromResponses(msgs []StreamableMessage) []conversations.StreamableMessage {
	result := make([]conversations.StreamableMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = conversations.StreamableMessage{
			Kind:       msg.Kind,
			Role:       msg.Role,
			Content:    msg.Content,
			RawItem:    msg.RawItem,
			ToolName:   msg.ToolName,
			ToolCallID: msg.ToolCallID,
			Input:      msg.Input,
		}
	}
	return result
}

func rawMessagesForSummary(items []StoredInputItem) json.RawMessage {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil
	}
	return raw
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
	toolResults := t.GetStructuredToolResults()
	messages, err := StreamMessages(rawMessagesForSummary(t.storedItems), toolResults)
	if err != nil {
		return errors.Wrap(err, "failed to parse conversation for summary")
	}
	metadata := t.GetMetadata()
	summary := base.FirstUserMessageFallback(conversations.ApplyDisplayToStreamableMessages(conversationsFromResponses(messages), metadata))

	// Generate a new summary if requested and enabled; otherwise keep the first user message.
	if summarize {
		if t.Config.ConversationSummaryMode.UsesLLM() {
			generatedSummary, err := t.ShortSummary(ctx)
			if err != nil {
				logger.G(ctx).WithError(err).Error("failed to generate summary")
			} else if generatedSummary != "" {
				summary = generatedSummary
			}
		}
	}
	t.summary = summary

	// Serialize stored items directly (already built inline during streaming)
	inputItemsJSON, err := json.Marshal(t.storedItems)
	if err != nil {
		return errors.Wrap(err, "error marshaling input items")
	}

	// Build the conversation record
	metadata["model"] = t.Config.Model
	metadata["api_mode"] = "responses"
	metadata["platform"] = resolvePlatformName(t.Config)
	if serviceTier := normalizeServiceTier(t.Config); serviceTier != "" {
		metadata["service_tier"] = string(serviceTier)
	}
	if profile := strings.TrimSpace(t.Config.Profile); profile != "" {
		metadata["profile"] = profile
	}
	snapshotConfig := t.Config
	if strings.TrimSpace(snapshotConfig.Provider) == "" {
		snapshotConfig.Provider = "openai"
	}
	if snapshotConfig.OpenAI == nil {
		snapshotConfig.OpenAI = &llmtypes.OpenAIConfig{}
	}
	snapshotConfig.OpenAI.APIMode = llmtypes.OpenAIAPIModeResponses
	metadata, err = conversations.AddConfigSnapshot(metadata, snapshotConfig)
	if err != nil {
		return errors.Wrap(err, "failed to persist conversation config snapshot")
	}

	record := convtypes.ConversationRecord{
		ID:          t.ConversationID,
		CWD:         t.Config.WorkingDirectory,
		RawMessages: inputItemsJSON,
		Provider:    "openai",
		Usage:       *t.Usage,
		Metadata:    metadata,
		Summary:     t.summary,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ToolResults: toolResults,
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
	t.SetMetadata(record.Metadata)
	t.SetStructuredToolResults(record.ToolResults)
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
	case "responses":
		return llmtypes.OpenAIAPIModeResponses, true
	default:
		return "", false
	}
}

func normalizeServiceTier(config llmtypes.Config) llmtypes.OpenAIServiceTier {
	if config.OpenAI == nil {
		return ""
	}

	tier, ok := llmtypes.ParseOpenAIServiceTier(string(config.OpenAI.ServiceTier))
	if !ok {
		return ""
	}

	return tier
}

func shouldUseResponsesWebSocket(config llmtypes.Config) bool {
	if config.OpenAI != nil && config.OpenAI.WebSocketMode != nil {
		return *config.OpenAI.WebSocketMode
	}
	return true
}

func supportsResponsesWebSocket(config llmtypes.Config) bool {
	platform := resolvePlatformName(config)
	if platform != "openai" && platform != "codex" {
		return false
	}

	baseURL := strings.TrimRight(getBaseURL(config), "/")
	switch platform {
	case "openai":
		return baseURL == "" || strings.EqualFold(baseURL, strings.TrimRight(openaipreset.BaseURL, "/"))
	case "codex":
		return baseURL == "" || strings.EqualFold(baseURL, strings.TrimRight(codexpreset.BaseURL, "/"))
	default:
		return false
	}
}

func supportsNativeResponsesCompact(config llmtypes.Config) bool {
	platform := resolvePlatformName(config)
	return platform == "openai" || platform == "codex"
}

func getPlatformAPIKeyEnvVar(platform string) string {
	return openaipreset.APIKeyEnvVar
}

func getPlatformBaseURL(platform string) string {
	switch normalizePlatformName(platform) {
	case "codex":
		return codexpreset.BaseURL
	case "copilot":
		return auth.CopilotBaseURL
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
		platformModels, platformPricing := loadPlatformDefaultsForConfig(platformName, config)
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

type responsesAuthInfo struct {
	useCodex   bool
	useCopilot bool
	baseURL    string
	authorizer auth.HTTPAuthorizer
}

// buildClientOptions constructs the OpenAI client options based on authentication mode.
// Returns the SDK options plus transport/auth metadata used by WebSocket mode.
func buildClientOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, responsesAuthInfo, error) {
	useCodex := resolvePlatformName(config) == "codex"
	useCopilot := resolvePlatformName(config) == "copilot"
	authInfo := responsesAuthInfo{
		useCodex:   useCodex,
		useCopilot: useCopilot,
		baseURL:    getBaseURL(config),
	}

	var opts []option.RequestOption
	var err error

	if useCopilot {
		if config.OpenAI == nil {
			config.OpenAI = &llmtypes.OpenAIConfig{}
		}
		if normalizePlatformName(config.OpenAI.Platform) == "" {
			config.OpenAI.Platform = "copilot"
		}
		opts, authInfo.authorizer, err = buildCopilotAuthOptions(config, log)
	} else if useCodex {
		opts, authInfo.authorizer = buildCodexAuthOptions(config, log)
	} else {
		opts, authInfo.authorizer, err = buildAPIKeyAuthOptions(config, log)
	}
	if err != nil {
		return nil, authInfo, err
	}

	opts = append(opts, errorLoggingMiddleware(log))

	return opts, authInfo, nil
}

func buildCopilotAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer, error) {
	copilotCredsExists, _ := auth.GetCopilotCredentialsExists()
	if !copilotCredsExists {
		return nil, nil, errors.New("GitHub Copilot credentials not found, run 'kodelet copilot-login'")
	}

	log.Debug("using GitHub Copilot authentication for Responses API")
	authorizer := auth.CopilotAuthorizer()
	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else {
		opts = append(opts, option.WithBaseURL(auth.CopilotBaseURL))
	}

	return opts, authorizer, nil
}

func (t *Thread) requestOptions(opt llmtypes.MessageOpt) []option.RequestOption {
	if !t.useCopilot {
		return nil
	}

	return auth.CopilotOpenAIRequestOptions(opt)
}

// buildCodexAuthOptions returns client options for Codex CLI authentication.
func buildCodexAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer) {
	log.Debug("using Codex authentication for Responses API")
	authorizer := auth.CodexAuthorizer()
	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else {
		opts = append(opts, option.WithBaseURL(auth.CodexAPIBaseURL))
	}
	return opts, authorizer
}

// buildAPIKeyAuthOptions returns client options for standard API key authentication.
func buildAPIKeyAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer, error) {
	apiKeyEnvVar := getAPIKeyEnvVar(config)
	authorizer, err := auth.OpenAIAPIKeyAuthorizerFromEnv(apiKeyEnvVar)
	if err != nil {
		return nil, nil, err
	}

	log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key for Responses API")

	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return opts, authorizer, nil
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

func loadPlatformDefaultsForConfig(platformName string, config llmtypes.Config) (map[string]string, map[string]llmtypes.ModelPricing) {
	return loadPlatformDefaultsForServiceTier(platformName, normalizeServiceTier(config))
}

func loadPlatformDefaultsForServiceTier(platformName string, serviceTier llmtypes.OpenAIServiceTier) (map[string]string, map[string]llmtypes.ModelPricing) {
	switch normalizePlatformName(platformName) {
	case "openai":
		return loadPlatformDefaultsFromConfig(openaipreset.Models, openaipreset.PricingForServiceTier(serviceTier))
	case "codex":
		return loadPlatformDefaultsFromConfig(codexpreset.Models, codexpreset.PricingForServiceTier(serviceTier))
	case "copilot":
		models, pricing, err := copilotdefaults.LoadPlatformDefaults(context.Background())
		if err == nil {
			return loadPlatformDefaultsFromConfig(*models, pricing)
		}
		return loadPlatformDefaultsFromConfig(openaipreset.Models, openaipreset.Pricing)
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

// getPricingForServiceTier selects built-in pricing using the processing tier
// reported by the API. Explicit pricing from configuration remains authoritative.
func (t *Thread) getPricingForServiceTier(model string, serviceTier llmtypes.OpenAIServiceTier) llmtypes.ModelPricing {
	if t.Config.OpenAI != nil && t.Config.OpenAI.Pricing != nil {
		if pricing, ok := t.Config.OpenAI.Pricing[model]; ok {
			return pricing
		}
	}

	tier, ok := llmtypes.ParseOpenAIServiceTier(string(serviceTier))
	if !ok {
		return t.getPricing(model)
	}

	platformName := resolvePlatformForLoading(t.Config)
	switch normalizePlatformName(platformName) {
	case "openai", "codex":
		_, tierPricing := loadPlatformDefaultsForServiceTier(platformName, tier)
		if pricing, ok := tierPricing[model]; ok {
			return pricing
		}
	}

	return t.getPricing(model)
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
	RawItem    json.RawMessage
	ToolName   string // For tool use/result
	ToolCallID string // For matching tool results
	Input      string // For tool use (JSON string)
}

const compactedHistoryNotice = "Context compacted"

func itemsForDisplay(items []StoredInputItem) ([]StoredInputItem, bool) {
	lastCompactionIdx := -1
	for i, item := range items {
		if item.Type == "compaction" || item.Type == "compaction_summary" {
			lastCompactionIdx = i
		}
	}

	if lastCompactionIdx < 0 {
		return items, false
	}

	if lastCompactionIdx+1 >= len(items) {
		return nil, true
	}

	return items[lastCompactionIdx+1:], true
}

// StreamMessages parses raw messages into streamable format for conversation streaming.
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
	var items []StoredInputItem
	if err := json.Unmarshal(rawMessages, &items); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling input items")
	}

	displayItems, compacted := itemsForDisplay(items)

	streamable := make([]StreamableMessage, 0, len(displayItems)+1)
	if compacted {
		streamable = append(streamable, StreamableMessage{
			Kind:    "text",
			Role:    "assistant",
			Content: compactedHistoryNotice,
		})
	}

	for _, item := range displayItems {
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

			if item.Content == "" && len(item.RawItem) > 0 {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    item.Role,
					RawItem: item.RawItem,
				})
				continue
			}

			if item.Content != "" {
				streamable = append(streamable, StreamableMessage{
					Kind:    "text",
					Role:    item.Role,
					Content: item.Content,
					RawItem: item.RawItem,
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
				RawItem:    item.RawOutput,
			})

		case "web_search_call":
			streamable = append(streamable, StreamableMessage{
				Kind:       "tool-use",
				Role:       "assistant",
				ToolName:   openAISearchToolName,
				ToolCallID: item.CallID,
				Input:      webSearchStoredInput(item),
			})

			resultStr := item.Content
			if structuredResult, ok := toolResults[item.CallID]; ok {
				if jsonData, err := structuredResult.MarshalJSON(); err == nil {
					resultStr = string(jsonData)
				}
			}
			streamable = append(streamable, StreamableMessage{
				Kind:       "tool-result",
				Role:       "assistant",
				ToolName:   openAISearchToolName,
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

	displayItems, compacted := itemsForDisplay(items)

	result := make([]llmtypes.Message, 0, len(displayItems)+1)
	if compacted {
		result = append(result, llmtypes.Message{
			Role:    "assistant",
			Content: compactedHistoryNotice,
		})
	}

	registry := renderers.NewRendererRegistry()

	for _, item := range displayItems {
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

		case "web_search_call":
			result = append(result, llmtypes.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("🔧 Using tool: %s\n  Arguments: %s", openAISearchToolName, webSearchStoredInput(item)),
			})

			text := item.Content
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

func webSearchStoredInput(item StoredInputItem) string {
	details := webSearchDetailsFromStoredItem(item)
	payload := map[string]any{
		"status": webSearchStatusMessage(item.Status),
		"type":   item.Action,
	}
	switch item.Action {
	case "open_page":
		if details.url != "" {
			payload["url"] = details.url
		}
	case "find_in_page":
		if details.url != "" {
			payload["url"] = details.url
		}
		if details.pattern != "" {
			payload["pattern"] = details.pattern
		}
	default:
		if len(details.queries) > 0 {
			payload["queries"] = details.queries
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, webSearchStatusMessage(item.Status))
	}
	return string(data)
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

	return false
}
