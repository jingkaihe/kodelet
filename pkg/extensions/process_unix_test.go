//go:build unix

package extensions

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessDoesNotExposeTerminalStderrToExtension(t *testing.T) {
	ptmx, tty, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})

	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "stderr")
	execPath := writeExecutable(t, filepath.Join(extDir, "kodelet-extension-stderr"), helperEnvExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	diagnosticLine := `{"level":"warn","extension":"mcp","message":"failed to initialize MCP server","server":"playwright","error":"spawn npxx ENOENT"}`
	t.Setenv("KODELET_TEST_EXTENSION_STDERR", diagnosticLine)

	var logOutput lockedBuffer
	testLogger := logrus.New()
	testLogger.SetOutput(&logOutput)
	sink := newRecordingDiagnosticSink()
	ctx := ContextWithDiagnosticSink(logger.WithLogger(context.Background(), logrus.NewEntry(testLogger)), sink)

	var process *Process
	originalStderr := os.Stderr
	func() {
		os.Stderr = tty
		defer func() { os.Stderr = originalStderr }()

		process, err = StartProcess(ctx, Extension{
			ID:       "stderr",
			Name:     "stderr",
			ExecPath: execPath,
			Dir:      extDir,
		}, DefaultConfig(), rootDir)
	}()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, process.Close()) })
	require.NotNil(t, process.cmd.SysProcAttr)
	assert.True(t, process.cmd.SysProcAttr.Setpgid)
	assert.Equal(t, extensionProcessWaitDelay, process.cmd.WaitDelay)

	result, err := process.Initialize(context.Background(), rootDir)
	require.NoError(t, err)
	assert.Equal(t, "env;stderr_tty=false", result.Name)
	assert.Eventually(t, func() bool {
		return strings.Contains(logOutput.String(), diagnosticLine)
	}, time.Second, 10*time.Millisecond)
	diagnostic := receiveDiagnostic(t, sink.ch)
	assert.Equal(t, DiagnosticLevelWarning, diagnostic.Level)
	assert.Equal(t, "mcp", diagnostic.Extension)
	assert.Equal(t, "playwright", diagnostic.Fields["server"])

	startTime := time.Now()
	require.NoError(t, process.Close())
	assert.Less(t, time.Since(startTime), time.Second, "started extension processes should close promptly")
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(payload)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestProcessCloseKillsProcessGroup(t *testing.T) {
	tempDir := t.TempDir()
	childPIDPath := filepath.Join(tempDir, "child.pid")
	cmd := exec.Command("bash", "-c", fmt.Sprintf("sleep 60 & echo $! > %q; wait", childPIDPath))
	osutil.SetProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	require.Eventually(t, func() bool {
		_, err := os.Stat(childPIDPath)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	childPID := readPID(t, childPIDPath)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	process := &Process{
		Extension: Extension{ID: "process-group"},
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
	}

	startTime := time.Now()
	require.NoError(t, process.Close())
	elapsed := time.Since(startTime)

	assert.Less(t, elapsed, osutil.GracefulShutdownDelay/2, "process groups should close promptly")
	assert.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) == syscall.ESRCH
	}, time.Second, 10*time.Millisecond)
}

func TestProcessLifetimeContextTerminatesProcess(t *testing.T) {
	rootDir := t.TempDir()
	extDir := filepath.Join(rootDir, "lifetime")
	execPath := writeExecutable(t, filepath.Join(extDir, "kodelet-extension-lifetime"), helperEnvExtensionScript(t))
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	process, err := StartProcess(runtimeCtx, Extension{
		ID:       "lifetime",
		Name:     "lifetime",
		ExecPath: execPath,
		Dir:      extDir,
	}, DefaultConfig(), rootDir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, process.Close()) })
	_, err = process.Initialize(context.Background(), rootDir)
	require.NoError(t, err)
	pid := process.cmd.Process.Pid

	cancelRuntime()

	assert.Eventually(t, func() bool {
		process.mu.Lock()
		defer process.mu.Unlock()
		return process.closed
	}, time.Second, 10*time.Millisecond)
	assert.Eventually(t, func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH
	}, time.Second, 10*time.Millisecond)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	return pid
}
