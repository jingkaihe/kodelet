package bridge

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
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
