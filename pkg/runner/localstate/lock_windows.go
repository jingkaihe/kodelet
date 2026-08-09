//go:build windows

package localstate

import (
	"os"

	"golang.org/x/sys/windows"
)

// Keep the mandatory Windows byte-range lock far beyond the diagnostic JSON so
// other processes can inspect metadata while the runner owns the lock.
func workspaceLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: 1}
}

func tryLockFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		workspaceLockOverlapped(),
	)
}

func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, workspaceLockOverlapped())
}
