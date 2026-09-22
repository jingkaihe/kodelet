package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreadSwapContextArchivesHistoryAndPreservesToolResults(t *testing.T) {
	state := tools.NewBasicState(context.Background())
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "conv-swap"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("old message", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "old message"}},
	}
	thread.SetState(state)
	thread.SetStructuredToolResult("call-1", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})

	require.NoError(t, thread.SwapContext(context.Background(), "summary of prior context"))

	require.Len(t, thread.inputItems, 1)
	assert.Equal(t, "summary of prior context", extractInputItemText(thread.inputItems[0]))
	assert.Equal(t, []StoredInputItem{{Type: "message", Role: "user", Content: "summary of prior context"}}, thread.storedItems)
	assert.Contains(t, thread.GetStructuredToolResults(), "call-1")
	require.NotNil(t, thread.CompactionHistory)
	require.Len(t, thread.CompactionHistory.Segments, 1)
	assert.Equal(t, 1, thread.CompactionHistory.ActiveDisplayStart)
	segment := thread.CompactionHistory.Segments[0]
	assert.JSONEq(t, `[{"type":"message","role":"user","content":"old message"}]`, string(segment.RawMessages))
	assert.Equal(t, "summary", segment.Marker.Method)
	assert.Equal(t, "summary of prior context", segment.Marker.Summary)
	assert.NotEmpty(t, segment.Marker.ID)
	assert.False(t, segment.Marker.CreatedAt.IsZero())
	assert.Greater(t, thread.GetUsage().CurrentContextWindow, 0)

	history, archive, usage := thread.snapshotHistory(), thread.GetCompactionHistory(), thread.GetUsage()
	for _, empty := range []string{"", " \n\t "} {
		require.ErrorContains(t, thread.SwapContext(t.Context(), empty), "compact summary is empty")
		assert.Equal(t, history, thread.snapshotHistory())
		assert.Equal(t, archive, thread.GetCompactionHistory())
		assert.Equal(t, usage, thread.GetUsage())
	}
}

func TestRepeatedCompactionArchivesOnlyNewChat(t *testing.T) {
	for _, method := range []string{"api", "summary"} {
		t.Run(method, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.5",
				Retry:    llmtypes.RetryConfig{Attempts: 1},
				OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
			}
			thread := &Thread{Thread: base.NewThread(config, "repeated-compact")}
			thread.AddUserMessage(t.Context(), "first request", "data:image/png;base64,aGVsbG8=")
			firstAnswer := []StoredInputItem{
				{Type: "reasoning", Role: "assistant", Content: "original reasoning"},
				{Type: "message", Role: "assistant", Content: "first answer"},
			}
			thread.appendHistoryItems(fromStoredItems(firstAnswer), firstAnswer)
			original := mustJSON(t, thread.snapshotHistory().storedItems)
			var requests []openairesponses.ResponseNewParams
			var nextEncrypted string
			thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				requests = append(requests, params)
				return remoteCompactionV2Stream(t, nextEncrypted)
			}
			compact := func(summary string) {
				t.Helper()
				if method == "api" {
					nextEncrypted = summary
					require.NoError(t, thread.CompactContext(t.Context()))
				} else {
					require.NoError(t, thread.SwapContext(t.Context(), summary))
				}
			}

			compact("first compact context")
			firstArchive := thread.CompactionHistory.Clone()
			require.Len(t, firstArchive.Segments, 1)
			assert.JSONEq(t, string(original), string(firstArchive.Segments[0].RawMessages))
			assert.Equal(t, len(thread.storedItems), firstArchive.ActiveDisplayStart)

			thread.AddUserMessage(t.Context(), "second request")
			secondAnswer := []StoredInputItem{
				{Type: "function_call", CallID: "call-1", Name: "bash", Arguments: `{}`},
				{Type: "function_call_output", CallID: "call-1", Output: "result"},
				{Type: "message", Role: "assistant", Content: "second answer"},
			}
			thread.appendHistoryItems(fromStoredItems(secondAnswer), secondAnswer)
			thread.SetStructuredToolResult("call-1", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})
			secondSegment := mustJSON(t, thread.storedItems[firstArchive.ActiveDisplayStart:])
			inputBeforeSecond := thread.inputItemsSnapshot()

			compact("second compact context")

			history := thread.CompactionHistory
			require.Len(t, history.Segments, 2)
			assert.Equal(t, firstArchive.Segments[0], history.Segments[0])
			assert.JSONEq(t, string(secondSegment), string(history.Segments[1].RawMessages))
			assert.NotContains(t, string(history.Segments[1].RawMessages), "first compact context")
			assert.NotEqual(t, history.Segments[0].Marker.ID, history.Segments[1].Marker.ID)
			assert.Equal(t, method, history.Segments[1].Marker.Method)
			assert.Contains(t, thread.GetStructuredToolResults(), "call-1")
			assert.Equal(t, len(thread.storedItems), history.ActiveDisplayStart)
			require.NoError(t, history.Validate(mustJSON(t, thread.storedItems)))

			if method == "api" {
				assert.Empty(t, history.Segments[0].Marker.Summary)
				assert.Empty(t, history.Segments[1].Marker.Summary)
				require.Len(t, requests, 2)
				secondInput := requests[1].Input.OfInputItemList
				require.Len(t, secondInput, len(inputBeforeSecond)+1)
				assert.JSONEq(t, string(mustJSON(t, inputBeforeSecond)), string(mustJSON(t, secondInput[:len(inputBeforeSecond)])))
				require.Len(t, thread.storedItems, 3)
				assert.Equal(t, "second compact context", thread.storedItems[2].EncryptedContent)
			} else {
				assert.Equal(t, "first compact context", history.Segments[0].Marker.Summary)
				assert.Equal(t, "second compact context", history.Segments[1].Marker.Summary)
				require.Len(t, thread.storedItems, 1)
				assert.Equal(t, "second compact context", thread.storedItems[0].Content)
			}
			thread.AddUserMessage(t.Context(), "third request")
			assert.Len(t, thread.storedItems[history.ActiveDisplayStart:], 1)
			assert.Equal(t, "third request", thread.storedItems[history.ActiveDisplayStart].Content)
			assert.NotContains(t, string(mustJSON(t, thread.inputItemsSnapshot())), "first answer")
			assert.NotContains(t, string(mustJSON(t, thread.inputItemsSnapshot())), "second answer")
		})
	}
}

func TestCompactionArchiveFailureDoesNotReplaceContext(t *testing.T) {
	for _, method := range []string{"api", "summary"} {
		t.Run(method, func(t *testing.T) {
			thread := &Thread{Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "invalid-archive")}
			thread.AddUserMessage(t.Context(), "original")
			thread.CompactionHistory = &convtypes.CompactionHistory{ActiveDisplayStart: 2}
			original := thread.snapshotHistory()
			archive := thread.CompactionHistory.Clone()
			usage := thread.GetUsage()
			var err error
			if method == "summary" {
				err = thread.SwapContext(t.Context(), "replacement")
			} else {
				replacement := []StoredInputItem{{Type: "compaction", EncryptedContent: "replacement"}}
				err = thread.replaceCompactedHistory(original.revision, fromStoredItems(replacement), replacement, 5)
			}
			require.Error(t, err)
			assert.Equal(t, original, thread.snapshotHistory())
			assert.Equal(t, archive, thread.CompactionHistory.Clone())
			assert.Equal(t, usage, thread.GetUsage())
		})
	}
}

func remoteCompactionV2Stream(t *testing.T, encryptedContent string, extraOutputItems ...map[string]any) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
	t.Helper()
	events := make([]map[string]any, 0, len(extraOutputItems)+2)
	for _, item := range extraOutputItems {
		events = append(events, map[string]any{
			"type": "response.output_item.done",
			"item": item,
		})
	}
	events = append(events,
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":              "compaction",
				"encrypted_content": encryptedContent,
			},
		},
		remoteCompactionV2TerminalEvent("response.completed", "", 100, 10, 20),
	)
	return responseStreamFromMaps(t, events)
}

func remoteCompactionV2TerminalEvent(
	eventType string,
	serviceTier string,
	inputTokens int,
	outputTokens int,
	cachedTokens int,
) map[string]any {
	status := strings.TrimPrefix(eventType, "response.")
	response := map[string]any{
		"id":     "resp_compact_" + status,
		"status": status,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
			"input_tokens_details": map[string]any{
				"cached_tokens": cachedTokens,
			},
			"output_tokens_details": map[string]any{
				"reasoning_tokens": 0,
			},
		},
	}
	if serviceTier != "" {
		response["service_tier"] = serviceTier
	}
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	return map[string]any{
		"type":     eventType,
		"response": response,
	}
}

func TestCompactContextUsesRemoteCompactionV2(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return remoteCompactionV2Stream(t, "encrypted-summary")
	}
	thread.compactWithSummaryFunc = func(context.Context) error {
		t.Fatal("summary fallback should not run when remote compaction v2 succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	require.True(t, captured.Instructions.Valid())
	assert.NotEmpty(t, captured.Instructions.Value)
	require.Len(t, captured.Input.OfInputItemList, 2)
	assert.NotNil(t, captured.Input.OfInputItemList[1].OfCompactionTrigger)
	require.True(t, captured.ParallelToolCalls.Valid())
	assert.Equal(t, len(captured.Tools) > 0, captured.ParallelToolCalls.Value)
	require.True(t, captured.PromptCacheKey.Valid())
	assert.Equal(t, "conv-test", captured.PromptCacheKey.Value)

	require.Len(t, thread.storedItems, 2)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, "compaction", thread.storedItems[1].Type)
	assert.Equal(t, "encrypted-summary", thread.storedItems[1].EncryptedContent)
	require.Len(t, thread.inputItems, 2)
	require.NotNil(t, thread.inputItems[1].OfCompaction)
	assert.Equal(t, "encrypted-summary", thread.inputItems[1].OfCompaction.EncryptedContent)
	assert.Equal(t, 100, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
}

func TestCompactContextRemoteV2SummaryFallbackAdvancesCodexWindowOnce(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	fakeWebSocket := &fakeResponsesWebSocketStreamer{}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-summary-fallback"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 4,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
		webSocket:   fakeWebSocket,
	}
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.completed", "", 1, 1, 0),
		})
	}
	thread.compactWithSummaryFunc = func(ctx context.Context) error {
		return thread.SwapContext(ctx, "summary fallback")
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, uint64(5), thread.snapshotHistory().codexWindowGeneration)
	assert.Equal(t, 1, fakeWebSocket.resets)
	require.Len(t, thread.inputItemsSnapshot(), 1)
	assert.Equal(t, "summary fallback", extractInputItemText(thread.inputItemsSnapshot()[0]))
	require.NotNil(t, thread.CompactionHistory)
	require.Len(t, thread.CompactionHistory.Segments, 1)
	assert.Equal(t, "summary", thread.CompactionHistory.Segments[0].Marker.Method)
	assert.Equal(t, "summary fallback", thread.CompactionHistory.Segments[0].Marker.Summary)
}

func TestCompactContextOpenAICompatiblePlatformsUseSummaryCompaction(t *testing.T) {
	for _, platform := range []string{"copilot", "fireworks"} {
		t.Run(platform, func(t *testing.T) {
			thread := &Thread{
				Thread: base.NewThread(
					llmtypes.Config{Provider: "openai", Model: "gpt-5", OpenAI: &llmtypes.OpenAIConfig{Platform: platform}},
					"conv-test",
				),
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
			}

			summaryCalled := false
			thread.compactWithSummaryFunc = func(_ context.Context) error {
				summaryCalled = true
				return nil
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.True(t, summaryCalled)
		})
	}
}

func TestSummaryContextReplacementRejectsConcurrentHistoryChange(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(
			llmtypes.Config{Provider: "openai", Model: "gpt-5", OpenAI: &llmtypes.OpenAIConfig{Platform: "fireworks"}},
			"conv-summary-stale",
		),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original"}},
	}
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	thread.compactWithSummaryFunc = func(context.Context) error {
		snapshot := thread.snapshotHistory()
		close(requestStarted)
		<-releaseRequest
		return thread.swapContextAtRevision(snapshot.revision, "stale summary")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- thread.CompactContext(context.Background())
	}()
	<-requestStarted
	thread.AddUserMessage(context.Background(), "arrived while summarizing")
	close(releaseRequest)

	err := <-errCh
	require.ErrorIs(t, err, errRemoteCompactionHistoryChanged)
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 2)
	assert.Equal(t, "original", extractInputItemText(history[0]))
	assert.Equal(t, "arrived while summarizing", extractInputItemText(history[1]))
	assert.Nil(t, thread.CompactionHistory)
}

func TestSupportsRemoteCompactionV2(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected bool
	}{
		{
			name:     "default platform is openai",
			config:   llmtypes.Config{},
			expected: true,
		},
		{
			name:     "openai",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			expected: true,
		},
		{
			name:     "codex",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: true,
		},
		{
			name:     "copilot",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot"}},
			expected: false,
		},
		{
			name:     "custom compatible platform",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "fireworks"}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, supportsRemoteCompactionV2(tt.config))
		})
	}
}

func TestCompactContextAccountsCacheWriteUsage(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.6-sol",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "openai",
			ServiceTier: llmtypes.OpenAIServiceTierDefault,
		},
	}
	_, customPricing := loadCustomConfiguration(config)
	thread := &Thread{
		Thread:        base.NewThread(config, "conv-test"),
		customPricing: customPricing,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return responseStreamFromMaps(t, []map[string]any{
			{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "enc"},
			},
			{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_compact",
					"status": "completed",
					"usage": map[string]any{
						"input_tokens":  100,
						"output_tokens": 10,
						"total_tokens":  140,
						"input_tokens_details": map[string]any{
							"cached_tokens":      20,
							"cache_write_tokens": 30,
						},
						"output_tokens_details": map[string]any{"reasoning_tokens": 0},
					},
				},
			},
		})
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, openairesponses.ResponseNewParamsServiceTierDefault, captured.ServiceTier)
	assert.Equal(t, 70, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 30, thread.Usage.CacheCreationInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
	assert.Equal(t, 130, thread.Usage.TotalTokens())
	assert.InDelta(t, 50*0.000005, thread.Usage.InputCost, 1e-12)
	assert.InDelta(t, 20*0.0000005, thread.Usage.CacheReadCost, 1e-12)
	assert.InDelta(t, 30*0.00000625, thread.Usage.CacheCreationCost, 1e-12)
	assert.InDelta(t, 10*0.00003, thread.Usage.OutputCost, 1e-12)
	assert.Equal(t,
		estimateRemoteCompactionV2ContextTokens(captured.Instructions.Value, thread.inputItemsSnapshot()),
		thread.Usage.CurrentContextWindow,
	)
	assert.Equal(t, 1_050_000, thread.Usage.MaxContextWindow)
}

func TestCompactContextRemoteV2UsesReturnedServiceTierForPricing(t *testing.T) {
	tests := []struct {
		name                  string
		configuredTier        llmtypes.OpenAIServiceTier
		returnedTier          string
		expectedInputCost     float64
		expectedCacheReadCost float64
		expectedOutputCost    float64
	}{
		{
			name:                  "auto served as priority",
			configuredTier:        llmtypes.OpenAIServiceTierAuto,
			returnedTier:          "priority",
			expectedInputCost:     80 * 0.00001,
			expectedCacheReadCost: 20 * 0.000001,
			expectedOutputCost:    10 * 0.00006,
		},
		{
			name:                  "priority served as default",
			configuredTier:        llmtypes.OpenAIServiceTierPriority,
			returnedTier:          "default",
			expectedInputCost:     80 * 0.000005,
			expectedCacheReadCost: 20 * 0.0000005,
			expectedOutputCost:    10 * 0.00003,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.6-sol",
				Retry:    llmtypes.RetryConfig{Attempts: 1},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:    "openai",
					ServiceTier: tt.configuredTier,
				},
			}
			_, customPricing := loadCustomConfiguration(config)
			thread := &Thread{
				Thread:        base.NewThread(config, "conv-returned-tier"),
				customPricing: customPricing,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}

			thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				return responseStreamFromMaps(t, []map[string]any{
					{
						"type": "response.output_item.done",
						"item": map[string]any{"type": "compaction", "encrypted_content": "enc"},
					},
					remoteCompactionV2TerminalEvent("response.completed", tt.returnedTier, 100, 10, 20),
				})
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.InDelta(t, tt.expectedInputCost, thread.Usage.InputCost, 1e-12)
			assert.InDelta(t, tt.expectedCacheReadCost, thread.Usage.CacheReadCost, 1e-12)
			assert.InDelta(t, tt.expectedOutputCost, thread.Usage.OutputCost, 1e-12)
		})
	}
}

func TestCompactContextFallsBackOnRemoteCompactionV2Error(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.completed", "", 1, 1, 0),
		})
	}

	fallbackCalled := false
	thread.compactWithSummaryFunc = func(_ context.Context) error {
		fallbackCalled = true
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.True(t, fallbackCalled)
	assert.Equal(t, 1, thread.Usage.InputTokens)
	assert.Equal(t, 1, thread.Usage.OutputTokens)
	assert.Zero(t, thread.Usage.CurrentContextWindow, "rejected output usage must not replace active-context metrics")
}

func TestCollectRemoteCompactionV2StreamIgnoresOtherOutputItems(t *testing.T) {
	stream := remoteCompactionV2Stream(t, "encrypted-summary", map[string]any{
		"type":      "function_call",
		"call_id":   "call-1",
		"name":      "bash",
		"arguments": `{"command":"false"}`,
	})

	result, err := collectRemoteCompactionV2Stream(context.Background(), stream)
	require.NoError(t, err)
	assert.Equal(t, "compaction", result.output.Type)
	assert.Equal(t, "encrypted-summary", result.output.AsCompaction().EncryptedContent)
}

func TestCollectRemoteCompactionV2StreamPreservesIncompleteUsage(t *testing.T) {
	result, err := collectRemoteCompactionV2Stream(
		context.Background(),
		responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.incomplete", "priority", 12, 3, 4),
		}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	require.Len(t, result.usageRecords, 1)
	assert.Equal(t, int64(12), result.usageRecords[0].usage.InputTokens)
	assert.Equal(t, int64(3), result.usageRecords[0].usage.OutputTokens)
	assert.Equal(t, int64(4), result.usageRecords[0].usage.InputTokensDetails.CachedTokens)
	assert.Equal(t, llmtypes.OpenAIServiceTierPriority, result.usageRecords[0].serviceTier)
}

func TestCollectRemoteCompactionV2StreamValidation(t *testing.T) {
	completed := remoteCompactionV2TerminalEvent("response.completed", "", 1, 1, 0)
	compaction := func(encrypted string) map[string]any {
		return map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{"type": "compaction", "encrypted_content": encrypted},
		}
	}

	tests := []struct {
		name        string
		events      []map[string]any
		message     string
		recoverable bool
	}{
		{name: "missing compaction", events: []map[string]any{completed}, message: "exactly one compaction"},
		{name: "multiple compactions", events: []map[string]any{compaction("one"), compaction("two"), completed}, message: "got 2"},
		{name: "missing completion", events: []map[string]any{compaction("one")}, message: "before response.completed", recoverable: true},
		{
			name: "incomplete response",
			events: []map[string]any{{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":                 "resp_incomplete",
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				},
			}},
			message:     "response incomplete",
			recoverable: true,
		},
		{name: "empty encrypted content", events: []map[string]any{compaction(""), completed}, message: "empty encrypted content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := collectRemoteCompactionV2Stream(context.Background(), responseStreamFromMaps(t, tt.events))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
			assert.Equal(t, tt.recoverable, retry.IsRecoverable(err))
		})
	}
}

func TestCompactContextRemoteV2RetriesRetryableStreamTermination(t *testing.T) {
	tests := []struct {
		name        string
		firstEvents []map[string]any
	}{
		{
			name: "clean close before completion",
			firstEvents: []map[string]any{{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "partial"},
			}},
		},
		{
			name: "incomplete response",
			firstEvents: []map[string]any{{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":                 "resp_incomplete",
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.5",
				Retry: llmtypes.RetryConfig{
					Attempts:     2,
					InitialDelay: 1,
					MaxDelay:     1,
					BackoffType:  "fixed",
				},
				OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
			}
			thread := &Thread{
				Thread: base.NewThread(config, "conv-test"),
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}

			attempts := 0
			thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				attempts++
				if attempts == 1 {
					return responseStreamFromMaps(t, tt.firstEvents)
				}
				return remoteCompactionV2Stream(t, "retried-summary")
			}
			thread.compactWithSummaryFunc = func(context.Context) error {
				t.Fatal("summary fallback should not run after a successful retry")
				return nil
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.Equal(t, 2, attempts)
			require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
			assert.Equal(t, "retried-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
		})
	}
}

func TestCompactContextRemoteV2AccumulatesIncompleteRetryUsage(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.6-sol",
		Retry: llmtypes.RetryConfig{
			Attempts:     2,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "openai",
			ServiceTier: llmtypes.OpenAIServiceTierDefault,
		},
	}
	_, customPricing := loadCustomConfiguration(config)
	thread := &Thread{
		Thread:        base.NewThread(config, "conv-incomplete-retries"),
		customPricing: customPricing,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.Usage.CurrentContextWindow = 55
	thread.Usage.MaxContextWindow = 100

	attempts := 0
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.incomplete", "default", 10, 1, 2),
		})
	}

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	assert.Equal(t, 2, attempts)
	usage := thread.GetUsage()
	assert.Equal(t, 20, usage.InputTokens)
	assert.Equal(t, 4, usage.CacheReadInputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
	assert.InDelta(t, 16*0.000005, usage.InputCost, 1e-12)
	assert.InDelta(t, 4*0.0000005, usage.CacheReadCost, 1e-12)
	assert.InDelta(t, 2*0.00003, usage.OutputCost, 1e-12)
	assert.Equal(t, 55, usage.CurrentContextWindow)
	assert.Equal(t, 100, usage.MaxContextWindow)
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 1)
	assert.Nil(t, history[0].OfCompaction)
}

func TestRetainedStoredItemsForRemoteCompactionV2(t *testing.T) {
	items := []StoredInputItem{
		{Type: "message", Role: "user", Content: "first user"},
		{Type: "message", Role: "assistant", Content: "final answer", RawItem: json.RawMessage(`{"phase":"final_answer"}`)},
		{Type: "message", Role: "assistant", Content: "commentary", RawItem: json.RawMessage(`{"phase":"commentary","content":[{"type":"output_text","text":"commentary"}]}`)},
		{Type: "function_call", CallID: "call-1", Name: "bash", Arguments: `{}`},
		{Type: "message", Role: "developer", Content: "stale instructions"},
		{Type: "message", Role: "user", Content: "latest user"},
		{Type: "compaction", EncryptedContent: "old-compaction"},
	}

	retained := retainedStoredItemsForRemoteCompactionV2(items)
	require.Len(t, retained, 2)
	assert.Equal(t, "first user", retained[0].Content)
	assert.Equal(t, "latest user", retained[1].Content)
}

func TestRetainedStoredItemsForRemoteCompactionV2AppliesBudget(t *testing.T) {
	oldContent := strings.Repeat("a", (remoteCompactionV2RetainedMessageTokenBudget+100)*4)
	retained := retainedStoredItemsForRemoteCompactionV2([]StoredInputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: oldContent,
			RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"old"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`),
		},
	})

	require.Len(t, retained, 1)
	assert.LessOrEqual(t, approximateStoredMessageTokens(retained[0]), remoteCompactionV2RetainedMessageTokenBudget)
	assert.Contains(t, retained[0].Content, "...[truncated]...")
	assert.Contains(t, string(retained[0].RawItem), "input_image")
}

func TestApproximateStoredMessageTokensUsesUTF8Bytes(t *testing.T) {
	assert.Equal(t, 2, approximateStoredMessageTokens(StoredInputItem{
		Type:    "message",
		Role:    "user",
		Content: "éééé",
	}))
}

func TestApproximateResponseInputItemTokensDiscountsInlineImagePayload(t *testing.T) {
	userImage := func(payloadLength int) openairesponses.ResponseInputItemUnionParam {
		return openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role: openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{
					OfInputItemContentList: openairesponses.ResponseInputMessageContentListParam{{
						OfInputImage: &openairesponses.ResponseInputImageParam{
							ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", payloadLength)),
						},
					}},
				},
			},
		}
	}
	functionOutputImage := func(payloadLength int) openairesponses.ResponseInputItemUnionParam {
		return openairesponses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
				CallID: param.NewOpt("call-image"),
				Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
						OfInputImage: &openairesponses.ResponseInputImageContentParam{
							ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", payloadLength)),
						},
					}},
				},
			},
		}
	}

	for name, build := range map[string]func(int) openairesponses.ResponseInputItemUnionParam{
		"user message":    userImage,
		"function output": functionOutputImage,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t,
				approximateResponseInputItemTokens(build(100)),
				approximateResponseInputItemTokens(build(100_000)),
			)
		})
	}
}

func TestTrimRemoteCompactionV2InputDoesNotRewriteForInlineImageEncodingSize(t *testing.T) {
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt("call-image"),
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
					OfInputImage: &openairesponses.ResponseInputImageContentParam{
						ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", 100_000)),
					},
				}},
			},
		},
	}}

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 2_500)

	assert.Zero(t, rewritten)
	assert.Empty(t, trimmed[0].OfFunctionCallOutput.Output.OfString)
	require.Len(t, trimmed[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray, 1)
}

func TestTrimRemoteCompactionV2InputAccountsForOriginalImagePatches(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewGray(image.Rect(0, 0, 2048, 2048))))
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt("call-original-image"),
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
					OfInputImage: &openairesponses.ResponseInputImageContentParam{
						Detail:   openairesponses.ResponseInputImageContentDetailOriginal,
						ImageURL: param.NewOpt(imageURL),
					},
				}},
			},
		},
	}}

	assert.Greater(t, approximateResponseInputItemTokens(input[0]), 4_000)
	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 3_000)

	assert.Equal(t, 1, rewritten)
	require.True(t, trimmed[0].OfFunctionCallOutput.Output.OfString.Valid())
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
}

func TestEstimateRemoteCompactionV2ContextTokensCountsRetainedImages(t *testing.T) {
	withoutImage := fromStoredItems([]StoredInputItem{{
		Type:    "message",
		Role:    "user",
		Content: "hello",
		RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`),
	}, {Type: "compaction", EncryptedContent: "encrypted"}})
	withImage := fromStoredItems([]StoredInputItem{{
		Type:    "message",
		Role:    "user",
		Content: "hello",
		RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`),
	}, {Type: "compaction", EncryptedContent: "encrypted"}})

	delta := estimateRemoteCompactionV2ContextTokens("system", withImage) -
		estimateRemoteCompactionV2ContextTokens("system", withoutImage)
	assert.Greater(t, delta, 1_800)
	assert.Less(t, delta, 1_900)
}

func TestTrimRemoteCompactionV2InputRewritesNewestFunctionOutputsFirst(t *testing.T) {
	oldOutput := openairesponses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt("call-old"),
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: param.NewOpt(strings.Repeat("o", 4_000)),
			},
		},
	}
	newOutput := openairesponses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt("call-new"),
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: param.NewOpt(strings.Repeat("n", 4_000)),
			},
		},
	}
	input := []openairesponses.ResponseInputItemUnionParam{oldOutput, newOutput}
	totalTokens := approximateResponseInputItemTokens(oldOutput) + approximateResponseInputItemTokens(newOutput)
	rewrittenNew, ok := rewriteResponseInputFunctionOutputForContextWindow(newOutput)
	require.True(t, ok)
	contextWindow := totalTokens - approximateResponseInputItemTokens(newOutput) + approximateResponseInputItemTokens(rewrittenNew)

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", contextWindow)

	assert.Equal(t, 1, rewritten)
	assert.Equal(t, strings.Repeat("o", 4_000), trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[1].OfFunctionCallOutput.Output.OfString.Value)
	assert.Equal(t, strings.Repeat("n", 4_000), input[1].OfFunctionCallOutput.Output.OfString.Value, "request fitting must not mutate the history snapshot")
}

func TestTrimRemoteCompactionV2InputRewritesMultimodalFunctionOutput(t *testing.T) {
	outputItems := openairesponses.ResponseFunctionCallOutputItemListParam{
		openairesponses.ResponseFunctionCallOutputItemParamOfInputText(strings.Repeat("large", 2_000)),
		{
			OfInputImage: &openairesponses.ResponseInputImageContentParam{
				ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", 4_000)),
			},
		},
	}
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: param.NewOpt("call-multimodal"),
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: outputItems,
			},
		},
	}}

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 1)

	assert.Equal(t, 1, rewritten)
	require.True(t, trimmed[0].OfFunctionCallOutput.Output.OfString.Valid())
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
	assert.Empty(t, trimmed[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray)
	assert.Len(t, input[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray, 2, "original multimodal output must remain intact")
}

func TestCompactContextRemoteV2UpdatesContextWindowEstimate(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.Usage.CurrentContextWindow = 200000
	thread.Usage.MaxContextWindow = 1047576
	thread.ToolResults = map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
	}

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return remoteCompactionV2Stream(t, "enc_value")
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.Equal(t,
		estimateRemoteCompactionV2ContextTokens(captured.Instructions.Value, thread.inputItemsSnapshot()),
		thread.Usage.CurrentContextWindow,
	)
	assert.Equal(t, 1047576, thread.Usage.MaxContextWindow)
	assert.Contains(t, thread.ToolResults, "tool-1")
}

func TestCompactContextRemoteV2UsesWebSocket(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.1-codex",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-test"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 3,
		useWebSocket:          true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
		webSocketContinuation: responsesWebSocketContinuation{
			connectionGeneration: 1,
			responseID:           "resp_before_compact",
		},
	}

	var captured openairesponses.ResponseNewParams
	var capturedHeaders []string
	fakeWebSocket := &fakeResponsesWebSocketStreamer{
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, headers []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			captured = params
			capturedHeaders = append([]string(nil), headers...)
			return remoteCompactionV2Stream(t, "ws-encrypted-summary"), nil
		},
	}
	thread.webSocket = fakeWebSocket
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		t.Fatal("HTTPS fallback should not run when websocket compaction succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	require.NotEmpty(t, captured.Input.OfInputItemList)
	assert.NotNil(t, captured.Input.OfInputItemList[len(captured.Input.OfInputItemList)-1].OfCompactionTrigger)
	assert.Contains(t, capturedHeaders, auth.CodexBetaFeaturesHeader+": "+auth.CodexBetaFeatures)
	assert.Contains(t, capturedHeaders, "session-id: conv-test")
	assert.Contains(t, capturedHeaders, "thread-id: conv-test")
	assert.Contains(t, capturedHeaders, auth.CodexInstallationIDHeader+": "+thread.codexInstallationID)
	assert.Contains(t, capturedHeaders, auth.CodexWindowIDHeader+": conv-test:3")
	clientMetadata := codexClientMetadataFromParams(t, captured)
	assert.Equal(t, "conv-test:3", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compaction, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "manual", compaction["trigger"])
	assert.Equal(t, "standalone_turn", compaction["phase"])
	assert.Empty(t, thread.webSocketContinuation.responseID)
	assert.Equal(t, 1, fakeWebSocket.resets)
	assert.Equal(t, uint64(4), thread.snapshotHistory().codexWindowGeneration)
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "ws-encrypted-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
}

func TestCompactContextRemoteV2FallsBackFromWebSocketToHTTPS(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     1,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	webSocketAttempts := 0
	thread.webSocket = &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			webSocketAttempts++
			return responseStreamFromMaps(t, []map[string]any{{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "partial"},
			}}), nil
		},
	}
	httpsAttempts := 0
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		httpsAttempts++
		return remoteCompactionV2Stream(t, "https-summary")
	}
	thread.compactWithSummaryFunc = func(context.Context) error {
		t.Fatal("summary fallback should not run when HTTPS fallback succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.Equal(t, 1, webSocketAttempts)
	assert.Equal(t, 1, httpsAttempts)
	assert.False(t, thread.useWebSocket)

	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		httpsAttempts++
		return nil
	}
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}
	_, _, _, err := thread.processMessageExchange(
		context.Background(),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, webSocketAttempts, "sticky fallback must keep later turns off websocket")
	assert.Equal(t, 2, httpsAttempts)
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "https-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
}

func TestCompactContextRemoteV2PreservesWebSocketUsageAcrossHTTPSFallback(t *testing.T) {
	for _, httpsSucceeds := range []bool{true, false} {
		name := "https failure"
		if httpsSucceeds {
			name = "https success"
		}
		t.Run(name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.6-sol",
				Retry: llmtypes.RetryConfig{
					Attempts:     1,
					InitialDelay: 1,
					MaxDelay:     1,
					BackoffType:  "fixed",
				},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:    "openai",
					ServiceTier: llmtypes.OpenAIServiceTierAuto,
				},
			}
			_, customPricing := loadCustomConfiguration(config)
			thread := &Thread{
				Thread:        base.NewThread(config, "conv-websocket-fallback-usage"),
				customPricing: customPricing,
				useWebSocket:  true,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}
			thread.Usage.CurrentContextWindow = 55
			thread.Usage.MaxContextWindow = 100
			thread.webSocket = &fakeResponsesWebSocketStreamer{
				streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
					return responseStreamFromMaps(t, []map[string]any{
						remoteCompactionV2TerminalEvent("response.incomplete", "priority", 10, 1, 2),
					}), nil
				},
			}
			thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				events := []map[string]any{
					remoteCompactionV2TerminalEvent("response.incomplete", "default", 20, 2, 4),
				}
				if httpsSucceeds {
					events = []map[string]any{
						{
							"type": "response.output_item.done",
							"item": map[string]any{"type": "compaction", "encrypted_content": "https-summary"},
						},
						remoteCompactionV2TerminalEvent("response.completed", "default", 20, 2, 4),
					}
				}
				return responseStreamFromMaps(t, events)
			}

			err := thread.compactContextRemoteV2(context.Background())
			if httpsSucceeds {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "response incomplete")
			}
			assert.False(t, thread.useWebSocket)
			usage := thread.GetUsage()
			assert.Equal(t, 30, usage.InputTokens)
			assert.Equal(t, 6, usage.CacheReadInputTokens)
			assert.Equal(t, 3, usage.OutputTokens)
			assert.InDelta(t, 8*0.00001+16*0.000005, usage.InputCost, 1e-12)
			assert.InDelta(t, 2*0.000001+4*0.0000005, usage.CacheReadCost, 1e-12)
			assert.InDelta(t, 1*0.00006+2*0.00003, usage.OutputCost, 1e-12)
			if httpsSucceeds {
				assert.NotEqual(t, 55, usage.CurrentContextWindow)
				require.NotNil(t, thread.inputItemsSnapshot()[1].OfCompaction)
			} else {
				assert.Equal(t, 55, usage.CurrentContextWindow)
				assert.Equal(t, 100, usage.MaxContextWindow)
				history := thread.inputItemsSnapshot()
				require.Len(t, history, 1)
				assert.Nil(t, history[0].OfCompaction)
			}
		})
	}
}

func TestCompactContextRemoteV2NonRetryableWebSocketErrorDoesNotDisableWebSocket(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 2},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	webSocketAttempts := 0
	fakeWebSocket := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			webSocketAttempts++
			return responseStreamFromMaps(t, []map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_without_compaction",
					"status": "completed",
				},
			}}), nil
		},
	}
	thread.webSocket = fakeWebSocket
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		t.Fatal("non-retryable websocket validation errors must not fall back to HTTPS")
		return nil
	}

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected exactly one compaction output item")
	assert.Equal(t, 1, webSocketAttempts)
	assert.True(t, thread.useWebSocket)
	assert.Equal(t, 0, fakeWebSocket.resets)
}

func TestCompactContextRemoteV2UsesSDKResponsesEndpoint(t *testing.T) {
	var requestBody map[string]any
	var requestHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		requestHeaders = r.Header.Clone()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))

		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"sdk-encrypted-summary\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_sdk_compact\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1,\"output_tokens_details\":{\"reasoning_tokens\":0},\"total_tokens\":2}}}\n\n"))
		require.NoError(t, err)
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.1-codex",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex", BaseURL: server.URL},
	}
	thread := &Thread{
		Thread:              base.NewThread(config, "conv-sdk"),
		client:              &client,
		isCodex:             true,
		codexInstallationID: "00000000-0000-4000-8000-000000000001",
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.newStreamingFunc = client.Responses.NewStreaming

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, auth.CodexBetaFeatures, requestHeaders.Get(auth.CodexBetaFeaturesHeader))
	assert.Equal(t, "conv-sdk", requestHeaders.Get("session-id"))
	assert.Equal(t, "conv-sdk", requestHeaders.Get("thread-id"))
	assert.Equal(t, thread.codexInstallationID, requestHeaders.Get(auth.CodexInstallationIDHeader))
	assert.Equal(t, "conv-sdk:0", requestHeaders.Get(auth.CodexWindowIDHeader))
	input, ok := requestBody["input"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	trigger, ok := input[len(input)-1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "compaction_trigger", trigger["type"])
	assert.Equal(t, "gpt-5.1-codex", requestBody["model"])
	assert.Equal(t, false, requestBody["store"])
	clientMetadata, ok := requestBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, thread.codexInstallationID, clientMetadata[auth.CodexInstallationIDHeader])
	assert.Equal(t, "conv-sdk:0", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compaction, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "manual", compaction["trigger"])
	assert.Equal(t, "user_requested", compaction["reason"])
	assert.Equal(t, "responses_compaction_v2", compaction["implementation"])
	assert.Equal(t, "standalone_turn", compaction["phase"])
	assert.Equal(t, "memento", compaction["strategy"])
	assert.Equal(t, uint64(1), thread.snapshotHistory().codexWindowGeneration)
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "sdk-encrypted-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
}

func TestCompactContextRemoteV2HTTPRetryOwnership(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		body         string
		wantRequests int32
	}{
		{
			name:         "server errors use outer retry budget only",
			status:       http.StatusInternalServerError,
			body:         `{"error":{"code":"server_error","message":"try again","type":"server_error"}}`,
			wantRequests: 3,
		},
		{
			name:         "generic rate limit is not retried",
			status:       http.StatusTooManyRequests,
			body:         `{"error":{"code":"rate_limit_exceeded","message":"rate limited","type":"rate_limit_error"}}`,
			wantRequests: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"))
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.5",
				Retry: llmtypes.RetryConfig{
					Attempts:     3,
					InitialDelay: 1,
					MaxDelay:     1,
					BackoffType:  "fixed",
				},
				OpenAI: &llmtypes.OpenAIConfig{Platform: "openai", BaseURL: server.URL},
			}
			thread := &Thread{
				Thread: base.NewThread(config, "conv-test"),
				client: &client,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					responseMessageItem(openairesponses.EasyInputMessageRoleUser, "hello"),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}
			thread.newStreamingFunc = client.Responses.NewStreaming

			require.Error(t, thread.compactContextRemoteV2(t.Context()))
			assert.Equal(t, tt.wantRequests, requests.Load())
		})
	}
}

func TestCompactContextRemoteV2RejectsStaleHistorySnapshot(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original"}},
	}
	thread.Usage.InputTokens = 7
	thread.Usage.CurrentContextWindow = 55
	thread.Usage.MaxContextWindow = 100

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	successStream := remoteCompactionV2Stream(t, "stale-summary")
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		close(requestStarted)
		<-releaseRequest
		return successStream
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- thread.compactContextRemoteV2(context.Background())
	}()
	<-requestStarted
	thread.AddUserMessage(context.Background(), "arrived while compacting")
	close(releaseRequest)

	err := <-errCh
	require.ErrorIs(t, err, errRemoteCompactionHistoryChanged)
	snapshot := thread.snapshotHistory()
	require.Len(t, snapshot.storedItems, 2)
	assert.Equal(t, "original", snapshot.storedItems[0].Content)
	assert.Equal(t, "arrived while compacting", snapshot.storedItems[1].Content)
	for _, item := range snapshot.storedItems {
		assert.NotEqual(t, "compaction", item.Type)
	}
	usage := thread.GetUsage()
	assert.Equal(t, 107, usage.InputTokens, "billable compaction input must still be recorded")
	assert.Equal(t, 10, usage.OutputTokens)
	assert.Equal(t, 55, usage.CurrentContextWindow, "stale compaction must not replace live context accounting")
	assert.Equal(t, 100, usage.MaxContextWindow)
	assert.Nil(t, thread.CompactionHistory)
}

// Live API tests require an explicit opt-in as well as credentials.
func skipIfNoAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("KODELET_OPENAI_INTEGRATION_TESTS") != "1" {
		t.Skip("set KODELET_OPENAI_INTEGRATION_TESTS=1 to run live OpenAI Responses API tests")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "test-key" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set or is test-key")
	}
}

func TestIntegration_CompactContext(t *testing.T) {
	skipIfNoAPIKey(t)

	ctx := context.Background()

	// Use a cheap model for testing
	config := llmtypes.Config{
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		WeakModel: "gpt-4.1-mini",
		MaxTokens: 1024,
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	// Add conversation history to compact
	thread.AddUserMessage(ctx, "Hello, I'm working on a project that involves building a REST API.")
	thread.AddUserMessage(ctx, "The API needs to handle user authentication, data validation, and rate limiting.")
	thread.AddUserMessage(ctx, "I'm using Go with the Gin framework.")
	thread.AddUserMessage(ctx, "Can you suggest a good project structure?")

	// Store the original input items count
	originalItemCount := len(thread.inputItems)
	t.Logf("Original input items count: %d", originalItemCount)

	// Compact the context
	err = thread.CompactContext(ctx)
	require.NoError(t, err)

	t.Logf("Compacted input items count: %d", len(thread.inputItems))

	// Verify the compaction worked
	assert.NotEmpty(t, thread.inputItems, "Compacted items should not be empty")

	// The compacted output should have fewer or equal items
	// (user messages + one compaction item)
	assert.LessOrEqual(t, len(thread.inputItems), originalItemCount+1,
		"Compacted items should be fewer or equal to original")

	// The original conversation remains archived for display.
	require.NotNil(t, thread.CompactionHistory)
	require.Len(t, thread.CompactionHistory.Segments, 1)
	assert.Equal(t, len(thread.storedItems), thread.CompactionHistory.ActiveDisplayStart)

	// Check that we have at least one compaction item
	hasCompactionItem := false
	for _, item := range thread.inputItems {
		if item.OfCompaction != nil {
			hasCompactionItem = true
			assert.NotEmpty(t, item.OfCompaction.EncryptedContent,
				"Compaction item should have encrypted content")
			break
		}
	}
	assert.True(t, hasCompactionItem, "Should have at least one compaction item after compacting")
}

func TestIntegration_SendMessageAndCompact(t *testing.T) {
	skipIfNoAPIKey(t)

	ctx := context.Background()

	// Use a cheap model for testing
	config := llmtypes.Config{
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		WeakModel: "gpt-4.1-mini",
		MaxTokens: 256,
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	// Create a simple handler that collects text
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	// Send a message and get a response
	_, err = thread.SendMessage(ctx, "What is 2 + 2? Reply with just the number.", handler, llmtypes.MessageOpt{
		NoToolUse: true,
		MaxTurns:  1,
	})
	require.NoError(t, err)

	response := handler.CollectedText()
	t.Logf("Response: %s", response)
	assert.NotEmpty(t, response)

	// Store the count before compacting
	itemCountBeforeCompact := len(thread.inputItems)
	t.Logf("Input items before compact: %d", itemCountBeforeCompact)

	// Now compact the context
	err = thread.CompactContext(ctx)
	require.NoError(t, err)

	t.Logf("Input items after compact: %d", len(thread.inputItems))

	// Verify we can still continue the conversation after compaction
	handler2 := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = thread.SendMessage(ctx, "What is 3 + 3? Reply with just the number.", handler2, llmtypes.MessageOpt{
		NoToolUse: true,
		MaxTurns:  1,
	})
	require.NoError(t, err)

	response2 := handler2.CollectedText()
	t.Logf("Response after compact: %s", response2)
	assert.NotEmpty(t, response2)
}
