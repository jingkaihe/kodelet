package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm"
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
	t.Setenv(llm.ConfigFileEnv, "")
	t.Setenv(llm.ConfigFileModeEnv, "")
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

func TestLoadProfileOptionsIncludesOverrideLastAndHonoursIsolatedMode(t *testing.T) {
	repoDir := t.TempDir()
	t.Chdir(repoDir)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".kodelet", "config.yaml"), []byte("profiles:\n  global-only:\n    provider: anthropic\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, llm.RepoConfigFile), []byte("profiles:\n  repo-only:\n    provider: openai\n"), 0o644))
	overridePath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte("profiles:\n  override-only:\n    provider: openai\n"), 0o644))
	t.Setenv(llm.ConfigFileEnv, overridePath)

	t.Run("merge", func(t *testing.T) {
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)
		assert.Equal(t, []string{"default", "global-only", "override-only", "repo-only"}, loadProfileOptions())
	})

	t.Run("isolated", func(t *testing.T) {
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)
		assert.Equal(t, []string{"default", "override-only"}, loadProfileOptions())
	})
}
