package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceWorkspaceGitDiff(t *testing.T) {
	workspace := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workspace
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("old\n"), 0o600))
	runGit("add", "file.txt")
	runGit("commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("new\n"), 0o600))

	service, err := NewService(t.Context(), workspace, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	result := callService[protocol.WorkspaceGitDiffResult](t, service, protocol.MethodWorkspaceGitDiff, protocol.WorkspaceGitDiffParams{})
	assert.Equal(t, workspace, result.CWD)
	assert.Equal(t, workspace, result.GitRoot)
	assert.True(t, result.HasDiff)
	assert.Contains(t, result.Diff, "diff --git a/file.txt b/file.txt")
	assert.False(t, result.Truncated)

	runGit("checkout", "--", "file.txt")
	clean := callService[protocol.WorkspaceGitDiffResult](t, service, protocol.MethodWorkspaceGitDiff, protocol.WorkspaceGitDiffParams{})
	assert.False(t, clean.HasDiff)
	assert.Empty(t, clean.Diff)
	assert.False(t, clean.Truncated)

	largeChange := strings.Repeat("changed workspace line\n", workspaceGitDiffLimit/len("changed workspace line\n")+1)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "file.txt"), []byte(largeChange), 0o600))
	truncated := callService[protocol.WorkspaceGitDiffResult](t, service, protocol.MethodWorkspaceGitDiff, protocol.WorkspaceGitDiffParams{})
	assert.True(t, truncated.HasDiff)
	assert.True(t, truncated.Truncated)
	assert.Len(t, truncated.Diff, workspaceGitDiffLimit)

	buffer := &cappedBuffer{limit: 4}
	written, err := buffer.Write([]byte("ab"))
	require.NoError(t, err)
	assert.Equal(t, 2, written)
	written, err = buffer.Write([]byte("cdef"))
	require.NoError(t, err)
	assert.Equal(t, 4, written)
	written, err = buffer.Write([]byte("g"))
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	assert.Equal(t, "abcd", buffer.String())
	assert.True(t, buffer.truncated)
}

func TestWorkspaceGitDiffErrors(t *testing.T) {
	var nilService *Service
	_, err := nilService.workspaceGitDiff(t.Context())
	require.ErrorContains(t, err, "runner service is required")

	closedService := &Service{closed: true}
	_, err = closedService.workspaceGitDiff(t.Context())
	require.ErrorContains(t, err, "runner service is closed")

	nonRepository := t.TempDir()
	_, err = resolveWorkspaceGitRoot(t.Context(), nonRepository)
	require.Error(t, err)

	_, exitCode, truncated, err := readWorkspaceGitDiff(t.Context(), nonRepository)
	require.Error(t, err)
	assert.NotZero(t, exitCode)
	assert.False(t, truncated)

	missingDirectory := filepath.Join(t.TempDir(), "missing")
	_, err = resolveWorkspaceGitRoot(t.Context(), missingDirectory)
	require.Error(t, err)

	_, exitCode, truncated, err = readWorkspaceGitDiff(t.Context(), missingDirectory)
	require.ErrorContains(t, err, "failed to execute git diff")
	assert.Zero(t, exitCode)
	assert.False(t, truncated)
}

func TestServiceWorkspaceTerminalPersistsAndStreamsWithoutActiveRun(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	opened := callService[protocol.WorkspaceTerminalOpenResult](t, service, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{Rows: 24, Cols: 80})
	require.NotEmpty(t, opened.SessionID)
	assert.Equal(t, "sh", opened.Name)
	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{
		SessionID: opened.SessionID,
		Data:      []byte("printf 'remote-terminal-ready\\n'\n"),
	})

	cursor := opened.ReplayCursor
	var output strings.Builder
	require.Eventually(t, func() bool {
		result := callService[protocol.WorkspaceTerminalReadResult](t, service, protocol.MethodWorkspaceTerminalRead, protocol.WorkspaceTerminalReadParams{
			SessionID: opened.SessionID,
			Cursor:    cursor,
			MaxBytes:  4096,
			WaitMS:    100,
		})
		cursor = result.NextCursor
		output.Write(result.Data)
		return strings.Contains(output.String(), "remote-terminal-ready")
	}, 3*time.Second, 10*time.Millisecond)

	reopened := callService[protocol.WorkspaceTerminalOpenResult](t, service, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{Rows: 30, Cols: 100})
	assert.Equal(t, opened.SessionID, reopened.SessionID)
	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{
		SessionID: opened.SessionID,
		Data:      []byte("printf 'final-terminal-output\\n'; exit 7\n"),
	})

	exited := false
	require.Eventually(t, func() bool {
		result := callService[protocol.WorkspaceTerminalReadResult](t, service, protocol.MethodWorkspaceTerminalRead, protocol.WorkspaceTerminalReadParams{
			SessionID: opened.SessionID,
			Cursor:    cursor,
			MaxBytes:  4096,
			WaitMS:    100,
		})
		cursor = result.NextCursor
		output.Write(result.Data)
		exited = result.Exited
		if exited {
			assert.Equal(t, 7, result.ExitCode)
		}
		return exited
	}, 3*time.Second, 10*time.Millisecond)
	assert.Contains(t, output.String(), "final-terminal-output")
}

func TestServiceWorkspaceTerminalResizeUsesRPCAndBounds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	t.Setenv("SHELL", "/bin/sh")
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	opened := callService[protocol.WorkspaceTerminalOpenResult](t, service, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{Rows: 24, Cols: 80})
	service.workspaceTerminals.mu.Lock()
	session := service.workspaceTerminals.current
	service.workspaceTerminals.mu.Unlock()
	require.NotNil(t, session)
	assertWorkspaceTerminalSize(t, session, 24, 80)

	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalResize, protocol.WorkspaceTerminalResizeParams{
		SessionID: opened.SessionID,
		Rows:      0,
		Cols:      workspaceTerminalMaxCols + 1,
	})
	assertWorkspaceTerminalSize(t, session, workspaceTerminalDefaultRows, workspaceTerminalMaxCols)

	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalResize, protocol.WorkspaceTerminalResizeParams{
		SessionID: opened.SessionID,
		Rows:      42,
		Cols:      120,
	})
	assertWorkspaceTerminalSize(t, session, 42, 120)

	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceTerminalResize, mustJSON(t, protocol.WorkspaceTerminalResizeParams{
		SessionID: "missing",
		Rows:      24,
		Cols:      80,
	}))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "terminal session was not found")
}

func TestServiceWorkspaceTerminalRejectsUnavailableManager(t *testing.T) {
	service := &Service{}
	tests := []struct {
		name   string
		method string
		params any
	}{
		{name: "open", method: protocol.MethodWorkspaceTerminalOpen, params: protocol.WorkspaceTerminalOpenParams{}},
		{name: "read", method: protocol.MethodWorkspaceTerminalRead, params: protocol.WorkspaceTerminalReadParams{SessionID: "terminal-1"}},
		{name: "input", method: protocol.MethodWorkspaceTerminalInput, params: protocol.WorkspaceTerminalInputParams{SessionID: "terminal-1", Data: []byte("x")}},
		{name: "resize", method: protocol.MethodWorkspaceTerminalResize, params: protocol.WorkspaceTerminalResizeParams{SessionID: "terminal-1", Rows: 24, Cols: 80}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, rpcErr := service.HandleRequest(t.Context(), test.method, mustJSON(t, test.params))
			require.NotNil(t, rpcErr)
			assert.Equal(t, protocol.ErrorCodeInternal, rpcErr.Code)
			assert.Equal(t, "workspace terminal is unavailable", rpcErr.Message)
		})
	}
}

func TestWorkspaceTerminalManagerValidatesRequests(t *testing.T) {
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{name: "read requires session", run: func() error {
			_, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{})
			return readErr
		}, want: "session id is required"},
		{name: "write requires session", run: func() error {
			return manager.Write(t.Context(), protocol.WorkspaceTerminalInputParams{Data: []byte("x")})
		}, want: "session id is required"},
		{name: "resize requires session", run: func() error {
			return manager.Resize(protocol.WorkspaceTerminalResizeParams{Rows: 24, Cols: 80})
		}, want: "session id is required"},
		{name: "missing session", run: func() error {
			_, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: "missing"})
			return readErr
		}, want: "session was not found"},
		{name: "oversized input", run: func() error {
			return manager.Write(t.Context(), protocol.WorkspaceTerminalInputParams{Data: make([]byte, workspaceTerminalMaxInputBytes+1)})
		}, want: "terminal input exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.run(), test.want)
		})
	}

	require.NoError(t, manager.Close())
	require.NoError(t, manager.Close())
	_, err := manager.Open(t.Context(), 24, 80)
	require.ErrorContains(t, err, "manager is closed")
	_, err = manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: "terminal-1"})
	require.ErrorContains(t, err, "manager is closed")
}

func TestWorkspaceTerminalManagerReplacesExitedSession(t *testing.T) {
	exitShell := filepath.Join(t.TempDir(), "exit-shell.sh")
	require.NoError(t, os.WriteFile(exitShell, []byte("#!/bin/sh\nexit 7\n"), 0o700))
	t.Setenv("SHELL", exitShell)
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())

	first, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	manager.mu.Lock()
	firstSession := manager.current
	manager.mu.Unlock()
	require.NotNil(t, firstSession)
	require.Eventually(t, func() bool { return workspaceTerminalDone(firstSession.done) }, 3*time.Second, 10*time.Millisecond)

	t.Setenv("SHELL", "/bin/sh")
	second, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	assert.NotEqual(t, first.SessionID, second.SessionID)
	_, err = manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: first.SessionID})
	require.ErrorContains(t, err, "session was not found")
	require.NoError(t, manager.Close())
}

func TestWorkspaceTerminalManagerRetainsSessionAfterCleanupFailure(t *testing.T) {
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	done := make(chan struct{})
	close(done)
	cleanupErr := errors.New("cleanup failed")
	failed := &workspaceTerminalSession{
		id:         "terminal-cleanup-failed",
		done:       done,
		cleanupErr: cleanupErr,
	}
	manager.current = failed

	_, err := manager.Open(t.Context(), 24, 80)
	require.ErrorContains(t, err, "previous workspace terminal cleanup is incomplete")
	require.ErrorIs(t, err, cleanupErr)
	assert.Same(t, failed, manager.current)

	err = manager.Close()
	require.ErrorContains(t, err, "failed to stop workspace terminal")
	require.ErrorIs(t, err, cleanupErr)
	assert.Nil(t, manager.current)
	require.ErrorIs(t, manager.Close(), cleanupErr)
}

func TestWorkspaceTerminalManagerReturnsShellStartErrors(t *testing.T) {
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())

	_, err := manager.Open(t.Context(), 24, 80)
	require.Error(t, err)
	require.NoError(t, manager.Close())
}

func TestWorkspaceTerminalOpenHonorsCanceledContext(t *testing.T) {
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := manager.Open(ctx, 24, 80)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, manager.Close())
}

func TestWorkspaceTerminalSessionIOEdgeCases(t *testing.T) {
	t.Run("read validates cursor and bounds chunks", func(t *testing.T) {
		session := &workspaceTerminalSession{updated: make(chan struct{}), done: make(chan struct{})}
		session.appendOutput([]byte("abc"))
		_, err := session.read(t.Context(), 4, 64, 1)
		require.ErrorContains(t, err, "cursor is ahead")

		bounded := &workspaceTerminalSession{updated: make(chan struct{}), done: make(chan struct{})}
		bounded.appendOutput(make([]byte, workspaceTerminalMaxReadBytes+1))
		result, err := bounded.read(t.Context(), 0, workspaceTerminalMaxReadBytes+1, 1)
		require.NoError(t, err)
		assert.Len(t, result.Data, workspaceTerminalMaxReadBytes)

		defaultBounded := &workspaceTerminalSession{updated: make(chan struct{}), done: make(chan struct{})}
		defaultBounded.appendOutput(make([]byte, workspaceTerminalDefaultReadBytes+1))
		result, err = defaultBounded.read(t.Context(), 0, 0, 1)
		require.NoError(t, err)
		assert.Len(t, result.Data, workspaceTerminalDefaultReadBytes)
	})

	t.Run("read reports an exited session without buffered output", func(t *testing.T) {
		session := &workspaceTerminalSession{
			updated:  make(chan struct{}),
			done:     make(chan struct{}),
			exited:   true,
			exitCode: 7,
		}
		result, err := session.read(nil, 0, 64, 1) //nolint:staticcheck // Verify a nil read context defaults to a background context.
		require.NoError(t, err)
		assert.True(t, result.Exited)
		assert.Equal(t, 7, result.ExitCode)
	})

	t.Run("write copies input and handles cancellation", func(t *testing.T) {
		sessionCtx, cancelSession := context.WithCancel(context.Background())
		defer cancelSession()
		session := &workspaceTerminalSession{
			ctx:     sessionCtx,
			done:    make(chan struct{}),
			inputCh: make(chan []byte, 1),
		}
		payload := []byte("abc")
		require.NoError(t, session.writeInput(nil, payload)) //nolint:staticcheck // Verify a nil input context defaults to a background context.
		payload[0] = 'x'
		assert.Equal(t, []byte("abc"), <-session.inputCh)
		require.NoError(t, session.writeInput(t.Context(), nil))

		blocked := &workspaceTerminalSession{
			ctx:     sessionCtx,
			done:    make(chan struct{}),
			inputCh: make(chan []byte),
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, blocked.writeInput(ctx, []byte("x")), context.Canceled)

		closedCtx, closeSession := context.WithCancel(context.Background())
		closeSession()
		closed := &workspaceTerminalSession{
			ctx:     closedCtx,
			done:    make(chan struct{}),
			inputCh: make(chan []byte),
		}
		require.ErrorContains(t, closed.writeInput(t.Context(), []byte("x")), "session is closed")

		closedDone := make(chan struct{})
		close(closedDone)
		finished := &workspaceTerminalSession{done: closedDone}
		require.ErrorContains(t, finished.writeInput(t.Context(), []byte("x")), "session is closed")
	})

	t.Run("resize rejects a closed session", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		session := &workspaceTerminalSession{done: done}
		require.ErrorContains(t, session.resize(24, 80), "session is closed")
	})
}

func TestWorkspaceTerminalManagerCloseIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	shell := filepath.Join(t.TempDir(), "ignore-hup.sh")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/bash\ntrap '' HUP TERM\nwhile :; do sleep 60; done\n"), 0o700))
	t.Setenv("SHELL", shell)
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	opened, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	require.NotEmpty(t, opened.SessionID)

	manager.mu.Lock()
	session := manager.current
	manager.mu.Unlock()
	require.NotNil(t, session)

	started := time.Now()
	require.NoError(t, manager.Close())
	assert.Less(t, time.Since(started), workspaceTerminalShutdownWait)
	assert.True(t, workspaceTerminalDone(session.done))
	require.NotNil(t, session.cmd.ProcessState)
	require.NoError(t, manager.Close())
}

func TestWorkspaceTerminalLeaderExitCompletesWhenDescendantHoldsPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	shell := filepath.Join(t.TempDir(), "exit-with-child.sh")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/bash\nset -m\ntrap '' HUP TERM\n(trap '' HUP TERM; while :; do sleep 60; done) &\necho $! > \"$KODELET_TERMINAL_CHILD_PID_FILE\"\nexit 7\n"), 0o700))
	t.Setenv("SHELL", shell)
	t.Setenv("KODELET_TERMINAL_CHILD_PID_FILE", childPIDPath)
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	opened, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	require.NotEmpty(t, opened.SessionID)
	t.Cleanup(func() { _ = manager.Close() })

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(childPIDPath)
		return statErr == nil
	}, time.Second, 10*time.Millisecond)
	childPIDData, err := os.ReadFile(childPIDPath)
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDData)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	cursor := opened.ReplayCursor
	var exitCode int
	require.Eventually(t, func() bool {
		result, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{
			SessionID: opened.SessionID,
			Cursor:    cursor,
			MaxBytes:  4096,
			WaitMS:    100,
		})
		if readErr != nil {
			return false
		}
		cursor = result.NextCursor
		exitCode = result.ExitCode
		return result.Exited
	}, 4*time.Second, 10*time.Millisecond)
	assert.Equal(t, 7, exitCode)
}

func TestWorkspaceTerminalLeaderExitKillsDescendantInShellProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	shell := filepath.Join(t.TempDir(), "exit-with-group-child.sh")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/bash\ntrap '' HUP TERM\n(trap '' HUP TERM; while :; do sleep 60; done) &\necho $! > \"$KODELET_TERMINAL_CHILD_PID_FILE\"\nsleep 0.2\nexit 7\n"), 0o700))
	t.Setenv("SHELL", shell)
	t.Setenv("KODELET_TERMINAL_CHILD_PID_FILE", childPIDPath)
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	opened, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)

	manager.mu.Lock()
	session := manager.current
	manager.mu.Unlock()
	require.NotNil(t, session)
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(childPIDPath)
		return statErr == nil
	}, time.Second, 10*time.Millisecond)
	childPIDData, err := os.ReadFile(childPIDPath)
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDData)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	childProcessGroup, err := syscall.Getpgid(childPID)
	require.NoError(t, err)
	assert.Equal(t, session.cmd.Process.Pid, childProcessGroup)

	cursor := opened.ReplayCursor
	require.Eventually(t, func() bool {
		result, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{
			SessionID: opened.SessionID,
			Cursor:    cursor,
			MaxBytes:  4096,
			WaitMS:    100,
		})
		if readErr != nil {
			return false
		}
		cursor = result.NextCursor
		return result.Exited && result.ExitCode == 7
	}, 4*time.Second, 10*time.Millisecond)
	assert.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH)
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, manager.Close())
}

func TestWorkspaceTerminalControlInputInterruptsForegroundProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	interruptedPath := filepath.Join(t.TempDir(), "interrupted")
	foreground := filepath.Join(t.TempDir(), "foreground.sh")
	require.NoError(t, os.WriteFile(foreground, []byte("#!/bin/bash\ntrap 'printf interrupted > \"$KODELET_TERMINAL_INTERRUPTED_FILE\"; exit 0' INT\nprintf ready > \"$KODELET_TERMINAL_READY_FILE\"\nwhile :; do sleep 60; done\n"), 0o700))
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("KODELET_TERMINAL_READY_FILE", readyPath)
	t.Setenv("KODELET_TERMINAL_INTERRUPTED_FILE", interruptedPath)
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	opened := callService[protocol.WorkspaceTerminalOpenResult](t, service, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{Rows: 24, Cols: 80})
	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{
		SessionID: opened.SessionID,
		Data:      []byte(strconv.Quote(foreground) + "\n"),
	})
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(readyPath)
		return statErr == nil
	}, time.Second, 10*time.Millisecond)

	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{
		SessionID: opened.SessionID,
		Data:      []byte{3},
	})
	require.Eventually(t, func() bool {
		payload, readErr := os.ReadFile(interruptedPath)
		return readErr == nil && string(payload) == "interrupted"
	}, time.Second, 10*time.Millisecond)
}

func TestWorkspaceTerminalReadReportsReplayTruncationAndCancellation(t *testing.T) {
	session := &workspaceTerminalSession{
		updated: make(chan struct{}),
		done:    make(chan struct{}),
	}
	session.appendOutput([]byte(strings.Repeat("x", workspaceTerminalReplayBufferLimit+32)))
	result, err := session.read(t.Context(), 0, 64, 1)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Len(t, result.Data, 64)
	assert.Equal(t, session.baseCursor+64, result.NextCursor)

	overflow := &workspaceTerminalSession{
		updated: make(chan struct{}),
		done:    make(chan struct{}),
	}
	overflow.appendOutput([]byte(strings.Repeat("a", workspaceTerminalReplayBufferLimit-2)))
	overflow.appendOutput([]byte("bcde"))
	assert.Len(t, overflow.buffer, workspaceTerminalReplayBufferLimit)
	assert.Equal(t, uint64(2), overflow.baseCursor)
	assert.Equal(t, uint64(workspaceTerminalReplayBufferLimit+2), overflow.writeCursor)
	assert.Equal(t, "bcde", string(overflow.buffer[len(overflow.buffer)-4:]))
	overflow.appendOutput(nil)
	overflow.exited = true
	overflow.appendOutput([]byte("ignored"))
	assert.Equal(t, uint64(workspaceTerminalReplayBufferLimit+2), overflow.writeCursor)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = session.read(ctx, session.writeCursor, 64, 1000)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWorkspaceTerminalValidationHelpers(t *testing.T) {
	assert.Equal(t, workspaceTerminalDefaultRows, boundedWorkspaceTerminalRows(0))
	assert.Equal(t, 42, boundedWorkspaceTerminalRows(42))
	assert.Equal(t, workspaceTerminalMaxRows, boundedWorkspaceTerminalRows(workspaceTerminalMaxRows+1))
	assert.Equal(t, workspaceTerminalDefaultCols, boundedWorkspaceTerminalCols(0))
	assert.Equal(t, 120, boundedWorkspaceTerminalCols(120))
	assert.Equal(t, workspaceTerminalMaxCols, boundedWorkspaceTerminalCols(workspaceTerminalMaxCols+1))
	assert.Equal(t, workspaceTerminalDefaultReadBytes, boundedWorkspaceTerminalReadBytes(0))
	assert.Equal(t, 1024, boundedWorkspaceTerminalReadBytes(1024))
	assert.Equal(t, workspaceTerminalMaxReadBytes, boundedWorkspaceTerminalReadBytes(workspaceTerminalMaxReadBytes+1))
	assert.Equal(t, workspaceTerminalDefaultReadWait, boundedWorkspaceTerminalReadWait(0))
	assert.Equal(t, 250*time.Millisecond, boundedWorkspaceTerminalReadWait(250))
	assert.Equal(t, workspaceTerminalMaxReadWait, boundedWorkspaceTerminalReadWait(int((workspaceTerminalMaxReadWait/time.Millisecond)+1)))

	first := errors.New("first")
	second := errors.New("second")
	assert.Equal(t, second, combineWorkspaceTerminalErrors(nil, second))
	assert.Equal(t, first, combineWorkspaceTerminalErrors(first, nil))
	combined := combineWorkspaceTerminalErrors(first, second)
	require.ErrorContains(t, combined, "first")
	require.ErrorContains(t, combined, "additional workspace terminal cleanup error: second")

	assert.True(t, workspaceTerminalDone(nil))
	open := make(chan struct{})
	assert.False(t, workspaceTerminalDone(open))
	assert.False(t, waitWorkspaceTerminalDone(open, time.Millisecond))
	close(open)
	assert.True(t, workspaceTerminalDone(open))
	assert.True(t, waitWorkspaceTerminalDone(open, time.Second))
	assert.True(t, waitWorkspaceTerminalDone(nil, time.Second))
	processDone := make(chan workspaceTerminalProcessResult, 1)
	processDone <- workspaceTerminalProcessResult{exitCode: 7}
	processResult, processExited := waitWorkspaceTerminalProcess(processDone, time.Second)
	assert.True(t, processExited)
	assert.Equal(t, 7, processResult.exitCode)
	_, processExited = waitWorkspaceTerminalProcess(make(chan workspaceTerminalProcessResult), time.Millisecond)
	assert.False(t, processExited)
	_, processExited = waitWorkspaceTerminalProcess(make(chan workspaceTerminalProcessResult), 0)
	assert.False(t, processExited)
	assert.True(t, waitWorkspaceTerminalProcessGroups(nil, time.Second))
	assert.True(t, workspaceTerminalProcessGroupsStopped(nil))
	stopWorkspaceTerminalTimer(nil)

	restoreEnvironmentVariable(t, "TERM")
	restoreEnvironmentVariable(t, "SHELL")
	require.NoError(t, os.Unsetenv("TERM"))
	require.NoError(t, os.Unsetenv("SHELL"))
	environment := workspaceTerminalEnv("/bin/test-shell")
	assert.Contains(t, environment, "TERM=xterm-256color")
	assert.Contains(t, environment, "SHELL=/bin/test-shell")
	shell, name := resolveWorkspaceTerminalShell()
	assert.Equal(t, "/bin/bash", shell)
	assert.Equal(t, "bash", name)
	require.NoError(t, os.Setenv("SHELL", string(os.PathSeparator)))
	shell, name = resolveWorkspaceTerminalShell()
	assert.Equal(t, string(os.PathSeparator), shell)
	assert.Equal(t, string(os.PathSeparator), name)
}

func assertWorkspaceTerminalSize(t *testing.T, session *workspaceTerminalSession, wantRows, wantCols int) {
	t.Helper()
	session.ptyMu.Lock()
	rows, cols, err := pty.Getsize(session.ptmx)
	session.ptyMu.Unlock()
	require.NoError(t, err)
	assert.Equal(t, wantRows, rows)
	assert.Equal(t, wantCols, cols)
}

func restoreEnvironmentVariable(t *testing.T, name string) {
	t.Helper()
	value, exists := os.LookupEnv(name)
	t.Cleanup(func() {
		if exists {
			require.NoError(t, os.Setenv(name, value))
			return
		}
		require.NoError(t, os.Unsetenv(name))
	})
}
