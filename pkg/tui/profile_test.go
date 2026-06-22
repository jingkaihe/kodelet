package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadProfileOptionsDefaultFirstThenSortedConfiguredProfiles(t *testing.T) {
	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	repoDir := t.TempDir()
	require.NoError(t, os.Chdir(repoDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldCWD))
	})

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".kodelet", "config.yaml"), []byte(`profiles:
  zeta:
    model: zeta-model
  shared:
    model: global-shared
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "kodelet-config.yaml"), []byte(`profiles:
  alpha:
    model: alpha-model
  shared:
    model: repo-shared
  default:
    model: ignored
`), 0o644))

	assert.Equal(t, []string{"default", "alpha", "shared", "zeta"}, loadProfileOptions())
}
