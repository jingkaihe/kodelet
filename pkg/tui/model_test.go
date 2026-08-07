package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
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

func TestNewModelRemoteModeKeepsDisplayWorkspaceWithoutLocalDiscovery(t *testing.T) {
	runner := &recordingRunner{}
	m := newModel(context.Background(), Config{
		CWD:    "/runner/kodelet",
		Runner: runner,
		Remote: true,
	})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	assert.True(t, m.remote)
	assert.Equal(t, "/runner/kodelet", m.cwd)
	assert.Empty(t, m.requestedCWD)
	assert.Empty(t, m.messageHistoryScopeCWD)
	assert.True(t, m.extensionDiscoveryBlocked)
}

func TestNewModelProvidesIdleExtensionUIBroker(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	_, hasInput := extensions.UIInputBrokerFromContext(m.ctx)
	_, hasConfirm := extensions.UIConfirmBrokerFromContext(m.ctx)
	_, hasSelect := extensions.UISelectBrokerFromContext(m.ctx)
	_, hasNotify := extensions.UINotifyBrokerFromContext(m.ctx)

	assert.True(t, hasInput)
	assert.True(t, hasConfirm)
	assert.True(t, hasSelect)
	assert.True(t, hasNotify)
}

func TestTUISinkStopsBlockingWhenUICloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sink := tuiSink{ch: make(chan tea.Msg), runID: 1, done: ctx.Done()}

	err := sink.Send(chat.ChatEvent{Kind: "tool-update"})

	require.ErrorIs(t, err, context.Canceled)
}
