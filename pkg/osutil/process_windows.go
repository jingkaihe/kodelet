//go:build windows

package osutil

import (
	"syscall"
)

var DetachSysProcAttr = syscall.SysProcAttr{
	CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	HideWindow:    true,
}
