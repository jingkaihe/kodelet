package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelActiveRunFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	cancelled := false
	m.running = true
	m.activeRunID = 1
	m.cancelRun = func() { cancelled = true }
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{
				{
					kind: blockThoughts,
					thoughts: []thoughtBlock{{
						text: "still thinking",
						done: false,
					}},
				},
				{
					kind: blockTools,
					tools: []toolCall{{
						name: "bash",
						done: false,
					}},
				},
			},
		},
	}

	m.cancelActiveRun()
	content, _ := m.renderTranscript()

	assert.True(t, cancelled)
	assert.False(t, m.running)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "cancelled", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.False(t, hasActiveTool(m.entries[1].blocks[1]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.Contains(t, content, "Ran 1 Tool")
	assert.NotContains(t, content, "Thinking")
}

func TestUpdateIgnoresStaleRunEvents(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "first"}}
	m.activeRunID = 2
	m.running = true

	updated, _ := m.Update(chatEventMsg{runID: 1, event: webui.ChatEvent{Kind: "text", Delta: "stale"}})
	m = updated.(model)
	content, _ := m.renderTranscript()
	assert.NotContains(t, content, "stale")

	updated, _ = m.Update(chatEventMsg{runID: 2, event: webui.ChatEvent{Kind: "text", Delta: "fresh"}})
	m = updated.(model)
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "fresh")
}

func TestDoneFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{{
				kind:     blockThoughts,
				thoughts: []thoughtBlock{{text: "still thinking"}},
			}},
		},
	}

	updated, _ := m.Update(chatDoneMsg{runID: 1, conversationID: "conv-1"})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.False(t, m.running)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "conv-1", m.conversationID)
	assert.Equal(t, "ready", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.NotContains(t, content, "Thinking")
}

func TestInitialHistoryErrorIsVisibleInTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "missing-conversation"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	updated, _ := m.Update(initialHistoryMsg{err: errors.New("conversation not found")})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Equal(t, "history load failed", m.status)
	assert.ErrorContains(t, m.err, "conversation not found")
	assert.Contains(t, content, "Failed to resume conversation")
	assert.Contains(t, content, "conversation not found")
	assert.NotContains(t, content, "Hello! What would you like me to work on?")
}

func TestInitialHistoryDoesNotClobberLocalEntries(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.status = "working"
	m.entries = []chatEntry{
		{kind: entryUser, content: "local prompt"},
		{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "streaming answer"}}},
	}

	updated, _ := m.Update(initialHistoryMsg{
		entries: []chatEntry{
			{kind: entryUser, content: "old prompt"},
			{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "old answer"}}},
		},
		usage: llmtypes.Usage{CurrentContextWindow: 10, MaxContextWindow: 100},
	})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Equal(t, "working", m.status)
	assert.Len(t, m.entries, 2)
	assert.Contains(t, content, "local prompt")
	assert.Contains(t, content, "streaming answer")
	assert.NotContains(t, content, "old prompt")
	assert.NotContains(t, content, "old answer")
	assert.Zero(t, m.usage.CurrentContextWindow)
}

func TestInitialHistorySeedsEmptyTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	updated, _ := m.Update(initialHistoryMsg{
		entries: []chatEntry{
			{kind: entryUser, content: "old prompt"},
			{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "old answer"}}},
		},
		usage: llmtypes.Usage{CurrentContextWindow: 10, MaxContextWindow: 100},
	})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Equal(t, "resumed conversa", m.status)
	assert.Equal(t, 10, m.usage.CurrentContextWindow)
	assert.Contains(t, content, "old prompt")
	assert.Contains(t, content, "old answer")
}

func TestEntriesFromHistoryBuildsTextThinkingAndToolBlocks(t *testing.T) {
	entries := entriesFromHistory([]conversations.StreamableMessage{
		{Kind: "text", Role: "user", Content: "  hello  "},
		{Kind: "text", Role: "assistant", Content: " first"},
		{Kind: "text", Role: "assistant", Content: " second "},
		{Kind: "thinking", Role: "assistant", Content: "considering"},
		{Kind: "tool-use", Role: "assistant", ToolCallID: "call-1", ToolName: "bash", Input: "{\n  \"cmd\": \"date\"\n}"},
		{Kind: "tool-result", Role: "user", ToolCallID: "call-1", Content: "Saturday"},
		{Kind: "tool-result", Role: "user", ToolCallID: "call-2", ToolName: "grep", Content: "orphan result"},
	})

	require.Len(t, entries, 2)
	assert.Equal(t, entryUser, entries[0].kind)
	assert.Equal(t, "hello", entries[0].content)
	require.Len(t, entries[1].blocks, 3)
	assert.Equal(t, "first second", entries[1].blocks[0].text)
	assert.Equal(t, "first second", entries[1].content)
	assert.Equal(t, blockThoughts, entries[1].blocks[1].kind)
	assert.Equal(t, []thoughtBlock{{text: "considering", done: true}}, entries[1].blocks[1].thoughts)
	assert.Equal(t, blockTools, entries[1].blocks[2].kind)
	assert.Equal(t, "bash", entries[1].blocks[2].tools[0].name)
	assert.Equal(t, "Saturday", entries[1].blocks[2].tools[0].result)
	assert.True(t, entries[1].blocks[2].tools[0].done)
	require.Len(t, entries[1].blocks[2].tools, 2)
	assert.Equal(t, "grep", entries[1].blocks[2].tools[1].name)
	assert.Equal(t, "orphan result", entries[1].blocks[2].tools[1].result)
}

func TestApplyChatEventUpdatesConversationAndBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	usage := llmtypes.Usage{InputCost: 0.25, OutputCost: 0.75}
	for _, event := range []webui.ChatEvent{
		{Kind: "conversation", ConversationID: "conv-1"},
		{Kind: "text-delta", Delta: "hello"},
		{Kind: "thinking-start"},
		{Kind: "thinking-delta", Delta: "think"},
		{Kind: "thinking-end"},
		{Kind: "tool-use", ToolCallID: "tool-1", ToolName: "bash", Input: "{\n  \"cmd\": \"pwd\"\n}"},
		{Kind: "tool-result", ToolCallID: "tool-1", ToolResult: &tooltypes.StructuredToolResult{
			ToolName: "bash",
			Success:  false,
			Error:    "boom",
			Metadata: &tooltypes.BashMetadata{},
		}},
		{Kind: "usage", Usage: &usage},
		{Kind: "ui-input"},
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
	assert.Contains(t, m.entries[0].blocks[3].text, "Extension requested interactive input")
	assert.Contains(t, m.entries[0].blocks[3].text, "model error")
}

func TestRenderTranscriptDetailsAndMouseToggle(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{
			{kind: blockThoughts, thoughts: []thoughtBlock{{text: "hidden thought", done: true}}},
			{kind: blockTools, tools: []toolCall{{name: "bash", input: "{\n  \"cmd\": \"pwd\"\n}", result: "ok", done: true}}},
		},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	require.Len(t, regions, 2)
	assert.Contains(t, content, "Had 1 Thought")
	assert.Contains(t, content, "Ran 1 Tool")
	assert.NotContains(t, content, "hidden thought")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "hidden thought")

	m.toggleAllDetails()
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "input: {\"cmd\":\"pwd\"}")
	assert.Contains(t, content, "result: ok")
}

func TestViewAndFormattingHelpers(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: " work ", CWD: ""})
	t.Cleanup(m.cancel)

	assert.Empty(t, m.View())
	m.width = 48
	m.height = 12
	m.resize()
	m.usage = llmtypes.Usage{
		CurrentContextWindow: 1500,
		MaxContextWindow:     3000,
		InputCost:            0.125,
		OutputCost:           0.125,
	}
	m.textarea.SetValue("draft")
	view := m.View()

	assert.Contains(t, view, "draft")
	assert.Contains(t, view, "1.5K/3.0K (50%)")
	assert.Contains(t, view, "work")
	assert.Equal(t, "default", displayProfile(""))
	assert.Equal(t, "", profileForRequest("default"))
	assert.Equal(t, "work", profileForRequest(" work "))
	assert.Equal(t, "abcdefgh", shortID("abcdefghi123"))
	assert.Equal(t, "…", fitVisible("abcdef", 1))
	assert.Equal(t, "abcdef", fitVisible("abcdef", 20))
	assert.Equal(t, "a   ", padVisible("a", 4))
	assert.Equal(t, "plain", compactJSON(" plain "))
	assert.Equal(t, `{"a":1}`, compactJSON("{\n  \"a\": 1\n}"))
	assert.Equal(t, "  one\n  \n  two", indentText("one\n\ntwo", "  "))
	assert.Equal(t, 2, lineCount("one\ntwo"))
	assert.True(t, strings.HasPrefix(rightLabeledBorder("╭", "╮", 12, "label"), "╭"))
}

var _ tea.Model = model{}
