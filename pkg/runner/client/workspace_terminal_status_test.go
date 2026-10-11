package client

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceTerminalStatusPTY(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				t.Skipf("shell unavailable: %v", err)
			}
			home := t.TempDir()
			resolvedHome, err := filepath.EvalSymlinks(home)
			require.NoError(t, err)
			t.Setenv("SHELL", shell)
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			t.Setenv("ENV", "")
			t.Setenv("PROMPT_COMMAND", "")
			t.Setenv("PS1", "status-ready> ")
			require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte(`PS1='status-ready> '
PROMPT_COMMAND=''
`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".zshrc"), []byte(`PROMPT='status-ready> '
precmd_functions=()
preexec_functions=()
`), 0o600))
			cwd, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			nextCWD := filepath.Join(cwd, "next directory")
			require.NoError(t, os.Mkdir(nextCWD, 0o700))
			manager := newWorkspaceTerminalManager(t.Context(), cwd)
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			opened, err := manager.Open(t.Context(), 24, 80)
			require.NoError(t, err)
			session, err := manager.session(opened.SessionID)
			require.NoError(t, err)
			cursor := opened.ReplayCursor
			var output strings.Builder
			read := func() protocol.WorkspaceTerminalReadResult {
				result, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{
					SessionID: opened.SessionID, Cursor: cursor, WaitMS: 10,
				})
				require.NoError(t, readErr)
				require.NotNil(t, result.Status)
				cursor = result.NextCursor
				output.Write(result.Data)
				return result
			}
			write := func(input string) {
				require.NoError(t, manager.Write(t.Context(), protocol.WorkspaceTerminalInputParams{
					SessionID: opened.SessionID, Data: []byte(input),
				}))
			}
			require.Eventually(t, func() bool {
				read()
				return strings.Contains(output.String(), "status-ready> ")
			}, 5*time.Second, 10*time.Millisecond)
			assert.Equal(t, cwd, read().Status.CWD)
			assert.Equal(t, resolvedHome, read().Status.HomeDir, "home comes from the runner environment")
			write("cd " + strconv.Quote(nextCWD) + "\n")
			require.Eventually(t, func() bool {
				return read().Status.CWD == nextCWD
			}, 5*time.Second, 10*time.Millisecond)
			assert.Equal(t, cwd, session.cwd, "observed cwd must not change session identity")
			assert.Equal(t, cwd, session.openResult().CWD)

			write("/bin/sleep 30\n")
			require.Eventually(t, func() bool {
				return read().Status.ForegroundCommand == "sleep 30"
			}, 5*time.Second, 10*time.Millisecond)
			write("\x03")
			require.Eventually(t, func() bool {
				return read().Status.ForegroundCommand == ""
			}, 5*time.Second, 10*time.Millisecond)
			write("/bin/sleep 2\n")
			require.Eventually(t, func() bool {
				return read().Status.ForegroundCommand == "sleep 2"
			}, 5*time.Second, 10*time.Millisecond)
			require.Eventually(t, func() bool {
				return read().Status.ForegroundCommand == ""
			}, 5*time.Second, 10*time.Millisecond, "normal command exit restores idle status")
			assert.NotContains(t, output.String(), "\x1b]", "directory and command changes need no OSC output")

			output.Reset()
			write("/bin/sleep 30 &\n")
			require.Eventually(t, func() bool {
				read()
				return strings.Contains(output.String(), "status-ready> ")
			}, 5*time.Second, 10*time.Millisecond)
			checked := session.statusChecked
			require.Eventually(t, func() bool {
				result := read()
				assert.Empty(t, result.Status.ForegroundCommand, "background job must not become the title")
				return session.statusChecked.After(checked)
			}, 5*time.Second, 10*time.Millisecond)
			write("kill %1; wait\n")

			// No shell integration or OSC output is needed to observe these changes.
			assert.NotContains(t, output.String(), "\x1b]")
			write("/bin/sleep 30\n")
			require.Eventually(t, func() bool {
				return read().Status.ForegroundCommand == "sleep 30"
			}, 5*time.Second, 10*time.Millisecond)
			foreground, err := session.foregroundProcessGroup()
			require.NoError(t, err)
			require.NotEqual(t, opened.PID, foreground)
			require.NoError(t, manager.Close())
			assert.True(t, workspaceTerminalDone(session.done))
			assert.Eventually(t, func() bool {
				return syscall.Kill(foreground, 0) == syscall.ESRCH
			}, time.Second, 10*time.Millisecond)
			assert.Equal(t, protocol.WorkspaceTerminalStatus{CWD: nextCWD, HomeDir: resolvedHome}, *session.readStatus(t.Context()))
		})
	}
}

func TestWorkspaceTerminalStatusCache(t *testing.T) {
	initial := protocol.WorkspaceTerminalStatus{CWD: "/last/cwd", ForegroundCommand: "sleep 30"}
	session := &workspaceTerminalSession{
		cwd: "/initial", status: initial, statusChecked: time.Now(),
		cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
	}
	checked := session.statusChecked
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			status := session.readStatus(t.Context())
			assert.Equal(t, initial, *status)
			status.CWD = "caller mutation"
		})
	}
	readers.Wait()
	assert.Equal(t, checked, session.statusChecked, "readers share the cached probe")
	assert.Equal(t, initial, session.status)

	// Expire the cache without sleeping; canceled observers still must not probe.
	checked = time.Now().Add(-workspaceTerminalStatusInterval)
	session.statusChecked = checked
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Equal(t, initial, *session.readStatus(ctx))
	assert.Equal(t, checked, session.statusChecked, "canceled observers do not probe")

	wantCWD, err := os.Getwd()
	require.NoError(t, err)
	wantCWD, err = filepath.EvalSymlinks(wantCWD)
	require.NoError(t, err)
	refreshed := session.readStatus(t.Context())
	assert.Equal(t, wantCWD, refreshed.CWD)
	assert.Equal(t, initial.ForegroundCommand, refreshed.ForegroundCommand, "failed ioctl retains foreground metadata")
	assert.True(t, session.statusChecked.After(checked))

	// A failed process lookup retains both the initial and last known directory.
	session.cmd.Process = &os.Process{Pid: -1}
	session.statusChecked = time.Time{}
	assert.Equal(t, *refreshed, *session.readStatus(t.Context()))
	session.status = protocol.WorkspaceTerminalStatus{}
	session.statusChecked = time.Time{}
	assert.Equal(t, session.cwd, session.readStatus(t.Context()).CWD)
}

func TestWorkspaceTerminalStatusReadWait(t *testing.T) {
	session := &workspaceTerminalSession{
		id: "test-session", cwd: "/initial", updated: make(chan struct{}),
	}
	manager := &workspaceTerminalManager{current: session}
	// A long-poll must return status for an otherwise silent terminal.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	started := time.Now()
	result, err := manager.Read(ctx, protocol.WorkspaceTerminalReadParams{
		SessionID: session.id, WaitMS: 20_000,
	})
	require.NoError(t, err)
	assert.Less(t, time.Since(started), 2*time.Second)
	assert.Empty(t, result.Data)
	require.NotNil(t, result.Status)
	assert.Equal(t, session.cwd, result.Status.CWD)
	assert.Empty(t, result.Status.ForegroundCommand)

	result, err = manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: session.id, Cursor: 1})
	assert.ErrorContains(t, err, "cursor is ahead")
	assert.Nil(t, result.Status)
}

func TestWorkspaceTerminalCommand(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "empty"},
		{name: "basename and arguments", args: []string{"/bin/sleep", "30"}, want: "sleep 30"},
		{name: "quoted argument", args: []string{"/bin/cat", "some file.txt"}, want: `cat "some file.txt"`},
		{name: "normalize whitespace", args: []string{" cmd ", "", " a\nb "}, want: `cmd "a b"`},
		{name: "unicode limit", args: []string{strings.Repeat("界", 101)}, want: strings.Repeat("界", 99) + "…"},
		{name: "exact limit", args: []string{strings.Repeat("界", 100)}, want: strings.Repeat("界", 100)},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, workspaceTerminalCommand(test.args))
		})
	}
}

func TestWorkspaceTerminalStatusJSON(t *testing.T) {
	legacy, err := json.Marshal(protocol.WorkspaceTerminalReadResult{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"nextCursor":0}`, string(legacy))
	current, err := json.Marshal(protocol.WorkspaceTerminalReadResult{
		Status: &protocol.WorkspaceTerminalStatus{CWD: "/tmp"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"nextCursor":0,"status":{"cwd":"/tmp","foregroundCommand":""}}`, string(current))
}
