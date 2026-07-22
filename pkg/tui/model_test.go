package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stringMsg string

func (m stringMsg) String() string {
	return string(m)
}

func numberedLines(count int) string {
	return strings.TrimRight(strings.Repeat("line\n", count), "\n")
}

var _ tea.Model = model{}

func TestNewModelSharesPersistentExtensionRuntimeManagerWithDefaultRunner(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	runner, ok := m.runner.(*chat.DefaultChatRunner)
	require.True(t, ok)
	require.NotNil(t, m.extensionRuntimes)
	assert.Same(t, m.extensionRuntimes, runner.ExtensionRuntimeProvider())
}

func TestTUISinkStopsBlockingWhenUICloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sink := tuiSink{ch: make(chan tea.Msg), runID: 1, done: ctx.Done()}

	err := sink.Send(chat.ChatEvent{Kind: "tool-update"})

	require.ErrorIs(t, err, context.Canceled)
}
