package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
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
	require.NotNil(t, cmd)
	switch msg := cmd().(type) {
	case serverStatusMsg:
		return msg
	case tea.BatchMsg:
		for _, batched := range msg {
			if batched == nil {
				continue
			}
			if status, ok := batched().(serverStatusMsg); ok {
				return status
			}
		}
	}
	require.FailNow(t, "command did not load the server status")
	return serverStatusMsg{}
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
