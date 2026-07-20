package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/avast/retry-go/v4"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

type fakeDecoder struct {
	events []ssestream.Event
	index  int
	err    error
}

func (d *fakeDecoder) Next() bool {
	if d.err != nil {
		return false
	}
	if d.index >= len(d.events) {
		return false
	}
	d.index++
	return true
}

func (d *fakeDecoder) Event() ssestream.Event {
	if d.index == 0 || d.index > len(d.events) {
		return ssestream.Event{}
	}
	return d.events[d.index-1]
}

func (d *fakeDecoder) Close() error {
	return nil
}

func (d *fakeDecoder) Err() error {
	return d.err
}

type responsesTestTool struct {
	name      string
	rawSchema map[string]any
}

func (t responsesTestTool) GenerateSchema() *jsonschema.Schema {
	return jsonschema.Reflect(map[string]any{})
}

func (t responsesTestTool) RawInputSchema() map[string]any { return t.rawSchema }

func (t responsesTestTool) Name() string { return t.name }

func (t responsesTestTool) Description() string { return "responses test tool" }

func (t responsesTestTool) ValidateInput(tooltypes.State, string) error { return nil }

func (t responsesTestTool) Execute(context.Context, tooltypes.State, string) tooltypes.ToolResult {
	return tooltypes.BaseToolResult{Result: "ok"}
}

func (t responsesTestTool) TracingKVs(string) ([]attribute.KeyValue, error) { return nil, nil }

func responseStreamFromMaps(t *testing.T, events []map[string]any) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	t.Helper()

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	return ssestream.NewStream[responses.ResponseStreamEventUnion](&fakeDecoder{events: streamEvents}, nil)
}

type captureStreamHandler struct {
	events []string
}

func (h *captureStreamHandler) HandleText(_ string) {}

func (h *captureStreamHandler) HandleToolUse(_ string, _ string, _ string) {}

func (h *captureStreamHandler) HandleToolResult(_, _ string, _ tooltypes.ToolResult) {}

func (h *captureStreamHandler) HandleThinking(_ string) {}

func (h *captureStreamHandler) HandleDone() {}

func (h *captureStreamHandler) HandleTextDelta(delta string) {
	h.events = append(h.events, "text_delta:"+delta)
}

func (h *captureStreamHandler) HandleThinkingStart() {
	h.events = append(h.events, "thinking_start")
}

func (h *captureStreamHandler) HandleThinkingDelta(delta string) {
	h.events = append(h.events, "thinking_delta:"+delta)
}

func (h *captureStreamHandler) HandleThinkingBlockEnd() {
	h.events = append(h.events, "thinking_block_end")
}

func (h *captureStreamHandler) HandleContentBlockEnd() {
	h.events = append(h.events, "content_block_end")
}

type fakeMultiModalToolResult struct {
	tooltypes.BaseToolResult
	parts []tooltypes.ToolResultContentPart
}

func (r fakeMultiModalToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return r.parts
}

func TestBuildStoredFunctionCallOutputKeepsAssistantSummary(t *testing.T) {
	result := fakeMultiModalToolResult{
		BaseToolResult: tooltypes.BaseToolResult{Result: "Viewed image /tmp/demo.png (1x1, image/png)"},
		parts: []tooltypes.ToolResultContentPart{
			{
				Type:     tooltypes.ToolResultContentPartTypeImage,
				ImageURL: "data:image/png;base64,aGVsbG8=",
				MimeType: "image/png",
			},
		},
	}

	outputUnion, storedOutput, rawOutput := buildStoredFunctionCallOutput(result)

	assert.Len(t, outputUnion.OfResponseFunctionCallOutputItemArray, 1)
	assert.Contains(t, storedOutput, "Viewed image /tmp/demo.png")
	assert.NotContains(t, storedOutput, "data:image/png;base64")
	assert.Contains(t, string(rawOutput), `"image_url":"data:image/png;base64,aGVsbG8="`)
}

func TestStructuredResultToolResultMethods(t *testing.T) {
	structured := tooltypes.StructuredToolResult{
		ToolName: "unknown_for_fallback",
		Success:  false,
		Error:    "boom",
	}

	result := structuredToolResultToToolResult(structured)

	assert.True(t, result.IsError())
	assert.Equal(t, "boom", result.GetError())
	assert.Contains(t, result.GetResult(), "boom")
	assert.Contains(t, result.AssistantFacing(), "boom")
	assert.Equal(t, structured, result.StructuredData())
}

func TestResponseFunctionCallOutputItemsFiltersAndPreservesDetail(t *testing.T) {
	items := responseFunctionCallOutputItems([]tooltypes.ToolResultContentPart{
		{Type: tooltypes.ToolResultContentPartTypeText, Text: "   "},
		{Type: tooltypes.ToolResultContentPartTypeText, Text: "caption"},
		{Type: tooltypes.ToolResultContentPartTypeImage},
		{Type: tooltypes.ToolResultContentPartTypeImage, ImageURL: "data:image/png;base64,aGVsbG8=", Detail: "original"},
	})

	require.Len(t, items, 2)
	assert.NotNil(t, items[0].OfInputText)
	assert.Equal(t, "caption", items[0].OfInputText.Text)
	require.NotNil(t, items[1].OfInputImage)
	assert.Equal(t, "data:image/png;base64,aGVsbG8=", items[1].OfInputImage.ImageURL.Value)
	assert.Equal(t, responses.ResponseInputImageContentDetailOriginal, items[1].OfInputImage.Detail)
}

func TestExecuteToolCallStoresStructuredResult(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "conv-test"),
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithExtensionTools([]tooltypes.Tool{responsesTestTool{name: "ok_tool"}})))

	result := thread.executeToolCall(context.Background(), "call-ok", "ok_tool", `{}`, &captureStreamHandler{})

	require.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "ok")
	structured := thread.GetStructuredToolResults()["call-ok"]
	assert.Equal(t, "unknown", structured.ToolName)
	assert.True(t, structured.Success)
}

func TestProcessStreamCompletesFunctionCallAndStoresToolOutput(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_1",
			"status": "completed",
			"usage":  usage,
		},
	}
	events := []map[string]any{
		{"type": "response.output_text.delta", "delta": "Before tool"},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "ok_tool",
				"arguments": `{}`,
			},
		},
		completedEvent,
	}

	stream := responseStreamFromMaps(t, events)
	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithExtensionTools([]tooltypes.Tool{responsesTestTool{name: "ok_tool"}})))
	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.True(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, "resp_1", streamResult.responseID)
	require.Len(t, streamResult.serverKnownItems, 1)
	require.NotNil(t, streamResult.serverKnownItems[0].OfFunctionCall)
	assert.Equal(t, "call_1", streamResult.serverKnownItems[0].OfFunctionCall.CallID)
	assert.Contains(t, handler.events, "text_delta:Before tool")
	assert.Contains(t, handler.events, "content_block_end")
	require.Len(t, thread.storedItems, 2)
	assert.Equal(t, "function_call", thread.storedItems[0].Type)
	assert.Equal(t, "call_1", thread.storedItems[0].CallID)
	assert.Equal(t, "function_call_output", thread.storedItems[1].Type)
	assert.Equal(t, "call_1", thread.storedItems[1].CallID)
	assert.True(t, strings.Contains(thread.storedItems[1].Output, "ok"))
	require.Len(t, thread.inputItems, 2)
	require.NotNil(t, thread.inputItems[0].OfFunctionCall)
	require.NotNil(t, thread.inputItems[1].OfFunctionCallOutput)
}

func TestProcessStreamThinkingEndsBeforeText(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_1",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{"type": "response.reasoning_text.delta", "delta": "Thought"},
		{"type": "response.reasoning_text.done"},
		{"type": "response.output_text.delta", "delta": "Answer"},
		completedEvent,
	}

	stream := responseStreamFromMaps(t, events)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"thinking_start",
		"thinking_delta:Thought",
		"thinking_block_end",
		"text_delta:Answer",
		"content_block_end",
	}, handler.events)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "reasoning", thread.storedItems[0].Type)
	assert.Equal(t, "Thought", thread.storedItems[0].Content)
}

func TestProcessStreamPersistsMultipleReasoningBlocksSeparately(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_1",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{"type": "response.reasoning_text.delta", "delta": "First thought"},
		{"type": "response.reasoning_text.done"},
		{"type": "response.reasoning_text.delta", "delta": "Second thought"},
		{"type": "response.reasoning_text.done"},
		{"type": "response.reasoning_text.delta", "delta": "Third thought"},
		{"type": "response.reasoning_text.done"},
		{"type": "response.output_text.delta", "delta": "Done"},
		completedEvent,
	}

	stream := responseStreamFromMaps(t, events)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"thinking_start",
		"thinking_delta:First thought",
		"thinking_block_end",
		"thinking_start",
		"thinking_delta:Second thought",
		"thinking_block_end",
		"thinking_start",
		"thinking_delta:Third thought",
		"thinking_block_end",
		"text_delta:Done",
		"content_block_end",
	}, handler.events)
	require.Len(t, thread.storedItems, 3)
	assert.Equal(t, "reasoning", thread.storedItems[0].Type)
	assert.Equal(t, "First thought", thread.storedItems[0].Content)
	assert.Equal(t, "reasoning", thread.storedItems[1].Type)
	assert.Equal(t, "Second thought", thread.storedItems[1].Content)
	assert.Equal(t, "reasoning", thread.storedItems[2].Type)
	assert.Equal(t, "Third thought", thread.storedItems[2].Content)
}

func TestProcessStreamThinkingEndsBeforeTextWithoutDoneEvent(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_1",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{"type": "response.reasoning_text.delta", "delta": "Thought"},
		{"type": "response.output_text.delta", "delta": "Answer"},
		completedEvent,
	}

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"thinking_start",
		"thinking_delta:Thought",
		"thinking_block_end",
		"text_delta:Answer",
		"content_block_end",
	}, handler.events)
}

func TestUpdateUsageAccumulatesCachedTokensLikeCodexTotals(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		customPricing: map[string]llmtypes.ModelPricing{
			"gpt-4.1": {
				Input:         1,
				Output:        1,
				CachedInput:   1,
				ContextWindow: 200000,
			},
		},
	}

	thread.updateUsage(responses.ResponseUsage{
		InputTokens:  100,
		OutputTokens: 10,
		InputTokensDetails: responses.ResponseUsageInputTokensDetails{
			CachedTokens: 2400,
		},
	}, "gpt-4.1", "")
	thread.updateUsage(responses.ResponseUsage{
		InputTokens:  120,
		OutputTokens: 20,
		InputTokensDetails: responses.ResponseUsageInputTokensDetails{
			CachedTokens: 143900,
		},
	}, "gpt-4.1", "")

	assert.Equal(t, 220, thread.Usage.InputTokens)
	assert.Equal(t, 30, thread.Usage.OutputTokens)
	assert.Equal(t, 146300, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 140, thread.Usage.CurrentContextWindow)
}

func TestUpdateUsageUsesLongContextPricing(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "test-model"}, "test"),
		customPricing: map[string]llmtypes.ModelPricing{
			"test-model": {
				Input:                      1,
				CachedInput:                0.1,
				CacheWriteInput:            1.25,
				Output:                     2,
				LongContextInput:           3,
				LongContextCachedInput:     0.3,
				LongContextCacheWriteInput: 3.75,
				LongContextOutput:          4,
				LongContextThreshold:       272_000,
				ContextWindow:              1_050_000,
			},
		},
	}

	usageJSON := `{
		"input_tokens": 272001,
		"output_tokens": 10,
		"input_tokens_details": {
			"cached_tokens": 1,
			"cache_write_tokens": 2
		}
	}`
	var usage responses.ResponseUsage
	require.NoError(t, json.Unmarshal([]byte(usageJSON), &usage))

	thread.updateUsage(usage, "test-model", "")

	assert.Equal(t, 271999, thread.Usage.InputTokens)
	assert.Equal(t, 1, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 2, thread.Usage.CacheCreationInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
	assert.Equal(t, 272012, thread.Usage.TotalTokens())
	assert.Equal(t, 272011, thread.Usage.CurrentContextWindow)
	assert.Equal(t, float64(271998)*3, thread.Usage.InputCost)
	assert.Equal(t, 0.3, thread.Usage.CacheReadCost)
	assert.Equal(t, 7.5, thread.Usage.CacheCreationCost)
	assert.Equal(t, 40.0, thread.Usage.OutputCost)
	assert.Equal(t, 1_050_000, thread.Usage.MaxContextWindow)
}

func TestUpdateUsageAccountsCacheWriteTokensOnlyWithConfiguredRate(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "test-model"}, "test"),
		customPricing: map[string]llmtypes.ModelPricing{
			"test-model": {
				Input:         1,
				CachedInput:   0.1,
				Output:        2,
				ContextWindow: 128_000,
			},
		},
	}

	usageJSON := `{
		"input_tokens": 100,
		"output_tokens": 10,
		"input_tokens_details": {
			"cached_tokens": 20,
			"cache_write_tokens": 30
		}
	}`
	var usage responses.ResponseUsage
	require.NoError(t, json.Unmarshal([]byte(usageJSON), &usage))

	thread.updateUsage(responses.ResponseUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		InputTokensDetails: responses.ResponseUsageInputTokensDetails{
			CachedTokens: usage.InputTokensDetails.CachedTokens,
		},
	}, "test-model", "")

	assert.Equal(t, 100, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 0, thread.Usage.CacheCreationInputTokens)
	assert.Equal(t, float64(80), thread.Usage.InputCost)
	assert.Equal(t, 2.0, thread.Usage.CacheReadCost)
	assert.Equal(t, 0.0, thread.Usage.CacheCreationCost)
	assert.Equal(t, 130, thread.Usage.TotalTokens())

	thread = &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "test-model"}, "test"),
		customPricing: map[string]llmtypes.ModelPricing{
			"test-model": {
				Input:         1,
				CachedInput:   0.1,
				Output:        2,
				ContextWindow: 128_000,
			},
		},
	}

	thread.updateUsage(usage, "test-model", "")

	assert.Equal(t, 70, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 30, thread.Usage.CacheCreationInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
	assert.Equal(t, 130, thread.Usage.TotalTokens())
	assert.Equal(t, 110, thread.Usage.CurrentContextWindow)
	assert.Equal(t, float64(50), thread.Usage.InputCost)
	assert.Equal(t, 2.0, thread.Usage.CacheReadCost)
	assert.Equal(t, 0.0, thread.Usage.CacheCreationCost)
	assert.Equal(t, 20.0, thread.Usage.OutputCost)
	assert.Equal(t, 128_000, thread.Usage.MaxContextWindow)
}

func TestProcessStreamUsesCallModelForLongContextPricing(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "main-model"}, "test"),
		customPricing: map[string]llmtypes.ModelPricing{
			"main-model": {
				Input:                  10,
				CachedInput:            1,
				Output:                 20,
				LongContextInput:       30,
				LongContextCachedInput: 3,
				LongContextOutput:      40,
				LongContextThreshold:   100,
				ContextWindow:          1_000_000,
			},
			"weak-model": {
				Input:                  0.5,
				CachedInput:            0.05,
				Output:                 0.75,
				LongContextInput:       1.5,
				LongContextCachedInput: 0.15,
				LongContextOutput:      2.5,
				LongContextThreshold:   100,
				ContextWindow:          200_000,
			},
		},
	}

	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_weak_usage",
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  101,
				"output_tokens": 10,
				"input_tokens_details": map[string]any{
					"cached_tokens": 1,
				},
			},
		},
	}

	payload, err := json.Marshal(completedEvent)
	require.NoError(t, err)

	decoder := &fakeDecoder{events: []ssestream.Event{{Data: payload}}}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	handler := &captureStreamHandler{}
	streamResult, err := thread.processStream(context.Background(), stream, handler, "weak-model", llmtypes.MessageOpt{DisableUsageLog: true})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)

	assert.Equal(t, 101, thread.Usage.InputTokens)
	assert.Equal(t, 1, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
	assert.Equal(t, float64(100)*1.5, thread.Usage.InputCost)
	assert.Equal(t, 0.15, thread.Usage.CacheReadCost)
	assert.Equal(t, 25.0, thread.Usage.OutputCost)
	assert.Equal(t, 200_000, thread.Usage.MaxContextWindow)
}

func TestProcessStreamUsesReturnedServiceTierForPricing(t *testing.T) {
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
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:    "openai",
					ServiceTier: tt.configuredTier,
				},
			}
			_, configuredPricing := loadCustomConfiguration(config)
			thread := &Thread{
				Thread:        base.NewThread(config, "test"),
				customPricing: configuredPricing,
			}

			stream := responseStreamFromMaps(t, []map[string]any{
				{
					"type": "response.completed",
					"response": map[string]any{
						"id":           "resp_tier_usage",
						"status":       "completed",
						"service_tier": tt.returnedTier,
						"usage": map[string]any{
							"input_tokens":  100,
							"output_tokens": 10,
							"input_tokens_details": map[string]any{
								"cached_tokens": 20,
							},
						},
					},
				},
			})

			streamResult, err := thread.processStream(
				context.Background(),
				stream,
				&captureStreamHandler{},
				"gpt-5.6-sol",
				llmtypes.MessageOpt{DisableUsageLog: true},
			)
			require.NoError(t, err)
			assert.True(t, streamResult.responseCompleted)
			assert.InDelta(t, tt.expectedInputCost, thread.Usage.InputCost, 1e-12)
			assert.InDelta(t, tt.expectedCacheReadCost, thread.Usage.CacheReadCost, 1e-12)
			assert.InDelta(t, tt.expectedOutputCost, thread.Usage.OutputCost, 1e-12)
			assert.Equal(t, 1_050_000, thread.Usage.MaxContextWindow)
		})
	}
}

func TestGetPricingForServiceTierPreservesConfiguredOverride(t *testing.T) {
	override := llmtypes.ModelPricing{
		Input:         1,
		CachedInput:   2,
		Output:        3,
		ContextWindow: 4,
	}
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.6-sol",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			Pricing: map[string]llmtypes.ModelPricing{
				"gpt-5.6-sol": override,
			},
		},
	}
	_, configuredPricing := loadCustomConfiguration(config)
	thread := &Thread{
		Thread:        base.NewThread(config, "test"),
		customPricing: configuredPricing,
	}

	assert.Equal(t, override, thread.getPricingForServiceTier("gpt-5.6-sol", llmtypes.OpenAIServiceTierPriority))
}

func TestProcessStreamReturnsErrorOnIncompleteResponse(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	incompleteEvent := map[string]any{
		"type": "response.incomplete",
		"response": map[string]any{
			"id":     "resp_2",
			"status": "incomplete",
			"incomplete_details": map[string]any{
				"reason": "max_output_tokens",
			},
			"usage": usage,
		},
	}

	events := []map[string]any{
		{"type": "response.output_text.delta", "delta": "Partial"},
		incompleteEvent,
	}

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	assert.False(t, streamResult.toolsUsed)
	assert.False(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"text_delta:Partial",
		"content_block_end",
	}, handler.events)
}

func TestProcessStreamReturnsRetryableErrorOnIncompleteResponse(t *testing.T) {
	stream := responseStreamFromMaps(t, []map[string]any{
		{
			"type": "response.incomplete",
			"response": map[string]any{
				"id":     "resp_incomplete",
				"status": "incomplete",
				"incomplete_details": map[string]any{
					"reason": "max_output_tokens",
				},
			},
		},
	})
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test")}
	handler := &captureStreamHandler{}

	_, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.Error(t, err)
	assert.True(t, retry.IsRecoverable(err))
}

func TestProcessStreamEndsCommittedMessageBeforeRetryableFailure(t *testing.T) {
	stream := responseStreamFromMaps(t, []map[string]any{
		{"type": "response.output_text.delta", "delta": "Committed"},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "msg_1",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"phase":  "commentary",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "Committed",
				}},
			},
		},
		{
			"type": "response.failed",
			"response": map[string]any{
				"id":     "resp_failed",
				"status": "failed",
				"error": map[string]any{
					"code":    "server_error",
					"message": "retry me",
				},
			},
		},
	})
	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}
	handler := &captureStreamHandler{}

	_, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.Error(t, err)
	assert.True(t, retry.IsRecoverable(err))
	assert.Equal(t, []string{"text_delta:Committed", "content_block_end"}, handler.events)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, "Committed", thread.storedItems[0].Content)
	assert.Contains(t, string(thread.storedItems[0].RawItem), `"id":"msg_1"`)
	require.Len(t, thread.inputItems, 1)
	require.NotNil(t, thread.inputItems[0].OfOutputMessage)
	assert.Equal(t, "msg_1", thread.inputItems[0].OfOutputMessage.ID)
	assert.Equal(t, responses.ResponseOutputMessageStatusCompleted, thread.inputItems[0].OfOutputMessage.Status)
	assert.Equal(t, responses.ResponseOutputMessagePhaseCommentary, thread.inputItems[0].OfOutputMessage.Phase)
}

func TestProcessStreamEndsEachAssistantMessageItem(t *testing.T) {
	stream := responseStreamFromMaps(t, []map[string]any{
		{"type": "response.output_text.delta", "delta": "Commentary"},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "msg_commentary",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"phase":  "commentary",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "Commentary",
				}},
			},
		},
		{"type": "response.output_text.delta", "delta": "Final"},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "msg_final",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"phase":  "final_answer",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "Final",
				}},
			},
		},
		{
			"type": "response.completed",
			"response": map[string]any{
				"id":     "resp_1",
				"status": "completed",
				"usage": map[string]any{
					"input_tokens":  1,
					"output_tokens": 1,
					"input_tokens_details": map[string]any{
						"cached_tokens": 0,
					},
				},
			},
		},
	})
	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}
	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"text_delta:Commentary",
		"content_block_end",
		"text_delta:Final",
		"content_block_end",
	}, handler.events)
	require.Len(t, thread.storedItems, 2)
	require.Len(t, thread.inputItems, 2)
	require.NotNil(t, thread.inputItems[0].OfOutputMessage)
	require.NotNil(t, thread.inputItems[1].OfOutputMessage)
	assert.Equal(t, "msg_commentary", thread.inputItems[0].OfOutputMessage.ID)
	assert.Equal(t, responses.ResponseOutputMessagePhaseCommentary, thread.inputItems[0].OfOutputMessage.Phase)
	assert.Equal(t, "msg_final", thread.inputItems[1].OfOutputMessage.ID)
	assert.Equal(t, responses.ResponseOutputMessagePhaseFinalAnswer, thread.inputItems[1].OfOutputMessage.Phase)
}

func TestProcessStreamResponseFailedErrorRetryabilityMatchesCodex(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		retryable   bool
		errorString string
	}{
		{name: "generic failure retries", code: "server_error", retryable: true, errorString: "temporary"},
		{name: "invalid prompt does not retry", code: "invalid_prompt", retryable: false, errorString: "bad prompt"},
		{name: "context window does not retry", code: "context_length_exceeded", retryable: false, errorString: "too long"},
		{name: "quota does not retry", code: "insufficient_quota", retryable: false, errorString: "quota"},
		{name: "usage not included does not retry", code: "usage_not_included", retryable: false, errorString: "usage"},
		{name: "cyber policy does not retry", code: "cyber_policy", retryable: false, errorString: "policy"},
		{name: "server overloaded does not retry", code: "server_is_overloaded", retryable: false, errorString: "overloaded"},
		{name: "slow down does not retry", code: "slow_down", retryable: false, errorString: "slow down"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := responseStreamFromMaps(t, []map[string]any{
				{
					"type": "response.failed",
					"response": map[string]any{
						"id":     "resp_failed",
						"status": "failed",
						"error": map[string]any{
							"code":    tt.code,
							"message": tt.errorString,
						},
					},
				},
			})
			thread := &Thread{Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test")}
			handler := &captureStreamHandler{}

			_, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
			require.Error(t, err)
			assert.Equal(t, tt.retryable, retry.IsRecoverable(err))
		})
	}
}

func TestProcessStreamWebSearchDoesNotTriggerFollowUpTurn(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_search",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "ws_123",
				"type":   "web_search_call",
				"status": "completed",
				"action": map[string]any{
					"type":    "search",
					"query":   "kodelet web search loop",
					"queries": []string{"kodelet web search loop"},
					"sources": []map[string]any{{"type": "url", "url": "https://example.com/result"}},
				},
			},
		},
		completedEvent,
	}

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "web_search_call", thread.storedItems[0].Type)
	assert.Equal(t, "ws_123", thread.storedItems[0].CallID)
	require.Len(t, thread.inputItems, 1)
	require.NotNil(t, thread.inputItems[0].OfWebSearchCall)
	restoredItems := fromStoredItems(thread.storedItems)
	require.Len(t, restoredItems, 1)
	require.NotNil(t, restoredItems[0].OfWebSearchCall)
}

func TestProcessStreamWebSearchOpenPagePreservesURL(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_open_page",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "ws_open_page",
				"type":   "web_search_call",
				"status": "completed",
				"action": map[string]any{
					"type": "open_page",
					"url":  "https://example.com/story",
				},
			},
		},
		completedEvent,
	}

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "https://example.com/story", thread.storedItems[0].Content)
	assert.JSONEq(t, `{"status":"completed","type":"open_page","url":"https://example.com/story"}`, webSearchStoredInput(thread.storedItems[0]))

	result, ok := thread.GetStructuredToolResults()["ws_open_page"]
	require.True(t, ok)
	meta, ok := result.Metadata.(tooltypes.OpenAIWebSearchMetadata)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/story", meta.URL)
}

func TestProcessStreamWebSearchFlushesReasoningIntoReplayState(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  1,
		"output_tokens": 1,
		"input_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_search_reasoning",
			"status": "completed",
			"usage":  usage,
		},
	}

	events := []map[string]any{
		{"type": "response.reasoning_text.delta", "delta": "Need to look this up."},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":     "ws_reasoning",
				"type":   "web_search_call",
				"status": "completed",
				"action": map[string]any{
					"type":    "search",
					"query":   "kodelet replay state",
					"queries": []string{"kodelet replay state"},
					"sources": []map[string]any{{"type": "url", "url": "https://example.com/replay"}},
				},
			},
		},
		completedEvent,
	}

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-5.5"}, "test"),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-5.5", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.False(t, streamResult.toolsUsed)
	assert.True(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"thinking_start",
		"thinking_delta:Need to look this up.",
		"thinking_block_end",
	}, handler.events)

	require.Len(t, thread.storedItems, 2)
	assert.Equal(t, "reasoning", thread.storedItems[0].Type)
	assert.Equal(t, "Need to look this up.", thread.storedItems[0].Content)
	assert.Equal(t, "web_search_call", thread.storedItems[1].Type)
	assert.Equal(t, "kodelet replay state", thread.storedItems[1].Content)
	assert.Empty(t, thread.pendingReasoning.String())

	require.Len(t, thread.inputItems, 1)
	require.NotNil(t, thread.inputItems[0].OfWebSearchCall)
	assert.Equal(t, "ws_reasoning", thread.inputItems[0].OfWebSearchCall.ID)
}
