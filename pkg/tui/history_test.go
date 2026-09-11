package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	convdb "github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteMessageHistoryRunner struct {
	recordingRunner
	result       protocol.WorkspaceMessageHistoryResult
	loadTarget   chat.WorkspaceTarget
	appendTarget chat.WorkspaceTarget
	entry        messagehistory.Entry
	historyErr   error
}

func (r *remoteMessageHistoryRunner) LoadMessageHistory(ctx context.Context, target chat.WorkspaceTarget) (protocol.WorkspaceMessageHistoryResult, error) {
	_, bounded := ctx.Deadline()
	if !bounded {
		return protocol.WorkspaceMessageHistoryResult{}, errors.New("history request must have a deadline")
	}
	r.loadTarget = target
	return r.result, r.historyErr
}

func (r *remoteMessageHistoryRunner) AppendMessageHistory(ctx context.Context, target chat.WorkspaceTarget, entry messagehistory.Entry) error {
	_, bounded := ctx.Deadline()
	if !bounded {
		return errors.New("history request must have a deadline")
	}
	r.appendTarget, r.entry = target, entry
	return r.historyErr
}

func TestRemoteMessageHistoryLoadsAfterInitializationAndRefreshesSearch(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("no client storage"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	runner := &remoteMessageHistoryRunner{result: protocol.WorkspaceMessageHistoryResult{CWD: "/runner/project/subdir", ScopeCWD: "/runner/project", Messages: []string{"run old tests"}}}
	m := newModel(t.Context(), Config{Initialize: func(context.Context) (Config, error) {
		return Config{Runner: runner, CWD: "/runner/project/subdir", Profile: "work", EnvironmentProfile: "sandbox"}, nil
	}})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 30
	updated, initial := m.Update(m.initializeCommand()())
	m = updated.(model)
	require.NotNil(t, initial)
	updated, load := m.Update(initial())
	m = updated.(model)
	require.NotNil(t, load, "daemon mode must load composer history, not just the transcript")
	assert.Empty(t, runner.loadTarget, "history loading must be asynchronous")

	// A submission and Ctrl+R can happen while the initial history request is
	// in flight. The current message stays newest and results refresh in place.
	m.appendSubmittedMessageToHistory("run new tests")
	m.textarea.SetValue("draft")
	updated, _ = m.Update(keyPressWithMod('r', tea.ModCtrl))
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("old"))
	m = updated.(model)
	assert.Empty(t, m.historySearch.matches)
	updated, _ = m.Update(load())
	m = updated.(model)
	assert.Equal(t, chat.WorkspaceTarget{CWD: "/runner/project/subdir", Profile: "work", EnvironmentProfile: "sandbox"}, runner.loadTarget)
	assert.Equal(t, "/runner/project", m.messageHistoryScopeCWD)
	assert.Equal(t, []string{"run old tests", "run new tests"}, m.messageHistory)
	assert.Equal(t, []string{"run old tests"}, m.historySearch.matches)
	assert.Equal(t, "run old tests", m.textarea.Value())
	assert.Nil(t, m.messageHistoryStore)
	data, err := os.ReadFile(base)
	require.NoError(t, err)
	assert.Equal(t, "no client storage", string(data))
}

func TestRemoteMessageHistoryUsesSavedAffinityAndDiscardsStaleResults(t *testing.T) {
	runner := &remoteMessageHistoryRunner{result: protocol.WorkspaceMessageHistoryResult{CWD: "/runner/stored", ScopeCWD: "/runner/stored", Messages: []string{"saved prompt"}}}
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, ConversationID: "saved-id", CWD: "/client/wrong"})
	t.Cleanup(m.cancel)
	updated, load := m.Update(initialHistoryMsg{conversationKey: m.key, loaded: true, cwd: "/runner/stored"})
	m = updated.(model)
	require.NotNil(t, load)
	result := load().(messageHistoryMsg)
	assert.Equal(t, chat.WorkspaceTarget{ConversationID: "saved-id"}, runner.loadTarget)
	saved := m.conversationState

	newConversation := m.createNewConversationAt("/runner/other")
	m.textarea.SetValue("unrelated draft")
	updated, _ = m.Update(result)
	m = updated.(model)
	assert.Equal(t, []string{"saved prompt"}, saved.messageHistory)
	assert.Empty(t, m.messageHistory)
	assert.Equal(t, "unrelated draft", m.textarea.Value(), "background history must not replace the active composer")

	// The /new workflow must schedule a fresh history load for its directory.
	batch := newConversation().(tea.BatchMsg)
	runner.result = protocol.WorkspaceMessageHistoryResult{CWD: "/runner/other", ScopeCWD: "/runner/other", Messages: []string{"other prompt"}}
	result = batch[len(batch)-1]().(messageHistoryMsg)
	assert.Equal(t, "/runner/other", runner.loadTarget.CWD)
	assert.Empty(t, runner.loadTarget.ConversationID)
	m.requestedCWD = "/runner/changed"
	updated, _ = m.Update(result)
	m = updated.(model)
	assert.Empty(t, m.messageHistory, "ignore history for a directory that is no longer selected")
}

func TestRemoteMessageHistoryPersistsRawSubmissionsWithoutLocalScopeResolution(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		for _, historyErr := range []error{nil, assert.AnError} {
			t.Run(fmt.Sprintf("resumed=%t/error=%t", resumed, historyErr != nil), func(t *testing.T) {
				runner := &remoteMessageHistoryRunner{historyErr: historyErr}
				config := Config{Runner: runner, Remote: true, CWD: "/only/on/runner", Profile: "work", EnvironmentProfile: "sandbox"}
				if resumed {
					config.ConversationID = "saved-id"
				}
				m := newModel(t.Context(), config)
				t.Cleanup(m.cancel)
				m.initialHistoryPending = false
				m.textarea.SetValue(" /goal ship raw history ")
				submit := m.submit()
				require.NotNil(t, submit)
				assert.Empty(t, m.messageHistoryScopeCWD, "the TUI must not resolve a remote path on the client")
				assert.Nil(t, submit())
				saved := receiveRunMsg(t, m.runCh).(messageHistorySavedMsg)
				assert.Equal(t, historyErr, saved.err)
				for {
					if _, done := receiveRunMsg(t, m.runCh).(chatDoneMsg); done {
						break
					}
				}
				assert.Equal(t, "/goal ship raw history", runner.req.Message)
				assert.Equal(t, runner.req.Message, runner.entry.Text)
				assert.Equal(t, m.conversationID, runner.entry.ConversationID)
				assert.Equal(t, "work", runner.entry.Profile)
				assert.Equal(t, "tui", runner.entry.Source)
				assert.Empty(t, runner.entry.ScopeCWD, "only the runner determines the history scope")
				if resumed {
					assert.Equal(t, chat.WorkspaceTarget{ConversationID: "saved-id"}, runner.appendTarget)
				} else {
					assert.Equal(t, chat.WorkspaceTarget{CWD: "/only/on/runner", Profile: "work", EnvironmentProfile: "sandbox"}, runner.appendTarget)
				}
				updated, _ := m.Update(saved)
				m = updated.(model)
				if historyErr != nil {
					require.Len(t, m.uiNotifications, 1)
					assert.Equal(t, "Message history was not saved", m.uiNotifications[0].title)
					assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
				}
			})
		}
	}
}

func TestRemoteMessageHistoryLoadFailureKeepsInMemoryHistory(t *testing.T) {
	runner := &remoteMessageHistoryRunner{historyErr: assert.AnError}
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, CWD: "/only/on/runner"})
	t.Cleanup(m.cancel)
	m.messageHistory = []string{"current prompt"}
	m.textarea.SetValue("draft")
	updated, _ := m.Update(m.loadRemoteMessageHistory(m.conversationState)())
	m = updated.(model)
	assert.Equal(t, []string{"current prompt"}, m.messageHistory)
	assert.Equal(t, "draft", m.textarea.Value())
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, "Message history unavailable", m.uiNotifications[0].title)
	assert.Nil(t, m.messageHistoryStore, "a failed runner must never fall back to the client store")
}

func TestLoadInitialHistorySkipsBlankConversationID(t *testing.T) {
	msg, ok := loadConversationHistoryFromSource(context.Background(), "", " \t\n ", nil)().(initialHistoryMsg)

	require.True(t, ok)
	assert.False(t, msg.loaded)
	assert.Empty(t, msg.entries)
	assert.NoError(t, msg.err)
}

func TestLoadInitialHistoryUsesInjectedSource(t *testing.T) {
	source := &conversationSourceRunner{history: chat.ConversationHistory{
		ID: "conversation-history", CWD: "/only/on/runner", Profile: "stored", Provider: "anthropic", ReasoningEffort: "high",
		ParentConversationID: " parent-id ",
		Usage:                llmtypes.Usage{CurrentContextWindow: 42, MaxContextWindow: 100},
		Messages: []conversations.StreamableMessage{
			{Kind: "text", Role: "user", Content: "old prompt"},
			{Kind: "text", Role: "assistant", Content: "old answer"},
		},
	}}
	msg, ok := loadConversationHistoryFromSource(t.Context(), "state-key", source.history.ID, source)().(initialHistoryMsg)

	require.True(t, ok)
	require.NoError(t, msg.err)
	assert.True(t, msg.loaded)
	assert.Equal(t, "/only/on/runner", msg.cwd)
	assert.Equal(t, "state-key", msg.conversationKey)
	assert.Equal(t, "parent-id", msg.parentConversationID)
	assert.Equal(t, "stored", msg.profile)
	assert.Equal(t, "anthropic", msg.provider)
	assert.Equal(t, "high", msg.reasoningEffort)
	assert.Equal(t, 42, msg.usage.CurrentContextWindow)
	require.Len(t, msg.entries, 2)
	assert.Equal(t, "old prompt", msg.entries[0].content)
	assert.Equal(t, "old answer", msg.entries[1].blocks[0].text)
}

func TestConversationSourcesNeverFallBackToLocalStore(t *testing.T) {
	localState := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(localState, []byte("unchanged"), 0o600))
	t.Setenv("KODELET_BASE_PATH", localState)
	history := loadConversationHistoryFromSource(t.Context(), "key", "conversation", nil)().(initialHistoryMsg)
	require.ErrorContains(t, history.err, "conversation history is unavailable")
	assert.False(t, history.loaded)
	list := loadConversationListFromSource(t.Context(), 5, nil)().(conversationListMsg)
	require.ErrorContains(t, list.err, "conversation history is unavailable")
	assert.Equal(t, 5, list.requestID)
	source := &conversationSourceRunner{loadErr: assert.AnError, listErr: assert.AnError}
	history = loadConversationHistoryFromSource(t.Context(), "key", "conversation", source)().(initialHistoryMsg)
	require.ErrorIs(t, history.err, assert.AnError)
	assert.False(t, history.loaded)
	list = loadConversationListFromSource(t.Context(), 6, source)().(conversationListMsg)
	require.ErrorIs(t, list.err, assert.AnError)
	data, err := os.ReadFile(localState)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
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
		loaded:          true,
		entries:         []chatEntry{{kind: entryUser, content: "old prompt"}},
		profile:         "stored",
		reasoningEffort: "max",
	})
	m = updated.(model)

	assert.Equal(t, "stored", m.profile)
	assert.Equal(t, 2, m.profileIndex)
	assert.False(t, m.profilePickerOpen)
	assert.False(t, m.canChangeProfile())
	assert.Equal(t, "max", m.reasoningEffort)
	assert.False(t, m.reasoningPickerOpen)
	assert.False(t, m.canChangeReasoningEffort())
}

func TestInitialHistoryUpdatesDisplayedCWDForEmptyConversation(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", CWD: "/tmp/shell"})
	t.Cleanup(m.cancel)

	updated, _ := m.Update(initialHistoryMsg{loaded: true, cwd: "/tmp/project"})
	m = updated.(model)

	assert.Equal(t, "/tmp/project", m.cwd)
}

func TestInitialHistoryRefreshesCWDAndScopeForActiveRun(t *testing.T) {
	shell := t.TempDir()
	project := t.TempDir()
	projectScope, err := messagehistory.ResolveScopeCWD(project)
	require.NoError(t, err)

	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789", CWD: shell})
	t.Cleanup(m.cancel)
	m.running = true
	m.entries = []chatEntry{{kind: entryUser, content: "local prompt"}}
	m.messageHistory = []string{"local prompt"}
	m.historySearch = &historySearchState{query: "local"}

	updated, _ := m.Update(initialHistoryMsg{
		loaded:  true,
		entries: []chatEntry{{kind: entryUser, content: "old prompt"}},
		cwd:     project,
	})
	m = updated.(model)

	assert.Equal(t, project, m.cwd)
	assert.Equal(t, projectScope, m.messageHistoryScopeCWD)
	assert.Nil(t, m.messageHistory)
	assert.Nil(t, m.historySearch)
	assert.Equal(t, []chatEntry{{kind: entryUser, content: "local prompt"}}, m.entries)
}

func TestFastSubmitBeforeInitialHistoryPersistsToStoredConversationScope(t *testing.T) {
	ctx := context.Background()
	basePath := setupTUIConversationStore(ctx, t)
	shell := t.TempDir()
	project := t.TempDir()

	store, err := conversations.GetConversationStore(ctx)
	require.NoError(t, err)
	record := convtypes.NewConversationRecord("conversation-123456789")
	record.CWD = project
	require.NoError(t, store.Save(ctx, record))
	require.NoError(t, store.Close())

	runner := &recordingRunner{conversationID: record.ID}
	m := newModel(ctx, Config{ConversationID: record.ID, CWD: shell, Runner: runner})
	t.Cleanup(m.cancel)
	assert.True(t, m.initialHistoryPending)
	assert.Empty(t, m.messageHistoryScopeCWD)

	m.textarea.SetValue(" fast prompt ")
	cmd := m.submit()
	require.NotNil(t, cmd)
	_ = cmd()

	historyStore := messagehistory.NewStoreWithBasePath(basePath)
	projectEntries, err := historyStore.List(ctx, project, messagehistory.MaxEntriesPerScope)
	require.NoError(t, err)
	require.Len(t, projectEntries, 1)
	assert.Equal(t, "fast prompt", projectEntries[0].Text)

	shellEntries, err := historyStore.List(ctx, shell, messagehistory.MaxEntriesPerScope)
	require.NoError(t, err)
	assert.Empty(t, shellEntries)
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
