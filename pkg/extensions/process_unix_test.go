//go:build unix

package extensions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/osutil"
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

	var process *Process
	originalStderr := os.Stderr
	func() {
		os.Stderr = tty
		defer func() { os.Stderr = originalStderr }()

		process, err = StartProcess(context.Background(), Extension{
			ID:       "stderr",
			Name:     "stderr",
			ExecPath: execPath,
			Dir:      extDir,
		}, DefaultConfig(), rootDir)
	}()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, process.Close()) })

	result, err := process.Initialize(context.Background(), rootDir)
	require.NoError(t, err)
	assert.Equal(t, "env;stderr_tty=false", result.Name)
}

func TestProcessCloseUsesCommandCancelForProcessGroup(t *testing.T) {
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

	cancelCalled := false
	cmd.Cancel = func() error {
		cancelCalled = true
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	process := &Process{
		Extension: Extension{ID: "process-group"},
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
	}

	_ = process.Close()

	assert.True(t, cancelCalled)
	assert.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) == syscall.ESRCH
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
