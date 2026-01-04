//go:build unix

package osutil

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetProcessGroup(t *testing.T) {
	cmd := exec.Command("echo", "test")
	SetProcessGroup(cmd)

	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setpgid, "Setpgid should be true")
}

func TestSetProcessGroupKill_GracefulShutdown(t *testing.T) {
	// This test verifies that a process responding to SIGTERM exits gracefully
	// without needing SIGKILL

	// Script that handles SIGTERM and exits cleanly
	script := `trap 'exit 0' TERM; while true; do sleep 0.1; done`

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	SetProcessGroup(cmd)
	SetProcessGroupKill(cmd)

	err := cmd.Start()
	require.NoError(t, err)

	// Give the process time to set up its trap handler
	time.Sleep(200 * time.Millisecond)

	// Cancel the context to trigger the Cancel function
	cancel()

	// Wait for the process to exit
	err = cmd.Wait()

	// The process should have exited (either from SIGTERM or context cancellation)
	// We don't check the exact error because it varies by how the process exits
	assert.Error(t, err, "Process should have been terminated")
}

func TestSetProcessGroupKill_ForceKillAfterTimeout(t *testing.T) {
	// This test verifies that a process ignoring SIGTERM gets SIGKILL
	// Note: This test takes ~2 seconds due to GracefulShutdownDelay

	if testing.Short() {
		t.Skip("Skipping test in short mode (takes ~2 seconds)")
	}

	// Script that ignores SIGTERM (but not SIGKILL)
	script := `trap '' TERM; while true; do sleep 0.1; done`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	SetProcessGroup(cmd)
	SetProcessGroupKill(cmd)

	err := cmd.Start()
	require.NoError(t, err)

	pid := cmd.Process.Pid

	// Give the process time to set up its trap handler
	time.Sleep(200 * time.Millisecond)

	// Verify process is running
	err = syscall.Kill(pid, 0)
	require.NoError(t, err, "Process should be running")

	startTime := time.Now()

	// Cancel the context to trigger the Cancel function
	cancel()

	// Wait for the process to exit
	_ = cmd.Wait()

	elapsed := time.Since(startTime)

	// Verify the process was killed (after the grace period)
	err = syscall.Kill(pid, 0)
	assert.Error(t, err, "Process should be terminated")

	// Verify it took approximately GracefulShutdownDelay
	// (with some tolerance for test execution overhead)
	assert.GreaterOrEqual(t, elapsed, GracefulShutdownDelay-100*time.Millisecond,
		"Should have waited for graceful shutdown delay")
}

func TestSetProcessGroupKill_KillsEntireProcessGroup(t *testing.T) {
	// This test verifies that child processes are also killed

	// Script that spawns a child process
	script := `
		# Spawn a child that also ignores SIGTERM
		(trap '' TERM; while true; do sleep 0.1; done) &
		CHILD_PID=$!
		echo "CHILD:$CHILD_PID"
		trap '' TERM
		while true; do sleep 0.1; done
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	SetProcessGroup(cmd)
	SetProcessGroupKill(cmd)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	parentPid := cmd.Process.Pid

	// Read the child PID from stdout
	buf := make([]byte, 100)
	n, err := stdout.Read(buf)
	require.NoError(t, err)

	var childPid int
	_, err = parseChildPid(string(buf[:n]), &childPid)
	require.NoError(t, err, "Failed to parse child PID from output: %s", string(buf[:n]))

	// Give processes time to start
	time.Sleep(200 * time.Millisecond)

	// Verify both processes are running
	err = syscall.Kill(parentPid, 0)
	require.NoError(t, err, "Parent process should be running")
	err = syscall.Kill(childPid, 0)
	require.NoError(t, err, "Child process should be running")

	// Cancel the context to trigger the Cancel function
	cancel()

	// Wait for the parent process to exit
	_ = cmd.Wait()

	// Give a moment for process cleanup
	time.Sleep(100 * time.Millisecond)

	// Verify both processes are terminated
	err = syscall.Kill(parentPid, 0)
	assert.Error(t, err, "Parent process should be terminated")
	err = syscall.Kill(childPid, 0)
	assert.Error(t, err, "Child process should be terminated")
}

func TestSetProcessGroupKill_ProcessAlreadyDead(t *testing.T) {
	// This test verifies that Cancel handles the case where the process
	// has already exited before Cancel is called

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "true") // Exits immediately
	SetProcessGroup(cmd)
	SetProcessGroupKill(cmd)

	err := cmd.Start()
	require.NoError(t, err)

	// Wait for process to complete naturally
	err = cmd.Wait()
	require.NoError(t, err)

	// Now call Cancel manually - it should not panic or error
	if cmd.Cancel != nil {
		err = cmd.Cancel()
		// Should return nil (process already dead, handled gracefully)
		assert.NoError(t, err, "Cancel should handle already-dead process gracefully")
	}
}

func TestGracefulShutdownDelay_Value(t *testing.T) {
	// Verify the delay is set to a reasonable value
	assert.Equal(t, 2*time.Second, GracefulShutdownDelay,
		"GracefulShutdownDelay should be 2 seconds")
}

// parseChildPid extracts child PID from output like "CHILD:12345\n"
func parseChildPid(output string, pid *int) (string, error) {
	prefix := "CHILD:"
	if len(output) < len(prefix) || output[:len(prefix)] != prefix {
		return output, os.ErrInvalid
	}

	rest := output[len(prefix):]
	var num int
	for i, c := range rest {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			if i == 0 {
				return output, os.ErrInvalid
			}
			break
		}
	}

	*pid = num
	return output, nil
}
