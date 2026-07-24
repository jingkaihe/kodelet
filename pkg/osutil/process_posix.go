//go:build unix

package osutil

import (
	"os/exec"
	"syscall"
	"time"
)

// GracefulShutdownDelay is the time to wait for graceful shutdown before sending SIGKILL.
// 2 seconds provides enough time for most processes to flush buffers, close connections,
// and perform cleanup, while not adding excessive delay after a timeout.
const GracefulShutdownDelay = 2 * time.Second

// DetachSysProcAttr provides syscall attributes for detaching processes on Unix systems
var DetachSysProcAttr = syscall.SysProcAttr{
	Setpgid: true, // Create a new process group
	Pgid:    0,    // Use the process's own PID as the process group ID
}

// SetProcessGroup configures the command to run in its own process group.
// This allows killing the entire process tree on timeout.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// SetProcessGroupKill sets up a cancel function that gracefully terminates
// the entire process group. It first sends SIGTERM to allow cleanup, waits
// briefly, then sends SIGKILL if processes are still running.
func SetProcessGroupKill(cmd *exec.Cmd) {
	setProcessGroupKill(cmd, GracefulShutdownDelay)
}

func setProcessGroupKill(cmd *exec.Cmd, gracefulShutdownDelay time.Duration) {
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid

		// Try graceful shutdown first with SIGTERM
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
			// Process group might already be dead, that's fine
			return nil
		}

		// Give processes time to cleanup.
		time.Sleep(gracefulShutdownDelay)

		// Check if any process in the group is still running (signal 0 = check only).
		if err := syscall.Kill(pgid, 0); err != nil {
			return nil
		}

		// Processes still running, force kill with SIGKILL.
		return syscall.Kill(pgid, syscall.SIGKILL)
	}
}

// ForceKillProcessGroup immediately kills the entire process group. Call it
// before waiting on the process so its process-group ID cannot be reused.
func ForceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
