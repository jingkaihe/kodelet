package bridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACPChatEventSinkTranslatesStreamingEvents(t *testing.T) {
	sender := &mockSender{}
	sink := NewACPChatEventSink(sender, "session-1")

	require.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", Delta: "hello"}))
	require.NoError(t, sink.Send(chat.ChatEvent{Kind: "thinking-delta", Delta: "checking"}))
	require.NoError(t, sink.Send(chat.ChatEvent{
		Kind:       "tool-use",
		ToolCallID: "tool-1",
		ToolName:   "bash",
		Input:      `{"command":"printf hello"}`,
	}))
	structured := tooltypes.StructuredToolResult{
		ToolName:  "bash",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: tooltypes.BashMetadata{
			Command:  "printf hello",
			ExitCode: 0,
			Output:   "hello",
		},
	}
	require.NoError(t, sink.Send(chat.ChatEvent{
		Kind:       "tool-update",
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolOutput: "hello",
		ToolResult: &structured,
	}))
	require.NoError(t, sink.Send(chat.ChatEvent{
		Kind:       "tool-result",
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolOutput: "hello",
		ToolResult: &structured,
	}))
	require.NoError(t, sink.Send(chat.ChatEvent{
		Kind: "user-message",
		Content: []any{
			map[string]any{"type": "text", "text": "steer here"},
			map[string]any{"type": "image"},
		},
	}))

	require.Len(t, sender.updates, 6)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, sender.updates[0].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateThoughtChunk, sender.updates[1].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCall, sender.updates[2].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCallUpdate, sender.updates[3].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCallUpdate, sender.updates[4].(map[string]any)["sessionUpdate"])
	userUpdate := sender.updates[5].(map[string]any)
	assert.Equal(t, acptypes.UpdateUserMessageChunk, userUpdate["sessionUpdate"])
	assert.Equal(t, "steer here", userUpdate["content"].(acptypes.ContentBlock).Text)

	require.Len(t, sender.transientUpdates, 1)
	assert.Equal(t, acptypes.ToolStatusInProgress, sender.transientUpdates[0].(map[string]any)["status"])
}

func TestACPChatEventSinkPreservesStructuredText(t *testing.T) {
	data := json.RawMessage(`{"text":"Finding.","citations":[{"url":"https://example.com","cited_text":"Evidence"}]}`)
	raw, err := json.Marshal(chat.ChatEvent{
		Kind: "text", Content: "Finding. [source](<https://example.com>)", TextData: data,
	})
	require.NoError(t, err)
	var event chat.ChatEvent
	require.NoError(t, json.Unmarshal(raw, &event))
	sender := &mockSender{}
	require.NoError(t, NewACPChatEventSink(sender, "session-1").Send(event))
	require.Len(t, sender.updates, 1)
	update := sender.updates[0].(map[string]any)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])
	assert.Equal(t, event.Content, update["content"].(map[string]any)["text"])
	payload, err := json.Marshal(update["_meta"].(map[string]any)["kodelet/textData"])
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(payload))
}

func TestReplayConversationHistory(t *testing.T) {
	sender := &mockSender{}
	structured := tooltypes.StructuredToolResult{ToolName: "file_write", Success: true, Timestamp: time.Now()}
	structuredJSON, err := structured.MarshalJSON()
	require.NoError(t, err)

	err = ReplayConversationHistory(sender, "session-1", []conversations.StreamableMessage{
		{
			Kind:    "text",
			Role:    "user",
			Content: "fix it",
			RawItem: []byte(`{"role":"user","content":[{"type":"input_text","text":"fix it"},{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="}]}`),
		},
		{Kind: "thinking", Role: "assistant", Content: "planning"},
		{Kind: "tool-use", Role: "assistant", ToolCallID: "tool-1", ToolName: "file_write", Input: `{"file_path":"a.txt"}`},
		{Kind: "tool-result", Role: "user", ToolCallID: "tool-1", ToolName: "file_write", Content: string(structuredJSON)},
		{Kind: "text", Role: "assistant", Content: "done"},
	})
	require.NoError(t, err)

	require.Len(t, sender.updates, 7)
	assert.Equal(t, acptypes.UpdateUserMessageChunk, sender.updates[0].(map[string]any)["sessionUpdate"])
	image := sender.updates[1].(map[string]any)["content"].(acptypes.ContentBlock)
	assert.Equal(t, acptypes.ContentTypeImage, image.Type)
	assert.Equal(t, "image/png", image.MimeType)
	assert.Equal(t, "aGVsbG8=", image.Data)
	assert.Equal(t, acptypes.UpdateThoughtChunk, sender.updates[2].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCall, sender.updates[3].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCallUpdate, sender.updates[4].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateToolCallUpdate, sender.updates[5].(map[string]any)["sessionUpdate"])
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, sender.updates[6].(map[string]any)["sessionUpdate"])
}

func TestACPCompactionLiveAndReplay(t *testing.T) {
	for _, method := range []string{"api", "summary"} {
		t.Run(method, func(t *testing.T) {
			marker := &llmtypes.CompactionMarker{
				ID:        "compact-1",
				Method:    method,
				CreatedAt: time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC),
			}
			if method == "summary" {
				marker.Summary = "Keep the earlier implementation decisions."
			}
			sender := &mockSender{}
			sink := NewACPChatEventSink(sender, "session-1")
			event := chat.ChatEvent{Kind: "context-compacted", Compaction: marker, BeforeCurrentUser: true}
			require.NoError(t, sink.Send(event))
			require.NoError(t, sink.Send(event))
			require.NoError(t, sink.Send(chat.ChatEvent{Kind: "context-compacted"}))
			require.Len(t, sender.updates, 1)

			update := sender.updates[0].(map[string]any)
			assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])
			text := update["content"].(acptypes.ContentBlock).Text
			assert.Contains(t, text, "Context compacted")
			if method == "api" {
				assert.Equal(t, "\n\nContext compacted\n\n", text)
			} else {
				assert.Contains(t, text, marker.Summary)
			}
			meta := update["_meta"].(map[string]any)[contextCompactedMetadataKey].(map[string]any)
			assert.Equal(t, marker, meta["compaction"])
			assert.Equal(t, true, meta["beforeCurrentUser"])
			payload, err := json.Marshal(update)
			require.NoError(t, err)
			assert.Contains(t, string(payload), `"createdAt":"2026-09-20T14:00:00Z"`)

			replayed := &mockSender{}
			require.NoError(t, ReplayConversationHistory(replayed, "session-1", []conversations.StreamableMessage{
				{Kind: "text", Role: "assistant", Content: "earlier answer"},
				{Kind: "context-compacted", Compaction: marker},
				{Kind: "context-compacted", Compaction: marker},
				{Kind: "text", Role: "user", Content: "continue"},
			}))
			require.Len(t, replayed.updates, 3)
			replayedUpdate := replayed.updates[1].(map[string]any)
			assert.Equal(t, update["content"], replayedUpdate["content"])
			replayedMeta := replayedUpdate["_meta"].(map[string]any)[contextCompactedMetadataKey].(map[string]any)
			assert.Equal(t, marker, replayedMeta["compaction"])
			assert.Equal(t, false, replayedMeta["beforeCurrentUser"], "replay is already in transcript order")
		})
	}
}

type failingCompactionSender struct {
	mockSender
	err error
}

func (s *failingCompactionSender) SendUpdate(sessionID acptypes.SessionID, update any) error {
	if s.err != nil {
		return s.err
	}
	return s.mockSender.SendUpdate(sessionID, update)
}

func TestACPCompactionSendFailureCanBeRetried(t *testing.T) {
	sender := &failingCompactionSender{err: assert.AnError}
	sink := NewACPChatEventSink(sender, "session-1")
	event := chat.ChatEvent{Kind: "context-compacted", Compaction: &llmtypes.CompactionMarker{ID: "compact-1", Method: "api"}}
	require.ErrorIs(t, sink.Send(event), assert.AnError)
	sender.err = nil
	require.NoError(t, sink.Send(event))
	require.Len(t, sender.updates, 1)

	sender.err = assert.AnError
	require.ErrorIs(t, ReplayConversationHistory(sender, "session-1", []conversations.StreamableMessage{
		{Kind: "context-compacted", Compaction: event.Compaction},
	}), assert.AnError)
}

func TestImageBlocksFromRawItemPreservesRemoteURI(t *testing.T) {
	blocks := imageBlocksFromRawItem([]byte(`{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}`))

	require.Len(t, blocks, 1)
	assert.Equal(t, acptypes.ContentTypeImage, blocks[0].Type)
	assert.Equal(t, "https://example.com/image.png", blocks[0].URI)
}

func TestReplayConversationHistoryPreservesGenericToolOutput(t *testing.T) {
	sender := &mockSender{}
	structured := tooltypes.StructuredToolResult{
		ToolName: "custom_tool",
		Success:  true,
		Metadata: tooltypes.ExtensionToolMetadata{
			ExtensionID: "example",
			ToolName:    "custom_tool",
			Output:      "raw extension output",
		},
	}
	payload, err := structured.MarshalJSON()
	require.NoError(t, err)

	err = ReplayConversationHistory(sender, "session-1", []conversations.StreamableMessage{
		{Kind: "tool-use", Role: "assistant", ToolCallID: "tool-1", ToolName: "custom_tool", Input: `{}`},
		{Kind: "tool-result", Role: "assistant", ToolCallID: "tool-1", ToolName: "custom_tool", Content: string(payload), ToolOutput: "raw extension output"},
	})
	require.NoError(t, err)
	require.Len(t, sender.updates, 3)
	result := sender.updates[2].(map[string]any)
	content := result["content"].([]map[string]any)
	require.Len(t, content, 1)
	assert.Equal(t, "raw extension output", content[0]["content"].(map[string]any)["text"])
}
