// Package responses implements the OpenAI Responses API client.
// The Responses API is OpenAI's next-generation API designed for building AI agents,
// offering native support for multi-turn conversations, built-in tool calling,
// and automatic conversation state management.
package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	_ "golang.org/x/image/webp"

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

	remoteCompactionV2RetainedMessageTokenBudget = 64_000
	remoteCompactionV2TruncatedOutputMessage     = "Output exceeded the available model context and was truncated"
	remoteCompactionV2ResizedImageBytesEstimate  = 7_373
	remoteCompactionV2OriginalImagePatchSize     = 32
	remoteCompactionV2OriginalImageMaxPatches    = 10_000
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

	// inputItems holds the complete conversation history as Responses API input items
	// This is used for persistence and display purposes
	inputItems []responses.ResponseInputItemUnionParam

	// storedItems is the canonical conversation history for persistence
	// It includes all items (messages, function calls, reasoning) in order
	// This mirrors how Anthropic stores thinking blocks inline with messages
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
	thread.processStreamFunc = thread.processStream
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
	if goals.IsContextText(message) {
		if imageItem, ok := userImageInputItem(ctx, imagePaths); ok {
			t.addInputItem(ctx, imageItem, "")
		}
		inputItem := responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(message)},
			},
		}
		t.addInputItem(ctx, inputItem, message)
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
	rawItem, err := json.Marshal(inputItem)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to marshal OpenAI Responses assistant input item for persistence")
	}
	t.appendHistoryItemsFromContext(ctx, []responses.ResponseInputItemUnionParam{inputItem}, []StoredInputItem{{
		Type:    "message",
		Role:    "assistant",
		Content: message,
		RawItem: rawItem,
	}})
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

func (t *Thread) addInputItem(ctx context.Context, inputItem responses.ResponseInputItemUnionParam, content string) {
	rawItem, err := json.Marshal(inputItem)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to marshal OpenAI Responses user input item for persistence")
	}
	t.appendHistoryItemsFromContext(ctx, []responses.ResponseInputItemUnionParam{inputItem}, []StoredInputItem{{
		Type:    "message",
		Role:    "user",
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

func (t *Thread) codexWindowGenerationSnapshot() uint64 {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	return t.codexWindowGeneration
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

func (t *Thread) snapshotHistoryForRemoteCompactionV2() responsesHistorySnapshot {
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

	logger.G(ctx).Debug("SendMessage called")
	ctx = withCodexTurnID(ctx)
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
		attribute.String("reasoning_effort", string(t.reasoningEffort)),
		attribute.String("api", "responses"),
		attribute.String("platform", resolvePlatformName(t.Config)),
	)
	defer func() {
		t.FinalizeMessageSpan(span, err)
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
			t.TryAutoCompact(ctx, t.CompactRatioOrDefault(opt.CompactRatio), func(ctx context.Context) error {
				return t.compactContext(ctx, autoCompactionMetadata)
			})
			if !incomingUserAppended {
				if len(opt.Images) > 0 {
					t.AddUserMessage(ctx, message, opt.Images...)
				} else {
					t.AddUserMessage(ctx, message)
				}
				incomingUserAppended = true
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
				if (maxTurns == 0 || turnCount < maxTurns) && base.HandleGoalAutoContinuation(ctx, t, base.AvailableEnvironmentToolsForThread(t, opt.NoToolUse)) {
					continue OUTER
				}
				if (maxTurns == 0 || turnCount < maxTurns) && base.HasPendingSteer(ctx, t.ConversationID) {
					continue OUTER
				}

				break OUTER
			}
		}
	}

	// Save conversation state
	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background()
		t.SaveConversation(saveCtx)
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

	if base.EnvironmentForThread(t) != nil {
		contexts := base.EnvironmentContexts(t)
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
		logResponsesAPIRequestFailure(log, err, model, len(tools), len(t.inputItemsSnapshot()))
		saveConversation()
		return "", false, false, err
	}

	saveConversation()
	return finalOutput, finalStreamResult.toolsUsed, finalStreamResult.responseCompleted, nil
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

	var streamErr *ssestream.StreamError
	if errors.As(err, &streamErr) {
		code := responsesAPIErrorCodeFromBody(string(streamErr.Event.Data))
		return !isPermanentResponsesErrorCode(code)
	}

	var eventErr *responsesWebSocketEventError
	if errors.As(err, &eventErr) {
		code := strings.ToLower(strings.TrimSpace(eventErr.code))
		switch code {
		case "websocket_connection_limit_reached", "previous_response_not_found", "server_is_overloaded", "slow_down":
			return true
		case "invalid_prompt", "context_length_exceeded", "insufficient_quota", "usage_not_included", "cyber_policy":
			return false
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

// SwapContext replaces the conversation history with a summary message.
func (t *Thread) SwapContext(_ context.Context, summary string) error {
	return t.replaceWithSummary(nil, summary)
}

func (t *Thread) swapContextAtRevision(expectedRevision uint64, summary string) error {
	return t.replaceWithSummary(&expectedRevision, summary)
}

func (t *Thread) replaceWithSummary(expectedRevision *uint64, summary string) error {
	t.historyMu.Lock()
	if expectedRevision != nil && t.historyRevision != *expectedRevision {
		t.historyMu.Unlock()
		return errRemoteCompactionHistoryChanged
	}

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
	t.historyRevision++
	if t.isCodex {
		t.codexWindowGeneration++
	}

	t.Mu.Lock()
	t.FinalizeSwapContextLocked(summary)
	t.Mu.Unlock()
	t.historyMu.Unlock()
	if t.isCodex {
		t.resetResponsesWebSocket()
	} else {
		t.webSocketContinuation.reset()
	}

	return nil
}

func (t *Thread) compactContextWithSummary(ctx context.Context) error {
	snapshot := t.snapshotHistory()
	return base.CompactContextWithSummary(
		ctx,
		func(ctx context.Context, prompt string, useWeakModel bool) (string, error) {
			return t.runUtilityPromptWithInput(ctx, prompt, useWeakModel, snapshot.inputItems)
		},
		func(_ context.Context, summary string) error {
			return t.swapContextAtRevision(snapshot.revision, summary)
		},
	)
}

// CompactContext compacts the conversation history. Native OpenAI and Codex use
// Remote Compaction V2 through the streaming Responses endpoint; other compatible
// providers use the in-harness summary compactor instead.
func (t *Thread) CompactContext(ctx context.Context) error {
	t.operationMu.Lock()
	defer t.operationMu.Unlock()
	return t.compactContext(withCodexTurnID(ctx), remoteCompactionV2ManualMetadata)
}

func (t *Thread) compactContext(ctx context.Context, compactionMetadata codexCompactionMetadata) error {
	if len(t.inputItemsSnapshot()) == 0 {
		return nil
	}

	compactWithSummary := t.compactWithSummaryFunc
	if compactWithSummary == nil {
		compactWithSummary = t.compactContextWithSummary
	}
	if supportsRemoteCompactionV2(t.Config) {
		if err := t.compactContextRemoteV2(ctx, compactionMetadata); err != nil {
			if errors.Is(err, errRemoteCompactionHistoryChanged) {
				return err
			}
			logger.G(ctx).WithError(err).Warn("responses remote compaction v2 failed, falling back to summary compaction")
			if fallbackErr := compactWithSummary(ctx); fallbackErr != nil {
				if errors.Is(fallbackErr, errRemoteCompactionHistoryChanged) {
					return fallbackErr
				}
				return errors.Wrapf(fallbackErr, "failed to compact context (responses v2 error: %v)", err)
			}
		}
		return nil
	}
	return compactWithSummary(ctx)
}

type remoteCompactionV2Result struct {
	output       responses.ResponseOutputItemUnion
	usageRecords []remoteCompactionV2UsageRecord
}

type remoteCompactionV2UsageRecord struct {
	usage       responses.ResponseUsage
	serviceTier llmtypes.OpenAIServiceTier
}

type codexCompactionMetadata struct {
	trigger        string
	reason         string
	implementation string
	phase          string
	strategy       string
}

var (
	remoteCompactionV2ManualMetadata = codexCompactionMetadata{
		trigger:        "manual",
		reason:         "user_requested",
		implementation: "responses_compaction_v2",
		phase:          "standalone_turn",
		strategy:       "memento",
	}
	remoteCompactionV2AutoMetadata = codexCompactionMetadata{
		trigger:        "auto",
		reason:         "context_limit",
		implementation: "responses_compaction_v2",
		phase:          "pre_turn",
		strategy:       "memento",
	}
	remoteCompactionV2MidTurnAutoMetadata = codexCompactionMetadata{
		trigger:        "auto",
		reason:         "context_limit",
		implementation: "responses_compaction_v2",
		phase:          "mid_turn",
		strategy:       "memento",
	}
)

type codexResponsesRequestMetadata struct {
	clientMetadata map[string]string
	turnMetadata   string
	windowID       string
}

type codexTurnMetadataPayload struct {
	InstallationID string                          `json:"installation_id,omitempty"`
	SessionID      string                          `json:"session_id"`
	ThreadID       string                          `json:"thread_id"`
	TurnID         string                          `json:"turn_id"`
	WindowID       string                          `json:"window_id"`
	RequestKind    string                          `json:"request_kind"`
	Compaction     *codexCompactionMetadataPayload `json:"compaction,omitempty"`
}

type codexCompactionMetadataPayload struct {
	Trigger        string `json:"trigger"`
	Reason         string `json:"reason"`
	Implementation string `json:"implementation"`
	Phase          string `json:"phase"`
	Strategy       string `json:"strategy"`
}

type codexTurnIDContextKey struct{}

func withCodexTurnID(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if codexTurnIDFromContext(ctx) != "" {
		return ctx
	}
	return context.WithValue(ctx, codexTurnIDContextKey{}, convtypes.GenerateID())
}

func codexTurnIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	turnID, _ := ctx.Value(codexTurnIDContextKey{}).(string)
	return strings.TrimSpace(turnID)
}

func (t *Thread) buildCodexResponsesRequestMetadata(
	turnID string,
	windowGeneration uint64,
	requestKind string,
	compaction *codexCompactionMetadata,
) (codexResponsesRequestMetadata, error) {
	if !t.isCodex {
		return codexResponsesRequestMetadata{}, nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = convtypes.GenerateID()
	}
	windowID := fmt.Sprintf("%s:%d", t.ConversationID, windowGeneration)
	payload := codexTurnMetadataPayload{
		InstallationID: t.codexInstallationID,
		SessionID:      t.ConversationID,
		ThreadID:       t.ConversationID,
		TurnID:         turnID,
		WindowID:       windowID,
		RequestKind:    requestKind,
	}
	if compaction != nil {
		payload.Compaction = &codexCompactionMetadataPayload{
			Trigger:        compaction.trigger,
			Reason:         compaction.reason,
			Implementation: compaction.implementation,
			Phase:          compaction.phase,
			Strategy:       compaction.strategy,
		}
	}
	turnMetadata, err := json.Marshal(payload)
	if err != nil {
		return codexResponsesRequestMetadata{}, errors.Wrap(err, "failed to encode codex turn metadata")
	}
	clientMetadata := map[string]string{
		"session_id":                 t.ConversationID,
		"thread_id":                  t.ConversationID,
		"turn_id":                    turnID,
		auth.CodexWindowIDHeader:     windowID,
		auth.CodexTurnMetadataHeader: string(turnMetadata),
	}
	if t.codexInstallationID != "" {
		clientMetadata[auth.CodexInstallationIDHeader] = t.codexInstallationID
	}
	return codexResponsesRequestMetadata{
		clientMetadata: clientMetadata,
		turnMetadata:   string(turnMetadata),
		windowID:       windowID,
	}, nil
}

func (m codexResponsesRequestMetadata) apply(params *responses.ResponseNewParams) {
	if params == nil || len(m.clientMetadata) == 0 {
		return
	}
	params.SetExtraFields(map[string]any{"client_metadata": maps.Clone(m.clientMetadata)})
}

func (t *Thread) codexResponsesRequestOptions(metadata codexResponsesRequestMetadata) []option.RequestOption {
	if !t.isCodex {
		return nil
	}
	opts := []option.RequestOption{
		option.WithHeader(auth.CodexBetaFeaturesHeader, auth.CodexBetaFeatures),
		option.WithHeader("session-id", t.ConversationID),
		option.WithHeader("thread-id", t.ConversationID),
	}
	if t.codexInstallationID != "" {
		opts = append(opts, option.WithHeader(auth.CodexInstallationIDHeader, t.codexInstallationID))
	}
	if metadata.windowID != "" {
		opts = append(opts, option.WithHeader(auth.CodexWindowIDHeader, metadata.windowID))
	}
	if metadata.turnMetadata != "" {
		opts = append(opts, option.WithHeader(auth.CodexTurnMetadataHeader, metadata.turnMetadata))
	}
	return opts
}

func (t *Thread) codexResponsesWebSocketHeaders(metadata codexResponsesRequestMetadata) []string {
	if !t.isCodex {
		return nil
	}
	headers := []string{
		fmt.Sprintf("%s: %s", auth.CodexBetaFeaturesHeader, auth.CodexBetaFeatures),
		"session-id: " + t.ConversationID,
		"thread-id: " + t.ConversationID,
	}
	if t.codexInstallationID != "" {
		headers = append(headers, auth.CodexInstallationIDHeader+": "+t.codexInstallationID)
	}
	if metadata.windowID != "" {
		headers = append(headers, auth.CodexWindowIDHeader+": "+metadata.windowID)
	}
	if metadata.turnMetadata != "" {
		headers = append(headers, auth.CodexTurnMetadataHeader+": "+metadata.turnMetadata)
	}
	return headers
}

var errRemoteCompactionHistoryChanged = errors.New("conversation history changed during remote compaction v2")

type responsesHistorySnapshot struct {
	revision              uint64
	inputItems            []responses.ResponseInputItemUnionParam
	storedItems           []StoredInputItem
	codexWindowGeneration uint64
}

type remoteCompactionV2StreamFactory func(
	context.Context,
	responses.ResponseNewParams,
) (*ssestream.Stream[responses.ResponseStreamEventUnion], error)

func (t *Thread) compactContextRemoteV2(ctx context.Context, compactionMetadata ...codexCompactionMetadata) error {
	metadata := remoteCompactionV2ManualMetadata
	if len(compactionMetadata) > 0 {
		metadata = compactionMetadata[0]
	}
	snapshot := t.snapshotHistoryForRemoteCompactionV2()
	requestMetadata, err := t.buildCodexResponsesRequestMetadata(
		codexTurnIDFromContext(ctx),
		snapshot.codexWindowGeneration,
		"compaction",
		&metadata,
	)
	if err != nil {
		return err
	}
	params, err := t.buildRemoteCompactionV2Params(snapshot.inputItems, requestMetadata)
	if err != nil {
		return err
	}

	result, err := t.runRemoteCompactionV2(ctx, params, requestMetadata)
	for _, usageRecord := range result.usageRecords {
		serviceTier := usageRecord.serviceTier
		if serviceTier == "" {
			serviceTier = normalizeServiceTier(t.Config)
		}
		t.updateUsageTotals(usageRecord.usage, t.Config.Model, serviceTier)
	}
	if err != nil {
		return err
	}

	raw := result.output.RawJSON()
	if raw == "" {
		rawJSON, marshalErr := json.Marshal(result.output)
		if marshalErr != nil {
			return errors.Wrap(marshalErr, "failed to preserve remote compaction v2 output")
		}
		raw = string(rawJSON)
	}

	compactionItem := storedItemFromCompactOutput(result.output, raw)
	if compactionItem.EncryptedContent == "" {
		return errors.New("remote compaction v2 returned empty encrypted content")
	}

	retainedItems := retainedStoredItemsForRemoteCompactionV2(snapshot.storedItems)
	newStoredItems := append(retainedItems, compactionItem)
	newInputItems := fromStoredItems(newStoredItems)
	if len(newInputItems) == 0 || newInputItems[len(newInputItems)-1].OfCompaction == nil {
		return errors.New("remote compaction v2 output could not be converted to conversation input")
	}

	estimatedContextTokens := estimateRemoteCompactionV2ContextTokens(params.Instructions.Value, newInputItems)
	if err := t.replaceCompactedHistory(snapshot.revision, newInputItems, newStoredItems, estimatedContextTokens); err != nil {
		return err
	}
	logger.G(ctx).
		WithField("compaction_implementation", "remote_v2").
		WithField("retained_items", len(retainedItems)).
		Info("responses compaction completed")
	return nil
}

func (t *Thread) buildRemoteCompactionV2Params(
	input []responses.ResponseInputItemUnionParam,
	requestMetadata ...codexResponsesRequestMetadata,
) (responses.ResponseNewParams, error) {
	textVerbosity, sendTextVerbosity, err := llmtypes.ConfiguredOpenAITextVerbosity(t.Config)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	contexts := base.EnvironmentContexts(t)
	systemPrompt := sysprompt.SystemPrompt(t.Config.Model, t.Config, contexts)
	tools := buildToolsForThread(t, nil, false)
	params := responses.ResponseNewParams{
		Model:             t.Config.Model,
		Input:             responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Instructions:      param.NewOpt(systemPrompt),
		Tools:             tools,
		Store:             param.NewOpt(false),
		PromptCacheKey:    param.NewOpt(t.ConversationID),
		ParallelToolCalls: param.NewOpt(len(tools) > 0),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		},
	}
	if sendTextVerbosity {
		params.Text = responses.ResponseTextConfigParam{
			Verbosity: responses.ResponseTextConfigVerbosity(textVerbosity),
		}
	}
	applyGPT56PromptCacheOptions(&params, t.Config, t.Config.Model)

	if serviceTier := normalizeServiceTier(t.Config).WireValue(); serviceTier != "" {
		params.ServiceTier = responses.ResponseNewParamsServiceTier(serviceTier)
	}
	if t.isReasoningModelDynamic(t.Config.Model) && t.reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort:  openAIReasoningEffortForRequest(t.reasoningEffort),
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	t.applyCodexRestrictions(&params)
	pricing := t.getPricingForServiceTier(t.Config.Model, normalizeServiceTier(t.Config))
	trimmedInput, rewrittenOutputs := trimRemoteCompactionV2InputToContextWindow(
		params.Input.OfInputItemList,
		systemPrompt,
		pricing.ContextWindow,
	)
	if rewrittenOutputs > 0 {
		logger.G(context.Background()).
			WithField("rewritten_outputs", rewrittenOutputs).
			Debug("rewrote history outputs before remote compaction v2")
	}
	trigger := responses.NewResponseInputItemCompactionTriggerParam()
	trimmedInput = append(trimmedInput, responses.ResponseInputItemUnionParam{OfCompactionTrigger: &trigger})
	params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: trimmedInput}
	if len(requestMetadata) > 0 {
		requestMetadata[0].apply(&params)
	} else if metadata, metadataErr := t.buildCodexResponsesRequestMetadata(
		convtypes.GenerateID(),
		t.codexWindowGenerationSnapshot(),
		"compaction",
		&remoteCompactionV2ManualMetadata,
	); metadataErr != nil {
		return responses.ResponseNewParams{}, metadataErr
	} else {
		metadata.apply(&params)
	}
	return params, nil
}

func trimRemoteCompactionV2InputToContextWindow(
	input []responses.ResponseInputItemUnionParam,
	instructions string,
	contextWindow int,
) ([]responses.ResponseInputItemUnionParam, int) {
	trimmed := cloneResponsesInputItems(input)
	if contextWindow <= 0 {
		return trimmed, 0
	}

	estimatedTokens := approximateTextTokens(instructions)
	for _, item := range trimmed {
		estimatedTokens += approximateResponseInputItemTokens(item)
	}

	rewritten := 0
	for i := len(trimmed) - 1; i >= 0 && estimatedTokens > contextWindow; i-- {
		replacement, ok := rewriteResponseInputFunctionOutputForContextWindow(trimmed[i])
		if !ok {
			continue
		}
		estimatedTokens -= approximateResponseInputItemTokens(trimmed[i])
		estimatedTokens += approximateResponseInputItemTokens(replacement)
		trimmed[i] = replacement
		rewritten++
	}
	return trimmed, rewritten
}

func rewriteResponseInputFunctionOutputForContextWindow(
	item responses.ResponseInputItemUnionParam,
) (responses.ResponseInputItemUnionParam, bool) {
	if item.OfFunctionCallOutput == nil {
		return responses.ResponseInputItemUnionParam{}, false
	}

	output := *item.OfFunctionCallOutput
	output.Output = responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
		OfString: param.NewOpt(remoteCompactionV2TruncatedOutputMessage),
	}
	item.OfFunctionCallOutput = &output
	return item, true
}

func approximateResponseInputItemTokens(item responses.ResponseInputItemUnionParam) int {
	if item.OfCompaction != nil {
		visibleBytes := max((len(item.OfCompaction.EncryptedContent)*3)/4-650, 0)
		return max((visibleBytes+3)/4, 1)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return 1
	}
	visibleBytes := approximateModelVisibleJSONBytes(raw)
	return max((visibleBytes+3)/4, 1)
}

func approximateModelVisibleJSONBytes(raw []byte) int {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return len(raw)
	}
	payloadBytes, replacementBytes := inlineImageDataURLEstimateAdjustment(payload)
	return max(len(raw)-payloadBytes+replacementBytes, 0)
}

func inlineImageDataURLEstimateAdjustment(value any) (payloadBytes, replacementBytes int) {
	switch value := value.(type) {
	case map[string]any:
		itemType, _ := value["type"].(string)
		imageURL, _ := value["image_url"].(string)
		if strings.EqualFold(itemType, "input_image") {
			if payloadLength, ok := inlineBase64ImagePayloadLength(imageURL); ok {
				payloadBytes += payloadLength
				replacement := remoteCompactionV2ResizedImageBytesEstimate
				detail, _ := value["detail"].(string)
				if strings.EqualFold(strings.TrimSpace(detail), "original") {
					if originalEstimate, estimated := originalImageDataURLEstimateBytes(imageURL); estimated {
						replacement = originalEstimate
					}
				}
				replacementBytes += replacement
			}
		}
		for _, child := range value {
			childPayloadBytes, childReplacementBytes := inlineImageDataURLEstimateAdjustment(child)
			payloadBytes += childPayloadBytes
			replacementBytes += childReplacementBytes
		}
	case []any:
		for _, child := range value {
			childPayloadBytes, childReplacementBytes := inlineImageDataURLEstimateAdjustment(child)
			payloadBytes += childPayloadBytes
			replacementBytes += childReplacementBytes
		}
	}
	return payloadBytes, replacementBytes
}

func inlineBase64ImagePayloadLength(dataURL string) (int, bool) {
	payload, ok := inlineBase64ImagePayload(dataURL)
	return len(payload), ok
}

func inlineBase64ImagePayload(dataURL string) (string, bool) {
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return "", false
	}
	metadata := strings.ToLower(strings.TrimSpace(dataURL[:comma]))
	if !strings.HasPrefix(metadata, "data:image/") {
		return "", false
	}
	base64Encoded := false
	for _, parameter := range strings.Split(metadata, ";")[1:] {
		if strings.TrimSpace(parameter) == "base64" {
			base64Encoded = true
			break
		}
	}
	if !base64Encoded {
		return "", false
	}
	return dataURL[comma+1:], true
}

func originalImageDataURLEstimateBytes(dataURL string) (int, bool) {
	payload, ok := inlineBase64ImagePayload(dataURL)
	if !ok {
		return 0, false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return 0, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, false
	}

	patchesWide := (int64(config.Width)-1)/remoteCompactionV2OriginalImagePatchSize + 1
	patchesHigh := (int64(config.Height)-1)/remoteCompactionV2OriginalImagePatchSize + 1
	patchCount := int64(remoteCompactionV2OriginalImageMaxPatches)
	if patchesWide < patchCount && patchesHigh < patchCount {
		if product := patchesWide * patchesHigh; product < patchCount {
			patchCount = product
		}
	}
	return int(patchCount * 4), true
}

func approximateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	return max((len(text)+3)/4, 1)
}

func (t *Thread) runRemoteCompactionV2(
	ctx context.Context,
	params responses.ResponseNewParams,
	requestMetadata ...codexResponsesRequestMetadata,
) (remoteCompactionV2Result, error) {
	var priorUsage []remoteCompactionV2UsageRecord
	var metadata codexResponsesRequestMetadata
	if len(requestMetadata) > 0 {
		metadata = requestMetadata[0]
	} else {
		var err error
		metadata, err = t.buildCodexResponsesRequestMetadata(
			codexTurnIDFromContext(ctx),
			t.codexWindowGenerationSnapshot(),
			"compaction",
			&remoteCompactionV2ManualMetadata,
		)
		if err != nil {
			return remoteCompactionV2Result{}, err
		}
		metadata.apply(&params)
	}
	if t.useWebSocket && t.webSocket != nil {
		webSocketHeaders := t.remoteCompactionV2WebSocketHeaders(metadata)
		result, err := t.runRemoteCompactionV2WithRetries(ctx, params, "websocket", func(
			ctx context.Context,
			params responses.ResponseNewParams,
		) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
			stream, _, err := t.webSocket.Stream(
				ctx,
				func(uint64) responses.ResponseNewParams { return params },
				webSocketHeaders,
				t.authorizer,
			)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create remote compaction v2 websocket stream")
			}
			return stream, nil
		})
		t.webSocketContinuation.reset()
		if err == nil {
			return result, nil
		}
		if !isRetryableResponsesStreamError(err) {
			return result, err
		}
		priorUsage = append(priorUsage, result.usageRecords...)
		t.useWebSocket = false
		t.resetResponsesWebSocket()
		logger.G(ctx).WithError(err).Warn("remote compaction v2 websocket failed, falling back to HTTPS")
	}

	requestOpts := t.remoteCompactionV2RequestOptions(metadata)
	newStreaming := t.newStreamingFunc
	if newStreaming == nil {
		if t.client == nil {
			return remoteCompactionV2Result{usageRecords: priorUsage}, errors.New("openai client is not initialized")
		}
		newStreaming = t.client.Responses.NewStreaming
	}

	result, err := t.runRemoteCompactionV2WithRetries(ctx, params, "https", func(
		ctx context.Context,
		params responses.ResponseNewParams,
	) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
		stream := newStreaming(ctx, params, requestOpts...)
		if stream == nil {
			return nil, errors.New("failed to create remote compaction v2 HTTPS stream")
		}
		if err := stream.Err(); err != nil {
			return nil, err
		}
		return stream, nil
	})
	result.usageRecords = append(priorUsage, result.usageRecords...)
	return result, err
}

func (t *Thread) remoteCompactionV2RequestOptions(requestMetadata ...codexResponsesRequestMetadata) []option.RequestOption {
	opts := append([]option.RequestOption{}, t.requestOptions(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent})...)
	opts = append(opts, option.WithMaxRetries(0), disableSDKServerOverloadRetries())
	metadata := codexResponsesRequestMetadata{}
	if len(requestMetadata) > 0 {
		metadata = requestMetadata[0]
	}
	opts = append(opts, t.codexResponsesRequestOptions(metadata)...)
	return opts
}

func (t *Thread) remoteCompactionV2WebSocketHeaders(metadata codexResponsesRequestMetadata) []string {
	return t.codexResponsesWebSocketHeaders(metadata)
}

func (t *Thread) runRemoteCompactionV2WithRetries(
	ctx context.Context,
	params responses.ResponseNewParams,
	transportName string,
	newStream remoteCompactionV2StreamFactory,
) (remoteCompactionV2Result, error) {
	retryConfig := responsesStreamRetryConfig(t.Config)
	var aggregateResult remoteCompactionV2Result
	err := retry.Do(
		func() error {
			stream, err := newStream(ctx, params)
			if err != nil {
				if !isRetryableResponsesStreamError(err) {
					return retry.Unrecoverable(err)
				}
				return err
			}

			result, err := collectRemoteCompactionV2Stream(ctx, stream)
			aggregateResult.usageRecords = append(aggregateResult.usageRecords, result.usageRecords...)
			if closeErr := stream.Close(); err == nil && closeErr != nil {
				err = errors.Wrap(closeErr, "failed to close remote compaction v2 stream")
			}
			if err != nil {
				if !isRetryableResponsesStreamError(err) {
					return retry.Unrecoverable(err)
				}
				return err
			}

			aggregateResult.output = result.output
			return nil
		},
		retry.RetryIf(retry.IsRecoverable),
		retry.Attempts(uint(retryConfig.Attempts)),
		retry.Delay(time.Duration(retryConfig.InitialDelay)*time.Millisecond),
		retry.DelayType(responsesStreamRetryDelayType(retryConfig)),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			logger.G(ctx).WithError(err).
				WithField("attempt", n+1).
				WithField("max_attempts", retryConfig.Attempts).
				WithField("transport", transportName).
				Warn("retrying remote compaction v2 request")
		}),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return aggregateResult, err
	}
	return aggregateResult, nil
}

func remoteCompactionV2ResultWithUsage(response responses.Response) remoteCompactionV2Result {
	return remoteCompactionV2Result{
		usageRecords: []remoteCompactionV2UsageRecord{{
			usage:       response.Usage,
			serviceTier: llmtypes.OpenAIServiceTier(response.ServiceTier),
		}},
	}
}

func collectRemoteCompactionV2Stream(
	ctx context.Context,
	stream *ssestream.Stream[responses.ResponseStreamEventUnion],
) (remoteCompactionV2Result, error) {
	if stream == nil {
		return remoteCompactionV2Result{}, retry.Unrecoverable(errors.New("remote compaction v2 stream is nil"))
	}

	var (
		outputItemCount int
		compactionCount int
		compactionItem  responses.ResponseOutputItemUnion
		completed       *responses.Response
	)

streamLoop:
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_item.done":
			outputItemCount++
			if event.Item.Type == "compaction" {
				compactionCount++
				if compactionCount == 1 {
					compactionItem = event.Item
				}
			}
		case "response.completed":
			response := event.Response
			completed = &response
			break streamLoop
		case "response.incomplete":
			return remoteCompactionV2ResultWithUsage(event.Response), errors.Errorf(
				"remote compaction v2 response incomplete: %s",
				event.Response.IncompleteDetails.Reason,
			)
		case "response.failed", "error":
			result := remoteCompactionV2Result{}
			if event.Type == "response.failed" {
				result = remoteCompactionV2ResultWithUsage(event.Response)
			}
			eventErr := &responseStreamEventError{
				code:    responseStreamEventErrorCode(event),
				message: responseStreamEventErrorMessage(event),
			}
			if !isRetryableResponseStreamEventError(event) {
				return result, retry.Unrecoverable(eventErr)
			}
			return result, eventErr
		}
	}

	if err := stream.Err(); err != nil {
		if completed != nil {
			return remoteCompactionV2ResultWithUsage(*completed), err
		}
		return remoteCompactionV2Result{}, err
	}
	if err := ctx.Err(); err != nil {
		if completed != nil {
			return remoteCompactionV2ResultWithUsage(*completed), err
		}
		return remoteCompactionV2Result{}, err
	}
	if completed == nil {
		return remoteCompactionV2Result{}, errors.New(
			"remote compaction v2 stream closed before response.completed",
		)
	}
	result := remoteCompactionV2ResultWithUsage(*completed)
	if compactionCount != 1 {
		return result, retry.Unrecoverable(errors.Errorf(
			"remote compaction v2 expected exactly one compaction output item, got %d from %d output items",
			compactionCount,
			outputItemCount,
		))
	}
	if compactionItem.AsCompaction().EncryptedContent == "" && compactionItem.EncryptedContent == "" {
		return result, retry.Unrecoverable(errors.New(
			"remote compaction v2 returned empty encrypted content",
		))
	}

	result.output = compactionItem
	return result, nil
}

func retainedStoredItemsForRemoteCompactionV2(items []StoredInputItem) []StoredInputItem {
	candidates := make([]StoredInputItem, 0, len(items))
	for _, item := range items {
		if shouldRetainStoredItemForRemoteCompactionV2(item) {
			candidates = append(candidates, item)
		}
	}

	remaining := remoteCompactionV2RetainedMessageTokenBudget
	retainedReversed := make([]StoredInputItem, 0, len(candidates))
	for i := len(candidates) - 1; i >= 0 && remaining > 0; i-- {
		item := candidates[i]
		tokens := approximateStoredMessageTokens(item)
		if tokens <= remaining {
			retainedReversed = append(retainedReversed, item)
			remaining -= tokens
			continue
		}

		if truncated, ok := truncateStoredMessageToApproxTokens(item, remaining); ok {
			retainedReversed = append(retainedReversed, truncated)
		}
		remaining = 0
	}

	retained := make([]StoredInputItem, len(retainedReversed))
	for i := range retainedReversed {
		retained[len(retainedReversed)-1-i] = retainedReversed[i]
	}
	return retained
}

func shouldRetainStoredItemForRemoteCompactionV2(item StoredInputItem) bool {
	if item.Type != "message" {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(item.Role), "user") && !goals.IsContextText(item.Content)
}

func approximateStoredMessageTokens(item StoredInputItem) int {
	return max(approximateTextTokens(item.Content), 1)
}

func truncateStoredMessageToApproxTokens(item StoredInputItem, maxTokens int) (StoredInputItem, bool) {
	if item.Type != "message" || maxTokens <= 0 {
		return StoredInputItem{}, false
	}

	content := truncateMiddleToApproxTokens(item.Content, maxTokens)
	if content == "" {
		return StoredInputItem{}, false
	}
	item.Content = content
	if len(item.RawItem) > 0 {
		item.RawItem = rewriteStoredMessageRawText(item.RawItem, item.Role, content)
	}
	return item, true
}

func truncateMiddleToApproxTokens(content string, maxTokens int) string {
	maxBytes := maxTokens * 4
	if maxBytes <= 0 {
		return ""
	}
	if len(content) <= maxBytes {
		return content
	}

	marker := "\n...[truncated]...\n"
	if maxBytes <= len(marker)+2 {
		return content[:utf8SafePrefixEnd(content, maxBytes)]
	}
	remaining := maxBytes - len(marker)
	headBudget := (remaining + 1) / 2
	tailBudget := remaining - headBudget
	headEnd := utf8SafePrefixEnd(content, headBudget)
	tailStart := utf8SafeSuffixStart(content, tailBudget)
	return content[:headEnd] + marker + content[tailStart:]
}

func utf8SafePrefixEnd(content string, maxBytes int) int {
	if maxBytes >= len(content) {
		return len(content)
	}
	end := max(maxBytes, 0)
	for end > 0 && !utf8.RuneStart(content[end]) {
		end--
	}
	return end
}

func utf8SafeSuffixStart(content string, maxBytes int) int {
	if maxBytes >= len(content) {
		return 0
	}
	start := max(len(content)-max(maxBytes, 0), 0)
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	return start
}

func rewriteStoredMessageRawText(raw json.RawMessage, role, content string) json.RawMessage {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}

	switch rawContent := payload["content"].(type) {
	case string:
		payload["content"] = content
	case []any:
		updated := make([]any, 0, len(rawContent))
		replaced := false
		for _, rawPart := range rawContent {
			part, ok := rawPart.(map[string]any)
			if !ok {
				updated = append(updated, rawPart)
				continue
			}
			partType, _ := part["type"].(string)
			if partType == "input_text" || partType == "output_text" {
				if replaced {
					continue
				}
				part["text"] = content
				replaced = true
			}
			updated = append(updated, part)
		}
		if !replaced {
			partType := "input_text"
			if strings.EqualFold(strings.TrimSpace(role), "assistant") {
				partType = "output_text"
			}
			updated = append(updated, map[string]any{"type": partType, "text": content})
		}
		payload["content"] = updated
	default:
		payload["content"] = content
	}

	updated, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return updated
}

func estimateRemoteCompactionV2ContextTokens(
	instructions string,
	items []responses.ResponseInputItemUnionParam,
) int {
	estimated := approximateTextTokens(instructions)
	for _, item := range items {
		estimated += approximateResponseInputItemTokens(item)
	}
	return max(estimated, 1)
}

func (t *Thread) replaceCompactedHistory(
	expectedRevision uint64,
	newInputItems []responses.ResponseInputItemUnionParam,
	newStoredItems []StoredInputItem,
	estimatedContextTokens int,
) error {
	t.historyMu.Lock()
	if t.historyRevision != expectedRevision {
		t.historyMu.Unlock()
		return errRemoteCompactionHistoryChanged
	}

	t.inputItems = newInputItems
	t.storedItems = newStoredItems
	t.historyRevision++
	if t.isCodex {
		t.codexWindowGeneration++
	}

	t.Mu.Lock()
	t.ResetContextStateLocked()
	pricing := t.getPricing(t.Config.Model)
	if t.Usage == nil {
		t.Usage = &llmtypes.Usage{}
	}
	if t.Usage != nil {
		t.Usage.MaxContextWindow = pricing.ContextWindow
	}

	if t.Usage != nil {
		t.Usage.CurrentContextWindow = max(estimatedContextTokens, 1)
	}
	t.Mu.Unlock()
	t.historyMu.Unlock()
	if t.isCodex {
		t.resetResponsesWebSocket()
	} else {
		t.webSocketContinuation.reset()
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

func (t *Thread) runUtilityPromptWithInput(
	ctx context.Context,
	prompt string,
	useWeakModel bool,
	inputItems []responses.ResponseInputItemUnionParam,
) (string, error) {
	config := t.utilityThreadConfig()
	return base.RunUtilityPrompt(ctx,
		func() (*Thread, error) {
			return NewThread(config)
		},
		func(summaryThread *Thread) {
			// Copy input items to the summary thread.
			summaryThread.inputItems = inputItems
		},
		prompt,
		useWeakModel,
	)
}

func (t *Thread) utilityThreadConfig() llmtypes.Config {
	config := t.Config
	if t.useWebSocket {
		return config
	}

	openAIConfig := llmtypes.OpenAIConfig{}
	if config.OpenAI != nil {
		openAIConfig = *config.OpenAI
	}
	useWebSocket := false
	openAIConfig.WebSocketMode = &useWebSocket
	config.OpenAI = &openAIConfig
	return config
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

// ForkConversation snapshots the live thread into a new persisted conversation.
func (t *Thread) ForkConversation(ctx context.Context) (string, error) {
	if t.ConversationForkBlocked() {
		return "", llmtypes.ErrConversationForkUnavailable
	}
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	if !t.Persisted || t.Store == nil {
		return "", llmtypes.ErrConversationForkUnavailable
	}
	record, err := t.buildConversationRecord(ctx, t.snapshotConversationState(false), false)
	if err != nil {
		return "", err
	}
	forkOptions := convtypes.ConversationForkOptions{Mode: convtypes.ConversationForkModeLiveSnapshot}
	if initiator, ok := convtypes.ConversationForkInitiatorFromContext(ctx); ok {
		forkOptions.Initiator = &initiator
	}
	forked, err := conversations.PersistConversationFork(ctx, t.Store, record, forkOptions)
	if err != nil {
		return "", err
	}
	return forked.ID, nil
}

type conversationStateSnapshot struct {
	storedItems      []StoredInputItem
	windowGeneration uint64
	usage            llmtypes.Usage
	toolResults      map[string]tooltypes.StructuredToolResult
	metadata         map[string]any
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
	t.Mu.Unlock()
	t.historyMu.Unlock()
	return conversationStateSnapshot{
		storedItems:      storedItems,
		windowGeneration: windowGeneration,
		usage:            usage,
		toolResults:      toolResults,
		metadata:         metadata,
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
		ID:          t.ConversationID,
		CWD:         t.Config.WorkingDirectory,
		RawMessages: inputItemsJSON,
		Provider:    "openai",
		Usage:       snapshot.usage,
		Metadata:    metadata,
		Summary:     name,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ToolResults: toolResults,
	}, nil
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
	if t.ToolResults == nil {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.Mu.Unlock()
	t.historyMu.Unlock()
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
	for len(t.storedItems) > 0 {
		lastItem := t.storedItems[len(t.storedItems)-1]
		if lastItem.Type == "function_call" {
			t.storedItems = t.storedItems[:len(t.storedItems)-1]
			changed = true
			continue
		}
		break
	}
	return changed
}

func (t *Thread) cleanupOrphanedItems() {
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	if t.cleanupOrphanedItemsLocked() {
		t.historyRevision++
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

func supportsRemoteCompactionV2(config llmtypes.Config) bool {
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

// disableSDKServerOverloadRetries leaves ordinary SDK retries unchanged while
// reserving structured overload retries for the outer Responses retry loop.
func disableSDKServerOverloadRetries() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil || resp.StatusCode < http.StatusBadRequest {
			return resp, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if readErr != nil {
			return resp, err
		}
		if isResponsesServerOverloadedCode(responsesAPIErrorCodeFromBody(string(body))) {
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			resp.Header.Set(responsesServerOverloadHeader, "true")
			resp.Header.Set("x-should-retry", "false")
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
