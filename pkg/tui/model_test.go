package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/stretchr/testify/assert"
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

var _ tea.Model = model{}
