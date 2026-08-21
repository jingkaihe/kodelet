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
	"golang.org/x/sys/unix"
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
		{name: "signal", method: protocol.MethodWorkspaceTerminalSignal, params: protocol.WorkspaceTerminalSignalParams{SessionID: "terminal-1", Name: "INT"}},
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
	var nilManager *workspaceTerminalManager
	_, err := nilManager.Open(t.Context(), 24, 80)
	require.ErrorContains(t, err, "manager is unavailable")
	_, err = nilManager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: "terminal-1"})
	require.ErrorContains(t, err, "manager is unavailable")
	require.ErrorContains(t, nilManager.Write(t.Context(), protocol.WorkspaceTerminalInputParams{SessionID: "terminal-1", Data: []byte("x")}), "manager is unavailable")
	require.ErrorContains(t, nilManager.Resize(protocol.WorkspaceTerminalResizeParams{SessionID: "terminal-1", Rows: 24, Cols: 80}), "manager is unavailable")
	require.ErrorContains(t, nilManager.Signal(t.Context(), protocol.WorkspaceTerminalSignalParams{SessionID: "terminal-1", Name: "INT"}), "manager is unavailable")
	require.NoError(t, nilManager.Close())

	backgroundManager := newWorkspaceTerminalManager(nil, t.TempDir()) //nolint:staticcheck // Verify a nil parent defaults to a background context.
	require.NoError(t, backgroundManager.Close())

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
		{name: "signal requires session", run: func() error {
			return manager.Signal(t.Context(), protocol.WorkspaceTerminalSignalParams{Name: "INT"})
		}, want: "session id is required"},
		{name: "missing session", run: func() error {
			_, readErr := manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: "missing"})
			return readErr
		}, want: "session was not found"},
		{name: "oversized input", run: func() error {
			return manager.Write(t.Context(), protocol.WorkspaceTerminalInputParams{Data: make([]byte, workspaceTerminalMaxInputBytes+1)})
		}, want: "terminal input exceeds"},
		{name: "unsupported signal", run: func() error {
			return manager.Signal(t.Context(), protocol.WorkspaceTerminalSignalParams{Name: "KILL"})
		}, want: "unsupported terminal signal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.run(), test.want)
		})
	}

	require.NoError(t, manager.Close())
	_, err = manager.Open(t.Context(), 24, 80)
	require.ErrorContains(t, err, "manager is closed")
	_, err = manager.Read(t.Context(), protocol.WorkspaceTerminalReadParams{SessionID: "terminal-1"})
	require.ErrorContains(t, err, "manager is closed")
}

func TestWorkspaceTerminalManagerReplacesExitedSessionAndReportsCleanupFailure(t *testing.T) {
	t.Run("reports incomplete stale session cleanup", func(t *testing.T) {
		manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
		done := make(chan struct{})
		close(done)
		stale := &workspaceTerminalSession{
			id:         "stale",
			done:       done,
			cleanupErr: errors.New("cleanup pending"),
		}
		manager.sessions[stale.id] = stale
		manager.current = stale

		_, err := manager.Open(t.Context(), 24, 80)
		require.ErrorContains(t, err, "previous workspace terminal cleanup is incomplete")
		require.ErrorContains(t, err, "cleanup pending")

		manager.sessions = make(map[string]*workspaceTerminalSession)
		manager.current = nil
		require.NoError(t, manager.Close())
	})

	t.Run("replaces an exited session", func(t *testing.T) {
		t.Setenv("SHELL", "/bin/sh")
		manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
		done := make(chan struct{})
		close(done)
		stale := &workspaceTerminalSession{id: "stale", done: done}
		manager.sessions[stale.id] = stale
		manager.current = stale

		opened, err := manager.Open(nil, 24, 80) //nolint:staticcheck // Verify a nil request context defaults to a background context.
		require.NoError(t, err)
		require.NotEqual(t, stale.id, opened.SessionID)
		assert.NotContains(t, manager.sessions, stale.id)
		require.NoError(t, manager.Close())
	})

	t.Run("returns shell start errors", func(t *testing.T) {
		t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
		manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())

		_, err := manager.Open(t.Context(), 24, 80)
		require.Error(t, err)
		require.NoError(t, manager.Close())
	})

	t.Run("returns resize errors from an existing live session", func(t *testing.T) {
		manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
		ptmx, err := os.CreateTemp(t.TempDir(), "closed-pty")
		require.NoError(t, err)
		require.NoError(t, ptmx.Close())
		session := &workspaceTerminalSession{
			id:   "terminal-live",
			ptmx: ptmx,
			done: make(chan struct{}),
		}
		manager.sessions[session.id] = session
		manager.current = session

		_, err = manager.Open(t.Context(), 24, 80)
		require.Error(t, err)

		manager.sessions = make(map[string]*workspaceTerminalSession)
		manager.current = nil
		require.NoError(t, manager.Close())
	})
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

	t.Run("signal handles success and cancellation while waiting", func(t *testing.T) {
		sessionCtx, cancelSession := context.WithCancel(context.Background())
		defer cancelSession()
		session := &workspaceTerminalSession{
			ctx:      sessionCtx,
			done:     make(chan struct{}),
			signalCh: make(chan workspaceTerminalSignalRequest),
		}
		receivedSignal := make(chan syscall.Signal, 1)
		go func() {
			request := <-session.signalCh
			receivedSignal <- request.signal
			request.result <- nil
		}()
		require.NoError(t, session.signal(nil, syscall.SIGTERM)) //nolint:staticcheck // Verify a nil signal context defaults to a background context.
		assert.Equal(t, syscall.SIGTERM, <-receivedSignal)

		signalErr := errors.New("signal failed")
		go func() {
			request := <-session.signalCh
			request.result <- signalErr
		}()
		require.ErrorIs(t, session.signal(t.Context(), syscall.SIGHUP), signalErr)

		canceledBeforeSend, cancelBeforeSend := context.WithCancel(t.Context())
		cancelBeforeSend()
		require.ErrorIs(t, session.signal(canceledBeforeSend, syscall.SIGINT), context.Canceled)

		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			result <- session.signal(ctx, syscall.SIGINT)
		}()
		<-session.signalCh
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)

		closedDone := make(chan struct{})
		close(closedDone)
		closed := &workspaceTerminalSession{done: closedDone}
		require.ErrorContains(t, closed.signal(t.Context(), syscall.SIGINT), "session is closed")

		doneWhileWaiting := make(chan struct{})
		waiting := &workspaceTerminalSession{
			ctx:      sessionCtx,
			done:     doneWhileWaiting,
			signalCh: make(chan workspaceTerminalSignalRequest),
		}
		result = make(chan error, 1)
		go func() {
			result <- waiting.signal(t.Context(), syscall.SIGINT)
		}()
		<-waiting.signalCh
		close(doneWhileWaiting)
		require.ErrorContains(t, <-result, "session is closed")
	})

	t.Run("resize rejects a closed session", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		session := &workspaceTerminalSession{done: done}
		require.ErrorContains(t, session.resize(24, 80), "session is closed")
	})
}

func TestWorkspaceTerminalManagerCloseReapsHUPIgnoringSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	shell := filepath.Join(t.TempDir(), "ignore-hup.sh")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/bash\nset -m\ntrap '' HUP TERM\n(trap '' HUP TERM; while :; do :; done) &\necho $! > \"$KODELET_TERMINAL_CHILD_PID_FILE\"\nkill -STOP $$\nwait\n"), 0o700))
	t.Setenv("SHELL", shell)
	t.Setenv("KODELET_TERMINAL_CHILD_PID_FILE", childPIDPath)
	manager := newWorkspaceTerminalManager(t.Context(), t.TempDir())
	opened, err := manager.Open(t.Context(), 24, 80)
	require.NoError(t, err)
	require.NotEmpty(t, opened.SessionID)

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
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})
	childSID, err := unix.Getsid(childPID)
	require.NoError(t, err)
	assert.Equal(t, session.cmd.Process.Pid, childSID)
	childPGID, err := unix.Getpgid(childPID)
	require.NoError(t, err)
	assert.NotEqual(t, session.cmd.Process.Pid, childPGID)

	started := time.Now()
	require.NoError(t, manager.Close())
	assert.Less(t, time.Since(started), workspaceTerminalShutdownWait)
	select {
	case <-session.done:
	default:
		t.Fatal("terminal session was not reaped before manager close returned")
	}
	require.NotNil(t, session.cmd.ProcessState)
	assert.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) == syscall.ESRCH
	}, time.Second, 10*time.Millisecond)
}

func TestWorkspaceTerminalLeaderExitReapsDescendantsHoldingPTY(t *testing.T) {
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
	assert.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) == syscall.ESRCH
	}, time.Second, 10*time.Millisecond)
}

func TestWorkspaceTerminalSignalTargetsForegroundProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY terminals are supported on Unix hosts")
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	signalPath := filepath.Join(t.TempDir(), "signal")
	foreground := filepath.Join(t.TempDir(), "foreground.sh")
	require.NoError(t, os.WriteFile(foreground, []byte("#!/bin/bash\ntrap 'printf hit > \"$KODELET_TERMINAL_SIGNAL_FILE\"; exit 0' INT\nprintf ready > \"$KODELET_TERMINAL_READY_FILE\"\nwhile :; do sleep 60; done\n"), 0o700))
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("KODELET_TERMINAL_READY_FILE", readyPath)
	t.Setenv("KODELET_TERMINAL_SIGNAL_FILE", signalPath)
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

	callService[struct{}](t, service, protocol.MethodWorkspaceTerminalSignal, protocol.WorkspaceTerminalSignalParams{
		SessionID: opened.SessionID,
		Name:      "INT",
	})
	require.Eventually(t, func() bool {
		payload, readErr := os.ReadFile(signalPath)
		return readErr == nil && string(payload) == "hit"
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
	for _, test := range []struct {
		name string
		want syscall.Signal
	}{
		{name: "INT", want: syscall.SIGINT},
		{name: "sigterm", want: syscall.SIGTERM},
		{name: " HUP ", want: syscall.SIGHUP},
		{name: "SIGQUIT", want: syscall.SIGQUIT},
	} {
		signal, ok := parseWorkspaceTerminalSignal(test.name)
		require.True(t, ok)
		assert.Equal(t, test.want, signal)
	}
	_, ok := parseWorkspaceTerminalSignal("KILL")
	assert.False(t, ok)

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
	var nilSession *workspaceTerminalSession
	require.NoError(t, nilSession.terminate())
	nilSession.requestShutdown()
	require.ErrorContains(t, terminateWorkspaceTerminalSession(0, 0), "session id is invalid")
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
