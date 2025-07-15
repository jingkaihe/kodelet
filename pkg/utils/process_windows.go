//go:build windows

package utils

import (
	"syscall"
)

var (
	DetachSysProcAttr = syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
)
