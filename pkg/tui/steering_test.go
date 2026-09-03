package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunningComposerOffersSlashCommands(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.slashCommands = []slashcommands.Command{{Name: "review", Description: "Review changes"}}
	m.textarea.SetValue("/rev")

	assert.True(t, m.slashCommandSuggestionsOpen())

	updated, cmd := m.Update(keyPress(tea.KeyTab))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "/review ", m.textarea.Value())
}

func TestRunningComposerQueuesSlashCommandAsFollowUp(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("/goal finish the review")

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "command queued", m.status)
	assert.Empty(t, m.textarea.Value())
	assert.Empty(t, m.queuedSteering)
	assert.Equal(t, []string{"/goal finish the review"}, m.queuedFollowUps)
	content, _ := m.renderTranscript()
	assert.Contains(t, content, "queued command")
	assert.Contains(t, content, "/goal finish the review")
}

func TestQueuedSlashCommandStartsAfterRunAndPreservesDraft(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	require.NotNil(t, m.startConversationRun(m.conversationState, "first request"))
	firstRunID := m.activeRunID
	m.queuedFollowUps = []string{"/goal finish the review"}
	m.textarea.SetValue("draft typed while waiting")

	updated, cmd := m.Update(chatDoneMsg{runID: firstRunID, conversationKey: m.key, conversationID: m.conversationID})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.True(t, m.running)
	assert.Greater(t, m.activeRunID, firstRunID)
	assert.Empty(t, m.queuedFollowUps)
	assert.Equal(t, "draft typed while waiting", m.textarea.Value())
	require.Len(t, m.entries, 2)
	assert.Equal(t, "Objective: finish the review", m.entries[1].content)
}

func TestBackgroundQueuedSlashCommandPreservesBothDrafts(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}})
	t.Cleanup(m.cancel)
	background := m.conversationState
	require.NotNil(t, m.startConversationRun(background, "first request"))
	backgroundRunID := background.activeRunID
	background.queuedFollowUps = []string{"/goal finish the review"}
	background.slashDismissedDraft = "background slash state"
	m.textarea.SetValue("background draft")

	active := newConversationState("conversation-active", "conversation-active", true, m.conversationDefaults)
	m.conversations[active.key] = active
	requireConversationActivation(t, &m, active.key)
	m.textarea.SetValue("active draft")

	updated, cmd := m.Update(chatDoneMsg{runID: backgroundRunID, conversationKey: background.key, conversationID: background.conversationID})
	m = updated.(model)

	require.NotNil(t, cmd)
	assert.Same(t, active, m.conversationState)
	assert.Equal(t, "active draft", m.textarea.Value())
	assert.Equal(t, "background draft", background.draft)
	assert.Equal(t, "background slash state", background.slashDismissedDraft)
	assert.True(t, background.running)
}

func TestRunningComposerQueuesSteering(t *testing.T) {
	homeDir := setupTUIConversationStore(context.Background(), t)
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

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "steering queued", m.status)
	assert.Empty(t, m.textarea.Value())
	assert.Equal(t, []string{"please focus on tests"}, m.queuedSteering)
	content, _ := m.renderTranscript()
	assert.Contains(t, content, "queued steering")
	assert.Contains(t, content, "please focus on tests")

	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	pending, err := steerStore.Peek(context.Background(), "conversation-123456789")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "please focus on tests", pending[0].Content)
}

func TestRunningComposerGeneratesConversationBeforeQueueingSteering(t *testing.T) {
	homeDir := setupTUIConversationStore(context.Background(), t)
	t.Setenv("HOME", homeDir)

	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("new chat steering")

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)

	assert.Nil(t, cmd)
	require.NotEmpty(t, m.conversationID)
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	pending, err := steerStore.Peek(context.Background(), m.conversationID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "new chat steering", pending[0].Content)
}

func TestRunningComposerRejectsOverlongSteering(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue(strings.Repeat("x", steer.MaxMessageLength+1))

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.Equal(t, "steering failed", m.status)
	assert.ErrorContains(t, m.err, "steering message too long")
	assert.Contains(t, m.steerError, "less than 10,000 characters")
	assert.Empty(t, m.queuedSteering)
	assert.NotEmpty(t, m.textarea.Value())
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

	updated, _ := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "user-message", Content: "please focus on tests"}})
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

	m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: "go on"})

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

	m.applyChatEvent(chat.ChatEvent{Kind: "user-message", Content: "repeat"})

	assert.Empty(t, m.queuedSteering)
	require.Len(t, m.entries, 1)
}
