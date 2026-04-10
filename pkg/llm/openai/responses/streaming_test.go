package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	streamEvents := make([]ssestream.Event, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		streamEvents = append(streamEvents, ssestream.Event{Data: payload})
	}

	decoder := &fakeDecoder{events: streamEvents}
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)

	thread := &Thread{
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-4.1", llmtypes.MessageOpt{})
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
		Thread: base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
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
	})
	thread.updateUsage(responses.ResponseUsage{
		InputTokens:  120,
		OutputTokens: 20,
		InputTokensDetails: responses.ResponseUsageInputTokensDetails{
			CachedTokens: 143900,
		},
	})

	assert.Equal(t, 220, thread.Usage.InputTokens)
	assert.Equal(t, 30, thread.Usage.OutputTokens)
	assert.Equal(t, 146300, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 140, thread.Usage.CurrentContextWindow)
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
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-4.1", llmtypes.MessageOpt{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	assert.False(t, streamResult.toolsUsed)
	assert.False(t, streamResult.responseCompleted)
	assert.Equal(t, []string{
		"text_delta:Partial",
		"content_block_end",
	}, handler.events)
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
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-4.1", llmtypes.MessageOpt{})
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
		Thread:      base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems: make([]StoredInputItem, 0),
		inputItems:  make([]responses.ResponseInputItemUnionParam, 0),
	}

	handler := &captureStreamHandler{}

	streamResult, err := thread.processStream(context.Background(), stream, handler, "gpt-4.1", llmtypes.MessageOpt{})
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
