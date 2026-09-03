package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestProfileSettingsAndProfiles(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	withTempHomeAndCWD(t, home, repo)

	repoConfig := `
profile: repo-active
profiles:
  repo-only:
    provider: openai
  shared:
    provider: anthropic
  default:
    provider: ignored
`
	require.NoError(t, os.WriteFile(filepath.Join(repo, "kodelet-config.yaml"), []byte(repoConfig), 0o644))

	globalConfigPath := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(globalConfigPath), 0o755))
	globalConfig := `
profile: global-active
profiles:
  global-only:
    provider: anthropic
  shared:
    provider: openai
  default:
    provider: ignored
`
	require.NoError(t, os.WriteFile(globalConfigPath, []byte(globalConfig), 0o644))

	assert.Equal(t, "repo-active", llm.RepoProfileSetting())
	assert.Equal(t, "global-active", llm.GlobalProfileSetting())

	repoProfiles := llm.RepoProfiles()
	assert.Contains(t, repoProfiles, "repo-only")
	assert.Contains(t, repoProfiles, "shared")
	assert.NotContains(t, repoProfiles, "default")

	globalProfiles := llm.GlobalProfiles()
	assert.Contains(t, globalProfiles, "global-only")
	assert.Contains(t, globalProfiles, "shared")
	assert.NotContains(t, globalProfiles, "default")

	merged := mergeProfiles(globalProfiles, repoProfiles, nil)
	assert.Equal(t, ScopeSourceGlobal, merged["global-only"])
	assert.Equal(t, ScopeSourceRepo, merged["repo-only"])
	assert.Equal(t, ScopeSourceBoth, merged["shared"])
	assert.Equal(t, merged, getMergedProfiles())
}

func TestMergeProfilesExplicitOverrideWins(t *testing.T) {
	globalProfiles := map[string]llmtypes.ProfileConfig{
		"global-only": {"provider": "anthropic"},
		"shared":      {"provider": "anthropic"},
		"both":        {"provider": "anthropic"},
	}
	repoProfiles := map[string]llmtypes.ProfileConfig{
		"repo-only": {"provider": "openai"},
		"shared":    {"provider": "openai"},
		"both":      {"provider": "openai"},
	}
	overrideProfiles := map[string]llmtypes.ProfileConfig{
		"override-only": {"provider": "openai"},
		"shared":        {"provider": "openai"},
	}

	merged := mergeProfiles(globalProfiles, repoProfiles, overrideProfiles)

	assert.Equal(t, ScopeSourceGlobal, merged["global-only"])
	assert.Equal(t, ScopeSourceRepo, merged["repo-only"])
	assert.Equal(t, ScopeSourceBoth, merged["both"])
	assert.Equal(t, ScopeSourceOverride, merged["override-only"])
	assert.Equal(t, ScopeSourceOverride, merged["shared"])
}

func TestProfileMissingConfigReturnsEmptyValues(t *testing.T) {
	withTempHomeAndCWD(t, t.TempDir(), t.TempDir())

	assert.Empty(t, llm.RepoProfileSetting())
	assert.Empty(t, llm.GlobalProfileSetting())
	assert.Nil(t, llm.RepoProfiles())
	assert.Nil(t, llm.GlobalProfiles())
}

func TestProfileHelpers(t *testing.T) {
	home := t.TempDir()
	withTempHomeAndCWD(t, home, t.TempDir())

	repoPath, err := getConfigFilePath(false)
	require.NoError(t, err)
	assert.Equal(t, "./kodelet-config.yaml", repoPath)

	globalPath, err := getConfigFilePath(true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kodelet", "config.yaml"), globalPath)

	assert.Equal(t, "Switched to default configuration in repo config", getProfileSwitchMessage("default", false))
	assert.Equal(t, "Switched to profile 'fast' in global config", getProfileSwitchMessage("fast", true))
}

func TestEnsureProfileSelectionWritable(t *testing.T) {
	writeOverride := func(t *testing.T, contents string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
		return path
	}

	t.Run("allows normal config", func(t *testing.T) {
		t.Setenv(llm.ConfigFileEnv, "")
		t.Setenv(llm.ConfigFileModeEnv, "")
		require.NoError(t, ensureProfileSelectionWritable(false))
	})

	t.Run("rejects isolated config", func(t *testing.T) {
		path := writeOverride(t, "profiles:\n  work:\n    provider: openai\n")
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)

		err := ensureProfileSelectionWritable(false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), llm.ConfigFileModeIsolated)
		assert.Contains(t, err.Error(), path)
	})

	t.Run("rejects override profile setting", func(t *testing.T) {
		path := writeOverride(t, "profile: locked\n")
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)

		err := ensureProfileSelectionWritable(false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), path)
	})

	t.Run("rejects explicit null override profile", func(t *testing.T) {
		path := writeOverride(t, "profile: null\n")
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)

		require.Error(t, ensureProfileSelectionWritable(false))
	})

	t.Run("allows override without profile setting", func(t *testing.T) {
		path := writeOverride(t, "profiles:\n  work:\n    provider: openai\n")
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)

		require.NoError(t, ensureProfileSelectionWritable(false))
	})

	t.Run("allows isolated override when updating the same repo file", func(t *testing.T) {
		repo := t.TempDir()
		t.Chdir(repo)
		path := filepath.Join(repo, llm.RepoConfigFile)
		require.NoError(t, os.WriteFile(path, []byte("profile: locked\n"), 0o644))
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)

		require.NoError(t, ensureProfileSelectionWritable(false))
	})

	t.Run("allows override when updating the same global file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		path := filepath.Join(home, ".kodelet", "config.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("profile: locked\n"), 0o644))
		t.Setenv(llm.ConfigFileEnv, path)
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)

		require.NoError(t, ensureProfileSelectionWritable(true))
	})
}

func TestUpdateProfileInConfig(t *testing.T) {
	t.Run("creates missing repo config", func(t *testing.T) {
		repo := t.TempDir()
		withTempHomeAndCWD(t, t.TempDir(), repo)

		require.NoError(t, updateProfileInConfig(false, "dev"))

		var config map[string]any
		data, err := os.ReadFile(filepath.Join(repo, "kodelet-config.yaml"))
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(data, &config))
		assert.Equal(t, "dev", config["profile"])
	})

	t.Run("updates existing global config and preserves fields", func(t *testing.T) {
		home := t.TempDir()
		withTempHomeAndCWD(t, home, t.TempDir())
		path := filepath.Join(home, ".kodelet", "config.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("provider: openai\nprofile: old\n"), 0o644))

		require.NoError(t, updateProfileInConfig(true, "new"))

		var config map[string]any
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(data, &config))
		assert.Equal(t, "new", config["profile"])
		assert.Equal(t, "openai", config["provider"])
		if runtime.GOOS != "windows" {
			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		}
	})

	t.Run("invalid yaml returns parse error", func(t *testing.T) {
		repo := t.TempDir()
		withTempHomeAndCWD(t, t.TempDir(), repo)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "kodelet-config.yaml"), []byte("profile: ["), 0o644))

		err := updateProfileInConfig(false, "dev")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse config file")
	})
}

func TestWriteYAMLConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	require.NoError(t, writeYAMLConfig(path, map[string]any{
		"profile": "dev",
		"profiles": map[string]llmtypes.ProfileConfig{
			"dev": {"provider": "openai"},
		},
	}, false))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "profile: dev")
	assert.Contains(t, string(data), "provider: openai")
}

func withTempHomeAndCWD(t *testing.T, home, cwd string) {
	t.Helper()

	t.Setenv("HOME", home)
	t.Setenv(llm.ConfigFileEnv, "")
	t.Setenv(llm.ConfigFileModeEnv, "")
	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldCWD))
	})
}
