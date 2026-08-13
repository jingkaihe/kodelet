//go:build !windows

package osutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsurePrivatePathsRestrictExistingModes(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.Chmod(directory, 0o755))

	require.NoError(t, EnsurePrivateDir(directory))
	info, err := os.Stat(directory)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	file := filepath.Join(directory, "secret")
	require.NoError(t, os.WriteFile(file, []byte("secret"), 0o644))
	require.NoError(t, os.Chmod(file, 0o644))

	require.NoError(t, EnsurePrivateFile(file))
	info, err = os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
