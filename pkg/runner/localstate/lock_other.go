//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package localstate

import (
	"os"

	"github.com/pkg/errors"
)

func tryLockFile(*os.File) error {
	return errors.New("runner workspace locks are unsupported on this platform")
}

func unlockFile(*os.File) error { return nil }
