package osutil

import (
	"os"
	"strings"

	"github.com/pkg/errors"
)

// EnsurePrivateDir creates a directory and restricts it to the current user.
func EnsurePrivateDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("private directory path is required")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.Wrap(err, "failed to create private directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return errors.Wrap(err, "failed to set private directory mode")
	}
	if err := restrictPrivatePath(path, true); err != nil {
		return errors.Wrap(err, "failed to restrict private directory access")
	}
	return nil
}

// EnsurePrivateFile restricts an existing regular file to the current user.
func EnsurePrivateFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("private file path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return errors.Wrap(err, "failed to inspect private file")
	}
	if !info.Mode().IsRegular() {
		return errors.New("private file must be a regular file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return errors.Wrap(err, "failed to set private file mode")
	}
	if err := restrictPrivatePath(path, false); err != nil {
		return errors.Wrap(err, "failed to restrict private file access")
	}
	return nil
}
