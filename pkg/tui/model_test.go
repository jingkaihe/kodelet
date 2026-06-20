package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/steer"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stringMsg string

func (m stringMsg) String() string {
	return string(m)
}

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
	assert.Contains(t, content, "Ran 1 command")
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

func TestTextareaNewlineKeysInsertNewline(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "named shift enter", msg: stringMsg("shift+enter")},
		{name: "alt enter", msg: tea.KeyMsg{Type: tea.KeyEnter, Alt: true}},
		{name: "ctrl j", msg: tea.KeyMsg{Type: tea.KeyCtrlJ}},
		{name: "kitty csi u shift enter", msg: stringMsg("?CSI[49 51 59 50 117]?")},
		{name: "xterm modify other keys shift enter", msg: stringMsg("?CSI[50 55 59 50 59 49 51 126]?")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			m.textarea.SetValue("first line")

			updated, cmd := m.Update(tt.msg)
			m = updated.(model)

			assert.Nil(t, cmd)
			assert.Equal(t, "first line\n", m.textarea.Value())
			assert.Empty(t, m.entries)
		})
	}
}

func TestRunningShiftEnterInsertsSteeringNewline(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("first line")

	updated, cmd := m.Update(stringMsg("?CSI[49 51 59 50 117]?"))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "first line\n", m.textarea.Value())
	assert.Empty(t, m.queuedSteering)
}

func TestCtrlOTogglesDetails(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			thoughts: []thoughtBlock{{text: "toggle me", done: true}},
		}},
	}}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Nil(t, cmd)
	assert.True(t, m.entries[0].blocks[0].expanded)
	assert.Contains(t, content, "toggle me")
}

func TestRunningComposerQueuesSteering(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "working on it"}}},
	}
	m.textarea.SetValue("please focus on tests")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "steering queued", m.status)
	assert.Empty(t, m.textarea.Value())
	assert.Equal(t, []string{"please focus on tests"}, m.queuedSteering)
	content, _ := m.renderTranscript()
	assert.Contains(t, content, "queued steering")
	assert.Contains(t, content, "please focus on tests")

	steerStore, err := steer.NewSteerStore()
	require.NoError(t, err)
	pending, err := steerStore.ReadPendingSteer("conversation-123456789")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "please focus on tests", pending[0].Content)
}

func TestRunningComposerGeneratesConversationBeforeQueueingSteering(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("new chat steering")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	assert.Nil(t, cmd)
	require.NotEmpty(t, m.conversationID)
	steerPath := filepath.Join(homeDir, ".kodelet", "steer", "steer-"+m.conversationID+".json")
	_, err := os.Stat(steerPath)
	assert.NoError(t, err)
}

func TestConsumedSteeringRendersAsUserMessage(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "still working"}}}}
	m.queuedSteering = []string{"please focus on tests"}

	updated, _ := m.Update(chatEventMsg{runID: 1, event: webui.ChatEvent{Kind: "user-message", Content: "please focus on tests"}})
	m = updated.(model)

	assert.Empty(t, m.queuedSteering)
	require.Len(t, m.entries, 2)
	assert.Equal(t, entryUser, m.entries[1].kind)
	assert.Equal(t, "please focus on tests", m.entries[1].content)
	content, _ := m.renderTranscript()
	assert.Contains(t, content, "please focus on tests")
	assert.NotContains(t, content, "queued steering")
}

func TestInitialSubmittedUserMessageEventIsStillIgnored(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "go on"}}

	m.applyChatEvent(webui.ChatEvent{Kind: "user-message", Content: "go on"})

	require.Len(t, m.entries, 1)
	assert.Equal(t, entryUser, m.entries[0].kind)
	assert.Equal(t, "go on", m.entries[0].content)
}

func TestDuplicateConsumedSteeringClearsQueuedIndicator(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "repeat"}}
	m.queuedSteering = []string{"repeat"}

	m.applyChatEvent(webui.ChatEvent{Kind: "user-message", Content: "repeat"})

	assert.Empty(t, m.queuedSteering)
	require.Len(t, m.entries, 1)
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
	assert.Contains(t, content, "Ran 1 command")
	assert.NotContains(t, content, "hidden thought")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "hidden thought")

	m.toggleAllDetails()
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "input: {\"cmd\":\"pwd\"}")
	assert.Contains(t, content, "result: ok")
}

func TestRenderTranscriptAddsSpacingBetweenAssistantBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{
			{kind: blockThoughts, thoughts: []thoughtBlock{{text: "thought", done: true}}},
			{kind: blockTools, tools: []toolCall{{name: "bash", done: true}}},
			{kind: blockText, text: "final answer"},
		},
	}}

	content, _ := m.renderTranscript()

	assert.Contains(t, content, "Had 1 Thought ▸\n\n")
	assert.Contains(t, content, "Ran 1 command ▸\n\n")
	assert.Contains(t, content, "\n\nfinal answer")
}

func TestRenderTranscriptGroupsToolBlocksByType(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{
				{name: "bash", done: true},
				{name: "bash", done: true},
				{
					name: "apply_patch",
					done: true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "apply_patch",
						Success:  true,
						Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{
							{Path: "edit.go", Operation: tooltypes.ApplyPatchOperationUpdate, UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n"},
							{Path: "new.go", Operation: tooltypes.ApplyPatchOperationAdd, NewContent: "package main\n"},
							{Path: "old.go", Operation: tooltypes.ApplyPatchOperationDelete, OldContent: "package old\n"},
						}},
					},
				},
				{
					name:  "web_fetch",
					input: `{"url":"https://example.com"}`,
					done:  true,
				},
				{name: "grep_tool", done: true},
				{name: "glob_tool", done: true},
			},
		}},
	}}

	content, regions := m.renderTranscript()

	assert.Contains(t, content, "Ran 2 commands")
	assert.Contains(t, content, "Edit edit.go")
	assert.Contains(t, content, "Write new.go")
	assert.Contains(t, content, "Delete old.go")
	assert.Contains(t, content, "Fetched https://example.com")
	assert.Contains(t, content, "Ran 2 tools")
	require.Len(t, regions, 6)
}

func TestRenderTranscriptApplyPatchDiffToggle(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{{
				name: "apply_patch",
				done: true,
				structured: &tooltypes.StructuredToolResult{
					ToolName: "apply_patch",
					Success:  true,
					Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{{
						Path:        "edit.go",
						Operation:   tooltypes.ApplyPatchOperationUpdate,
						UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
					}}},
				},
			}},
		}},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	require.Len(t, regions, 1)
	m.detailRegions = regions
	assert.Contains(t, content, "Edit edit.go")
	assert.NotContains(t, content, "@@ -1 +1 @@")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "@@ -1 +1 @@")
	assert.Contains(t, content, "-old")
	assert.Contains(t, content, "+new")
}

func TestRenderDiffLineUsesAdditionAndRemovalStyles(t *testing.T) {
	assert.Equal(t, diffAddedStyle.Render("  +new"), renderDiffLine("  +new"))
	assert.Equal(t, diffRemovedStyle.Render("  -old"), renderDiffLine("  -old"))
	assert.Equal(t, toolBodyStyle.Render("  +++ b/file.go"), renderDiffLine("  +++ b/file.go"))
	assert.Equal(t, toolBodyStyle.Render("  --- a/file.go"), renderDiffLine("  --- a/file.go"))
}

func TestEntriesFromHistoryPreservesStructuredToolResultMetadata(t *testing.T) {
	structured := tooltypes.StructuredToolResult{
		ToolName: "web_fetch",
		Success:  true,
		Metadata: &tooltypes.WebFetchMetadata{URL: "https://example.com", Content: "ok"},
	}
	data, err := structured.MarshalJSON()
	require.NoError(t, err)

	entries := entriesFromHistory([]conversations.StreamableMessage{
		{Kind: "tool-use", Role: "assistant", ToolCallID: "call-1", ToolName: "web_fetch", Input: `{"url":"https://example.com"}`},
		{Kind: "tool-result", Role: "user", ToolCallID: "call-1", Content: string(data)},
	})

	require.Len(t, entries, 1)
	require.Len(t, entries[0].blocks, 1)
	require.Len(t, entries[0].blocks[0].tools, 1)
	tool := entries[0].blocks[0].tools[0]
	require.NotNil(t, tool.structured)
	assert.Equal(t, "web_fetch", tool.structured.ToolName)
	assert.Contains(t, tool.result, "Web Fetch: https://example.com")
}

func TestRenderTranscriptRendersAssistantMarkdown(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockText,
			text: "Here is `code`:\n\n- first\n- second",
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "Here is")
	assert.Contains(t, plain, "code")
	assert.Contains(t, plain, "• first")
	assert.Contains(t, plain, "• second")
}

func TestRenderTranscriptSeparatesThinkingMarkdownBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			expanded: true,
			thoughts: []thoughtBlock{
				{text: "First thought"},
				{text: "Second thought"},
			},
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, joinThoughts(m.entries[0].blocks[0].thoughts), "First thought\n\nSecond thought")
	assert.Contains(t, plain, "First thought")
	assert.Regexp(t, `First thought\s*\n\s*\n\s*Second thought`, plain)
}

func TestStreamingDeltasAreDebouncedBeforeViewportRefresh(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.refreshViewport(true)
	initialContent := m.viewport.View()

	updated, cmd := m.Update(chatEventMsg{runID: 1, event: webui.ChatEvent{Kind: "text-delta", Delta: "**hello**"}})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.True(t, m.pendingRefresh)
	require.Len(t, m.entries, 1)
	assert.Equal(t, "**hello**", m.entries[0].blocks[0].text)
	assert.Equal(t, initialContent, m.viewport.View())

	updated, _ = m.Update(transcriptRefreshMsg{})
	m = updated.(model)

	assert.False(t, m.pendingRefresh)
	assert.Contains(t, xansi.Strip(m.viewport.View()), "hello")
}

func TestStreamingPreservesViewportAfterUserScrollsUp(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)
	bottomOffset := m.viewport.YOffset
	require.Greater(t, bottomOffset, 0)
	require.True(t, m.autoFollow)

	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	m = updated.(model)
	scrolledOffset := m.viewport.YOffset
	require.Less(t, scrolledOffset, bottomOffset)
	assert.False(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: webui.ChatEvent{Kind: "text-delta", Delta: "\nstill streaming"}})
	m = updated.(model)

	assert.Equal(t, scrolledOffset, m.viewport.YOffset)
	assert.False(t, m.autoFollow)
}

func TestScrollingBackToBottomResumesStreamingAutoFollow(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	require.False(t, m.autoFollow)
	require.False(t, m.viewport.AtBottom())

	for range 10 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(model)
		if m.viewport.AtBottom() {
			break
		}
	}
	require.True(t, m.viewport.AtBottom())
	require.True(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: webui.ChatEvent{Kind: "text-delta", Delta: "\nnew bottom line"}})
	m = updated.(model)

	assert.True(t, m.viewport.AtBottom())
	assert.True(t, m.autoFollow)
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

func TestRenderExitSummary(t *testing.T) {
	summary := renderExitSummary(" conversation-123 ", llmtypes.Usage{
		InputTokens:              1200,
		OutputTokens:             300,
		CacheCreationInputTokens: 40,
		CacheReadInputTokens:     60,
		InputCost:                0.01,
		OutputCost:               0.02,
		CacheCreationCost:        0.003,
		CacheReadCost:            0.001,
		CurrentContextWindow:     1600,
		MaxContextWindow:         3200,
	})

	assert.Contains(t, summary, "Conversation ID: conversation-123")
	assert.Contains(t, summary, "Token usage: 1.2K input · 300 output · 40 cache write · 60 cache read · 1.6K total")
	assert.Contains(t, summary, "Context window: 1.6K/3.2K (50%)")
	assert.Contains(t, summary, "Cost: $0.0340")
	assert.Contains(t, summary, "Resume: kodelet chat -r conversation-123")
	assert.Empty(t, renderExitSummary(" ", llmtypes.Usage{}))
}

func numberedLines(count int) string {
	return strings.TrimRight(strings.Repeat("line\n", count), "\n")
}

var _ tea.Model = model{}
