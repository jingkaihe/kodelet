package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestRunningComposerRejectsOverlongSteering(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue(strings.Repeat("x", steer.MaxMessageLength+1))

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
