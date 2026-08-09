package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.NotNil(t, m.conversationPicker)
	assert.True(t, m.running)
	assert.Equal(t, "draft while running", m.textarea.Value())

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
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

	updated, cmd := m.Update(stringMsg("?CSI[49 51 59 50 117]?"))
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
	m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, itemCount-1, m.conversationPicker.selected)
	m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyDown})
	assert.Zero(t, m.conversationPicker.selected)

	m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("café")})
	assert.Equal(t, "café", m.conversationPicker.query)
	m.conversationPicker.selected = 1
	m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	assert.Equal(t, "caf", m.conversationPicker.query)
	assert.Zero(t, m.conversationPicker.selected)
	m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyCtrlU})
	assert.Empty(t, m.conversationPicker.query)

	previousKey := m.activeConversationKey
	m.conversationPicker.selected = itemCount + 10
	cmd := m.updateConversationPickerKey(tea.KeyMsg{Type: tea.KeyEnter})

	require.NotNil(t, cmd)
	assert.Nil(t, m.conversationPicker)
	assert.NotEqual(t, previousKey, m.activeConversationKey)
	assert.True(t, strings.HasPrefix(m.activeConversationKey, "new:"))
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
	m.conversations[background.key] = background

	updated, cmd := m.Update(extensionLifecycleMsg{
		conversationKey: background.key,
		conversationID:  background.conversationID,
	})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.Same(t, active, m.conversationState)
	assert.Equal(t, "active draft", m.textarea.Value())
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
	assert.NotContains(t, xansi.Strip(m.View()), "Background update")

	m.addUINotification(uiNotification{title: "Global status", message: "visible everywhere"})
	assert.Contains(t, xansi.Strip(m.View()), "Global status")
	requireConversationActivation(t, &m, background.key)
	view := xansi.Strip(m.View())
	assert.Contains(t, view, "Background update")
	assert.Contains(t, view, "Global status")
	requireConversationActivation(t, &m, active.key)
	assert.NotContains(t, xansi.Strip(m.View()), "Background update")
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
