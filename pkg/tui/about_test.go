package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
		{name: "page down", msg: keyPress(tea.KeyPgDown), offset: 3},
		{name: "down at bottom", msg: keyPress(tea.KeyDown), offset: 3},
		{name: "wheel up", msg: tea.MouseWheelMsg{Button: tea.MouseWheelUp}, offset: 2},
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
			if test.offset == 3 {
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

func TestAboutDialogTabNavigationResetsScroll(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.height = 8
	m.openAboutDialog()
	for _, key := range []tea.KeyPressMsg{
		keyPress(tea.KeyTab),
		keyPressWithMod(tea.KeyTab, tea.ModShift),
		keyPress(tea.KeyRight),
		keyPress(tea.KeyLeft),
	} {
		wasContext := m.aboutDialog.contextTab
		m.updateAboutDialogKey("end")
		require.Positive(t, m.aboutDialog.scrollOffset)
		updated, cmd := m.Update(key)
		*m = updated.(model)
		assert.Nil(t, cmd)
		assert.Equal(t, !wasContext, m.aboutDialog.contextTab)
		assert.Zero(t, m.aboutDialog.scrollOffset)
		view := xansi.Strip(m.renderAboutDialog())
		assert.Contains(t, view, "Tab/←/→")
		if m.aboutDialog.contextTab {
			assert.Contains(t, view, "[Context]")
			assert.Contains(t, view, "Context window")
		} else {
			assert.Contains(t, view, "[About]")
			assert.Contains(t, view, "Client")
		}
	}
	m.updateAboutDialogKey("tab")
	m.dismissInfoDialogs()
	m.openAboutDialog()
	assert.False(t, m.aboutDialog.contextTab, "reopening starts on About")
}

func TestAboutDialogRetryPreservesContextTab(t *testing.T) {
	runner := &serverStatusRunner{err: errors.New("connection refused")}
	m := newThemeTestModel(t, Config{Runner: runner})
	m.height = 8
	m.applyServerStatus(serverStatusMsgFromCmd(t, m.openAboutDialog()))
	m.updateAboutDialogKey("tab")
	m.updateAboutDialogKey("end")
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "r retry")
	runner.err = nil
	runner.status = chat.ServerStatus{Version: "retried-version"}
	cmd := m.updateAboutDialogKey("r")
	require.NotNil(t, cmd)
	assert.True(t, m.aboutDialog.contextTab)
	assert.Zero(t, m.aboutDialog.scrollOffset)
	m.applyServerStatus(serverStatusMsgFromCmd(t, cmd))
	assert.True(t, m.aboutDialog.contextTab)
	m.updateAboutDialogKey("tab")
	lines, _ := m.aboutDialogContent()
	assert.Contains(t, xansi.Strip(strings.Join(lines, "\n")), "retried-version")
}

func TestAboutContextShowsSeparateUsageAndEstimates(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	thinkingTokens := 25
	m.usage = llmtypes.Usage{
		CurrentContextWindow: 1500,
		MaxContextWindow:     3000,
		ContextBreakdown: &llmtypes.ContextBreakdown{
			SystemPromptTokens:   100,
			ToolDefinitionTokens: 200,
			InputTextTokens:      100,
			OutputTextTokens:     100,
			ThinkingTokens:       &thinkingTokens,
			OtherTokens:          25,
			TotalTokens:          1000,
			CodeMode:             "only",
			Estimated:            true,
			Tools: []llmtypes.ToolContextUsage{
				{Name: "code_execute", InputTokens: 100, OutputTokens: 250, Calls: 3},
				{Name: "skill", InputTokens: 20, OutputTokens: 80, Calls: 1},
			},
		},
	}
	m.openAboutDialog()
	m.updateAboutDialogKey("tab")
	lines, _ := m.aboutDialogContent()
	view := xansi.Strip(strings.Join(lines, "\n"))
	for _, expected := range []string{
		"1.5K / 3.0K tokens (50.0%)",
		"Composition",
		"~1.0K tokens",
		"Code mode: only",
		"Used split estimated · remainder available",
		"System prompt",
		"Tool definitions",
		"Input text",
		"Output text",
		"Thinking",
		"Other",
		"Tool usage",
		"code_execute",
		"450 tokens · 45.0%",
		"~450 tokens",
		"350 tokens · 77.8%",
		"3 calls · 100 input · 250 output tokens",
		"skill",
		"1 call · 20 input · 80 output tokens",
		"█", "░",
	} {
		assert.Contains(t, view, expected)
	}
	var rows, bars []string
	for _, line := range lines {
		plain := xansi.Strip(line)
		if strings.HasPrefix(plain, "■ ") {
			rows = append(rows, strings.Join(strings.Fields(plain), " "))
		}
		if strings.ContainsAny(plain, "█░") {
			bars = append(bars, plain)
		}
	}
	assert.Equal(t, []string{
		"■ System prompt 100 tokens · 10.0%",
		"■ Tool definitions 200 tokens · 20.0%",
		"■ Input text 100 tokens · 10.0%",
		"■ Output text 100 tokens · 10.0%",
		"■ Thinking 25 tokens · 2.5%",
		"■ Other 25 tokens · 2.5%",
		"■ Tool usage 450 tokens · 45.0%",
		"■ code_execute 350 tokens · 77.8%",
		"■ skill 100 tokens · 22.2%",
	}, rows)
	require.Len(t, bars, 2, "no per-row bars")
	width := m.uiDialogWidth() - 4
	assert.Equal(t, width, lipgloss.Width(bars[0]))
	assert.Equal(t, width/2, strings.Count(bars[0], "░"))
	assert.Equal(t, strings.Repeat("█", width), bars[1], "tool bar is independent of capacity")

	// The open tab reflects newer usage snapshots, not a cached UI-event estimate.
	m.usage.CurrentContextWindow = 1800
	m.usage.ContextBreakdown = nil
	lines, _ = m.aboutDialogContent()
	view = xansi.Strip(strings.Join(lines, "\n"))
	assert.Contains(t, view, "1.8K / 3.0K tokens (60.0%)")
	assert.Contains(t, view, "Breakdown unavailable")
	assert.NotContains(t, view, "code_execute")
}

func TestAboutContextThinkingAvailability(t *testing.T) {
	zero, reported := 0, 100
	for _, test := range []struct {
		name   string
		tokens *int
		row    string
	}{
		{name: "unknown", row: "■ Thinking —"},
		{name: "zero", tokens: &zero, row: "■ Thinking 0 tokens · 0.0%"},
		{name: "reported", tokens: &reported, row: "■ Thinking 100 tokens · 50.0%"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			total := 100
			if test.tokens != nil {
				total += *test.tokens
			}
			m.usage = llmtypes.Usage{
				CurrentContextWindow: 150, MaxContextWindow: 300,
				ContextBreakdown: &llmtypes.ContextBreakdown{
					InputTextTokens: 100, ThinkingTokens: test.tokens, TotalTokens: total,
				},
			}
			lines := m.contextDialogContent()
			for i, line := range lines {
				plain := strings.Join(strings.Fields(xansi.Strip(line)), " ")
				if strings.HasPrefix(plain, "■ Thinking") {
					assert.Equal(t, test.row, plain)
					assert.Contains(t, xansi.Strip(lines[i+1]), "■ Other")
				}
			}
			view := xansi.Strip(strings.Join(lines, "\n"))
			assert.Contains(t, view, "Thinking")
			assert.Contains(t, view, "Other")
			assert.NotContains(t, view, "Other / reasoning")
			assert.NotContains(t, view, "Not reported")
			assert.NotContains(t, view, "Last response")
			thinking := 0
			if test.tokens != nil {
				thinking = *test.tokens
			}
			assert.Equal(t, contextStackedBar([]contextCategory{
				{label: "Input text", tokens: 100, style: composerFlowStyle},
				{label: "Thinking", tokens: thinking, style: thoughtHeaderStyle},
			}, total, 0.5, m.uiDialogWidth()-4), lines[2])
		})
	}
}

func TestAboutContextHandlesUnavailableAndEmptyUsage(t *testing.T) {
	for _, test := range []struct {
		name     string
		usage    llmtypes.Usage
		expected string
	}{
		{name: "new conversation", expected: "Context usage unavailable until a model turn."},
		{name: "unknown capacity", usage: llmtypes.Usage{CurrentContextWindow: 500}, expected: "500 tokens · Capacity unavailable."},
		{name: "old conversation", usage: llmtypes.Usage{CurrentContextWindow: 500, MaxContextWindow: 1000}, expected: "500 / 1.0K tokens (50.0%)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			m.usage = test.usage
			view := xansi.Strip(strings.Join(m.contextDialogContent(), "\n"))
			assert.Contains(t, view, test.expected)
			assert.Contains(t, view, "Breakdown unavailable until a model turn.")
			assert.NotContains(t, view, "Composition")
		})
	}
	for _, mode := range []string{"off", "on", "only"} {
		t.Run(mode, func(t *testing.T) {
			m := newThemeTestModel(t, Config{})
			m.usage.ContextBreakdown = &llmtypes.ContextBreakdown{Estimated: true, CodeMode: mode}
			view := xansi.Strip(strings.Join(m.contextDialogContent(), "\n"))
			assert.Contains(t, view, "Code mode: "+mode)
			assert.Contains(t, view, "~0 tokens")
			assert.Contains(t, view, "0 tokens · 0.0%")
			assert.Contains(t, view, "No tool usage in this context.")
			assert.NotContains(t, view, "NaN")
			assert.NotContains(t, view, "Inf")
		})
	}
}

func TestAboutContextStaysBoundedOnSmallTerminals(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.usage.ContextBreakdown = &llmtypes.ContextBreakdown{
		Estimated: true, CodeMode: "only", TotalTokens: 1000,
		Tools: []llmtypes.ToolContextUsage{{Name: strings.Repeat("tool", 30), InputTokens: 500, OutputTokens: 500, Calls: 2}},
	}
	m.openAboutDialog()
	for _, width := range []int{24, 42, 80} {
		for height := 1; height <= 12; height++ {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m.width, m.height = width, height
				m.resize()
				for _, contextTab := range []bool{false, true} {
					m.aboutDialog.contextTab = contextTab
					for _, key := range []string{"home", "end"} {
						m.updateAboutDialogKey(key)
						view := m.renderAboutDialog()
						lines := strings.Split(view, "\n")
						assert.LessOrEqual(t, len(lines), height)
						for _, line := range lines {
							assert.LessOrEqual(t, lipgloss.Width(line), m.uiDialogWidth())
						}
						assert.Contains(t, xansi.Strip(view), "Esc close")
						if height >= 4 {
							assert.Contains(t, xansi.Strip(view), "Tab")
						}
						if height >= 5 {
							assert.Contains(t, xansi.Strip(view), "About Kodelet")
						}
					}
				}
			})
		}
	}
}

func TestContextStackedBar(t *testing.T) {
	newThemeTestModel(t, Config{})
	categories := []contextCategory{
		{label: "System prompt", tokens: 100, style: toolHeaderStyle},
		{label: "Tool definitions", tokens: 200, style: steeringStyle.Italic(false)},
		{label: "Input text", tokens: 100, style: composerFlowStyle},
		{label: "Output text", tokens: 100, style: diffAddedStyle},
		{label: "Thinking", tokens: 25, style: thoughtHeaderStyle},
		{label: "Other", tokens: 25, style: uiDialogMutedStyle},
		{label: "Tool usage", tokens: 450, style: uiDialogCancelStyle},
	}
	// Shared UI snapshot: used is 50%; category shares within it are
	// 10/20/10/10/2.5/2.5/45%, giving 4/8/4/4/1/1/18 cells of an 80-cell bar.
	bar := contextStackedBar(categories, 1000, 0.5, 80)
	want := ""
	for i, cells := range []int{4, 8, 4, 4, 1, 1, 18} {
		want += renderPersistentStyle(categories[i].style, strings.Repeat("█", cells))
	}
	want += renderPersistentStyle(uiDialogMutedStyle, strings.Repeat("░", 40))
	assert.Equal(t, want, bar)
	assert.Equal(t, 80, lipgloss.Width(bar))

	for _, test := range []struct {
		name  string
		used  float64
		total int
		width int
		want  string
	}{
		{name: "unknown capacity", width: 10, want: "░░░░░░░░░░"},
		{name: "half", used: 0.5, total: 1000, width: 10, want: "█████░░░░░"},
		{name: "over capacity", used: 1.5, total: 1000, width: 10, want: "██████████"},
		{name: "negative usage", used: -0.1, total: 1000, width: 10, want: "░░░░░░░░░░"},
		{name: "empty estimate", used: 0.5, width: 10, want: "█████░░░░░"},
		{name: "fractional cell", used: 0.16, total: 1000, width: 10, want: "██░░░░░░░░"},
		{name: "sub-cell categories", used: 0.1, total: 1000, width: 10, want: "█░░░░░░░░░"},
		{name: "one column", used: 0.5, total: 1000, width: 1, want: "█"},
		{name: "no columns", used: 0.5, total: 1000},
		{name: "negative width", used: 0.5, total: 1000, width: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			bar := contextStackedBar(categories, test.total, test.used, test.width)
			assert.Equal(t, test.want, xansi.Strip(bar))
			assert.Equal(t, max(0, test.width), lipgloss.Width(bar))
		})
	}
}

func TestAboutContextBarAndLegendUseThemeColours(t *testing.T) {
	for _, themeName := range []string{DefaultThemeName, LightThemeName} {
		t.Run(themeName, func(t *testing.T) {
			m := newThemeTestModel(t, Config{Theme: themeName})
			thinkingTokens := 2
			m.usage = llmtypes.Usage{
				CurrentContextWindow: 14, MaxContextWindow: 14,
				ContextBreakdown: &llmtypes.ContextBreakdown{
					TotalTokens: 14, SystemPromptTokens: 2, ToolDefinitionTokens: 2,
					InputTextTokens: 2, OutputTextTokens: 2, OtherTokens: 2,
					ThinkingTokens: &thinkingTokens,
					Tools: []llmtypes.ToolContextUsage{
						{Name: "code_execute", InputTokens: 1},
						{Name: "skill", OutputTokens: 1},
					},
				},
			}
			lines := m.contextDialogContent()
			var rows []string
			for _, line := range lines {
				if strings.HasPrefix(xansi.Strip(line), "■ ") {
					rows = append(rows, line)
				}
			}
			colours := []string{m.theme.ToolHeader, m.theme.Steering, m.theme.ComposerFlow, m.theme.DiffAdded, m.theme.ThoughtHeader, m.theme.UI.DialogMuted, m.theme.UI.DialogCancel}
			for i, colour := range colours {
				style := lipgloss.NewStyle().Foreground(themeColor(colour))
				start, _ := styleSequences(style)
				assert.Contains(t, lines[2], start+"█", "bar colour %d", i)
				assert.Contains(t, rows[i], renderPersistentStyle(style, "■"), "legend colour %d", i)
			}
			// The tool palette starts orange, then teal, without inheriting a
			// category's italic text styling or making free space look used.
			assert.Contains(t, rows[7], renderPersistentStyle(uiDialogCancelStyle, "■"))
			assert.Contains(t, rows[8], renderPersistentStyle(toolHeaderStyle, "■"))
			assert.NotContains(t, strings.Join(lines, "\n"), "\x1b[3m")
		})
	}
}

func TestAboutContextUnknownCapacityAndZeroToolTokens(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.usage.ContextBreakdown = &llmtypes.ContextBreakdown{
		TotalTokens: 1_000_000, SystemPromptTokens: 1_000_000,
		Tools: []llmtypes.ToolContextUsage{{Name: "empty_tool", Calls: 1}},
	}
	lines := m.contextDialogContent()
	view := xansi.Strip(strings.Join(lines, "\n"))
	assert.Contains(t, view, "Estimated composition · capacity unavailable")
	assert.Contains(t, view, "~1.0M tokens")
	assert.Contains(t, view, "~0 tokens")
	assert.Contains(t, view, "1 call · 0 input · 0 output tokens")
	assert.NotContains(t, view, "NaN")
	assert.NotContains(t, view, "Inf")
	assert.NotContains(t, view, "░", "unknown capacity cannot imply available space")
	assert.Equal(t, strings.Repeat("█", m.uiDialogWidth()-4), xansi.Strip(lines[2]))
	assert.Equal(t, 1, strings.Count(view, strings.Repeat("█", m.uiDialogWidth()-4)), "zero-token tools have no second bar")
}
