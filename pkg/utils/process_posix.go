//go:build unix

package utils

import "syscall"

// DetachSysProcAttr provides syscall attributes for detaching processes on Unix systems
var DetachSysProcAttr = syscall.SysProcAttr{
	Setpgid: true, // Create a new process group
	Pgid:    0,    // Use the process's own PID as the process group ID
}
