package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type conversationSourceRunner struct {
	recordingRunner
	summaries []convtypes.ConversationSummary
	history   chat.ConversationHistory
	listErr   error
	loadErr   error
}

func (r *conversationSourceRunner) ListConversations(context.Context, int) ([]convtypes.ConversationSummary, error) {
	return r.summaries, r.listErr
}

func (r *conversationSourceRunner) LoadConversation(context.Context, string) (chat.ConversationHistory, error) {
	return r.history, r.loadErr
}

type conversationStreamCall struct {
	conversationID string
	sink           chat.ChatEventSink
	done           <-chan struct{}
}

type streamingConversationSourceRunner struct {
	conversationSourceRunner
	streamCalls chan conversationStreamCall
}

func newStreamingConversationSourceRunner() *streamingConversationSourceRunner {
	return &streamingConversationSourceRunner{streamCalls: make(chan conversationStreamCall, 4)}
}

func (r *streamingConversationSourceRunner) StreamConversation(ctx context.Context, conversationID string, sink chat.ChatEventSink) error {
	select {
	case r.streamCalls <- conversationStreamCall{conversationID: conversationID, sink: sink, done: ctx.Done()}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestConversationSwitchPreservesDraftAndTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	first := m.conversationState
	first.entries = []chatEntry{{kind: entryUser, content: "first transcript"}}
	m.textarea.SetValue("first draft")

	second := newConversationState("conversation-two", "conversation-two", true, m.conversationDefaults)
	second.loaded = true
	second.draft = "second draft"
	second.entries = []chatEntry{{kind: entryUser, content: "second transcript"}}
	m.conversations[second.key] = second

	requireConversationActivation(t, &m, second.key)
	assert.Equal(t, "first draft", first.draft)
	assert.Equal(t, "second draft", m.textarea.Value())
	assert.Equal(t, "second transcript", m.entries[0].content)

	m.textarea.SetValue("updated second draft")
	requireConversationActivation(t, &m, first.key)
	assert.Equal(t, "updated second draft", second.draft)
	assert.Equal(t, "first draft", m.textarea.Value())
	assert.Equal(t, "first transcript", m.entries[0].content)
}

func TestSessionsSlashCommandOpensPickerWhileRunIsActive(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.slashCommands = withTUIBuiltInSlashCommands(nil)
	m.textarea.SetValue("/sessions")

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotNil(t, m.conversationPicker)
	assert.True(t, m.running)
	assert.Empty(t, m.queuedSteering)
	assert.Empty(t, m.textarea.Value())
}

func TestSessionsShortcutTogglesPickerWhileRunIsActive(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("draft while running")

	updated, cmd := m.Update(keyPressWithMod('l', tea.ModCtrl))
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotNil(t, m.conversationPicker)
	assert.True(t, m.running)
	assert.Equal(t, "draft while running", m.textarea.Value())

	updated, cmd = m.Update(keyPressWithMod('l', tea.ModCtrl))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Nil(t, m.conversationPicker)
	assert.True(t, m.running)
	assert.Equal(t, "draft while running", m.textarea.Value())
}

func TestSessionsShortcutReplacesShortcutsAndHistorySearch(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("original draft")
	m.openHistorySearch()
	m.shortcutsOpen = true

	updated, cmd := m.Update(keyPressWithMod('l', tea.ModCtrl))
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotNil(t, m.conversationPicker)
	assert.False(t, m.shortcutsOpen)
	assert.Nil(t, m.historySearch)
	assert.Equal(t, "original draft", m.textarea.Value())
}

func TestRemoteConversationPickerDoesNotLoadClientLocalSessions(t *testing.T) {
	m := newModel(context.Background(), Config{Remote: true})
	t.Cleanup(m.cancel)
	m.openConversationPicker("")

	require.NotNil(t, m.conversationPicker)
	assert.False(t, m.conversationPicker.loading)
	assert.Empty(t, m.conversationPicker.summaries)
	for _, item := range m.filteredConversationPickerItems() {
		if item.isNew {
			continue
		}
		_, exists := m.conversations[item.key]
		assert.True(t, exists)
	}
}

func TestRemoteConversationPickerLoadsControlPlaneSessions(t *testing.T) {
	runner := &conversationSourceRunner{summaries: []convtypes.ConversationSummary{{
		ID:           "conversation-remote",
		FirstMessage: "Remote conversation",
		CWD:          "/Users/jingkaihe/Workspace/kodelet",
		UpdatedAt:    time.Now(),
		IsRunning:    true,
	}}}
	m := newModel(context.Background(), Config{Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.openConversationPicker("")

	require.NotNil(t, m.conversationPicker)
	assert.True(t, m.conversationPicker.loading)
	msg, ok := loadConversationListFromSource(m.ctx, m.conversationPicker.requestID, m.conversationSource)().(conversationListMsg)
	require.True(t, ok)
	m.applyConversationList(msg)

	assert.False(t, m.conversationPicker.loading)
	items := m.filteredConversationPickerItems()
	assert.True(t, items[0].isNew)
	assert.Contains(t, itemConversationIDs(items), "conversation-remote")
	for _, item := range items {
		if item.id == "conversation-remote" {
			assert.True(t, item.running)
		}
	}
}

func TestRemoteConversationStartsPersistentStreamAfterHistoryLoad(t *testing.T) {
	runner := newStreamingConversationSourceRunner()
	m := newModel(context.Background(), Config{ConversationID: "conversation-remote", Remote: true, Runner: runner})
	t.Cleanup(m.cancel)

	updated, cmd := m.Update(initialHistoryMsg{
		conversationKey: m.activeConversationKey,
		conversationID:  m.conversationID,
		loaded:          true,
		entries:         []chatEntry{{kind: entryUser, content: "earlier prompt"}},
	})
	m = updated.(model)
	require.NotNil(t, cmd)
	require.NotZero(t, m.streamRunID)
	streamRunID := m.streamRunID
	require.NotNil(t, m.runs[streamRunID])
	assert.True(t, m.runs[streamRunID].observed)
	assert.Nil(t, m.uiBrokerState(streamRunID, m.activeConversationKey))
	m.beginObservedConversationRun(m.conversationState, streamRunID)
	assert.Same(t, m.conversationState, m.uiBrokerState(streamRunID, m.activeConversationKey))
	m.running = false
	m.activeRunID = 0
	delete(m.runByState, m.activeConversationKey)

	assert.Nil(t, cmd())

	select {
	case call := <-runner.streamCalls:
		assert.Equal(t, "conversation-remote", call.conversationID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conversation stream")
	}
}

func TestRemoteConversationStreamAppliesWebUITurnsAcrossCompletions(t *testing.T) {
	runner := newStreamingConversationSourceRunner()
	m := newModel(context.Background(), Config{ConversationID: "conversation-remote", Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.loaded = true
	m.entries = []chatEntry{{kind: entryUser, content: "earlier prompt"}}
	startCmd := m.startConversationStream(m.conversationState)
	require.NotNil(t, startCmd)
	assert.Nil(t, startCmd())
	call := <-runner.streamCalls
	streamRunID := m.streamRunID

	applyStreamEvent := func(event chat.ChatEvent) tea.Cmd {
		require.NoError(t, call.sink.Send(event))
		msg, ok := receiveRunMsg(t, m.runCh).(chatEventMsg)
		require.True(t, ok)
		updated, cmd := m.Update(msg)
		m = updated.(model)
		return cmd
	}

	applyStreamEvent(chat.ChatEvent{Kind: "conversation", ConversationID: "conversation-remote"})
	assert.True(t, m.running)
	assert.Equal(t, streamRunID, m.activeRunID)
	assert.Equal(t, 1, m.streamTurn)
	applyStreamEvent(chat.ChatEvent{Kind: "user-message", ConversationID: "conversation-remote", Content: "started in web ui"})
	applyStreamEvent(chat.ChatEvent{Kind: "text-delta", ConversationID: "conversation-remote", Delta: "first answer"})
	applyStreamEvent(chat.ChatEvent{Kind: "done", ConversationID: "conversation-remote"})

	assert.False(t, m.running)
	assert.Zero(t, m.activeRunID)
	assert.Equal(t, streamRunID, m.streamRunID)
	assert.Contains(t, m.runs, streamRunID)
	require.Len(t, m.entries, 3)
	assert.Equal(t, "started in web ui", m.entries[1].content)
	assert.Equal(t, "first answer", m.entries[2].blocks[0].text)

	applyStreamEvent(chat.ChatEvent{Kind: "conversation", ConversationID: "conversation-remote"})
	assert.True(t, m.running)
	assert.Equal(t, 2, m.streamTurn)
	applyStreamEvent(chat.ChatEvent{Kind: "user-message", ConversationID: "conversation-remote", Content: "second web turn"})
	applyStreamEvent(chat.ChatEvent{Kind: "text-delta", ConversationID: "conversation-remote", Delta: "second answer"})

	assert.Equal(t, "second web turn", m.entries[3].content)
	assert.Equal(t, "second answer", m.entries[4].blocks[0].text)
}

func TestRemoteConversationStreamRefreshesCompletedHistoryWithoutOverwritingNewTurn(t *testing.T) {
	runner := newStreamingConversationSourceRunner()
	m := newModel(context.Background(), Config{ConversationID: "conversation-remote", Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.loaded = true
	require.NotNil(t, m.startConversationStream(m.conversationState))
	runID := m.streamRunID
	m.beginObservedConversationRun(m.conversationState, runID)
	turn := m.streamTurn
	_, _ = m.finishObservedConversationRun(m.conversationState, runID, chat.ChatEvent{Kind: "done", ConversationID: m.conversationID})

	canonicalEntries := []chatEntry{
		{kind: entryUser, content: "complete web prompt"},
		{kind: entryAssistant, blocks: []assistantBlock{{kind: blockText, text: "complete web answer"}}},
	}
	updated, _ := m.Update(conversationHistoryRefreshMsg{
		runID: runID,
		turn:  turn,
		history: initialHistoryMsg{
			conversationKey: m.activeConversationKey,
			conversationID:  m.conversationID,
			loaded:          true,
			entries:         canonicalEntries,
		},
	})
	m = updated.(model)
	assert.Equal(t, canonicalEntries, m.entries)

	m.beginObservedConversationRun(m.conversationState, runID)
	updated, _ = m.Update(conversationHistoryRefreshMsg{
		runID: runID,
		turn:  turn,
		history: initialHistoryMsg{
			conversationKey: m.activeConversationKey,
			conversationID:  m.conversationID,
			loaded:          true,
			entries:         []chatEntry{{kind: entryUser, content: "stale"}},
		},
	})
	m = updated.(model)
	assert.Equal(t, canonicalEntries, m.entries)
}

func TestTUIRunPausesAndRecreatesRemoteConversationStream(t *testing.T) {
	runner := newStreamingConversationSourceRunner()
	runner.conversationID = "conversation-remote"
	m := newModel(context.Background(), Config{ConversationID: "conversation-remote", Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.loaded = true
	streamCmd := m.startConversationStream(m.conversationState)
	require.NotNil(t, streamCmd)
	assert.Nil(t, streamCmd())
	firstCall := <-runner.streamCalls
	firstStreamRunID := m.streamRunID

	localRunCmd := m.startConversationRun(m.conversationState, "started in tui")
	require.NotNil(t, localRunCmd)
	localRunID := m.activeRunID
	assert.NotEqual(t, firstStreamRunID, localRunID)
	assert.Zero(t, m.streamRunID)
	assert.NotContains(t, m.runs, firstStreamRunID)
	entryCount := len(m.entries)
	updated, _ := m.Update(chatEventMsg{runID: firstStreamRunID, conversationKey: m.activeConversationKey, event: chat.ChatEvent{Kind: "text-delta", Delta: "stale stream text"}})
	m = updated.(model)
	assert.Len(t, m.entries, entryCount)
	select {
	case <-firstCall.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old conversation stream cancellation")
	}

	updated, _ = m.Update(chatDoneMsg{
		runID:           localRunID,
		conversationKey: m.activeConversationKey,
		conversationID:  m.conversationID,
	})
	m = updated.(model)
	assert.False(t, m.running)
	require.NotZero(t, m.streamRunID)
	assert.NotEqual(t, firstStreamRunID, m.streamRunID)
	require.NotNil(t, m.runs[m.streamRunID])
	assert.True(t, m.runs[m.streamRunID].observed)
	entryCount = len(m.entries)
	updated, _ = m.Update(chatEventMsg{runID: firstStreamRunID, conversationKey: m.activeConversationKey, event: chat.ChatEvent{Kind: "text-delta", Delta: "late stale stream text"}})
	m = updated.(model)
	assert.Len(t, m.entries, entryCount)
}

func TestRemoteConversationHistoryLoadsFromControlPlaneSource(t *testing.T) {
	updatedAt := time.Now()
	runner := &conversationSourceRunner{history: chat.ConversationHistory{
		ID:              "conversation-remote",
		CWD:             "/Users/jingkaihe/Workspace/kodelet",
		Title:           "Remote conversation",
		Provider:        "OpenAI",
		Profile:         "deep",
		ReasoningEffort: "max",
		UpdatedAt:       updatedAt,
		Messages: []conversations.StreamableMessage{
			{Kind: "text", Role: "user", Content: "old prompt"},
			{Kind: "text", Role: "assistant", Content: "old answer"},
		},
	}}

	msg, ok := loadConversationHistoryFromSource(t.Context(), "conversation-remote", "conversation-remote", "", runner)().(initialHistoryMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	assert.True(t, msg.loaded)
	assert.Equal(t, runner.history.CWD, msg.cwd)
	assert.Equal(t, "deep", msg.profile)
	assert.Equal(t, "max", msg.reasoningEffort)
	assert.Equal(t, updatedAt, msg.updatedAt)
	require.Len(t, msg.entries, 2)
	assert.Equal(t, "old prompt", msg.entries[0].content)
	assert.Equal(t, "old answer", msg.entries[1].blocks[0].text)
}

func TestRemoteHistoryQueuesSubmitWithoutLocalExtensionLifecycle(t *testing.T) {
	runner := &conversationSourceRunner{}
	runner.conversationID = "conversation-remote"
	m := newModel(context.Background(), Config{ConversationID: "conversation-remote", Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.submitAfterHistoryLoad = "continue remotely"

	updated, cmd := m.Update(initialHistoryMsg{
		conversationKey: m.activeConversationKey,
		conversationID:  "conversation-remote",
		loaded:          true,
		cwd:             "/Users/jingkaihe/Workspace/kodelet",
		entries:         []chatEntry{{kind: entryUser, content: "old prompt"}},
	})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.False(t, m.extensionLifecyclePending)
	assert.True(t, m.running)

	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				_ = child()
			}
		}
	}
	_, ok := receiveRunMsg(t, m.runCh).(chatEventMsg)
	require.True(t, ok)
	assert.Equal(t, "continue remotely", runner.req.Message)
	assert.Equal(t, "conversation-remote", runner.req.ConversationID)
}

func itemConversationIDs(items []conversationPickerItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.id != "" {
			ids = append(ids, item.id)
		}
	}
	return ids
}

func TestConversationPickerFiltersAndLoadsPersistedConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.conversationPicker = &conversationPickerState{
		query: "persisted project",
		summaries: []convtypes.ConversationSummary{{
			ID:           "conversation-persisted",
			FirstMessage: "Persisted project discussion",
			CWD:          "/tmp/project",
			UpdatedAt:    time.Now(),
		}},
	}

	items := m.filteredConversationPickerItems()
	require.Len(t, items, 1)
	assert.Equal(t, "conversation-persisted", items[0].id)

	cmd := m.selectConversationPickerItem()
	require.NotNil(t, cmd)
	assert.Nil(t, m.conversationPicker)
	assert.Equal(t, "conversation-persisted", m.activeConversationKey)
	assert.Equal(t, "conversation-persisted", m.conversationID)
	assert.True(t, m.initialHistoryPending)
	assert.Equal(t, "Persisted project discussion", m.title)
}

func TestConversationPickerIgnoresResultsFromEarlierOpening(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)

	m.openConversationPicker("")
	firstRequestID := m.conversationPicker.requestID
	m.conversationPicker = nil
	m.openConversationPicker("")
	secondRequestID := m.conversationPicker.requestID
	require.Greater(t, secondRequestID, firstRequestID)

	m.applyConversationList(conversationListMsg{
		requestID: firstRequestID,
		summaries: []convtypes.ConversationSummary{{ID: "stale"}},
	})

	assert.True(t, m.conversationPicker.loading)
	assert.Empty(t, m.conversationPicker.summaries)
}

func TestConversationPickerKeepsSelectedConversationAcrossAsyncReorder(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	now := time.Now()
	m.conversationPicker = &conversationPickerState{
		summaries: []convtypes.ConversationSummary{
			{ID: "conversation-a", FirstMessage: "A", UpdatedAt: now},
			{ID: "conversation-b", FirstMessage: "B", UpdatedAt: now.Add(-time.Hour)},
		},
		selected:  2,
		requestID: 7,
	}
	m.clampConversationPickerSelection()
	assert.Equal(t, "conversation:conversation-b", m.conversationPicker.selectedKey)

	m.applyConversationList(conversationListMsg{
		requestID: 7,
		summaries: []convtypes.ConversationSummary{
			{ID: "conversation-a", FirstMessage: "A", UpdatedAt: now.Add(-time.Hour)},
			{ID: "conversation-b", FirstMessage: "B", UpdatedAt: now.Add(time.Hour)},
		},
	})

	items := m.filteredConversationPickerItems()
	selected := m.conversationPickerSelectedIndex(items)
	assert.Equal(t, "conversation-b", items[selected].id)
	assert.Equal(t, 1, selected)
}

func TestPickerSelectedConversationQueuesSubmitUntilHistoryLoads(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	m.conversationPicker = &conversationPickerState{
		query: "persisted",
		summaries: []convtypes.ConversationSummary{{
			ID:           "conversation-persisted",
			FirstMessage: "Persisted conversation",
			CWD:          t.TempDir(),
		}},
	}
	require.NotNil(t, m.selectConversationPickerItem())
	state := m.conversationState
	require.True(t, state.initialHistoryPending)
	require.True(t, state.deferSubmitUntilHistory)

	m.textarea.SetValue("continue after history")
	assert.Nil(t, m.submit())
	assert.False(t, state.running)
	assert.Empty(t, state.entries)
	assert.Equal(t, "continue after history", state.submitAfterHistoryLoad)

	updated, _ := m.Update(initialHistoryMsg{
		conversationKey: state.key,
		conversationID:  state.conversationID,
		loaded:          true,
		cwd:             state.cwd,
		entries:         []chatEntry{{kind: entryUser, content: "earlier prompt"}},
	})
	m = updated.(model)
	assert.Equal(t, "continue after history", state.submitAfterExtensionLifecycle)
	require.Len(t, state.entries, 1)
	assert.Equal(t, "earlier prompt", state.entries[0].content)

	updated, cmd := m.Update(extensionLifecycleMsg{conversationKey: state.key, conversationID: state.conversationID})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.True(t, state.running)
	require.Len(t, state.entries, 2)
	assert.Equal(t, "continue after history", state.entries[1].content)
}

func TestPickerSelectedConversationUsesStoredWorkspaceAndReloadsMessageHistoryScope(t *testing.T) {
	explicitCWD := t.TempDir()
	storedCWD := t.TempDir()
	m := newModel(context.Background(), Config{CWD: explicitCWD})
	t.Cleanup(m.cancel)
	m.conversationPicker = &conversationPickerState{
		query: "persisted",
		summaries: []convtypes.ConversationSummary{{
			ID:           "conversation-persisted",
			FirstMessage: "Persisted conversation",
			CWD:          storedCWD,
		}},
	}
	require.NotNil(t, m.selectConversationPickerItem())
	state := m.conversationState
	assert.Empty(t, state.requestedCWD)
	assert.Equal(t, storedCWD, state.cwd)
	assert.Empty(t, state.messageHistoryScopeCWD)

	updated, _ := m.Update(initialHistoryMsg{
		conversationKey: state.key,
		conversationID:  state.conversationID,
		loaded:          true,
		cwd:             storedCWD,
	})
	m = updated.(model)
	wantScope, err := messagehistory.ResolveScopeCWD(storedCWD)
	require.NoError(t, err)
	assert.Equal(t, wantScope, state.messageHistoryScopeCWD)
}

func TestConversationPickerConsumesShiftEnter(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("draft")
	m.conversationPicker = &conversationPickerState{}

	updated, cmd := m.Update(keyPressWithMod(tea.KeyEnter, tea.ModShift))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "draft", m.textarea.Value())
	require.NotNil(t, m.conversationPicker)
}

func TestConversationPickerUsesWideDialogAndKeepsTitlePrefix(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	now := time.Date(2026, time.July, 31, 6, 49, 0, 0, time.UTC)
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 200
	m.height = 30
	m.resize()

	assert.Equal(t, m.contentWidth()*conversationPickerWidthPercent/100, m.conversationPickerDialogWidth())
	assert.Greater(t, m.conversationPickerDialogWidth(), conversationPickerPreferredMinWidth)
	assert.Equal(t, "First few…", fitVisiblePrefix("First few characters remain visible", 10))

	line := m.renderConversationPickerItemAt(conversationPickerItem{
		key:       m.activeConversationKey,
		id:        "20260731abcdef",
		title:     "First few characters should remain visible instead of the suffix",
		cwd:       filepath.Join(homeDir, "workspace", "kodelet"),
		updatedAt: now.Add(-2 * time.Hour),
		running:   true,
	}, 96, now)
	assert.Contains(t, line, "First few characters")
	assert.Contains(t, line, "~/workspace/kodelet")
	assert.Contains(t, line, "2h ago")
	assert.NotContains(t, line, homeDir)
	assert.NotRegexp(t, `^…`, line)
	assert.NotContains(t, line, "20260731")
	assert.NotContains(t, line, "current")
	assert.NotContains(t, line, "running")
	assert.True(t, strings.HasPrefix(line, "› "+m.spinnerGlyph()))

	other := m.renderConversationPickerItemAt(conversationPickerItem{
		title:     "Short title",
		cwd:       "/srv/workspaces/project",
		updatedAt: now.Add(-17 * time.Hour),
	}, 96, now)
	assert.Contains(t, other, "/srv/workspaces/project")
	assert.Less(t, visibleTextColumn(line, "First few characters"), visibleTextColumn(line, "~/workspace/kodelet"))
	assert.Equal(t, visibleTextColumn(line, "~/workspace/kodelet"), visibleTextColumn(other, "/srv/workspaces/project"))
	assert.Less(t, visibleTextColumn(line, "~/workspace/kodelet"), visibleTextColumn(line, "2h ago"))
	assert.Equal(t, visibleTextEndColumn(line, "2h ago"), visibleTextEndColumn(other, "17h ago"))
	assert.True(t, strings.HasSuffix(line, "  2h ago"))

	m.width = 90
	m.resize()
	assert.Equal(t, m.contentWidth(), m.conversationPickerDialogWidth())
	assert.Equal(t, "~/already-compact", conversationPickerDisplayCWD("~/already-compact"))
}

func TestConversationPickerRelativeAge(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		updatedAt time.Time
		want      string
	}{
		{name: "missing", want: ""},
		{name: "future", updatedAt: now.Add(time.Minute), want: "now"},
		{name: "seconds", updatedAt: now.Add(-59 * time.Second), want: "now"},
		{name: "minute boundary", updatedAt: now.Add(-time.Minute), want: "1m ago"},
		{name: "minutes", updatedAt: now.Add(-12 * time.Minute), want: "12m ago"},
		{name: "hour boundary", updatedAt: now.Add(-time.Hour), want: "1h ago"},
		{name: "hours", updatedAt: now.Add(-17 * time.Hour), want: "17h ago"},
		{name: "day boundary", updatedAt: now.Add(-24 * time.Hour), want: "1d ago"},
		{name: "days", updatedAt: now.Add(-3 * 24 * time.Hour), want: "3d ago"},
		{name: "week boundary", updatedAt: now.Add(-7 * 24 * time.Hour), want: "1w ago"},
		{name: "weeks", updatedAt: now.Add(-14 * 24 * time.Hour), want: "2w ago"},
		{name: "month boundary", updatedAt: now.Add(-30 * 24 * time.Hour), want: "1mo ago"},
		{name: "months", updatedAt: now.Add(-180 * 24 * time.Hour), want: "6mo ago"},
		{name: "before year boundary", updatedAt: now.Add(-364 * 24 * time.Hour), want: "11mo ago"},
		{name: "year boundary", updatedAt: now.Add(-365 * 24 * time.Hour), want: "1y ago"},
		{name: "years", updatedAt: now.Add(-730 * 24 * time.Hour), want: "2y ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, conversationPickerRelativeAge(tt.updatedAt, now))
		})
	}
}

func TestConversationPickerColumnWidthsPrioritizeWorkspaceOverAge(t *testing.T) {
	titleWidth, workspaceWidth, ageWidth := conversationPickerColumnWidths(35)

	assert.Equal(t, conversationPickerTitleMinimumWidth, titleWidth)
	assert.Equal(t, 9, workspaceWidth)
	assert.Zero(t, ageWidth)

	titleWidth, workspaceWidth, ageWidth = conversationPickerColumnWidths(29)
	assert.Equal(t, 25, titleWidth)
	assert.Zero(t, workspaceWidth)
	assert.Zero(t, ageWidth)
}

func TestConversationPickerKeyboardNavigationEditingAndNewConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.conversationPicker = &conversationPickerState{
		summaries: []convtypes.ConversationSummary{
			{ID: "conversation-one", FirstMessage: "First conversation"},
			{ID: "conversation-two", FirstMessage: "Second conversation"},
		},
	}

	itemCount := len(m.filteredConversationPickerItems())
	require.Greater(t, itemCount, 1)
	m.updateConversationPickerKey(keyPress(tea.KeyUp))
	assert.Equal(t, itemCount-1, m.conversationPicker.selected)
	m.updateConversationPickerKey(keyPress(tea.KeyDown))
	assert.Zero(t, m.conversationPicker.selected)

	m.updateConversationPickerKey(textKeyPress("café"))
	assert.Equal(t, "café", m.conversationPicker.query)
	m.conversationPicker.selected = 1
	m.updateConversationPickerKey(keyPress(tea.KeyBackspace))
	assert.Equal(t, "caf", m.conversationPicker.query)
	assert.Zero(t, m.conversationPicker.selected)
	m.updateConversationPickerKey(keyPressWithMod('u', tea.ModCtrl))
	assert.Empty(t, m.conversationPicker.query)

	previousKey := m.activeConversationKey
	previousCount := len(m.conversations)
	m.conversationPicker.selected = itemCount + 10
	cmd := m.updateConversationPickerKey(keyPress(tea.KeyEnter))

	require.NotNil(t, cmd)
	require.NotNil(t, m.conversationPicker)
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, uiPromptInput, m.activeUIPrompt.mode)
	assert.Equal(t, "Back", m.activeUIPrompt.cancelButtonText)
	assert.Equal(t, previousKey, m.activeConversationKey)
	assert.Len(t, m.conversations, previousCount)

	cmd = m.updateUIPromptKey(keyPress(tea.KeyEnter))

	require.NotNil(t, cmd)
	assert.Nil(t, m.conversationPicker)
	assert.Nil(t, m.activeUIPrompt)
	assert.NotEqual(t, previousKey, m.activeConversationKey)
	assert.True(t, strings.HasPrefix(m.activeConversationKey, "new:"))
}

func TestConversationPickerPasteAlwaysInsertsText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "enter", content: "enter", want: "prefix-enter"},
		{name: "escape", content: "esc", want: "prefix-esc"},
		{name: "up", content: "up", want: "prefix-up"},
		{name: "down", content: "down", want: "prefix-down"},
		{name: "backspace", content: "backspace", want: "prefix-backspace"},
		{name: "clear", content: "ctrl+u", want: "prefix-ctrl+u"},
		{name: "multiline", content: " first\r\nsecond\tthird ", want: "prefix-first second third"},
		{name: "terminal controls", content: " \x1b[31mred\x1b[0m\x07\x00 alert ", want: "prefix-red alert"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
			m.conversationPicker = &conversationPickerState{
				query:       "prefix-",
				selected:    1,
				selectedKey: "selected",
				summaries:   []convtypes.ConversationSummary{{ID: "conversation-one", FirstMessage: "First conversation"}},
			}

			updated, cmd := m.Update(tea.PasteMsg{Content: tt.content})
			m = updated.(model)

			assert.Nil(t, cmd)
			require.NotNil(t, m.conversationPicker)
			assert.Equal(t, tt.want, m.conversationPicker.query)
			assert.Zero(t, m.conversationPicker.selected)
			assert.Empty(t, m.conversationPicker.selectedKey)
			assert.Nil(t, m.activeUIPrompt)
		})
	}
}

func TestConversationPickerKeepsIdenticalUntitledRowsStable(t *testing.T) {
	workspace := t.TempDir()
	m := newModel(context.Background(), Config{CWD: workspace})
	t.Cleanup(m.cancel)
	first := m.conversationState
	second := newConversationState("new:2", "", false, m.conversationDefaults)
	third := newConversationState("new:3", "", false, m.conversationDefaults)
	m.conversations = map[string]*conversationState{
		first.key:  first,
		second.key: second,
		third.key:  third,
	}
	m.activeConversationKey = third.key
	m.conversationState = third
	m.conversationPicker = &conversationPickerState{query: workspace}
	wantKeys := []string{first.key, second.key, third.key}

	for range 50 {
		items := m.filteredConversationPickerItems()
		keys := make([]string, 0, len(items))
		for _, item := range items {
			keys = append(keys, item.key)
			if item.key == third.key {
				assert.Contains(t, m.conversationPickerItemStatus(item), "›")
			} else {
				assert.NotContains(t, m.conversationPickerItemStatus(item), "›")
			}
		}
		assert.Equal(t, wantKeys, keys)
	}
}

func TestNewConversationPromptDefaultsToActiveCWDAndReturnsToPickerOnCancel(t *testing.T) {
	startupCWD := t.TempDir()
	activeCWD := t.TempDir()
	m := newModel(context.Background(), Config{CWD: startupCWD})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.cwd = activeCWD
	m.requestedCWD = ""
	m.conversationPicker = &conversationPickerState{}
	previousKey := m.activeConversationKey

	cmd := m.openNewConversationPrompt("")

	require.NotNil(t, cmd)
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, displayCWD(activeCWD), m.activeUIPrompt.input.Value())
	assert.Equal(t, activeCWD, m.activeUIPrompt.newConversationCWDBase)
	assert.Equal(t, "Back", m.activeUIPrompt.cancelButtonText)
	assert.Contains(t, xansi.Strip(m.renderUIDialog()), "Working directory")

	m.dismissUIPrompt()

	assert.Nil(t, m.activeUIPrompt)
	require.NotNil(t, m.conversationPicker)
	assert.Equal(t, previousKey, m.activeConversationKey)

	m.openNewConversationPrompt("")
	cmd = m.submitUIPrompt()

	require.NotNil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Nil(t, m.conversationPicker)
	assert.NotEqual(t, previousKey, m.activeConversationKey)
	expectedCWD, err := conversations.NormalizeCWD(activeCWD)
	require.NoError(t, err)
	assert.Equal(t, expectedCWD, m.cwd)
	assert.Equal(t, expectedCWD, m.requestedCWD)
	assert.Equal(t, startupCWD, m.conversationDefaults.cwd)
	assert.Equal(t, startupCWD, m.conversationDefaults.requestedCWD)
}

func TestNewSlashCommandPrefillsAndCreatesConversationInRelativeCWD(t *testing.T) {
	currentCWD := t.TempDir()
	targetCWD := t.TempDir()
	relativeCWD, err := filepath.Rel(currentCWD, targetCWD)
	require.NoError(t, err)
	m := newModel(context.Background(), Config{CWD: currentCWD})
	t.Cleanup(m.cancel)
	previousKey := m.activeConversationKey
	m.textarea.SetValue("/new " + relativeCWD)

	cmd := m.submit()

	require.NotNil(t, cmd)
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, relativeCWD, m.activeUIPrompt.input.Value())
	assert.Equal(t, currentCWD, m.activeUIPrompt.newConversationCWDBase)
	assert.Equal(t, "Cancel", m.activeUIPrompt.cancelButtonText)
	assert.Equal(t, previousKey, m.activeConversationKey)
	assert.Empty(t, m.textarea.Value())

	cmd = m.submitUIPrompt()

	require.NotNil(t, cmd)
	expectedCWD, err := conversations.NormalizeCWD(targetCWD)
	require.NoError(t, err)
	assert.NotEqual(t, previousKey, m.activeConversationKey)
	assert.Equal(t, expectedCWD, m.cwd)
	assert.Equal(t, expectedCWD, m.requestedCWD)
	assert.Equal(t, currentCWD, m.conversationDefaults.cwd)
	assert.Equal(t, currentCWD, m.conversationDefaults.requestedCWD)
}

func TestNewConversationPromptRejectsInvalidCWDBeforeCreatingState(t *testing.T) {
	baseCWD := t.TempDir()
	missingCWD := filepath.Join(baseCWD, "missing-workspace")
	m := newModel(context.Background(), Config{CWD: baseCWD})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.openNewConversationPrompt(missingCWD)
	previousKey := m.activeConversationKey
	previousCount := len(m.conversations)
	previousNextKey := m.nextConversationKey

	cmd := m.submitUIPrompt()

	require.NotNil(t, cmd)
	require.NotNil(t, m.activeUIPrompt)
	require.Len(t, m.uiNotifications, 1)
	notification := m.uiNotifications[0]
	assert.Equal(t, uiNotificationError, notification.level)
	assert.Equal(t, "Working directory unavailable", notification.title)
	assert.Equal(t, "Directory does not exist: "+missingCWD, notification.message)
	assert.NotContains(t, xansi.Strip(m.renderUIDialog()), "Directory does not exist")
	assert.Equal(t, previousKey, m.activeConversationKey)
	assert.Len(t, m.conversations, previousCount)
	assert.Equal(t, previousNextKey, m.nextConversationKey)
}

func TestNewConversationCWDNotificationRendersAbovePickerAndDialog(t *testing.T) {
	baseCWD := t.TempDir()
	missingCWD := filepath.Join(baseCWD, "missing-workspace")
	m := newModel(context.Background(), Config{CWD: baseCWD})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 16
	m.resize()
	m.conversationPicker = &conversationPickerState{}
	for index := range 20 {
		m.conversationPicker.summaries = append(m.conversationPicker.summaries, convtypes.ConversationSummary{
			ID:           fmt.Sprintf("conversation-%02d", index),
			FirstMessage: fmt.Sprintf("Conversation %02d", index),
		})
	}
	m.openNewConversationPrompt(missingCWD)

	require.NotNil(t, m.submitUIPrompt())
	view := xansi.Strip(m.View().Content)

	assert.Contains(t, view, "Working directory unavailable")
	assert.Contains(t, view, "Directory does not exist")
	require.NotNil(t, m.conversationPicker)
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, missingCWD, m.activeUIPrompt.input.Value())
}

func TestRunCompletionKeepsNewConversationPromptOpen(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir(), Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	state := m.conversationState
	require.NotNil(t, m.startConversationRun(state, "background request"))
	runID := state.activeRunID
	targetCWD := t.TempDir()
	m.openNewConversationPrompt("")
	require.NotNil(t, m.activeUIPrompt)
	m.activeUIPrompt.input.SetValue(targetCWD)

	updated, _ := m.Update(chatDoneMsg{
		runID:           runID,
		conversationKey: state.key,
		conversationID:  state.conversationID,
	})
	m = updated.(model)

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, targetCWD, m.activeUIPrompt.input.Value())
	assert.Equal(t, "ready", m.status)
}

func TestNewConversationPromptPreservesCompletedRunStatusOnDismiss(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		cancelling bool
		wantStatus string
	}{
		{name: "error", err: errors.New("run failed"), wantStatus: "error"},
		{name: "cancelled", cancelling: true, wantStatus: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{CWD: t.TempDir(), Runner: &recordingRunner{}})
			t.Cleanup(m.cancel)
			state := m.conversationState
			require.NotNil(t, m.startConversationRun(state, "background request"))
			runID := state.activeRunID
			state.runCancelling = test.cancelling
			m.openNewConversationPrompt("")

			updated, _ := m.Update(chatDoneMsg{
				runID:           runID,
				conversationKey: state.key,
				conversationID:  state.conversationID,
				err:             test.err,
			})
			m = updated.(model)

			require.NotNil(t, m.activeUIPrompt)
			assert.Equal(t, test.wantStatus, m.status)
			m.dismissUIPrompt()
			assert.Nil(t, m.activeUIPrompt)
			assert.Equal(t, test.wantStatus, m.status)
		})
	}
}

func TestExtensionPromptTemporarilySuspendsNewConversationPrompt(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir(), Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	state := m.conversationState
	require.NotNil(t, m.startConversationRun(state, "background request"))
	runID := state.activeRunID
	targetCWD := t.TempDir()
	m.openNewConversationPrompt("")
	require.NotNil(t, m.activeUIPrompt)
	m.activeUIPrompt.input.SetValue(targetCWD)
	responses := make(chan extensions.UIInputResponse, 1)

	updated, _ := m.Update(uiPromptRequestMsg{
		runID:           runID,
		conversationKey: state.key,
		prompt: uiPromptState{
			mode:     uiPromptConfirm,
			title:    "Approve extension action",
			response: responses,
		},
	})
	m = updated.(model)

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptExtension, m.activeUIPrompt.origin)
	assert.Equal(t, "Approve extension action", m.activeUIPrompt.title)
	require.NotNil(t, m.suspendedUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.suspendedUIPrompt.origin)
	assert.Equal(t, targetCWD, m.suspendedUIPrompt.input.Value())

	require.NotNil(t, m.submitUIPrompt())
	require.NotNil(t, m.activeUIPrompt)
	assert.Nil(t, m.suspendedUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, targetCWD, m.activeUIPrompt.input.Value())
	assert.Equal(t, "working", m.status)
	select {
	case response := <-responses:
		assert.Equal(t, extensions.UIInputStatusSubmitted, response.Status)
		assert.True(t, response.Confirmed)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for extension prompt response")
	}
}

func TestInitialHistoryRefreshesNewConversationPromptCWD(t *testing.T) {
	shellCWD := t.TempDir()
	projectCWD := t.TempDir()
	m := newModel(context.Background(), Config{ConversationID: "conversation-resumed", CWD: shellCWD})
	t.Cleanup(m.cancel)
	m.openNewConversationPrompt("")
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, displayCWD(shellCWD), m.activeUIPrompt.input.Value())

	updated, _ := m.Update(initialHistoryMsg{loaded: true, cwd: projectCWD})
	m = updated.(model)

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, displayCWD(projectCWD), m.activeUIPrompt.input.Value())
	assert.Equal(t, displayCWD(projectCWD), m.activeUIPrompt.defaultValue)
	assert.Equal(t, projectCWD, m.activeUIPrompt.newConversationCWDBase)
}

func TestInitialHistoryRefreshPreservesEditedNewConversationPath(t *testing.T) {
	shellCWD := t.TempDir()
	projectCWD := t.TempDir()
	targetCWD := filepath.Join(projectCWD, "nested")
	require.NoError(t, os.Mkdir(targetCWD, 0o755))
	m := newModel(context.Background(), Config{ConversationID: "conversation-resumed", CWD: shellCWD})
	t.Cleanup(m.cancel)
	m.openNewConversationPrompt("")
	require.NotNil(t, m.activeUIPrompt)
	m.activeUIPrompt.input.SetValue("nested")

	updated, _ := m.Update(initialHistoryMsg{loaded: true, cwd: projectCWD})
	m = updated.(model)

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, "nested", m.activeUIPrompt.input.Value())
	assert.Equal(t, projectCWD, m.activeUIPrompt.newConversationCWDBase)
	require.NotNil(t, m.submitUIPrompt())
	expectedCWD, err := conversations.NormalizeCWD(targetCWD)
	require.NoError(t, err)
	assert.Equal(t, expectedCWD, m.cwd)
}

func TestInitialHistoryRefreshesSuspendedNewConversationPromptCWD(t *testing.T) {
	shellCWD := t.TempDir()
	projectCWD := t.TempDir()
	targetCWD := filepath.Join(projectCWD, "nested")
	require.NoError(t, os.Mkdir(targetCWD, 0o755))
	m := newModel(context.Background(), Config{
		ConversationID: "conversation-resumed",
		CWD:            shellCWD,
		Runner:         &recordingRunner{},
	})
	t.Cleanup(m.cancel)
	state := m.conversationState
	require.NotNil(t, m.startConversationRun(state, "fast request"))
	runID := state.activeRunID
	m.openNewConversationPrompt("")
	require.NotNil(t, m.activeUIPrompt)
	m.activeUIPrompt.input.SetValue("nested")
	responses := make(chan extensions.UIInputResponse, 1)

	updated, _ := m.Update(uiPromptRequestMsg{
		runID:           runID,
		conversationKey: state.key,
		prompt: uiPromptState{
			mode:     uiPromptConfirm,
			title:    "Approve extension action",
			response: responses,
		},
	})
	m = updated.(model)
	require.NotNil(t, m.suspendedUIPrompt)

	updated, _ = m.Update(initialHistoryMsg{loaded: true, cwd: projectCWD})
	m = updated.(model)

	require.NotNil(t, m.suspendedUIPrompt)
	assert.Equal(t, "nested", m.suspendedUIPrompt.input.Value())
	assert.Equal(t, projectCWD, m.suspendedUIPrompt.newConversationCWDBase)
	require.NotNil(t, m.submitUIPrompt())
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	require.NotNil(t, m.submitUIPrompt())
	expectedCWD, err := conversations.NormalizeCWD(targetCWD)
	require.NoError(t, err)
	assert.Equal(t, expectedCWD, m.cwd)
	select {
	case response := <-responses:
		assert.Equal(t, extensions.UIInputStatusSubmitted, response.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for extension prompt response")
	}
}

func TestRemoteNewConversationPromptUsesEnvironmentDefault(t *testing.T) {
	m := newModel(context.Background(), Config{
		DefaultCWD: "~/runner/kodelet",
		Runner:     &recordingRunner{},
		Remote:     true,
	})
	t.Cleanup(m.cancel)
	m.conversationPicker = &conversationPickerState{}
	previousKey := m.activeConversationKey

	m.openNewConversationPrompt("")

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, uiPromptInput, m.activeUIPrompt.mode)
	assert.Equal(t, "Working directory", m.activeUIPrompt.message)
	assert.Contains(t, m.activeUIPrompt.helpText, "~/runner/kodelet")
	assert.Empty(t, m.activeUIPrompt.input.Value())
	assert.Equal(t, "Back", m.activeUIPrompt.cancelButtonText)
	require.NotNil(t, m.conversationPicker)

	cmd := m.submitUIPrompt()

	require.NotNil(t, cmd)
	assert.Nil(t, m.activeUIPrompt)
	assert.Nil(t, m.conversationPicker)
	assert.NotEqual(t, previousKey, m.activeConversationKey)
	assert.Equal(t, "~/runner/kodelet", m.cwd)
	assert.Empty(t, m.requestedCWD)
}

func TestRemoteNewConversationPromptAcceptsCWDOverride(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}, Remote: true})
	t.Cleanup(m.cancel)
	m.conversationDefaults.cwd = ""

	m.openNewConversationPrompt("/tmp/client-workspace")

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptNewConversation, m.activeUIPrompt.origin)
	assert.Equal(t, uiPromptInput, m.activeUIPrompt.mode)
	assert.Equal(t, "/tmp/client-workspace", m.activeUIPrompt.input.Value())
	assert.Contains(t, m.activeUIPrompt.helpText, "execution host")

	cmd := m.submitUIPrompt()
	require.NotNil(t, cmd)
	assert.Equal(t, "/tmp/client-workspace", m.cwd)
	assert.Equal(t, "/tmp/client-workspace", m.requestedCWD)
}

func TestConversationPickerRendersLoadingErrorAndEmptyState(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 20
	m.resize()
	m.conversationPicker = &conversationPickerState{
		query:   "does-not-match-any-conversation",
		loading: true,
		err:     errors.New("conversation store unavailable"),
	}

	rendered := xansi.Strip(m.renderConversationPicker())

	assert.Contains(t, rendered, "Conversations")
	assert.Contains(t, rendered, "Loading saved conversations")
	assert.Contains(t, rendered, "conversation store unavailable")
	assert.Contains(t, rendered, "No matching conversations")
}

func TestConversationPickerHeightBudgetIncludesStatusAndOverflowRows(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 10
	m.resize()
	m.conversationPicker = &conversationPickerState{
		loading: true,
		err:     errors.New("conversation store unavailable"),
	}
	for index := range 12 {
		m.conversationPicker.summaries = append(m.conversationPicker.summaries, convtypes.ConversationSummary{
			ID:           fmt.Sprintf("conversation-%02d", index),
			FirstMessage: fmt.Sprintf("Conversation %02d", index),
			UpdatedAt:    time.Now().Add(-time.Duration(index) * time.Minute),
		})
	}
	m.conversationPicker.selected = 8
	m.clampConversationPickerSelection()

	rendered := xansi.Strip(m.renderConversationPicker())
	assert.LessOrEqual(t, len(strings.Split(rendered, "\n")), m.height)
	items := m.filteredConversationPickerItems()
	selected := m.conversationPickerSelectedIndex(items)
	assert.Contains(t, rendered, items[selected].title)
}

func TestParallelConversationRunsRouteEventsAndCompletion(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	first := m.conversationState
	require.NotNil(t, m.startConversationRun(first, "first request"))
	firstKey := first.key
	firstRunID := first.activeRunID

	second := newConversationState("new:2", "", false, m.conversationDefaults)
	m.conversations[second.key] = second
	requireConversationActivation(t, &m, second.key)
	require.NotNil(t, m.startConversationRun(second, "second request"))
	secondKey := second.key
	secondRunID := second.activeRunID

	assert.NotEqual(t, firstRunID, secondRunID)
	assert.Len(t, m.runs, 2)
	assert.True(t, first.running)
	assert.True(t, second.running)
	assert.Nil(t, m.startConversationRun(second, "duplicate turn"))

	updated, _ := m.Update(chatEventMsg{
		runID:           firstRunID,
		conversationKey: firstKey,
		event:           chat.ChatEvent{Kind: "text", Delta: "first response"},
	})
	m = updated.(model)
	assert.True(t, first.unread)
	assert.Contains(t, first.entries[len(first.entries)-1].blocks[0].text, "first response")
	assert.Len(t, second.entries, 1)

	updated, _ = m.Update(chatEventMsg{
		runID:           secondRunID,
		conversationKey: secondKey,
		event:           chat.ChatEvent{Kind: "text", Delta: "second response"},
	})
	m = updated.(model)
	assert.Contains(t, second.entries[len(second.entries)-1].blocks[0].text, "second response")

	updated, _ = m.Update(chatDoneMsg{runID: firstRunID, conversationKey: firstKey, conversationID: first.conversationID})
	m = updated.(model)
	assert.False(t, first.running)
	assert.True(t, second.running)
	assert.NotContains(t, m.runs, firstRunID)
	assert.Contains(t, m.runs, secondRunID)
	assert.Equal(t, secondKey, m.activeConversationKey)
}

func TestBackgroundRunErrorDismissesPromptWithoutChangingActiveConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	active := m.conversationState
	m.textarea.SetValue("active draft")

	responses := make(chan extensions.UIInputResponse, 1)
	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	background.running = true
	background.activeRunID = 7
	background.entries = []chatEntry{{kind: entryUser, content: "background request"}}
	background.activeUIPrompt = &uiPromptState{
		mode:     uiPromptConfirm,
		title:    "Approve background action",
		response: responses,
	}
	m.conversations[background.key] = background
	m.runs[background.activeRunID] = &conversationRun{conversationKey: background.key}
	m.runByState[background.key] = background.activeRunID

	updated, _ := m.Update(chatDoneMsg{
		runID:           background.activeRunID,
		conversationKey: background.key,
		conversationID:  background.conversationID,
		err:             errors.New("background failed"),
	})
	m = updated.(model)

	assert.Same(t, active, m.conversationState)
	assert.Equal(t, "active draft", m.textarea.Value())
	assert.False(t, background.running)
	assert.Equal(t, "error", background.status)
	assert.ErrorContains(t, background.err, "background failed")
	assert.True(t, background.unread)
	assert.Nil(t, background.activeUIPrompt)
	assert.NotContains(t, m.runs, 7)
	require.NotEmpty(t, background.entries)
	lastEntry := background.entries[len(background.entries)-1]
	require.NotEmpty(t, lastEntry.blocks)
	assert.Contains(t, lastEntry.blocks[0].text, "Error: background failed")
	select {
	case response := <-responses:
		assert.Equal(t, extensions.UIInputStatusDismissed, response.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background prompt dismissal")
	}
}

func TestFailedAndCancelledRunsDiscardQueuedSlashFollowUps(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		cancelling bool
	}{
		{name: "error", err: errors.New("run failed")},
		{name: "cancelled", err: context.Canceled, cancelling: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			state := m.conversationState
			state.running = true
			state.runCancelling = test.cancelling
			state.activeRunID = 9
			state.queuedFollowUps = []string{"/goal should not run"}
			m.runs[9] = &conversationRun{conversationKey: state.key}
			m.runByState[state.key] = 9

			updated, _ := m.Update(chatDoneMsg{runID: 9, conversationKey: state.key, conversationID: state.conversationID, err: test.err})
			m = updated.(model)

			assert.False(t, state.running)
			assert.Empty(t, state.queuedFollowUps)
			assert.Empty(t, m.runs)
		})
	}
}

func TestConversationRenameEventUpdatesOpenPickerTitle(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.title = "Old title"
	m.conversationPicker = &conversationPickerState{}

	m.applyChatEvent(chat.ChatEvent{Kind: "conversation", ConversationID: m.conversationID, ConversationName: "New title"})

	assert.Equal(t, "New title", m.title)
	items := m.filteredConversationPickerItems()
	var current conversationPickerItem
	for _, item := range items {
		if item.key == m.activeConversationKey {
			current = item
			break
		}
	}
	assert.Equal(t, "New title", current.title)
}

func TestGeneratedConversationIDKeepsStableStateKey(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	state := m.conversationState
	originalKey := state.key

	require.NotNil(t, m.startConversationRun(state, "first request"))

	assert.NotEmpty(t, state.conversationID)
	assert.Equal(t, originalKey, state.key)
	assert.Equal(t, originalKey, m.activeConversationKey)
	assert.Same(t, state, m.stateForKey(originalKey))
	assert.Equal(t, state.activeRunID, m.runByState[originalKey])
}

func TestBackgroundLifecycleQueuedSubmitPreservesActiveDraft(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	active := m.conversationState
	m.textarea.SetValue("active draft")

	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	background.extensionLifecyclePending = true
	background.submitAfterExtensionLifecycle = "queued background request"
	background.submitAfterExtensionLifecyclePreserveComposer = true
	background.draft = "background draft"
	m.conversations[background.key] = background

	updated, cmd := m.Update(extensionLifecycleMsg{
		conversationKey: background.key,
		conversationID:  background.conversationID,
	})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.Same(t, active, m.conversationState)
	assert.Equal(t, "active draft", m.textarea.Value())
	assert.Equal(t, "background draft", background.draft)
	assert.True(t, background.running)
	assert.Equal(t, "queued background request", background.entries[0].content)
}

func TestBackgroundConversationPromptWaitsForThatConversation(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	first := m.conversationState
	require.NotNil(t, m.startConversationRun(first, "first request"))
	firstRunID := first.activeRunID

	second := newConversationState("conversation-two", "conversation-two", true, m.conversationDefaults)
	m.conversations[second.key] = second
	requireConversationActivation(t, &m, second.key)

	responses := make(chan extensions.UIInputResponse, 1)
	updated, cmd := m.Update(uiPromptRequestMsg{
		runID: firstRunID,
		prompt: uiPromptState{
			mode:     uiPromptConfirm,
			title:    "Approve background action",
			response: responses,
		},
	})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotNil(t, first.activeUIPrompt)
	assert.Nil(t, second.activeUIPrompt)
	assert.True(t, first.unread)
	require.NotEmpty(t, m.uiNotifications)
	assert.Equal(t, "Conversation needs input", m.uiNotifications[len(m.uiNotifications)-1].title)

	requireConversationActivation(t, &m, first.key)
	m.submitUIPrompt()
	select {
	case response := <-responses:
		assert.Equal(t, extensions.UIInputStatusSubmitted, response.Status)
		assert.True(t, response.Confirmed)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt response")
	}
}

func TestBackgroundLifecyclePromptRoutesByConversationKey(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	active := m.conversationState
	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	background.extensionLifecyclePending = true
	m.conversations[background.key] = background
	responses := make(chan extensions.UIInputResponse, 1)

	updated, _ := m.Update(uiPromptRequestMsg{
		runID:           0,
		conversationKey: background.key,
		prompt: uiPromptState{
			mode:     uiPromptConfirm,
			title:    "Lifecycle approval",
			response: responses,
		},
	})
	m = updated.(model)

	assert.Nil(t, active.activeUIPrompt)
	require.NotNil(t, background.activeUIPrompt)
	assert.Equal(t, "Lifecycle approval", background.activeUIPrompt.title)
	m.resolveUIPromptForState(background, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
}

func TestExtensionTranscriptRoutesToBackgroundConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	active := m.conversationState
	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	m.conversations[background.key] = background

	updated, _ := m.Update(extensionUITranscriptMsg{
		conversationKey: background.key,
		title:           "Progress",
		message:         "background update",
	})
	m = updated.(model)

	assert.Empty(t, active.entries)
	require.Len(t, background.entries, 1)
	assert.Equal(t, entryInfo, background.entries[0].kind)
	assert.Equal(t, "background update", background.entries[0].content)
	assert.True(t, background.unread)
}

func TestExtensionNotificationStaysWithOriginatingConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 30
	m.resize()
	active := m.conversationState
	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	background.running = true
	background.activeRunID = 2
	m.conversations[background.key] = background
	m.runs[2] = &conversationRun{conversationKey: background.key}

	updated, _ := m.Update(uiNotificationMsg{
		runID:           2,
		conversationKey: background.key,
		notification:    uiNotification{title: "Background update", message: "still working"},
		response:        make(chan extensions.UIInputResponse, 1),
	})
	m = updated.(model)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, background.key, m.uiNotifications[0].conversationKey)
	assert.NotContains(t, xansi.Strip(m.View().Content), "Background update")

	m.addUINotification(uiNotification{title: "Global status", message: "visible everywhere"})
	assert.Contains(t, xansi.Strip(m.View().Content), "Global status")
	requireConversationActivation(t, &m, background.key)
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "Background update")
	assert.Contains(t, view, "Global status")
	requireConversationActivation(t, &m, active.key)
	assert.NotContains(t, xansi.Strip(m.View().Content), "Background update")
}

func TestCancelActiveConversationDoesNotCancelBackgroundRun(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	first := m.conversationState
	first.running = true
	first.activeRunID = 1
	firstCancelled := false
	first.cancelRun = func() { firstCancelled = true }

	second := newConversationState("conversation-two", "conversation-two", true, m.conversationDefaults)
	second.running = true
	second.activeRunID = 2
	secondCancelled := false
	second.cancelRun = func() { secondCancelled = true }
	m.conversations[second.key] = second
	requireConversationActivation(t, &m, second.key)

	m.cancelActiveRun()

	assert.False(t, firstCancelled)
	assert.True(t, first.running)
	assert.True(t, secondCancelled)
	assert.True(t, second.runCancelling)
}

func TestKeyedHistoryLoadDoesNotReplaceActiveConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	active := m.conversationState
	active.entries = []chatEntry{{kind: entryUser, content: "active transcript"}}

	background := newConversationState("conversation-background", "conversation-background", true, m.conversationDefaults)
	background.initialHistoryPending = true
	m.conversations[background.key] = background

	updated, _ := m.Update(initialHistoryMsg{
		conversationKey: background.key,
		conversationID:  background.conversationID,
		loaded:          true,
		entries:         []chatEntry{{kind: entryUser, content: "background transcript"}},
		title:           "Background work",
	})
	m = updated.(model)

	assert.Same(t, active, m.conversationState)
	assert.Equal(t, "active transcript", m.entries[0].content)
	assert.True(t, background.loaded)
	assert.Equal(t, "background transcript", background.entries[0].content)
	assert.Equal(t, "Background work", background.title)
}

func requireConversationActivation(t *testing.T, m *model, key string) {
	t.Helper()
	activated, _ := m.activateConversation(key)
	require.True(t, activated)
}

func visibleTextColumn(line, target string) int {
	index := strings.Index(line, target)
	if index < 0 {
		return -1
	}
	return lipgloss.Width(line[:index])
}

func visibleTextEndColumn(line, target string) int {
	start := visibleTextColumn(line, target)
	if start < 0 {
		return -1
	}
	return start + lipgloss.Width(target)
}
