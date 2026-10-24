package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// CompactContext keeps the application-owned trigger while delegating summarization
// to Anthropic when the selected model advertises on-demand compaction support.
func (t *Thread) CompactContext(ctx context.Context) error {
	if len(t.messages) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	model := t.Config.Model
	contexts := base.EnvironmentContexts(t)
	systemPrompt, err := base.ProcessSystemPrompt(ctx, t, sysprompt.SystemPrompt(model, t.Config, contexts))
	if err != nil {
		return err
	}
	params, err := t.messageParams(model, t.Config.MaxTokens, systemPrompt, llmtypes.MessageOpt{})
	if err != nil {
		return err
	}
	return t.compactContext(ctx, params)
}

var errCompactionHistoryChanged = errors.New("conversation history changed during Anthropic compaction")

func (t *Thread) compactContext(ctx context.Context, params anthropic.MessageNewParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !t.useCopilot {
		model, err := t.client.Beta.Models.Get(ctx, params.Model, anthropic.BetaModelGetParams{
			Betas: []anthropic.AnthropicBeta{anthropic.AnthropicBetaCompact2026_09_04},
		}, option.WithMaxRetries(t.Config.Retry.Attempts))
		if err == nil &&
			model.Capabilities.Compaction.Supported &&
			model.Capabilities.Compaction.Summarize.Supported {
			return t.compactContextNativeWithFallback(ctx, params)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.G(ctx).WithError(err).Warn(
				"Anthropic native compaction unavailable, falling back to summary compaction",
			)
		}
	}
	return base.CompactContextWithSummary(ctx, t.runUtilityPrompt, t.SwapContext)
}

// A failed native compaction (including the documented null-content block) falls
// back to a summary. A cancelled turn or a history that moved on must not.
func (t *Thread) compactContextNativeWithFallback(ctx context.Context, params anthropic.MessageNewParams) error {
	nativeErr := t.compactContextNative(ctx, params)
	if nativeErr == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(nativeErr, errCompactionHistoryChanged) {
		return nativeErr
	}
	logger.G(ctx).WithError(nativeErr).Warn(
		"Anthropic native compaction failed, falling back to summary compaction",
	)
	if err := base.CompactContextWithSummary(ctx, t.runUtilityPrompt, t.SwapContext); err != nil {
		return errors.Wrapf(err, "failed to compact context (native compaction error: %v)", nativeErr)
	}
	return nil
}

func (t *Thread) compactContextNative(ctx context.Context, params anthropic.MessageNewParams) (err error) {
	history, err := json.Marshal(params.Messages)
	if err != nil {
		return errors.Wrap(err, "failed to snapshot compaction history")
	}
	if len(params.Messages) > 0 && isMessageToolUse(params.Messages[len(params.Messages)-1]) {
		return errors.New("cannot compact context with unresolved tool calls")
	}
	model := params.Model
	params.MaxTokens = max(params.MaxTokens, 4096)
	raw, err := json.Marshal(params)
	if err != nil {
		return errors.Wrap(err, "failed to encode compaction request")
	}
	var betaParams anthropic.BetaMessageNewParams
	if err := json.Unmarshal(raw, &betaParams); err != nil {
		return errors.Wrap(err, "failed to prepare beta compaction request")
	}
	// Signed blocks must round-trip without the SDK adding or dropping fields.
	betaParams.SetExtraFields(map[string]any{"messages": params.Messages})
	betaParams.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaCompact2026_09_04}
	betaParams.Compaction = anthropic.BetaCompactionConfigUnionParam{
		OfSummarize: &anthropic.BetaSummarizeCompactionParam{},
	}
	requestOpts := []option.RequestOption{option.WithMaxRetries(t.Config.Retry.Attempts)}
	if requiresInterleavedThinkingBeta(params) {
		requestOpts = append(requestOpts,
			option.WithHeaderAdd("anthropic-beta", "interleaved-thinking-2025-05-14"),
		)
	}
	ctx, span := t.StartModelSpan(ctx, "anthropic", model,
		attribute.String("kodelet.model_call.purpose", "compaction"),
	)
	var message anthropic.BetaMessage
	defer func() {
		// A disconnected stream can still report billable usage before it ends.
		total, usageErr := t.recordCompactionUsage(message, model)
		if err == nil {
			err = usageErr
		}
		span.SetAttributes(
			attribute.String("gen_ai.response.id", message.ID),
			attribute.String("gen_ai.response.model", message.Model),
			attribute.Int64("gen_ai.usage.input_tokens",
				total.InputTokens+total.CacheReadInputTokens+total.CacheCreationInputTokens,
			),
			attribute.Int64("gen_ai.usage.output_tokens", total.OutputTokens),
			attribute.Int64("gen_ai.usage.cache_read.input_tokens", total.CacheReadInputTokens),
			attribute.Int64("gen_ai.usage.cache_write.input_tokens", total.CacheCreationInputTokens),
		)
		base.FinishModelSpan(span, err)
	}()
	stream := t.client.Beta.Messages.NewStreaming(ctx, betaParams, requestOpts...)
	defer stream.Close()
	completed := false
	for stream.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return errors.Wrap(err, "failed to accumulate compaction response")
		}
		if event.Type == "message_stop" {
			completed = true
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stream.Err(); err != nil {
		return err
	}
	if !completed {
		return errors.New("Anthropic compaction stream ended before message_stop event")
	}

	if message.StopReason != anthropic.BetaStopReasonCompaction ||
		len(message.Content) != 1 ||
		message.Content[0].Type != "compaction" {
		return errors.Errorf(
			"Anthropic compaction did not return a single compaction block (stop reason: %s)",
			message.StopReason,
		)
	}
	block := message.Content[0].AsCompaction()
	if strings.TrimSpace(block.Content) == "" || strings.TrimSpace(block.Signature) == "" {
		return errors.New("Anthropic compaction returned an empty summary or signature")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	t.Mu.Lock()
	defer t.Mu.Unlock()
	current, err := json.Marshal(t.messages)
	if err != nil {
		return errors.Wrap(err, "failed to archive context")
	}
	if !bytes.Equal(history, current) {
		return errCompactionHistoryChanged
	}
	if err := t.ArchiveCompactionLocked(history, 1, "api", ""); err != nil {
		return err
	}
	t.messages = []anthropic.MessageParam{
		anthropic.NewAssistantMessage(
			param.Override[anthropic.ContentBlockParamUnion](json.RawMessage(message.Content[0].RawJSON())),
		),
	}
	t.FinalizeSwapContextLocked(block.Content)
	return nil
}

// Top-level usage excludes compaction iterations. Count those in addition to the
// ordinary usage, but never use compaction billing tokens as the new context size.
func (t *Thread) recordCompactionUsage(
	message anthropic.BetaMessage,
	model anthropic.Model,
) (anthropic.Usage, error) {
	var total anthropic.Usage
	if message.RawJSON() == "" {
		return total, nil
	}
	var response struct {
		Usage anthropic.Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(message.RawJSON()), &response); err != nil {
		return total, errors.Wrap(err, "failed to decode compaction response usage")
	}
	usageRecords := []anthropic.Usage{response.Usage}
	for _, iteration := range message.Usage.Iterations {
		if iteration.Type != "compaction" {
			continue
		}
		var usage anthropic.Usage
		if err := json.Unmarshal([]byte(iteration.RawJSON()), &usage); err != nil {
			return total, errors.Wrap(err, "failed to decode compaction iteration usage")
		}
		usageRecords = append(usageRecords, usage)
	}
	for _, usage := range usageRecords {
		t.recordUsage(usage, model, false)
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.CacheReadInputTokens += usage.CacheReadInputTokens
		total.CacheCreationInputTokens += usage.CacheCreationInputTokens
	}
	return total, nil
}

// The stable SDK does not model compaction. Keep the signed block opaque instead
// of converting it to text or changing every ordinary message to beta types.
func hasCompactionBlock(messages []anthropic.MessageParam) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			raw, _ := block.Overrides()
			data, ok := raw.(json.RawMessage)
			if !ok {
				continue
			}
			var header struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &header) == nil && header.Type == "compaction" {
				return true
			}
		}
	}
	return false
}
