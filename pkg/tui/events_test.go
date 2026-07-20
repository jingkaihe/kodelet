package tui

import (
	"context"
	"testing"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyChatEventUpdatesConversationAndBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	usage := llmtypes.Usage{InputCost: 0.25, OutputCost: 0.75}
	for _, event := range []chat.ChatEvent{
		{Kind: "conversation", ConversationID: "conv-1"},
		{Kind: "text-delta", Delta: "hello"},
		{Kind: "thinking-start"},
		{Kind: "thinking-delta", Delta: "think"},
		{Kind: "thinking-end"},
		{Kind: "tool-use", ToolCallID: "tool-1", ToolName: "bash", Input: "{\n  \"cmd\": \"pwd\"\n}"},
		{Kind: "tool-update", ToolCallID: "tool-1", ToolResult: &tooltypes.StructuredToolResult{
			ToolName: "bash",
			Success:  true,
			Metadata: &tooltypes.BashMetadata{Output: "partial"},
		}},
		{Kind: "tool-result", ToolCallID: "tool-1", ToolResult: &tooltypes.StructuredToolResult{
			ToolName: "bash",
			Success:  false,
			Error:    "boom",
			Metadata: &tooltypes.BashMetadata{},
		}},
		{Kind: "usage", Usage: &usage},
		{Kind: "error", Error: "model error"},
	} {
		m.applyChatEvent(event)
	}

	require.Len(t, m.entries, 1)
	require.Len(t, m.entries[0].blocks, 4)
	assert.Equal(t, "conv-1", m.conversationID)
	assert.Equal(t, usage.TotalCost(), m.usage.TotalCost())
	assert.Equal(t, "hello", m.entries[0].blocks[0].text)
	assert.False(t, hasActiveThought(m.entries[0].blocks[1]))
	assert.Contains(t, joinThoughts(m.entries[0].blocks[1].thoughts), "think")
	assert.True(t, m.entries[0].blocks[2].tools[0].failed)
	assert.Contains(t, m.entries[0].blocks[2].tools[0].result, "boom")
	assert.Contains(t, m.entries[0].blocks[3].text, "model error")
}

func TestApplyChatEventReplacesToolUpdateWithFinalResult(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	m.applyChatEvent(chat.ChatEvent{Kind: "tool-use", ToolCallID: "tool-1", ToolName: "bash", Input: `{}`})
	m.applyChatEvent(chat.ChatEvent{Kind: "tool-update", ToolCallID: "tool-1", ToolResult: &tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
		Metadata: &tooltypes.BashMetadata{Output: "partial"},
	}})

	tool := m.entries[0].blocks[0].tools[0]
	assert.False(t, tool.done)
	assert.Contains(t, tool.result, "partial")

	m.applyChatEvent(chat.ChatEvent{Kind: "tool-result", ToolCallID: "tool-1", ToolResult: &tooltypes.StructuredToolResult{
		ToolName: "bash",
		Success:  true,
		Metadata: &tooltypes.BashMetadata{Output: "complete"},
	}})

	tool = m.entries[0].blocks[0].tools[0]
	assert.True(t, tool.done)
	assert.Contains(t, tool.result, "complete")
	assert.NotContains(t, tool.result, "partial")
}

func TestApplyChatEventPreservesWhitespaceOnlyTextDeltas(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	for _, event := range []chat.ChatEvent{
		{Kind: "text-delta", Delta: "hello"},
		{Kind: "text-delta", Delta: " "},
		{Kind: "text-delta", Delta: "world"},
		{Kind: "text-delta", Delta: "\n\n"},
		{Kind: "text-delta", Delta: "again"},
	} {
		m.applyChatEvent(event)
	}

	require.Len(t, m.entries, 1)
	require.Len(t, m.entries[0].blocks, 1)
	assert.Equal(t, "hello world\n\nagain", m.entries[0].blocks[0].text)
	assert.Equal(t, "hello world\n\nagain", m.entries[0].content)
}

func TestUserMessageContentTextHandlesStructuredBlocks(t *testing.T) {
	assert.Equal(t, "hello", userMessageContentText(" hello "))
	assert.Equal(t, "hello\n[2 images]", userMessageContentText([]chat.ChatContentBlock{
		{Type: "text", Text: " hello "},
		{Type: "image"},
		{Type: "image"},
	}))
	assert.Equal(t, "from any\n[1 image]", userMessageContentText([]any{
		map[string]any{"type": "text", "text": " from any "},
		map[string]any{"type": "image"},
		"ignored",
	}))
	assert.Empty(t, userMessageContentText(42))
}

func TestApplyChatEventRecordsStructuredOrphanToolResult(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	m.applyChatEvent(chat.ChatEvent{Kind: "tool-result", ToolCallID: "missing", ToolResult: &tooltypes.StructuredToolResult{
		ToolName: "web_fetch",
		Success:  false,
		Error:    "fetch failed",
		Metadata: &tooltypes.WebFetchMetadata{URL: "https://example.com"},
	}})

	require.Len(t, m.entries, 1)
	require.Len(t, m.entries[0].blocks, 1)
	require.Len(t, m.entries[0].blocks[0].tools, 1)
	tool := m.entries[0].blocks[0].tools[0]
	assert.Equal(t, "web_fetch", tool.name)
	assert.True(t, tool.done)
	assert.True(t, tool.failed)
	assert.Contains(t, tool.result, "fetch failed")
}
