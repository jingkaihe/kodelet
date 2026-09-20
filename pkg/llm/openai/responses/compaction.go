package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	_ "golang.org/x/image/webp"
)

const (
	remoteCompactionV2RetainedMessageTokenBudget = 64_000
	remoteCompactionV2TruncatedOutputMessage     = "Output exceeded the available model context and was truncated"
	remoteCompactionV2ResizedImageBytesEstimate  = 7_373
	remoteCompactionV2OriginalImagePatchSize     = 32
	remoteCompactionV2OriginalImageMaxPatches    = 10_000
)

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
	responseID  string
	model       string
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

var errRemoteCompactionHistoryChanged = errors.New("conversation history changed during remote compaction v2")

type remoteCompactionV2StreamFactory func(
	context.Context,
	responses.ResponseNewParams,
) (*ssestream.Stream[responses.ResponseStreamEventUnion], error)

func (t *Thread) compactContextRemoteV2(ctx context.Context, compactionMetadata ...codexCompactionMetadata) error {
	metadata := remoteCompactionV2ManualMetadata
	if len(compactionMetadata) > 0 {
		metadata = compactionMetadata[0]
	}
	snapshot := t.snapshotHistory()
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

	// The stream collector accepts exactly one compaction item with encrypted content.
	compactionItem := StoredInputItem{
		Type:             "compaction",
		RawItem:          json.RawMessage(raw),
		EncryptedContent: result.output.AsCompaction().EncryptedContent,
	}
	if compactionItem.EncryptedContent == "" {
		compactionItem.EncryptedContent = result.output.EncryptedContent
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
	requestMetadata codexResponsesRequestMetadata,
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
	applyPromptCacheOptions(&params, t.Config, t.Config.Model)

	if serviceTier := normalizeServiceTier(t.Config).WireValue(); serviceTier != "" {
		params.ServiceTier = responses.ResponseNewParamsServiceTier(serviceTier)
	}
	if t.isReasoningModelDynamic(t.Config.Model) && t.reasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort:  openAIReasoningEffortForRequest(t.Config.Model, t.reasoningEffort),
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
	requestMetadata.apply(&params)
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
	requestMetadata codexResponsesRequestMetadata,
) (result remoteCompactionV2Result, err error) {
	ctx, span := t.StartModelSpan(ctx, "openai", params.Model,
		attribute.Bool("gen_ai.request.stream", true),
		attribute.String("kodelet.model_call.purpose", "compaction"))
	defer func() {
		var inputTokens, outputTokens, cacheTokens, reasoningTokens int64
		for _, record := range result.usageRecords {
			inputTokens += record.usage.InputTokens
			outputTokens += record.usage.OutputTokens
			cacheTokens += record.usage.InputTokensDetails.CachedTokens
			reasoningTokens += record.usage.OutputTokensDetails.ReasoningTokens
			span.SetAttributes(
				attribute.String("gen_ai.response.id", record.responseID),
				attribute.String("gen_ai.response.model", record.model),
			)
		}
		if len(result.usageRecords) > 0 {
			span.SetAttributes(
				attribute.Int64("gen_ai.usage.input_tokens", inputTokens),
				attribute.Int64("gen_ai.usage.output_tokens", outputTokens),
				attribute.Int64("gen_ai.usage.cache_read.input_tokens", cacheTokens),
				attribute.Int64("gen_ai.usage.reasoning.output_tokens", reasoningTokens),
			)
		}
		spanErr := err
		if spanErr == nil {
			spanErr = ctx.Err()
		}
		base.FinishModelSpan(span, spanErr)
	}()
	var priorUsage []remoteCompactionV2UsageRecord
	if t.useWebSocket && t.webSocket != nil {
		webSocketHeaders := t.codexResponsesWebSocketHeaders(requestMetadata)
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
		telemetry.AddEvent(ctx, "kodelet.model.transport_fallback",
			attribute.String("kodelet.transport.from", "websocket"),
			attribute.String("kodelet.transport.to", "https"))
		t.useWebSocket = false
		t.resetResponsesWebSocket()
		logger.G(ctx).WithError(err).Warn("remote compaction v2 websocket failed, falling back to HTTPS")
	}

	requestOpts := append(t.requestOptions(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent}),
		option.WithMaxRetries(0), disableSDKServerOverloadRetries())
	requestOpts = append(requestOpts, t.codexResponsesRequestOptions(requestMetadata)...)
	newStreaming := t.newStreamingFunc
	if newStreaming == nil {
		if t.client == nil {
			return remoteCompactionV2Result{usageRecords: priorUsage}, errors.New("openai client is not initialized")
		}
		newStreaming = t.client.Responses.NewStreaming
	}

	result, err = t.runRemoteCompactionV2WithRetries(ctx, params, "https", func(
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
			telemetry.AddEvent(ctx, "kodelet.model.retry",
				attribute.Int("retry.attempt", int(n)+1), attribute.String("kodelet.model.transport", transportName))
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
			responseID:  response.ID,
			model:       response.Model,
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

	return strings.EqualFold(strings.TrimSpace(item.Role), "user")
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
	t.Usage.MaxContextWindow = pricing.ContextWindow
	t.Usage.CurrentContextWindow = max(estimatedContextTokens, 1)
	t.Mu.Unlock()
	t.historyMu.Unlock()
	if t.isCodex {
		t.resetResponsesWebSocket()
	} else {
		t.webSocketContinuation.reset()
	}
	return nil
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
