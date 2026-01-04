//go:build windows

package osutil

import (
	"os"
	"os/exec"
	"syscall"
)

var DetachSysProcAttr = syscall.SysProcAttr{
	CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	HideWindow:    true,
}

// SetProcessGroup configures the command to run in its own process group.
// On Windows, this is a no-op as process groups work differently.
func SetProcessGroup(_ *exec.Cmd) {
	// No equivalent to Setpgid on Windows for foreground processes
}

// SetProcessGroupKill sets up a cancel function that kills the process.
// On Windows, we can only kill the main process, not the entire tree.
func SetProcessGroupKill(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Kill)
	}
}
