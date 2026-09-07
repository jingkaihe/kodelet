package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
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
		require.ErrorContains(t, err, "chat requires a server connection")
	}
	data, err := os.ReadFile(base)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
}

func TestRunRejectsRedirectedTerminalBeforeInitialization(t *testing.T) {
	master, terminal, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = master.Close(); _ = terminal.Close() })
	nonterminal, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nonterminal.Close() })
	for _, redirected := range []string{"stdin", "stdout"} {
		t.Run(redirected, func(t *testing.T) {
			stdin, stdout := os.Stdin, os.Stdout
			t.Cleanup(func() { os.Stdin, os.Stdout = stdin, stdout })
			os.Stdin, os.Stdout = terminal, terminal
			if redirected == "stdin" {
				os.Stdin = nonterminal
			} else {
				os.Stdout = nonterminal
			}
			initialized := false
			err := Run(t.Context(), Config{Initialize: func(context.Context) (Config, error) {
				initialized = true
				return Config{Runner: &recordingRunner{}}, nil
			}})
			require.ErrorContains(t, err, "chat requires an interactive terminal on stdin and stdout")
			assert.False(t, initialized)
		})
	}
}

func TestDeferredInitializationKeepsComposerResponsive(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	runner := &remoteDiscoveryRunner{}
	m := newModel(t.Context(), Config{Theme: DefaultThemeName, Initialize: func(ctx context.Context) (Config, error) {
		close(started)
		select {
		case <-release:
			return Config{Runner: runner, Profile: "work", ProfileOptions: []string{"default", "work"}, ReasoningEffort: "high", CWD: "/runner/project", DefaultCWD: "/runner/default", EnvironmentProfile: "sandbox"}, nil
		case <-ctx.Done():
			return Config{}, ctx.Err()
		}
	}})
	t.Cleanup(m.cancel)
	batch := m.Init()().(tea.BatchMsg)
	require.Len(t, batch, 4, "bootstrap must not start history or discovery before configuration is ready")
	done := make(chan tea.Msg, 1)
	go func() { done <- batch[3]() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "initializer did not start")
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
	m = updated.(model)
	updated, _ = m.Update(textKeyPress("draft while starting"))
	m = updated.(model)
	assert.Contains(t, xansi.Strip(m.View().Content), "draft while starting")
	assert.Contains(t, xansi.Strip(m.View().Content), "Starting…")
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 25})
	m = updated.(model)
	updated, command := m.Update(keyPress(tea.KeyEnter))
	m = updated.(model)
	assert.Nil(t, command)
	assert.Nil(t, m.submit())
	assert.Nil(t, m.startConversationRun(m.conversationState, "must not run"))
	assert.False(t, m.running)
	assert.Empty(t, m.entries)
	assert.Equal(t, "draft while starting", m.textarea.Value())
	assert.Empty(t, runner.req.Message)
	ctx, channel, ui := m.ctx, m.runCh, m.extensionUI
	close(release)
	result := receiveRunMsg(t, done)
	assert.Nil(t, m.runner, "the worker must not install configuration outside Update")
	m.startupStartedAt = time.Now().Add(-1200 * time.Millisecond)
	updated, history := m.Update(result)
	m = updated.(model)
	require.NotNil(t, history)
	assert.False(t, m.startupPending)
	assert.Same(t, runner, m.runner)
	assert.Same(t, ctx, m.ctx)
	assert.Equal(t, channel, m.runCh)
	assert.Same(t, ui, m.extensionUI)
	assert.NoError(t, m.ctx.Err())
	assert.Equal(t, 100, m.width)
	assert.Equal(t, 25, m.height)
	assert.Equal(t, "draft while starting", m.textarea.Value())
	assert.Equal(t, m.textarea.Value(), m.draft)
	assert.Equal(t, "work", m.profile)
	assert.Equal(t, "high", m.reasoningEffort)
	assert.Equal(t, "/runner/project", m.cwd)
	assert.Equal(t, "/runner/default", m.remoteDefaultCWD)
	assert.Equal(t, "sandbox", m.environmentProfile)
	assert.Equal(t, DefaultThemeName, m.themeSelection)
	assert.True(t, m.remote, "prepared settings must not enable a client-local runtime")
	assert.Equal(t, m.spinnerGlyph()+" Loading extensions…", m.inputBottomLeftLabel())
	assert.Empty(t, runner.target, "resource discovery must remain asynchronous")
	updated, discovery := m.Update(history())
	m = updated.(model)
	require.NotNil(t, discovery)
	assert.True(t, m.resourcesLoading)
	updated, _ = m.Update(discovery())
	m = updated.(model)
	assert.False(t, m.resourcesLoading)
	assert.GreaterOrEqual(t, m.readyDuration, 1200*time.Millisecond, "ready timing includes bootstrap, not just command discovery")
	assert.Contains(t, m.inputBottomLeftLabel(), "Ready in ")
	assert.Contains(t, slashCommandNames(m.slashCommands), "runner-only")
	assert.Equal(t, "draft while starting", m.textarea.Value())
	assert.NotNil(t, m.submit(), "the user can explicitly submit their retained draft after readiness")
	assert.True(t, m.running)
}

func TestDeferredInitializationFailurePreservesDraft(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "bootstrap failure", err: assert.AnError, want: assert.AnError.Error()},
		{name: "missing connection", want: "did not return a server connection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newModel(t.Context(), Config{Initialize: func(context.Context) (Config, error) { return Config{}, test.err }})
			t.Cleanup(m.cancel)
			m.width, m.height = 100, 25
			m.resize()
			m.textarea.SetValue("keep this draft")
			updated, next := m.Update(m.initializeCommand()())
			m = updated.(model)
			assert.Nil(t, next)
			assert.False(t, m.startupPending)
			require.ErrorContains(t, m.startupErr, test.want)
			assert.Contains(t, xansi.Strip(m.View().Content), "Could not start chat")
			assert.Contains(t, xansi.Strip(m.View().Content), test.want)
			assert.Equal(t, "Startup failed", m.inputBottomLeftLabel())
			assert.Nil(t, m.submit())
			assert.False(t, m.running)
			updated, next = m.Update(keyPress(tea.KeyEnter))
			m = updated.(model)
			assert.Nil(t, next)
			assert.Equal(t, "keep this draft", m.textarea.Value())
			_, quit := m.Update(keyPressWithMod('d', tea.ModCtrl))
			require.NotNil(t, quit)
			assert.IsType(t, tea.QuitMsg{}, quit())
			assert.ErrorIs(t, m.ctx.Err(), context.Canceled)
		})
	}
}

type closableStartupRunner struct {
	recordingRunner
	closes atomic.Int32
}

func (r *closableStartupRunner) Close() error {
	r.closes.Add(1)
	return nil
}

func TestDeferredInitializationClosesLateAndInstalledRunners(t *testing.T) {
	for _, phase := range []string{"before result", "before install", "after install"} {
		t.Run(phase, func(t *testing.T) {
			runner := &closableStartupRunner{}
			m := newModel(t.Context(), Config{Initialize: func(ctx context.Context) (Config, error) {
				if phase == "before result" {
					<-ctx.Done()
				}
				return Config{Runner: runner}, nil
			}})
			t.Cleanup(m.cancel)
			initialize := m.initializeCommand()
			if phase == "before result" {
				_, quit := m.Update(keyPressWithMod('c', tea.ModCtrl))
				require.NotNil(t, quit)
				assert.IsType(t, tea.QuitMsg{}, quit())
			}
			result := initialize()
			if phase == "before install" {
				m.cancel()
			}
			updated, _ := m.Update(result)
			m = updated.(model)
			if phase == "after install" {
				assert.Same(t, runner, m.runner)
				m.cancel()
				m.closeInitializedRunner()
			} else {
				assert.Nil(t, m.runner)
			}
			assert.Eventually(t, func() bool { return runner.closes.Load() == 1 }, time.Second, time.Millisecond)
			result.(initializedMsg).closeRunner()
			assert.EqualValues(t, 1, runner.closes.Load(), "late cleanup and final shutdown must close only once")
		})
	}
}

func TestDeferredInitializationInstallsResumedHistorySource(t *testing.T) {
	runner := &conversationSourceRunner{history: chat.ConversationHistory{ID: "saved", CWD: "/runner/saved", Profile: "work"}}
	m := newModel(t.Context(), Config{Initialize: func(context.Context) (Config, error) {
		return Config{Runner: runner, ConversationID: "saved", CWD: "/runner/saved", Profile: "work"}, nil
	}})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("draft")
	updated, history := m.Update(m.initializeCommand()())
	m = updated.(model)
	assert.Same(t, runner, m.conversationSource)
	assert.Equal(t, "saved", m.activeConversationKey)
	assert.Same(t, m.conversationState, m.conversations["saved"])
	assert.True(t, m.initialHistoryPending)
	assert.Equal(t, "Loading conversation…", m.inputBottomLeftLabel())
	require.NotNil(t, history)
	updated, _ = m.Update(history())
	m = updated.(model)
	assert.False(t, m.initialHistoryPending)
	assert.True(t, m.loaded)
	assert.Equal(t, "draft", m.textarea.Value())
	assert.Empty(t, m.inputBottomLeftLabel(), "resumed conversations have no startup readiness badge")
	assert.True(t, m.readinessStartedAt.IsZero())
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

func TestRemoteDiscoveryReadinessIgnoresStaleResults(t *testing.T) {
	runner := &remoteDiscoveryRunner{}
	m := newModel(t.Context(), Config{Remote: true, Runner: runner, CWD: "/runner/workspace", Profile: "work"})
	t.Cleanup(m.cancel)
	stale := m.loadRemoteSlashCommands(m.conversationState)().(slashCommandsMsg)
	current := m.loadRemoteSlashCommands(m.conversationState)().(slashCommandsMsg)
	stale.readyDuration = 9 * time.Second
	stale.extensionCount = new(9)
	current.readyDuration = 627 * time.Millisecond
	updated, _ := m.Update(stale)
	m = updated.(model)
	assert.True(t, m.resourcesLoading)
	assert.Zero(t, m.readyDuration)
	assert.Nil(t, m.extensionCount)
	updated, _ = m.Update(current)
	m = updated.(model)
	assert.False(t, m.resourcesLoading)
	assert.Equal(t, "Ready in 627 ms", m.inputBottomLeftLabel())
	for _, staleTarget := range []string{"directory", "profile", "conversation"} {
		message := current
		switch staleTarget {
		case "directory":
			message.cwd = "/old/directory"
		case "profile":
			message.profile = "old-profile"
		case "conversation":
			message.conversationID = "old-conversation"
		}
		message.err = assert.AnError
		updated, _ = m.Update(message)
		m = updated.(model)
		assert.Equal(t, "Ready in 627 ms", m.inputBottomLeftLabel(), staleTarget)
	}
	current.err = assert.AnError
	updated, _ = m.Update(current)
	m = updated.(model)
	assert.Equal(t, "Could not load commands", m.inputBottomLeftLabel())
	assert.Zero(t, m.readyDuration)
}

func TestRemoteDiscoveryReadinessLabels(t *testing.T) {
	for _, test := range []struct {
		name     string
		count    *int
		duration time.Duration
		want     string
	}{
		{name: "older server", duration: 1900 * time.Millisecond, want: "Ready in 1.9 s"},
		{name: "older server milliseconds", duration: 627 * time.Millisecond, want: "Ready in 627 ms"},
		{name: "zero", count: new(0), duration: 1900 * time.Millisecond, want: "0 extensions ready in 1.9 s"},
		{name: "singular", count: new(1), duration: 1900 * time.Millisecond, want: "1 extension ready in 1.9 s"},
		{name: "multiple", count: new(9), duration: 1900 * time.Millisecond, want: "9 extensions ready in 1.9 s"},
		{name: "milliseconds", count: new(9), duration: 627 * time.Millisecond, want: "9 extensions ready in 627 ms"},
		{name: "submillisecond", count: new(1), duration: time.Microsecond, want: "1 extension ready in 1 ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{ExtensionCount: test.count}}
			m := newModel(t.Context(), Config{Remote: true, Runner: runner})
			t.Cleanup(m.cancel)
			message := m.loadRemoteSlashCommands(m.conversationState)().(slashCommandsMsg)
			assert.Equal(t, test.count, message.extensionCount)
			message.readyDuration = test.duration
			updated, _ := m.Update(message)
			m = updated.(model)
			assert.Equal(t, test.count, m.extensionCount)
			assert.Equal(t, test.want, m.inputBottomLeftLabel())
		})
	}
}

func TestConversationReadinessEndsAtFirstRun(t *testing.T) {
	for _, initialDiscovery := range []string{"pending", "complete"} {
		t.Run(initialDiscovery, func(t *testing.T) {
			runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{ExtensionCount: new(9)}}
			m := newModel(t.Context(), Config{Remote: true, Runner: runner})
			t.Cleanup(m.cancel)
			discover := m.loadRemoteSlashCommands(m.conversationState)
			if initialDiscovery == "complete" {
				updated, _ := m.Update(discover())
				m = updated.(model)
				assert.Contains(t, m.inputBottomLeftLabel(), "9 extensions ready in ")
			}
			m.textarea.SetValue("first turn")
			require.NotNil(t, m.submit())
			require.True(t, m.running)
			require.NotEmpty(t, m.conversationID)
			assert.Contains(t, m.inputBottomLeftLabel(), "Following the thread…")
			assert.False(t, m.resourcesLoading)
			assert.True(t, m.readinessStartedAt.IsZero(), "a pending startup must not include the model response")
			assert.Zero(t, m.readyDuration)
			updated, _ := m.Update(chatDoneMsg{runID: m.activeRunID, conversationKey: m.activeConversationKey, conversationID: m.conversationID})
			m = updated.(model)
			assert.False(t, m.running)
			assert.Empty(t, m.inputBottomLeftLabel())
			updated, _ = m.Update(discover())
			m = updated.(model)
			assert.Empty(t, m.inputBottomLeftLabel(), "late unsaved discovery cannot restore the badge")
			refresh := m.loadRemoteSlashCommands(m.conversationState)
			assert.False(t, m.resourcesLoading)
			assert.True(t, m.readinessStartedAt.IsZero(), "saved discovery must not restart readiness timing")
			assert.Empty(t, m.inputBottomLeftLabel())
			message := refresh().(slashCommandsMsg)
			assert.Zero(t, message.readyDuration)
			message.readyDuration = 5 * time.Second
			updated, _ = m.Update(message)
			m = updated.(model)
			assert.Zero(t, m.readyDuration)
			assert.Empty(t, m.inputBottomLeftLabel(), "later refreshes cannot restore the badge")
		})
	}
}

func TestResumedReadinessStaysHiddenAcrossBackgroundDiscovery(t *testing.T) {
	m := newModel(t.Context(), Config{Remote: true, Runner: &remoteDiscoveryRunner{}, ConversationID: "saved"})
	t.Cleanup(m.cancel)
	state := m.conversationState
	assert.True(t, state.readinessStartedAt.IsZero())
	updated, discover := m.Update(initialHistoryMsg{conversationKey: state.key, loaded: true, entries: []chatEntry{{kind: entryUser, content: "earlier turn"}}})
	m = updated.(model)
	require.NotNil(t, discover)
	assert.Empty(t, m.inputBottomLeftLabel())
	other := newConversationState("other", "", false, m.conversationDefaults)
	other.readyDuration = 42 * time.Millisecond
	m.conversations[other.key] = other
	_, _ = m.activateConversation(other.key)
	updated, _ = m.Update(discover())
	m = updated.(model)
	assert.Equal(t, "Ready in 42 ms", m.inputBottomLeftLabel(), "background discovery must not change the active conversation's readiness")
	assert.Zero(t, state.readyDuration)
	state.streamRunID = 1
	m.runs[1] = &conversationRun{conversationKey: state.key, observed: true}
	m.beginObservedConversationRun(state, 1)
	assert.True(t, state.running)
	assert.True(t, state.readinessStartedAt.IsZero())
	_, _ = m.activateConversation(state.key)
	assert.Contains(t, m.inputBottomLeftLabel(), "Following the thread…")
	_, _ = m.finishObservedConversationRun(state, 1, chat.ChatEvent{Kind: "done", ConversationID: state.conversationID})
	assert.False(t, m.running)
	assert.Empty(t, m.inputBottomLeftLabel())
	updated, _ = m.Update(m.loadRemoteSlashCommands(state)())
	m = updated.(model)
	assert.Empty(t, m.inputBottomLeftLabel())
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
	assert.ElementsMatch(t, []string{"goal", "new", "rename", "sessions", "stop", "theme"}, slashCommandNames(m.slashCommands))

	m.createNewConversation()
	assert.ElementsMatch(t, []string{"goal", "new", "rename", "sessions", "stop", "theme"}, slashCommandNames(m.slashCommands))
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
