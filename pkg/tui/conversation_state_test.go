package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 160
	m.height = 30
	m.resize()

	assert.Equal(t, conversationPickerMaxWidth, m.conversationPickerDialogWidth())
	assert.Equal(t, "First few…", fitVisiblePrefix("First few characters remain visible", 10))

	line := m.renderConversationPickerItem(conversationPickerItem{
		key:       m.activeConversationKey,
		id:        "20260731abcdef",
		title:     "First few characters should remain visible instead of the suffix",
		cwd:       "/tmp/kodelet",
		updatedAt: time.Date(2026, time.July, 31, 4, 49, 0, 0, time.Local),
		running:   true,
	}, 64)
	assert.Contains(t, line, "First few characters")
	assert.NotRegexp(t, `^…`, line)
	assert.NotContains(t, line, "20260731")
	assert.NotContains(t, line, "current")
	assert.NotContains(t, line, "running")
	assert.True(t, strings.HasPrefix(line, "› "+m.spinnerGlyph()))

	other := m.renderConversationPickerItem(conversationPickerItem{
		title:     "Short title",
		cwd:       "/tmp/workspace",
		updatedAt: time.Date(2026, time.July, 30, 20, 9, 0, 0, time.Local),
	}, 64)
	assert.Equal(t, visibleTextColumn(line, "Jul 31 04:49"), visibleTextColumn(other, "Jul 30 20:09"))
	assert.Equal(t, visibleTextColumn(line, "kodelet"), visibleTextColumn(other, "workspace"))
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
