//go:build windows

package userauth

import (
	"os"

	"golang.org/x/sys/windows"
)

func stateLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: 1}
}

func tryLockFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		stateLockOverlapped(),
	)
}

func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, stateLockOverlapped())
}
