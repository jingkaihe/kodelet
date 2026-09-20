// Package responses implements the OpenAI Responses API client.
// The Responses API is OpenAI's next-generation API designed for building AI agents,
// offering native support for multi-turn conversations, built-in tool calling,
// and automatic conversation state management.
package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
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

// Keep structured overload retries aligned with openai/codex's retry_on_overload helper.
const (
	responsesServerOverloadInitialDelay = 250 * time.Millisecond
	responsesServerOverloadMaxDelay     = 2 * time.Second
	responsesServerOverloadJitterRatio  = 0.2
	responsesServerOverloadHeader       = "X-Kodelet-Server-Overloaded"
)

// Thread represents a conversation thread using the OpenAI Responses API.
// It implements the llmtypes.Thread interface with feature parity to the
// Chat Completions implementation.
type Thread struct {
	*base.Thread

	// operationMu serializes model turns and standalone compaction. Automatic
	// compaction uses compactContext directly while SendMessage holds this lock.
	operationMu sync.Mutex
	// historyMu guards inputItems, storedItems, historyRevision, and no-save
	// operation state. SDK input items are treated as immutable after insertion,
	// so snapshots clone the slice.
	historyMu sync.Mutex

	// client is the OpenAI client for making API calls
	client *openai.Client

	// compactWithSummaryFunc allows overriding summary-based compaction in tests.
	compactWithSummaryFunc func(context.Context) error

	// inputItems holds only the active Responses API inference context.
	inputItems []responses.ResponseInputItemUnionParam

	// storedItems is the canonical active context for persistence. Earlier chat
	// is retained in CompactionHistory; the replacement prefix is inference-only.
	// It includes messages, function calls, and display-only reasoning in order.
	storedItems []StoredInputItem
	// historyRevision is guarded by historyMu and changes whenever either
	// history representation changes.
	historyRevision uint64
	// codexWindowGeneration is guarded by historyMu and identifies the logical
	// installed context lineage advertised to Codex Responses requests.
	codexWindowGeneration uint64
	// activeNoSaveOperation records public history appends that arrive while a
	// no-save model call is in flight so rollback can preserve them.
	activeNoSaveOperation *responsesNoSaveOperation

	// pendingReasoning accumulates reasoning content during streaming
	// It's stored here (not locally in processStream) to persist across API calls
	pendingReasoning strings.Builder

	// reasoningEffort controls the reasoning depth for o-series models
	reasoningEffort shared.ReasoningEffort

	// customModels contains provider-specific model aliases
	customModels map[string]string

	// customPricing contains provider-specific pricing information
	customPricing map[string]llmtypes.ModelPricing

	// isCodex indicates if this thread is using Codex authentication
	// Some API parameters may not be supported by the Codex API
	isCodex bool
	// codexInstallationID is a stable per-installation identifier advertised on
	// Codex Responses requests.
	codexInstallationID string
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
		config.Model = openaipreset.DefaultModel
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
	codexInstallationID := ""
	if authInfo.useCodex {
		codexInstallationID, err = auth.GetCodexInstallationID()
		if err != nil {
			return nil, errors.Wrap(err, "failed to resolve codex installation ID")
		}
	}

	thread := &Thread{
		Thread:              baseThread,
		client:              &client,
		inputItems:          make([]responses.ResponseInputItemUnionParam, 0),
		storedItems:         make([]StoredInputItem, 0),
		reasoningEffort:     reasoningEffort,
		customModels:        customModels,
		customPricing:       customPricing,
		isCodex:             authInfo.useCodex,
		codexInstallationID: codexInstallationID,
		useCopilot:          authInfo.useCopilot,
		useWebSocket:        shouldUseResponsesWebSocket(config),
		authorizer:          authInfo.authorizer,
	}
	if thread.useWebSocket && supportsResponsesWebSocket(config) {
		thread.webSocket = newResponsesWebSocketTransport(authInfo.baseURL)
	}
	thread.processMessageExchangeFunc = thread.processMessageExchange
	thread.newStreamingFunc = thread.client.Responses.NewStreaming
	thread.compactWithSummaryFunc = thread.compactContextWithSummary

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

func (t *Thread) resetResponsesWebSocket() {
	t.webSocketContinuation.reset()
	if t.webSocket == nil {
		return
	}
	if err := t.webSocket.Reset(); err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to reset Responses API websocket")
	}
}

// AddUserMessage adds a user message with optional images to the thread.
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
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

	t.addInputItem(ctx, inputItem, message)
}

// AddAssistantMessage appends a provider-native assistant message without calling the model.
func (t *Thread) AddAssistantMessage(ctx context.Context, message string) {
	inputItem := responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleAssistant,
			Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(message)},
		},
	}
	t.addInputItem(ctx, inputItem, message)
}

func userImageContentParts(ctx context.Context, imagePaths []string) responses.ResponseInputMessageContentListParam {
	// Validate image count
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).
			WithField("image_count", len(imagePaths)).
			WithField("max_image_count", base.MaxImageCount).
			Warn("too many images provided; truncating")
		imagePaths = imagePaths[:base.MaxImageCount]
	}

	contentParts := responses.ResponseInputMessageContentListParam{}
	for _, imagePath := range imagePaths {
		imagePart, err := processImage(imagePath)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("image_path", imagePath).Warn("failed to process image")
			continue
		}
		contentParts = append(contentParts, imagePart)
	}
	return contentParts
}

func (t *Thread) addInputItem(ctx context.Context, inputItem responses.ResponseInputItemUnionParam, content string) {
	role := string(inputItem.OfMessage.Role)
	rawItem, err := json.Marshal(inputItem)
	if err != nil {
		logger.G(ctx).WithError(err).WithField("role", role).Warn("failed to marshal OpenAI Responses input item for persistence")
	}
	t.appendHistoryItemsFromContext(ctx, []responses.ResponseInputItemUnionParam{inputItem}, []StoredInputItem{{
		Type:    "message",
		Role:    role,
		Content: content,
		RawItem: rawItem,
	}})
}

func (t *Thread) appendHistoryItems(
	inputItems []responses.ResponseInputItemUnionParam,
	storedItems []StoredInputItem,
) {
	t.appendHistoryItemsWithOperation(inputItems, storedItems, nil, false)
}

func (t *Thread) appendHistoryItemsFromContext(
	ctx context.Context,
	inputItems []responses.ResponseInputItemUnionParam,
	storedItems []StoredInputItem,
) {
	operation, _ := ctx.Value(responsesNoSaveOperationContextKey{}).(*responsesNoSaveOperation)
	t.appendHistoryItemsWithOperation(inputItems, storedItems, operation, true)
}

func (t *Thread) appendHistoryItemsWithOperation(
	inputItems []responses.ResponseInputItemUnionParam,
	storedItems []StoredInputItem,
	operation *responsesNoSaveOperation,
	trackExternalAppend bool,
) {
	if len(inputItems) == 0 && len(storedItems) == 0 {
		return
	}
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	t.inputItems = append(t.inputItems, inputItems...)
	t.storedItems = append(t.storedItems, storedItems...)
	t.historyRevision++
	if trackExternalAppend && t.activeNoSaveOperation != nil && operation != t.activeNoSaveOperation {
		t.activeNoSaveOperation.externalAppends = append(
			t.activeNoSaveOperation.externalAppends,
			responsesHistoryAppend{
				inputItems:  cloneResponsesInputItems(inputItems),
				storedItems: cloneStoredInputItems(storedItems),
			},
		)
	}
}

func (t *Thread) inputItemsSnapshot() []responses.ResponseInputItemUnionParam {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	return cloneResponsesInputItems(t.inputItems)
}

func (t *Thread) inputItemsAndWindowSnapshot() ([]responses.ResponseInputItemUnionParam, uint64) {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	return cloneResponsesInputItems(t.inputItems), t.codexWindowGeneration
}

func (t *Thread) snapshotHistory() responsesHistorySnapshot {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	return responsesHistorySnapshot{
		revision:              t.historyRevision,
		inputItems:            cloneResponsesInputItems(t.inputItems),
		storedItems:           cloneStoredInputItems(t.storedItems),
		codexWindowGeneration: t.codexWindowGeneration,
	}
}

type responsesNoSaveSnapshot struct {
	history               responsesHistorySnapshot
	compactionHistory     *convtypes.CompactionHistory
	currentContextWindow  int
	maxContextWindow      int
	structuredToolResults map[string]tooltypes.StructuredToolResult
	pendingReasoning      string
	operation             *responsesNoSaveOperation
}

type responsesNoSaveOperationContextKey struct{}

type responsesNoSaveOperation struct {
	externalAppends []responsesHistoryAppend
}

type responsesHistoryAppend struct {
	inputItems  []responses.ResponseInputItemUnionParam
	storedItems []StoredInputItem
}

func (t *Thread) snapshotNoSaveState() responsesNoSaveSnapshot {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()

	operation := &responsesNoSaveOperation{}
	t.activeNoSaveOperation = operation
	snapshot := responsesNoSaveSnapshot{
		history: responsesHistorySnapshot{
			revision:              t.historyRevision,
			inputItems:            cloneResponsesInputItems(t.inputItems),
			storedItems:           cloneStoredInputItems(t.storedItems),
			codexWindowGeneration: t.codexWindowGeneration,
		},
		pendingReasoning: t.pendingReasoning.String(),
		operation:        operation,
	}
	t.Mu.Lock()
	if t.Usage != nil {
		snapshot.currentContextWindow = t.Usage.CurrentContextWindow
		snapshot.maxContextWindow = t.Usage.MaxContextWindow
	}
	snapshot.structuredToolResults = maps.Clone(t.ToolResults)
	snapshot.compactionHistory = t.CompactionHistory.Clone()
	t.Mu.Unlock()
	return snapshot
}

func (t *Thread) restoreNoSaveState(snapshot responsesNoSaveSnapshot) {
	t.historyMu.Lock()
	windowChanged := t.codexWindowGeneration != snapshot.history.codexWindowGeneration
	t.inputItems = cloneResponsesInputItems(snapshot.history.inputItems)
	t.storedItems = cloneStoredInputItems(snapshot.history.storedItems)
	for _, appendBatch := range snapshot.operation.externalAppends {
		t.inputItems = append(t.inputItems, cloneResponsesInputItems(appendBatch.inputItems)...)
		t.storedItems = append(t.storedItems, cloneStoredInputItems(appendBatch.storedItems)...)
	}
	t.codexWindowGeneration = snapshot.history.codexWindowGeneration
	t.historyRevision++
	if t.activeNoSaveOperation == snapshot.operation {
		t.activeNoSaveOperation = nil
	}
	t.pendingReasoning.Reset()
	_, _ = t.pendingReasoning.WriteString(snapshot.pendingReasoning)

	t.Mu.Lock()
	if t.Usage == nil {
		t.Usage = &llmtypes.Usage{}
	}
	t.Usage.CurrentContextWindow = snapshot.currentContextWindow
	t.Usage.MaxContextWindow = snapshot.maxContextWindow
	t.ToolResults = maps.Clone(snapshot.structuredToolResults)
	t.CompactionHistory = snapshot.compactionHistory.Clone()
	if t.ToolResults == nil {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.Mu.Unlock()
	t.historyMu.Unlock()

	if windowChanged {
		t.resetResponsesWebSocket()
	} else {
		t.webSocketContinuation.reset()
	}
}

func cloneStoredInputItems(items []StoredInputItem) []StoredInputItem {
	if items == nil {
		return nil
	}
	cloned := make([]StoredInputItem, len(items))
	copy(cloned, items)
	for i := range cloned {
		cloned[i].RawItem = append(json.RawMessage(nil), items[i].RawItem...)
		cloned[i].RawOutput = append(json.RawMessage(nil), items[i].RawOutput...)
	}
	return cloned
}

// SendMessage sends a message to the LLM and processes the response.
func (t *Thread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	t.operationMu.Lock()
	defer t.operationMu.Unlock()
	if opt.NoSaveConversation {
		defer t.BlockConversationFork()()
	}

	logger.G(ctx).Debug("SendMessage called")
	ctx = withCodexTurnID(ctx)
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
		attribute.String("reasoning_effort", string(t.reasoningEffort)),
		attribute.String("api", "responses"),
		attribute.String("platform", resolvePlatformName(t.Config)),
	)
	defer func() {
		spanErr := err
		if spanErr == nil {
			spanErr = ctx.Err()
		}
		t.FinalizeMessageSpan(span, spanErr)
	}()
	if _, err = base.OpenEnvironment(ctx, t); err != nil {
		return "", errors.Wrap(err, "failed to open agent environment")
	}
	defer func() {
		runErr := err
		if runErr == nil {
			runErr = ctx.Err()
		}
		if closeErr := base.CloseEnvironmentWithError(context.WithoutCancel(ctx), t, runErr); err == nil && closeErr != nil {
			err = errors.Wrap(closeErr, "failed to close agent environment")
		}
	}()

	if opt.NoSaveConversation {
		snapshot := t.snapshotNoSaveState()
		defer t.restoreNoSaveState(snapshot)
		ctx = context.WithValue(ctx, responsesNoSaveOperationContextKey{}, snapshot.operation)
	}

	message, err = base.ProcessUserMessage(ctx, t, message)
	if err != nil {
		return "", err
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
	incomingUserAppended := false
	maxTurns := max(opt.MaxTurns, 0)
	if err := base.DispatchAgentStart(ctx, t); err != nil {
		return "", errors.Wrap(err, "failed to dispatch agent start")
	}

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

			if err := base.DispatchTurnStart(ctx, t, turnCount+1); err != nil {
				return "", errors.Wrap(err, "failed to dispatch turn start")
			}

			// Regenerate the system prompt from the context snapshot pinned when this run opened.
			contexts := base.EnvironmentContexts(t)

			systemPrompt, err := base.ProcessSystemPrompt(ctx, t, sysprompt.SystemPrompt(model, t.Config, contexts))
			if err != nil {
				return "", errors.Wrap(err, "failed to process agent initialization")
			}

			// Pre-turn compaction intentionally excludes the incoming user item. The
			// item is appended exactly once after any context replacement, matching
			// Codex's current pre-turn request ordering.
			autoCompactionMetadata := remoteCompactionV2AutoMetadata
			if incomingUserAppended {
				autoCompactionMetadata = remoteCompactionV2MidTurnAutoMetadata
			}
			previousMarkerID := t.CompactionMarkerID()
			beforeCurrentUser := !incomingUserAppended
			t.TryAutoCompact(ctx, t.CompactRatioOrDefault(opt.CompactRatio), func(ctx context.Context) error {
				return t.compactContext(ctx, autoCompactionMetadata)
			})
			if !incomingUserAppended {
				t.AddUserMessage(ctx, message, opt.Images...)
				incomingUserAppended = true
			}
			if !opt.NoSaveConversation {
				// Preserve admitted input when checkpointing a pre-turn compaction;
				// publishing before AddUserMessage would overwrite that checkpoint.
				t.PublishCompaction(ctx, t, handler, previousMarkerID, beforeCurrentUser)
			}

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
					t.SaveConversation(ctx)
				}
				return "", err
			}

			if !responseCompleted {
				return "", errors.New("response stream ended without response.completed event")
			}

			turnCount++
			finalOutput = exchangeOutput

			if err := base.TriggerTurnEnd(ctx, t, finalOutput, turnCount); err != nil {
				return "", errors.Wrap(err, "failed to dispatch turn end")
			}

			// If no tools were used, check for queued continuations before stopping
			if !toolsUsed {
				continued, err := base.HandleAgentStopFollowUps(ctx, t, handler)
				if err != nil {
					return "", errors.Wrap(err, "failed to dispatch agent end")
				}
				if continued {
					continue OUTER
				}
				if (maxTurns == 0 || turnCount < maxTurns) && base.HasPendingSteer(ctx, t.ConversationID) {
					continue OUTER
				}

				break OUTER
			}
		}
	}

	// A cancelled pre-turn hook can leave input only in the durable admission
	// checkpoint. Do not overwrite it with the pre-input compaction context.
	if incomingUserAppended && t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background()
		t.SaveConversation(saveCtx)
	}

	handler.HandleDone()

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
) (string, bool, bool, error) {
	log := logger.G(ctx)
	textVerbosity, sendTextVerbosity, err := llmtypes.ConfiguredOpenAITextVerbosity(t.Config)
	if err != nil {
		return "", false, false, err
	}

	saveConversation := func() {
		if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
			t.SaveConversation(ctx)
		}
	}

	if err := t.processPendingSteer(ctx, handler); err != nil {
		return "", false, false, errors.Wrap(err, "failed to process pending steer")
	}

	// Build tools
	tools := buildToolsForThread(t, nil, opt.NoToolUse)
	log.WithField("tool_count", len(tools)).Debug("built tools for request")

	// Keep a complete local input history for persistence, HTTP prompt caching, and
	// WebSocket reconnect recovery. The WebSocket path derives an incremental input
	// from this full request only while its connection-local continuation is valid.
	inputItems, windowGeneration := t.inputItemsAndWindowSnapshot()
	params := responses.ResponseNewParams{
		Model:          model,
		Input:          responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
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
	applyPromptCacheOptions(&params, t.Config, model)

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
		reasoningEffort = openAIReasoningEffortForRequest(model, reasoningEffort)
		params.Reasoning = shared.ReasoningParam{
			Effort:  reasoningEffort,
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	// Apply Codex-specific restrictions (overrides unsupported params)
	t.applyCodexRestrictions(&params)
	requestMetadata, err := t.buildCodexResponsesRequestMetadata(
		codexTurnIDFromContext(ctx),
		windowGeneration,
		"turn",
		nil,
	)
	if err != nil {
		return "", false, false, err
	}
	requestMetadata.apply(&params)

	requestOpts := append(t.requestOptions(opt), t.codexResponsesRequestOptions(requestMetadata)...)

	log.WithField("model", model).
		WithField("input_items", len(inputItems)).
		WithField("tool_count", len(tools)).
		WithField("is_codex", t.isCodex).
		Debug("sending request to Responses API")

	useWebSocket := t.useWebSocket && t.webSocket != nil
	var newStreaming func(context.Context, responses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
	if !useWebSocket {
		requestOpts = append(requestOpts, disableSDKServerOverloadRetries())
		newStreaming = t.newStreamingFunc
		if newStreaming == nil {
			newStreaming = t.client.Responses.NewStreaming
		}
	}
	processStream := t.processStreamFunc
	processStreamHandlesNilStream := processStream != nil
	if processStream == nil {
		processStream = t.readStream
	}

	var newResponsesStream responsesStreamFactory
	transportName := "https"
	if useWebSocket {
		transportName = "websocket"
		newResponsesStream = func(ctx context.Context, params responses.ResponseNewParams) (*responsesStreamAttempt, error) {
			stream, generation, err := t.webSocket.Stream(
				ctx,
				func(connectionGeneration uint64) responses.ResponseNewParams {
					return t.webSocketContinuation.prepare(params, connectionGeneration)
				},
				t.codexResponsesWebSocketHeaders(requestMetadata),
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

	return t.processMessageExchangeWithStreamRetries(ctx, handler, model, params, tools, newResponsesStream, processStream, opt, saveConversation, transportName)
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
	processStream func(context.Context, *ssestream.Stream[responses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error),
	opt llmtypes.MessageOpt,
	saveConversation func(),
	transportName string,
) (output string, toolsUsed bool, completed bool, resultErr error) {
	invocationCtx := ctx
	ctx, span := t.StartModelSpan(ctx, "openai", model,
		attribute.Bool("gen_ai.request.stream", true))
	finished := false
	finish := func(err error) {
		if !finished {
			finished = true
			base.FinishModelSpan(span, err)
		}
	}
	defer func() {
		spanErr := resultErr
		if spanErr == nil {
			spanErr = ctx.Err()
		}
		finish(spanErr)
	}()
	log := logger.G(ctx)
	retryConfig := responsesStreamRetryConfig(t.Config)
	var finalOutput string
	var finalStreamResult processStreamResult
	pendingReasoningBeforeAttempt := t.pendingReasoning.String()

	err := retry.Do(
		func() error {
			t.pendingReasoning.Reset()
			t.pendingReasoning.WriteString(pendingReasoningBeforeAttempt)
			attemptParams := params
			attemptParams.Input = responses.ResponseNewParamsInputUnion{
				OfInputItemList: t.inputItemsSnapshot(),
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
			if attempt.stream != nil {
				if closeErr := attempt.stream.Close(); err == nil && closeErr != nil {
					err = errors.Wrap(closeErr, "failed to close Responses API stream")
				}
			}
			if response := streamResult.response; response != nil {
				span.SetAttributes(responseSpanAttributes(*response)...)
			}
			if err == nil && !streamResult.responseCompleted {
				err = errors.New("response stream ended before response.completed event")
			}
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				// End the model span before any tool work, keeping tool failures
				// separate from a successfully completed model generation.
				finish(nil)
			}
			if complete := streamResult.complete; complete != nil {
				completedResult, completionErr := complete(invocationCtx)
				streamResult = completedResult
				if completionErr != nil {
					return retry.Unrecoverable(completionErr)
				}
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
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			telemetry.AddEvent(ctx, "kodelet.model.retry",
				attribute.Int("retry.attempt", int(n)+1), attribute.String("kodelet.model.transport", transportName))
			log.WithError(err).
				WithField("attempt", n+1).
				WithField("max_attempts", retryConfig.Attempts).
				WithField("transport", transportName).
				Warn("retrying Responses API stream request")
		}),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		logResponsesAPIRequestFailure(log, err, model, len(tools), len(t.inputItemsSnapshot()))
		saveConversation()
		return "", false, false, err
	}

	saveConversation()
	return finalOutput, finalStreamResult.toolsUsed, finalStreamResult.responseCompleted, nil
}

func responseSpanAttributes(response responses.Response) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("gen_ai.response.id", response.ID),
		attribute.String("gen_ai.response.model", response.Model),
		attribute.Int64("gen_ai.usage.input_tokens", response.Usage.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", response.Usage.OutputTokens),
		attribute.Int64("gen_ai.usage.cache_read.input_tokens", response.Usage.InputTokensDetails.CachedTokens),
		attribute.Int64("gen_ai.usage.reasoning.output_tokens", response.Usage.OutputTokensDetails.ReasoningTokens),
	}
}

func (t *Thread) lastAssistantMessageText() string {
	inputItems := t.inputItemsSnapshot()
	for i := len(inputItems) - 1; i >= 0; i-- {
		item := inputItems[i]
		if item.OfOutputMessage != nil {
			var text strings.Builder
			for _, content := range item.OfOutputMessage.Content {
				if content.OfOutputText != nil {
					text.WriteString(content.OfOutputText.Text)
				}
			}
			if text.Len() > 0 {
				return text.String()
			}
		}
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
	var regularDelayType retry.DelayTypeFunc
	if retryConfig.BackoffType == "fixed" {
		regularDelayType = retry.FixedDelay
	} else {
		regularDelayType = retry.BackOffDelay
	}

	maxDelay := time.Duration(retryConfig.MaxDelay) * time.Millisecond
	return func(n uint, err error, config *retry.Config) time.Duration {
		if isResponsesServerOverloadedError(err) {
			return responsesServerOverloadRetryDelay(n)
		}

		delay := regularDelayType(n, err, config)
		if maxDelay > 0 && delay > maxDelay {
			return maxDelay
		}
		return delay
	}
}

func responsesServerOverloadRetryDelay(attempt uint) time.Duration {
	delay := responsesServerOverloadInitialDelay
	for retryNumber := uint(1); retryNumber < max(attempt, 1) && delay < responsesServerOverloadMaxDelay; retryNumber++ {
		delay = min(delay*2, responsesServerOverloadMaxDelay)
	}

	jitterMultiplier := 1 + ((rand.Float64()*2)-1)*responsesServerOverloadJitterRatio
	return time.Duration(float64(delay) * jitterMultiplier)
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

	var streamErr *ssestream.StreamError
	if errors.As(err, &streamErr) {
		code := responsesAPIErrorCodeFromBody(string(streamErr.Event.Data))
		return !isPermanentResponsesErrorCode(code)
	}

	var eventErr *responsesWebSocketEventError
	if errors.As(err, &eventErr) {
		code := strings.ToLower(strings.TrimSpace(eventErr.code))
		if isPermanentResponsesErrorCode(code) {
			return false
		}
		switch code {
		case "websocket_connection_limit_reached", "previous_response_not_found", "server_is_overloaded", "slow_down":
			return true
		}
		if eventErr.statusCode != 0 {
			return isRetryableResponsesHTTPStatus(eventErr.statusCode, code)
		}
		return true
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if isResponsesServerOverloadedError(apiErr) {
			return true
		}
		return isRetryableResponsesHTTPStatus(apiErr.StatusCode, apiErr.Code)
	}

	return true
}

func isPermanentResponsesErrorCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_prompt", "context_length_exceeded", "insufficient_quota", "usage_not_included", "cyber_policy":
		return true
	default:
		return false
	}
}

func isRetryableResponsesHTTPStatus(statusCode int, errorCode string) bool {
	if isResponsesServerOverloadedCode(errorCode) {
		return true
	}

	switch statusCode {
	case http.StatusBadRequest, http.StatusTooManyRequests:
		return false
	case 0:
		return true
	default:
		return statusCode >= http.StatusInternalServerError || statusCode == http.StatusRequestTimeout || statusCode == http.StatusConflict
	}
}

func isRetryableResponsesWebSocketHandshakeStatus(statusCode int, body string) bool {
	if isResponsesServerOverloadedCode(responsesAPIErrorCodeFromBody(body)) {
		return true
	}

	switch statusCode {
	case http.StatusBadRequest, http.StatusTooManyRequests:
		return false
	default:
		return true
	}
}

func isResponsesServerOverloadedError(err error) bool {
	if err == nil {
		return false
	}

	var streamEventErr *responseStreamEventError
	if errors.As(err, &streamEventErr) && isResponsesServerOverloadedCode(streamEventErr.code) {
		return true
	}

	var eventErr *responsesWebSocketEventError
	if errors.As(err, &eventErr) && isResponsesServerOverloadedCode(eventErr.code) {
		return true
	}

	var statusErr *websocketHandshakeStatusError
	if errors.As(err, &statusErr) && isResponsesServerOverloadedCode(responsesAPIErrorCodeFromBody(statusErr.body)) {
		return true
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.Response != nil && strings.EqualFold(apiErr.Response.Header.Get(responsesServerOverloadHeader), "true") {
			return true
		}
		if isResponsesServerOverloadedCode(apiErr.Code) {
			return true
		}
		return isResponsesServerOverloadedCode(responsesAPIErrorCodeFromBody(apiErr.RawJSON()))
	}

	return false
}

func isResponsesServerOverloadedCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "server_is_overloaded", "slow_down":
		return true
	default:
		return false
	}
}

func responsesAPIErrorCodeFromBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}

	var payload struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(firstNonEmpty(payload.Code, payload.Error.Code)))
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

		rawItem, err := json.Marshal(inputItem)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to marshal steering input item for persistence")
		}

		t.appendHistoryItems([]responses.ResponseInputItemUnionParam{inputItem}, []StoredInputItem{{
			Type:    "message",
			Role:    "user",
			Content: steerMsg.Content,
			RawItem: rawItem,
		}})

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
	inputItems := t.inputItemsSnapshot()
	result := make([]llmtypes.Message, 0, len(inputItems))

	for _, item := range inputItems {
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

type responsesHistorySnapshot struct {
	revision              uint64
	inputItems            []responses.ResponseInputItemUnionParam
	storedItems           []StoredInputItem
	codexWindowGeneration uint64
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

func rawMessagesForName(items []StoredInputItem) json.RawMessage {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil
	}
	return raw
}

// SavePendingUserMessage saves an admission checkpoint without changing the live
// context, including the Responses pre-turn compaction ordering.
func (t *Thread) SavePendingUserMessage(ctx context.Context, message string, images ...string) error {
	t.operationMu.Lock()
	defer t.operationMu.Unlock()
	return t.Thread.SavePendingUserMessage(ctx, t, func(ctx context.Context) (context.Context, func()) {
		snapshot := t.snapshotNoSaveState()
		return context.WithValue(ctx, responsesNoSaveOperationContextKey{}, snapshot.operation), func() {
			t.restoreNoSaveState(snapshot)
		}
	}, message, images...)
}

// SaveConversation saves the current thread to the conversation store.
func (t *Thread) SaveConversation(ctx context.Context) error {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return nil
	}
	record, err := t.buildConversationRecord(ctx, t.snapshotConversationState(true), true)
	if err != nil {
		return err
	}
	return t.Store.Save(ctx, record)
}

// SnapshotConversationFork captures the safe live fork source without publishing a new ID.
func (t *Thread) SnapshotConversationFork(ctx context.Context) (convtypes.ConversationRecord, error) {
	if t.ConversationForkBlocked() {
		return convtypes.ConversationRecord{}, llmtypes.ErrConversationForkUnavailable
	}
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return convtypes.ConversationRecord{}, llmtypes.ErrConversationForkUnavailable
	}
	return t.buildConversationRecord(ctx, t.snapshotConversationState(false), false)
}

// ForkConversation snapshots the live thread into a new persisted conversation.
func (t *Thread) ForkConversation(ctx context.Context) (string, error) {
	return t.Thread.ForkConversation(ctx, t.SnapshotConversationFork)
}

type conversationStateSnapshot struct {
	storedItems       []StoredInputItem
	compactionHistory *convtypes.CompactionHistory
	windowGeneration  uint64
	usage             llmtypes.Usage
	toolResults       map[string]tooltypes.StructuredToolResult
	metadata          map[string]any
}

func (t *Thread) snapshotConversationState(cleanupLive bool) conversationStateSnapshot {
	// Lock order is historyMu then base Mu, matching context replacement. This
	// snapshots transcript, logical window identity, usage, metadata, and tool
	// results from one coherent checkpoint.
	t.historyMu.Lock()
	if cleanupLive && t.cleanupOrphanedItemsLocked() {
		t.historyRevision++
	}
	t.Mu.Lock()
	storedItems := cloneStoredInputItems(t.storedItems)
	if !cleanupLive {
		storedItems = cleanedStoredInputItems(storedItems)
	}
	windowGeneration := t.codexWindowGeneration
	usage := llmtypes.Usage{}
	if t.Usage != nil {
		usage = *t.Usage
	}
	toolResults := maps.Clone(t.ToolResults)
	metadata := maps.Clone(t.Metadata)
	compactionHistory := t.CompactionHistory.Clone()
	t.Mu.Unlock()
	t.historyMu.Unlock()
	return conversationStateSnapshot{
		storedItems:       storedItems,
		compactionHistory: compactionHistory,
		windowGeneration:  windowGeneration,
		usage:             usage,
		toolResults:       toolResults,
		metadata:          metadata,
	}
}

func (t *Thread) buildConversationRecord(ctx context.Context, snapshot conversationStateSnapshot, updateThreadMetadata bool) (convtypes.ConversationRecord, error) {
	storedItems := snapshot.storedItems
	toolResults := snapshot.toolResults
	metadata := snapshot.metadata
	if toolResults == nil {
		toolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	messages, err := StreamMessages(rawMessagesForName(storedItems), toolResults)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "failed to parse conversation for naming")
	}
	metadata = conversations.PreserveStoredConversationName(ctx, t.Store, t.ConversationID, metadata)
	if explicitName := conversations.ExplicitConversationName(metadata); updateThreadMetadata && explicitName != "" {
		t.SetMetadataValue(conversations.ConversationNameMetadataKey, explicitName)
	}
	fallbackName := base.FirstUserMessageName(conversations.ApplyDisplayToStreamableMessages(conversationsFromResponses(messages), metadata))
	metadata, name := conversations.EnsureConversationName(metadata, fallbackName)
	if automaticName := conversations.AutomaticConversationName(metadata); updateThreadMetadata && automaticName != "" {
		t.SetMetadataValue(conversations.ConversationAutoNameMetadataKey, automaticName)
	}

	// Serialize stored items directly (already built inline during streaming)
	inputItemsJSON, err := json.Marshal(storedItems)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "error marshaling input items")
	}
	if err := snapshot.compactionHistory.Validate(inputItemsJSON); err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "invalid Responses compaction history")
	}

	// Build the conversation record
	metadata["model"] = t.Config.Model
	metadata["api_mode"] = "responses"
	metadata["platform"] = resolvePlatformName(t.Config)
	if t.isCodex {
		metadata[convtypes.CodexResponsesWindowGenerationMetadataKey] = snapshot.windowGeneration
	}
	if serviceTier := normalizeServiceTier(t.Config); serviceTier != "" {
		metadata["service_tier"] = string(serviceTier)
	}
	if profile := strings.TrimSpace(t.Config.Profile); profile != "" {
		metadata["profile"] = profile
	}
	snapshotConfig := t.Config
	if snapshotConfig.OpenAI != nil {
		openAIConfig := *snapshotConfig.OpenAI
		snapshotConfig.OpenAI = &openAIConfig
	} else {
		snapshotConfig.OpenAI = &llmtypes.OpenAIConfig{}
	}
	if strings.TrimSpace(snapshotConfig.Provider) == "" {
		snapshotConfig.Provider = "openai"
	}
	snapshotConfig.OpenAI.APIMode = llmtypes.OpenAIAPIModeResponses
	metadata, err = conversations.AddConfigSnapshot(metadata, snapshotConfig)
	if err != nil {
		return convtypes.ConversationRecord{}, errors.Wrap(err, "failed to persist conversation config snapshot")
	}

	return convtypes.ConversationRecord{
		ID:                t.ConversationID,
		CWD:               t.Config.WorkingDirectory,
		RawMessages:       inputItemsJSON,
		CompactionHistory: snapshot.compactionHistory,
		Provider:          "openai",
		Usage:             snapshot.usage,
		Metadata:          metadata,
		Summary:           name,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		ToolResults:       toolResults,
	}, nil
}

// loadConversation loads a conversation from the store.
// NOTE: This function expects the caller to hold ConversationMu lock.
func (t *Thread) loadConversation(ctx context.Context) error {
	if !t.Persisted || t.Store == nil {
		return nil
	}

	record, err := t.Store.Load(ctx, t.ConversationID)
	if err != nil {
		return err
	}

	if record.Provider != "" {
		if record.Provider != "openai-responses" {
			if record.Provider != "openai" {
				return errors.Errorf("cannot load provider %q conversation into Responses thread", record.Provider)
			}
			if !recordUsesResponsesAPI(record.Metadata) {
				return errors.New("cannot load non-Responses API conversation into Responses thread")
			}
		}
	}

	// Deserialize from storage format
	var storedItems []StoredInputItem
	if err := json.Unmarshal(record.RawMessages, &storedItems); err != nil {
		return errors.Wrap(err, "failed to decode Responses messages")
	}
	if err := record.CompactionHistory.Validate(record.RawMessages); err != nil {
		return errors.Wrap(err, "invalid Responses compaction history")
	}
	if record.CompactionHistory != nil && record.CompactionHistory.ActiveDisplayStart > len(cleanedStoredInputItems(storedItems)) {
		return errors.New("invalid Responses compaction display boundary after cleanup of incomplete tool calls")
	}

	windowGeneration := persistedCodexWindowGeneration(record.Metadata)

	// Install persisted provider and shared state under the same lock order used
	// by context replacement and SaveConversation.
	t.historyMu.Lock()
	t.Mu.Lock()
	t.storedItems = storedItems
	t.inputItems = fromStoredItems(storedItems)
	t.cleanupOrphanedItemsLocked()
	t.codexWindowGeneration = windowGeneration
	t.historyRevision++
	t.Usage = &record.Usage
	t.Metadata = maps.Clone(record.Metadata)
	if t.Metadata == nil {
		t.Metadata = make(map[string]any)
	}
	t.ToolResults = maps.Clone(record.ToolResults)
	t.CompactionHistory = record.CompactionHistory.Clone()
	if t.ToolResults == nil {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.Mu.Unlock()
	t.historyMu.Unlock()
	return nil
}

func persistedCodexWindowGeneration(metadata map[string]any) uint64 {
	value, ok := metadata[convtypes.CodexResponsesWindowGenerationMetadataKey]
	if !ok {
		return 0
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	var generation uint64
	if err := json.Unmarshal(raw, &generation); err != nil {
		return 0
	}
	return generation
}

func cleanedStoredInputItems(items []StoredInputItem) []StoredInputItem {
	for len(items) > 0 && items[len(items)-1].Type == "function_call" {
		items = items[:len(items)-1]
	}
	return items
}

// cleanupOrphanedItemsLocked removes incomplete tool call sequences from the end.
// The caller must hold historyMu.
func (t *Thread) cleanupOrphanedItemsLocked() bool {
	changed := false
	// Remove trailing tool calls without results
	for len(t.inputItems) > 0 {
		lastItem := t.inputItems[len(t.inputItems)-1]

		// If last item is a tool call without a result, remove it
		if lastItem.OfFunctionCall != nil {
			t.inputItems = t.inputItems[:len(t.inputItems)-1]
			changed = true
			continue
		}

		break
	}

	// Keep persisted history in sync with cleanup logic.
	storedItems := cleanedStoredInputItems(t.storedItems)
	changed = changed || len(storedItems) != len(t.storedItems)
	t.storedItems = storedItems
	return changed
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
	ToolOutput string // Display output retained alongside structured results
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

// StreamMessages parses raw messages into normalized persisted conversation entries.
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

			if item.Content != "" || len(item.RawItem) > 0 {
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
				ToolOutput: item.Output,
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
				ToolOutput: item.Content,
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
