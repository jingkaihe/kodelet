package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type serverStatusRunner struct {
	recordingRunner
	status chat.ServerStatus
	err    error
	calls  int
}

func (r *serverStatusRunner) ServerStatus(context.Context) (chat.ServerStatus, error) {
	r.calls++
	return r.status, r.err
}

type aboutRunner struct {
	serverStatusRunner
	runnerStatus chat.RunnerStatus
	runnerErr    error
	targets      []chat.WorkspaceTarget
}

func (r *aboutRunner) RunnerStatus(_ context.Context, target chat.WorkspaceTarget) (chat.RunnerStatus, error) {
	r.targets = append(r.targets, target)
	return r.runnerStatus, r.runnerErr
}

func setTestBuildInfo(t *testing.T, buildVersion, gitCommit, buildTime string) {
	t.Helper()
	previousVersion, previousCommit, previousBuildTime := version.Version, version.GitCommit, version.BuildTime
	version.Version, version.GitCommit, version.BuildTime = buildVersion, gitCommit, buildTime
	t.Cleanup(func() {
		version.Version, version.GitCommit, version.BuildTime = previousVersion, previousCommit, previousBuildTime
	})
}

func serverStatusMsgFromCmd(t *testing.T, cmd tea.Cmd) serverStatusMsg {
	t.Helper()
	for _, msg := range aboutMessagesFromCmd(t, cmd) {
		if status, ok := msg.(serverStatusMsg); ok {
			return status
		}
	}
	require.FailNow(t, "command did not load the server status")
	return serverStatusMsg{}
}

func aboutMessagesFromCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var messages []tea.Msg
		for _, cmd := range batch {
			if cmd != nil {
				messages = append(messages, aboutMessagesFromCmd(t, cmd)...)
			}
		}
		return messages
	}
	return []tea.Msg{msg}
}

func TestAboutSlashCommandShowsClientAndServerDetails(t *testing.T) {
	setTestBuildInfo(t, "0.6.24-beta", "e9f7b8631234", "Sun Sep 27 10:11:12 UTC 2026")
	runner := &serverStatusRunner{status: chat.ServerStatus{
		Version:   "0.6.23-beta",
		GitCommit: "abcdef0123456789",
		BuildTime: "2026-09-20T08:00:00Z",
		APIReady:  true,
	}}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true, ServerURL: "http://127.0.0.1:8123"})

	m.textarea.SetValue("/ab")
	require.True(t, m.slashCommandSuggestionsOpen())
	updated, _ := m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	require.Equal(t, "/about ", m.textarea.Value())

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	require.NotNil(t, m.aboutDialog)
	assert.False(t, m.running)
	assert.Empty(t, m.conversationID)
	assert.Empty(t, m.textarea.Value())
	loading := xansi.Strip(m.View().Content)
	assert.Contains(t, loading, "About Kodelet")
	assert.Contains(t, loading, "Loading…")

	updated, _ = m.Update(serverStatusMsgFromCmd(t, cmd))
	*m = updated.(model)
	assert.Equal(t, 1, runner.calls)
	view := xansi.Strip(m.View().Content)
	for _, expected := range []string{
		"Client",
		"0.6.24-beta",
		"e9f7b86",
		"Sep 27, 2026",
		"Server",
		"http://127.0.0.1:8123",
		"0.6.23-beta",
		"abcdef0",
		"Sep 20, 2026",
		"Press Esc, Enter, or q to close.",
	} {
		assert.Contains(t, view, expected)
	}
	assert.NotContains(t, view, "abcdef01")
	assert.NotContains(t, view, "Loading…")

	updated, cmd = m.Update(keyPress(tea.KeyEsc))
	*m = updated.(model)
	assert.Nil(t, cmd)
	assert.Nil(t, m.aboutDialog)
	assert.NotContains(t, xansi.Strip(m.View().Content), "About Kodelet")
}

func TestAboutDialogRetriesFailedServerStatusAndIgnoresStaleResponses(t *testing.T) {
	runner := &serverStatusRunner{err: errors.New("connection refused")}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true})
	m.running = true
	m.textarea.SetValue("/about")

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	require.NotNil(t, m.aboutDialog, "/about opens immediately during an active turn")
	assert.Empty(t, m.queuedFollowUps)
	failed := serverStatusMsgFromCmd(t, cmd)
	updated, _ = m.Update(failed)
	*m = updated.(model)

	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "Unavailable")
	assert.Contains(t, view, "connection refused")
	assert.Contains(t, view, "Press r to try again, or Esc to close.")

	runner.err = nil
	runner.status = chat.ServerStatus{Version: "0.6.24-beta", GitCommit: "unknown", BuildTime: "unknown"}
	updated, cmd = m.Update(textKeyPress("r"))
	*m = updated.(model)
	require.True(t, m.aboutDialog.loading)
	retried := serverStatusMsgFromCmd(t, cmd)

	updated, _ = m.Update(failed)
	*m = updated.(model)
	assert.True(t, m.aboutDialog.loading, "a stale response must not replace the retry")

	updated, _ = m.Update(retried)
	*m = updated.(model)
	view = xansi.Strip(m.View().Content)
	assert.Contains(t, view, "0.6.24-beta")
	assert.Contains(t, view, "Unknown")
	assert.NotContains(t, view, "connection refused")

	updated, cmd = m.Update(textKeyPress("r"))
	*m = updated.(model)
	assert.Nil(t, cmd, "retry is only available after a failure")
	assert.Equal(t, 2, runner.calls)
}

func TestAboutDialogWithoutServerStatusSupport(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &recordingRunner{}, Remote: true})

	cmd := m.openAboutDialog()

	assert.Nil(t, cmd)
	require.NotNil(t, m.aboutDialog)
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "Unavailable")
	assert.Contains(t, view, "This connection does not report server details.")
	assert.Contains(t, view, "Press Esc, Enter, or q to close.")
	assert.NotContains(t, view, "Press r")

	updated, _ := m.Update(tea.PasteMsg{Content: "ignored"})
	*m = updated.(model)
	assert.Empty(t, m.textarea.Value())

	updated, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	*m = updated.(model)
	assert.Nil(t, m.aboutDialog)
}

func TestAboutSlashCommandRejectsArguments(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &serverStatusRunner{}, Remote: true})
	m.textarea.SetValue("/about now")

	cmd := m.submit()

	require.NotNil(t, cmd)
	assert.Nil(t, m.aboutDialog)
	assert.Empty(t, m.textarea.Value())
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, "usage: /about", m.uiNotifications[0].message)
}

func TestAboutAndShortcutsDialogsReplaceEachOther(t *testing.T) {
	m := newThemeTestModel(t, Config{Runner: &serverStatusRunner{}, Remote: true})

	m.openShortcutsDialog()
	m.openAboutDialog()
	assert.False(t, m.shortcutsOpen)
	require.NotNil(t, m.aboutDialog)

	m.openShortcutsDialog()
	assert.True(t, m.shortcutsOpen)
	assert.Nil(t, m.aboutDialog)
}

func TestAboutDialogScrollsOnShortTerminals(t *testing.T) {
	runner := &serverStatusRunner{status: chat.ServerStatus{
		Version:   "0.6.23-beta",
		GitCommit: "abcdef0123456789",
		BuildTime: "2026-09-20T08:00:00Z",
	}}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true, ServerURL: "http://127.0.0.1:8123"})
	m.height = 12
	m.resize()
	m.applyServerStatus(serverStatusMsgFromCmd(t, m.openAboutDialog()))
	footer := "↑/↓ scroll · Esc close"
	assert.Contains(t, xansi.Strip(m.View().Content), footer)
	assert.NotContains(t, xansi.Strip(m.View().Content), "Sep 20, 2026")

	for _, test := range []struct {
		name   string
		msg    tea.Msg
		offset int
	}{
		{name: "page down", msg: keyPress(tea.KeyPgDown), offset: 2},
		{name: "down at bottom", msg: keyPress(tea.KeyDown), offset: 2},
		{name: "wheel up", msg: tea.MouseWheelMsg{Button: tea.MouseWheelUp}, offset: 1},
		{name: "home", msg: keyPress(tea.KeyHome), offset: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated, cmd := m.Update(test.msg)
			*m = updated.(model)
			assert.Nil(t, cmd)
			assert.Equal(t, test.offset, m.aboutDialog.scrollOffset)
			view := xansi.Strip(m.View().Content)
			assert.Contains(t, view, "About Kodelet")
			assert.Contains(t, view, footer)
			assert.Contains(t, view, "╰")
			assert.LessOrEqual(t, len(strings.Split(m.renderAboutDialog(), "\n")), m.height)
			if test.offset == 2 {
				assert.Contains(t, view, "Sep 20, 2026")
			}
		})
	}

	// Resizing must clamp the displayed offset without losing any build details.
	m.updateAboutDialogKey("end")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	*m = updated.(model)
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "Client")
	assert.Contains(t, view, "Sep 20, 2026")
	assert.Contains(t, view, "Press Esc, Enter, or q to close.")
	assert.NotContains(t, view, "scroll")
	m.updateAboutDialogKey("up")
	assert.Zero(t, m.aboutDialog.scrollOffset)
}

func TestAboutDialogKeepsRetryVisibleForLongErrors(t *testing.T) {
	runner := &serverStatusRunner{err: errors.New(strings.Repeat("upstream unavailable ", 150) + "last error detail")}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true, ServerURL: "http://127.0.0.1:8123"})
	m.applyServerStatus(serverStatusMsgFromCmd(t, m.openAboutDialog()))
	footer := "↑/↓ scroll · r retry · Esc close"
	assert.Contains(t, xansi.Strip(m.View().Content), footer)
	assert.LessOrEqual(t, len(strings.Split(m.renderAboutDialog(), "\n")), m.height)

	updated, _ := m.Update(keyPress(tea.KeyEnd))
	*m = updated.(model)
	assert.Positive(t, m.aboutDialog.scrollOffset)
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "last error detail")
	assert.Contains(t, view, footer)
	assert.Contains(t, view, "╰")

	// Rewrapping at a wider width must clamp an offset from the narrower view.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 42, Height: 12})
	*m = updated.(model)
	m.updateAboutDialogKey("end")
	assert.Contains(t, xansi.Strip(m.View().Content), footer)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	*m = updated.(model)
	assert.Contains(t, xansi.Strip(m.View().Content), "last error detail")
	assert.Contains(t, xansi.Strip(m.View().Content), footer)

	runner.err = nil
	runner.status = chat.ServerStatus{Version: "0.6.24-beta"}
	updated, cmd := m.Update(textKeyPress("r"))
	*m = updated.(model)
	assert.Zero(t, m.aboutDialog.scrollOffset)
	assert.True(t, m.aboutDialog.loading)
	m.applyServerStatus(serverStatusMsgFromCmd(t, cmd))
	view = xansi.Strip(m.View().Content)
	assert.Contains(t, view, "0.6.24-beta")
	assert.Contains(t, view, "Press Esc, Enter, or q to close.")
	assert.NotContains(t, view, "last error detail")
}

func TestAboutDialogShowsRunnerCapabilities(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       runnerregistry.RunnerStatus
		offline      bool
		noRunner     bool
		noTerminal   bool
		wantGit      string
		wantTerminal string
	}{
		{name: "idle", status: runnerregistry.RunnerStatusIdle, wantGit: "Enabled", wantTerminal: "Enabled"},
		{name: "busy", status: runnerregistry.RunnerStatusBusy, wantGit: "Enabled", wantTerminal: "Enabled"},
		{name: "permission denied", status: runnerregistry.RunnerStatusIdle, noTerminal: true, wantGit: "Enabled", wantTerminal: "Not enabled"},
		{name: "disconnected", status: runnerregistry.RunnerStatusIdle, offline: true, wantGit: "Unavailable", wantTerminal: "Unavailable"},
		{name: "incompatible", status: runnerregistry.RunnerStatusIncompatible, wantGit: "Unavailable", wantTerminal: "Unavailable"},
		{name: "no runner", noRunner: true, wantGit: "Unavailable", wantTerminal: "Unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			m.aboutDialog = &aboutDialogState{
				runnerSupported: true,
				runnerStatus: chat.RunnerStatus{
					Runner: &runnerregistry.Runner{
						Connected:          !test.offline,
						Status:             test.status,
						ActiveRunIDs:       []string{"run-1", "run-2"},
						WorkspaceGitDiff:   true,
						WorkspaceTerminal:  true,
						WorkspaceBrowser:   true,
						WorkspaceDiscovery: true,
						WorkspaceCWD:       true,
					},
					TerminalAuthorized: !test.noTerminal,
				},
			}
			if test.noRunner {
				m.aboutDialog.runnerStatus.Runner = nil
			}
			// The capability section remains accessible at the normal terminal height.
			updated, _ := m.Update(keyPress(tea.KeyEnd))
			*m = updated.(model)
			view := xansi.Strip(m.View().Content)
			assert.Contains(t, view, "Capabilities")
			if test.status == runnerregistry.RunnerStatusBusy {
				assert.Contains(t, view, padVisible("Status", aboutLabelWidth)+"  2 active")
			}
			for _, label := range []string{"Git diffs", "Slash commands", "Working directory"} {
				assert.Contains(t, view, padVisible(label, aboutLabelWidth)+"  "+test.wantGit)
			}
			for _, label := range []string{"Terminal", "Browser"} {
				assert.Contains(t, view, padVisible(label, aboutLabelWidth)+"  "+test.wantTerminal)
			}
			assert.Contains(t, view, "Esc close")
			assert.LessOrEqual(t, len(strings.Split(m.renderAboutDialog(), "\n")), m.height)
		})
	}
}

func TestAboutRunnerDetailsUseActiveConversationAndIgnoreStaleResponses(t *testing.T) {
	runner := &aboutRunner{
		serverStatusRunner: serverStatusRunner{status: chat.ServerStatus{Version: "server-build"}},
		runnerErr:          errors.New("runner registry unavailable"),
	}
	m := newThemeTestModel(t, Config{Runner: runner, Remote: true, Profile: "work"})
	failed := aboutMessagesFromCmd(t, m.openAboutDialog())
	assert.Equal(t, chat.WorkspaceTarget{Profile: "work"}, runner.targets[0])
	for _, msg := range failed {
		updated, _ := m.Update(msg)
		*m = updated.(model)
	}
	assert.Contains(t, xansi.Strip(m.View().Content), "server-build")
	m.updateAboutDialogKey("end")
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "runner registry unavailable")
	assert.Contains(t, view, "r retry")

	runner.runnerErr = nil
	runner.runnerStatus = chat.RunnerStatus{Runner: &runnerregistry.Runner{
		ID:               "selected-runner",
		Workspace:        protocol.Workspace{Name: "Workspace name"},
		KodeletVersion:   "runner-build",
		Connected:        true,
		Status:           runnerregistry.RunnerStatusBusy,
		WorkspaceGitDiff: true,
	}}
	updated, cmd := m.Update(textKeyPress("r"))
	*m = updated.(model)
	assert.True(t, m.aboutDialog.runnerLoading)
	for _, msg := range failed {
		updated, _ = m.Update(msg)
		*m = updated.(model)
	}
	assert.True(t, m.aboutDialog.runnerLoading, "stale results must not replace a retry")
	for _, msg := range aboutMessagesFromCmd(t, cmd) {
		updated, _ = m.Update(msg)
		*m = updated.(model)
	}
	assert.Nil(t, m.aboutDialog.runnerErr)
	assert.Contains(t, xansi.Strip(m.View().Content), "Workspace name")
	assert.Contains(t, xansi.Strip(m.View().Content), "runner-build")
	assert.Contains(t, xansi.Strip(m.View().Content), padVisible("Status", aboutLabelWidth)+"  1 active")

	// A delayed request must keep the target captured when the dialog opened.
	m.dismissInfoDialogs()
	m.conversationID = "active-session"
	runner.err = errors.New("server status unavailable")
	runner.runnerErr = errors.New("runner permissions unavailable")
	cmd = m.openAboutDialog()
	m.conversationID = "other-session"
	messages := aboutMessagesFromCmd(t, cmd)
	assert.Equal(t, chat.WorkspaceTarget{ConversationID: "active-session"}, runner.targets[len(runner.targets)-1])
	for _, msg := range messages {
		updated, _ = m.Update(msg)
		*m = updated.(model)
	}
	m.updateAboutDialogKey("end")
	view = xansi.Strip(m.View().Content)
	assert.Contains(t, view, padVisible("Git diffs", aboutLabelWidth)+"  Enabled", "server status failures must not hide runner capabilities")
	assert.Contains(t, view, "server status unavailable")
	assert.Contains(t, view, "runner permissions unavailable")

	m.dismissInfoDialogs()
	for _, msg := range messages {
		updated, _ = m.Update(msg)
		*m = updated.(model)
	}
	assert.Nil(t, m.aboutDialog, "late responses must not reopen a dismissed dialog")
}

func TestAboutBuildMetadataFormatting(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		expected string
	}{
		{name: "release timestamp", input: "2026-09-27T23:30:00-02:00", expected: "Sep 28, 2026"},
		{name: "local build timestamp", input: "Sun Sep 07 10:11:12 UTC 2026", expected: "Sep 7, 2026"},
		{name: "unknown", input: "unknown", expected: "Unknown"},
		{name: "empty", input: " ", expected: "Unknown"},
		{name: "unrecognized", input: "last tuesday", expected: "last tuesday"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, formatAboutBuildDate(test.input))
		})
	}

	assert.Equal(t, "e9f7b86", formatAboutCommit("e9f7b8631234"))
	assert.Equal(t, "e9f7b", formatAboutCommit("e9f7b"))
	assert.Equal(t, "Unknown", formatAboutCommit("unknown"))
	assert.Equal(t, "Unknown", aboutValue(""))
}
