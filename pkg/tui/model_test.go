package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func keyPress(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

func keyPressWithMod(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

func textKeyPress(text string) tea.KeyPressMsg {
	return textKeyPressWithMod(text, 0)
}

func textKeyPressWithMod(text string, mod tea.KeyMod) tea.KeyPressMsg {
	code := tea.KeyExtended
	if runes := []rune(text); len(runes) == 1 {
		code = runes[0]
	}
	return tea.KeyPressMsg{Code: code, Text: text, Mod: mod}
}

func numberedLines(count int) string {
	return strings.TrimRight(strings.Repeat("line\n", count), "\n")
}

var _ tea.Model = model{}

func TestRunRequiresExplicitDaemonRunnerBeforeLocalInitialization(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(base, []byte("unchanged"), 0o600))
	t.Setenv("KODELET_BASE_PATH", base)
	for _, remote := range []bool{false, true} {
		err := Run(t.Context(), Config{Remote: remote, CWD: "/only/on/runner", Profile: "missing-profile"})
		require.ErrorContains(t, err, "TUI requires an explicit daemon runner")
	}
	data, err := os.ReadFile(base)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
}

type remoteDiscoveryRunner struct {
	recordingRunner
	target chat.WorkspaceTarget
}

func (r *remoteDiscoveryRunner) DiscoverWorkspace(_ context.Context, target chat.WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error) {
	r.target = target
	return protocol.WorkspaceDiscoverResult{CWD: "/only/on/runner", Commands: []slashcommands.Command{{Name: "runner-only", Description: "Remote extension"}}}, nil
}

func TestRemoteSlashDiscoveryUsesRunnerTargetAndDiscardsStaleDirectory(t *testing.T) {
	runner := &remoteDiscoveryRunner{}
	m := newModel(t.Context(), Config{Runner: runner, Remote: true, CWD: "../only-on-runner", Profile: "model-profile", EnvironmentProfile: "review"})
	t.Cleanup(m.cancel)
	command := m.loadRemoteSlashCommands(m.conversationState)
	require.NotNil(t, command)
	message := command().(slashCommandsMsg)
	assert.Equal(t, chat.WorkspaceTarget{CWD: "../only-on-runner", Profile: "model-profile", EnvironmentProfile: "review"}, runner.target)
	updated, next := m.Update(message)
	m = updated.(model)
	assert.Contains(t, slashCommandNames(m.slashCommands), "runner-only")
	assert.True(t, m.extensionDiscoveryBlocked, "remote discovery must not schedule a client extension runtime")
	assert.Nil(t, next)
	m.requestedCWD = "/different/runner-directory"
	m.slashCommands = withTUIBuiltInSlashCommands(nil)
	updated, _ = m.Update(message)
	m = updated.(model)
	assert.NotContains(t, slashCommandNames(m.slashCommands), "runner-only")
	m.profile = "default"
	m.loadRemoteSlashCommands(m.conversationState)()
	assert.Equal(t, "default", runner.target.Profile, "explicit default must not inherit the daemon's active profile")
	m.conversationID = "persisted-conversation"
	m.loadRemoteSlashCommands(m.conversationState)()
	assert.Equal(t, chat.WorkspaceTarget{ConversationID: "persisted-conversation"}, runner.target, "stored affinity must replace CLI directory/profile defaults on resume")
}

func TestRemoteSlashDiscoveryMatchesConversationTargetNotDisplayProfile(t *testing.T) {
	for _, test := range []struct {
		name           string
		conversationID string
		nextID         string
		background     bool
		accept         bool
	}{
		{name: "new probe after save", nextID: "saved"},
		{name: "saved probe after identity change", conversationID: "saved", nextID: "other"},
		{name: "saved probe after reset", conversationID: "saved"},
		{name: "saved profile display change", conversationID: "saved", nextID: "saved", accept: true},
		{name: "background saved profile display change", conversationID: "saved", nextID: "saved", background: true, accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{
				Commands:  []slashcommands.Command{{Name: "discovered-command"}},
				Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "discovered", Generation: 1}}, Digest: "discovered-digest",
			}}
			m := newModel(t.Context(), Config{Remote: true, Runner: runner, CWD: "/runner/project", Profile: "work", ConversationID: test.conversationID})
			t.Cleanup(m.cancel)
			state := m.conversationState
			discover := m.loadRemoteSlashCommands(state)
			state.conversationID = test.nextID
			if test.conversationID != "" {
				state.profile = "updated-display-profile"
			}
			if test.background {
				other := newConversationState("other", "other", true, m.conversationDefaults)
				m.conversations[other.key] = other
				_, _ = m.activateConversation(other.key)
			}
			message := discover().(slashCommandsMsg)
			assert.Equal(t, test.conversationID, message.conversationID)
			if test.conversationID != "" {
				assert.Empty(t, message.profile, "saved discovery must use the server-pinned profile")
			}
			updated, cmd := m.Update(message)
			m = updated.(model)
			assert.Nil(t, cmd)
			if test.accept {
				assert.Contains(t, slashCommandNames(state.slashCommands), "discovered-command")
				assert.Len(t, state.extensionShortcuts, 1)
				assert.Equal(t, "discovered-digest", state.shortcutDigest)
			} else {
				assert.NotContains(t, slashCommandNames(state.slashCommands), "discovered-command")
				assert.Empty(t, state.extensionShortcuts)
				assert.Empty(t, state.shortcutDigest)
			}
			if test.background {
				assert.Equal(t, "other", m.activeConversationKey)
				assert.NotContains(t, slashCommandNames(m.slashCommands), "discovered-command")
				assert.Empty(t, m.extensionShortcuts)
			}
		})
	}
}

func TestNewModelDoesNotConstructRunnerOrExtensionRuntime(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	assert.Nil(t, m.runner)
	assert.Nil(t, m.extensionRuntimes)
}

func TestNewModelRemoteModeKeepsDisplayWorkspaceWithoutLocalDiscovery(t *testing.T) {
	runner := &recordingRunner{}
	m := newModel(context.Background(), Config{
		DefaultCWD: "~/runner/kodelet",
		Runner:     runner,
		Remote:     true,
	})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })

	assert.True(t, m.remote)
	assert.Nil(t, m.extensionRuntimes)
	assert.Nil(t, m.messageHistoryStore)
	assert.Equal(t, "~/runner/kodelet", m.cwd)
	assert.Empty(t, m.requestedCWD)
	assert.Empty(t, m.messageHistoryScopeCWD)
	assert.True(t, m.extensionDiscoveryBlocked)
	assert.Equal(t, "~/runner/kodelet", displayCWD(m.cwd))
	assert.ElementsMatch(t, []string{"goal", "new", "rename", "sessions", "stop", "theme", "take-control"}, slashCommandNames(m.slashCommands))

	m.createNewConversation()
	assert.ElementsMatch(t, []string{"goal", "new", "rename", "sessions", "stop", "theme", "take-control"}, slashCommandNames(m.slashCommands))
}

func TestNewModelRemoteModeKeepsExplicitRequestedCWD(t *testing.T) {
	m := newModel(context.Background(), Config{
		CWD:        "../other-project",
		DefaultCWD: "~/runner/kodelet",
		Runner:     &recordingRunner{},
		Remote:     true,
	})
	t.Cleanup(m.cancel)

	assert.Equal(t, "../other-project", m.cwd)
	assert.Equal(t, "../other-project", m.requestedCWD)
}

func TestDisplayCWDPreservesServerCompactedWorkspace(t *testing.T) {
	assert.Equal(t, "~/workspace/kodelet", displayCWD("~/workspace/kodelet"))
	assert.Equal(t, `~\workspace\kodelet`, displayCWD(`~\workspace\kodelet`))
}

func TestRemoteModelCanSelectTUISlashCommand(t *testing.T) {
	m := newModel(context.Background(), Config{Runner: &recordingRunner{}, Remote: true})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.textarea.SetValue("/sess")

	assert.True(t, m.slashCommandSuggestionsOpen())
	updated, _ := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	assert.Equal(t, "/sessions ", m.textarea.Value())

	updated, _ = m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	assert.NotNil(t, m.conversationPicker)
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
