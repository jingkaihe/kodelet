//go:build windows

package osutil

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// GracefulShutdownDelay is the time to wait for graceful shutdown before force killing.
// On Windows, this is defined for API consistency but not used since Windows
// doesn't have the same signal semantics as Unix.
const GracefulShutdownDelay = 2 * time.Second

var DetachSysProcAttr = syscall.SysProcAttr{
	CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	HideWindow:    true,
}

// SetProcessGroup configures the command to run in its own process group.
// On Windows, this is a no-op as process groups work differently.
func SetProcessGroup(_ *exec.Cmd) {
	// No equivalent to Setpgid on Windows for foreground processes
}

// SetProcessGroupKill sets up a cancel function that terminates the process.
// On Windows, we can only terminate the main process directly; child processes
// may continue running as Windows doesn't have Unix-style process groups.
func SetProcessGroupKill(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Kill)
	}
}

// ForceKillProcessGroup immediately terminates the process and its descendants.
func ForceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	taskkill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	taskkill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return taskkill.Run()
}
