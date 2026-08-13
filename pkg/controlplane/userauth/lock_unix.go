//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package userauth

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
