package tui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	convdb "github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadInitialHistorySkipsBlankConversationID(t *testing.T) {
	msg, ok := loadInitialHistory(context.Background(), " \t\n ")().(initialHistoryMsg)

	require.True(t, ok)
	assert.False(t, msg.loaded)
	assert.Empty(t, msg.entries)
	assert.NoError(t, msg.err)
}

func TestLoadInitialHistoryLoadsStoredConversation(t *testing.T) {
	ctx := context.Background()
	basePath := setupTUIConversationStore(ctx, t)

	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)

	record := convtypes.NewConversationRecord("conversation-history")
	record.Provider = "anthropic"
	record.CWD = t.TempDir()
	record.Metadata = map[string]any{"profile": " stored "}
	record.Usage = llmtypes.Usage{CurrentContextWindow: 42, MaxContextWindow: 100}
	record.RawMessages = []byte(`[
		{"role":"user","content":[{"type":"text","text":"old prompt"}]},
		{"role":"assistant","content":[{"type":"text","text":"old answer"}]}
	]`)
	require.NoError(t, store.Save(ctx, record))
	require.NoError(t, store.Close())

	msg, ok := loadInitialHistory(ctx, record.ID)().(initialHistoryMsg)

	require.True(t, ok)
	require.NoError(t, msg.err)
	assert.True(t, msg.loaded)
	assert.Equal(t, record.CWD, msg.cwd)
	assert.Equal(t, "stored", msg.profile)
	assert.Equal(t, 42, msg.usage.CurrentContextWindow)
	require.Len(t, msg.entries, 2)
	assert.Equal(t, "old prompt", msg.entries[0].content)
	assert.Equal(t, "old answer", msg.entries[1].blocks[0].text)
	assert.FileExists(t, filepath.Join(basePath, "storage.db"))
}

func TestLoadInitialHistoryReportsLoadAndParseErrors(t *testing.T) {
	ctx := context.Background()
	setupTUIConversationStore(ctx, t)

	missing, ok := loadInitialHistory(ctx, "missing-conversation")().(initialHistoryMsg)
	require.True(t, ok)
	assert.ErrorContains(t, missing.err, "failed to load conversation")

	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)
	record := convtypes.NewConversationRecord("bad-provider")
	record.Provider = "unsupported"
	record.RawMessages = []byte(`[]`)
	require.NoError(t, store.Save(ctx, record))
	require.NoError(t, store.Close())

	parsed, ok := loadInitialHistory(ctx, record.ID)().(initialHistoryMsg)
	require.True(t, ok)
	assert.ErrorContains(t, parsed.err, "failed to parse conversation")
}

func setupTUIConversationStore(ctx context.Context, t *testing.T) string {
	t.Helper()
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	database, err := convdb.Open(ctx, filepath.Join(basePath, "storage.db"))
	require.NoError(t, err)
	require.NoError(t, convdb.NewMigrationRunner(database).Run(ctx, migrations.All()))
	require.NoError(t, database.Close())
	return basePath
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
		loaded: true,
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

func TestInitialHistoryUpdatesDisplayedCWD(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", CWD: "/tmp/shell"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	updated, _ := m.Update(initialHistoryMsg{
		loaded:  true,
		entries: []chatEntry{{kind: entryUser, content: "old prompt"}},
		cwd:     "/tmp/project",
	})
	m = updated.(model)

	assert.Equal(t, "/tmp/project", m.cwd)
}

func TestInitialHistoryUpdatesDisplayedProfileAndLocksPicker(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", Profile: "current", ProfileOptions: []string{"default", "current", "stored"}})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.profilePickerOpen = true

	updated, _ := m.Update(initialHistoryMsg{
		loaded:  true,
		entries: []chatEntry{{kind: entryUser, content: "old prompt"}},
		profile: "stored",
	})
	m = updated.(model)

	assert.Equal(t, "stored", m.profile)
	assert.Equal(t, 2, m.profileIndex)
	assert.False(t, m.profilePickerOpen)
	assert.False(t, m.canChangeProfile())
}

func TestInitialHistoryUpdatesDisplayedCWDForEmptyConversation(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", CWD: "/tmp/shell"})
	t.Cleanup(m.cancel)

	updated, _ := m.Update(initialHistoryMsg{loaded: true, cwd: "/tmp/project"})
	m = updated.(model)

	assert.Equal(t, "/tmp/project", m.cwd)
}

func TestInitialHistoryDoesNotClobberDisplayedCWDForActiveRun(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", CWD: "/tmp/shell"})
	t.Cleanup(m.cancel)
	m.running = true
	m.entries = []chatEntry{{kind: entryUser, content: "local prompt"}}

	updated, _ := m.Update(initialHistoryMsg{
		loaded:  true,
		entries: []chatEntry{{kind: entryUser, content: "old prompt"}},
		cwd:     "/tmp/project",
	})
	m = updated.(model)

	assert.Equal(t, "/tmp/shell", m.cwd)
}

func TestInitialHistorySeedsEmptyTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	updated, _ := m.Update(initialHistoryMsg{
		loaded: true,
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

func TestInitialHistoryPrependsUserMessagesToSearchHistory(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.messageHistory = []string{"newer persisted prompt"}
	m.width = 100
	m.height = 30
	m.resize()

	updated, _ := m.Update(initialHistoryMsg{
		loaded: true,
		entries: []chatEntry{
			{kind: entryUser, content: "old prompt"},
			{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "old answer"}}},
		},
	})
	m = updated.(model)

	assert.Equal(t, []string{"old prompt", "newer persisted prompt"}, m.messageHistory)
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
