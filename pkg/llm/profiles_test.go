package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const overrideConfigContents = `
profile: override-active
profiles:
  override-only:
    provider: anthropic
  shared:
    provider: anthropic
  default:
    provider: ignored
`

const homeConfigContents = `
profile: home-active
profiles:
  home-only:
    provider: openai
  shared:
    provider: openai
`

// writeHomeConfig points HOME at a temporary directory, optionally seeding
// $HOME/.kodelet/config.yaml with the supplied contents.
func writeHomeConfig(t *testing.T, contents string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(ConfigFileEnv, "")
	t.Setenv(ConfigFileModeEnv, "")
	if contents == "" {
		return
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte(contents), 0o644))
}

func writeOverrideConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func writeRepoConfig(t *testing.T, contents string) {
	t.Helper()

	repo := t.TempDir()
	t.Chdir(repo)
	if contents != "" {
		require.NoError(t, os.WriteFile(filepath.Join(repo, RepoConfigFile), []byte(contents), 0o644))
	}
}

func TestGlobalProfilesWithoutOverride(t *testing.T) {
	writeHomeConfig(t, homeConfigContents)

	profiles := GlobalProfiles()
	assert.Contains(t, profiles, "home-only")
	assert.Contains(t, profiles, "shared")
	assert.Equal(t, "home-active", GlobalProfileSetting())
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "home-active", profile)
	assert.Equal(t, ProfileSourceGlobal, source)
}

// Regression test: with the config supplied solely through KODELET_CONFIG_FILE
// (as in the containerised control plane), profile discovery used to find
// nothing because it only ever read $HOME/.kodelet/config.yaml.
func TestOverrideProfilesFromOverrideOnly(t *testing.T) {
	writeHomeConfig(t, "")
	t.Setenv(ConfigFileEnv, writeOverrideConfig(t, overrideConfigContents))

	profiles := OverrideProfiles()
	assert.Contains(t, profiles, "override-only")
	assert.Contains(t, profiles, "shared")
	assert.NotContains(t, profiles, "default")
	assert.Nil(t, GlobalProfiles())
	profile, configured := OverrideProfileSetting()
	assert.True(t, configured)
	assert.Equal(t, "override-active", profile)
	activeProfile, source := ActiveProfileSetting()
	assert.Equal(t, "override-active", activeProfile)
	assert.Equal(t, ProfileSourceOverride, source)
}

func TestProfileLayersPreserveMergePrecedence(t *testing.T) {
	writeHomeConfig(t, homeConfigContents)
	writeRepoConfig(t, "profile: repo-active\nprofiles:\n  repo-only:\n    provider: openai\n  shared:\n    provider: repo\n")
	t.Setenv(ConfigFileEnv, writeOverrideConfig(t, overrideConfigContents))
	t.Setenv(ConfigFileModeEnv, ConfigFileModeMerge)

	assert.Contains(t, GlobalProfiles(), "home-only")
	assert.Equal(t, "openai", GlobalProfiles()["shared"]["provider"])
	assert.Contains(t, RepoProfiles(), "repo-only")
	assert.Equal(t, "repo", RepoProfiles()["shared"]["provider"])
	assert.Contains(t, OverrideProfiles(), "override-only")
	assert.Equal(t, "anthropic", OverrideProfiles()["shared"]["provider"])
	assert.False(t, UsesIsolatedConfigFile())
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "override-active", profile)
	assert.Equal(t, ProfileSourceOverride, source)
}

func TestIsolatedModeIgnoresHomeAndRepoConfig(t *testing.T) {
	writeHomeConfig(t, homeConfigContents)
	writeRepoConfig(t, "profile: repo-active\nprofiles:\n  repo-only:\n    provider: openai\n")
	t.Setenv(ConfigFileEnv, writeOverrideConfig(t, overrideConfigContents))
	t.Setenv(ConfigFileModeEnv, ConfigFileModeIsolated)

	profiles := OverrideProfiles()
	assert.Contains(t, profiles, "override-only")
	assert.Nil(t, GlobalProfiles())
	assert.Nil(t, RepoProfiles())
	assert.Empty(t, GlobalProfileSetting())
	assert.Empty(t, RepoProfileSetting())
	assert.True(t, UsesIsolatedConfigFile())
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "override-active", profile)
	assert.Equal(t, ProfileSourceOverride, source)
}

func TestProfileLayersMissingConfigsReturnEmpty(t *testing.T) {
	writeHomeConfig(t, "")
	writeRepoConfig(t, "")
	t.Setenv(ConfigFileEnv, filepath.Join(t.TempDir(), "missing.yaml"))

	assert.Nil(t, GlobalProfiles())
	assert.Nil(t, RepoProfiles())
	assert.Nil(t, OverrideProfiles())
	assert.Empty(t, GlobalProfileSetting())
	assert.Empty(t, RepoProfileSetting())
	_, configured := OverrideProfileSetting()
	assert.False(t, configured)
	profile, source := ActiveProfileSetting()
	assert.Empty(t, profile)
	assert.Empty(t, source)
}

func TestRepoProfilesReadRepositoryConfig(t *testing.T) {
	writeHomeConfig(t, "")
	writeRepoConfig(t, "profile: repo-active\nprofiles:\n  repo-only:\n    provider: openai\n")

	profiles := RepoProfiles()
	assert.Contains(t, profiles, "repo-only")
	assert.Equal(t, "repo-active", RepoProfileSetting())
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "repo-active", profile)
	assert.Equal(t, ProfileSourceRepo, source)
}

func TestGlobalProfilesDiscoverConfigYML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(ConfigFileEnv, "")
	t.Setenv(ConfigFileModeEnv, "")
	writeRepoConfig(t, "")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yml"), []byte("profile: yml-profile\nprofiles:\n  yml-profile:\n    provider: anthropic\n"), 0o644))

	assert.Contains(t, GlobalProfiles(), "yml-profile")
	assert.Equal(t, "yml-profile", GlobalProfileSetting())
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "yml-profile", profile)
	assert.Equal(t, ProfileSourceGlobal, source)
}

func TestOverrideProfilesMergeModeSupportsExtensionlessFile(t *testing.T) {
	writeHomeConfig(t, "")
	writeRepoConfig(t, "")
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(overrideConfigContents), 0o644))
	t.Setenv(ConfigFileEnv, path)
	t.Setenv(ConfigFileModeEnv, ConfigFileModeMerge)

	assert.Contains(t, OverrideProfiles(), "override-only")
	profile, source := ActiveProfileSetting()
	assert.Equal(t, "override-active", profile)
	assert.Equal(t, ProfileSourceOverride, source)
}

func TestOverrideProfileSettingDetectsExplicitNull(t *testing.T) {
	writeHomeConfig(t, homeConfigContents)
	writeRepoConfig(t, "profile: repo-active\n")
	t.Setenv(ConfigFileEnv, writeOverrideConfig(t, "profile: null\n"))

	profile, configured := OverrideProfileSetting()
	assert.True(t, configured)
	assert.Empty(t, profile)
	activeProfile, source := ActiveProfileSetting()
	assert.Empty(t, activeProfile)
	assert.Equal(t, ProfileSourceOverride, source)
}
