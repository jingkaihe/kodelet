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
		Thread:       base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems:  make([]StoredInputItem, 0),
		inputItems:   make([]responses.ResponseInputItemUnionParam, 0),
		pendingItems: make([]responses.ResponseInputItemUnionParam, 0),
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
		Thread:       base.NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}, "test", hooks.Trigger{}),
		storedItems:  make([]StoredInputItem, 0),
		inputItems:   make([]responses.ResponseInputItemUnionParam, 0),
		pendingItems: make([]responses.ResponseInputItemUnionParam, 0),
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
