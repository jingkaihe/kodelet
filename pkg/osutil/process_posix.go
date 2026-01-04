//go:build unix

package osutil

import (
	"os/exec"
	"syscall"
)

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

// SetProcessGroupKill sets up a cancel function that kills the entire process group.
// Must be called after SetProcessGroup and before cmd.Start().
func SetProcessGroupKill(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
