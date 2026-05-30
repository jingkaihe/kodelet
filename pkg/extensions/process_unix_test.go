//go:build unix

package extensions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
