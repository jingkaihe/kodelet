package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
    provider: openai
    model: repo-default-model
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
    provider: anthropic
    model: global-default-model
`
	require.NoError(t, os.WriteFile(globalConfigPath, []byte(globalConfig), 0o644))

	assert.Equal(t, "repo-active", llm.RepoProfileSetting())
	assert.Equal(t, "global-active", llm.GlobalProfileSetting())

	repoProfiles := llm.RepoProfiles()
	assert.Contains(t, repoProfiles, "repo-only")
	assert.Contains(t, repoProfiles, "shared")
	assert.Contains(t, repoProfiles, "default")

	globalProfiles := llm.GlobalProfiles()
	assert.Contains(t, globalProfiles, "global-only")
	assert.Contains(t, globalProfiles, "shared")
	assert.Contains(t, globalProfiles, "default")

	sources := llm.ProfileSources()
	assert.Equal(t, llm.ProfileSourceGlobal, sources["global-only"])
	assert.Equal(t, llm.ProfileSourceRepo, sources["repo-only"])
	assert.Equal(t, llm.ProfileSourceRepoOverridesGlobal, sources["shared"])
	assert.Equal(t, llm.ProfileSourceRepoOverridesGlobal, sources["default"])
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

	assert.Equal(t, "Switched to profile 'default' in repo config", getProfileSwitchMessage("default", false))
	assert.Equal(t, "Switched to profile 'fast' in global config", getProfileSwitchMessage("fast", true))
}

func TestEffectiveProfileSetting(t *testing.T) {
	originalProfile := viper.GetString("profile")
	t.Cleanup(func() { viper.Set("profile", originalProfile) })

	t.Run("command-line flag takes precedence", func(t *testing.T) {
		cmd := &cobra.Command{Use: "current"}
		cmd.Flags().String("profile", "", "")
		require.NoError(t, cmd.Flags().Set("profile", "flag-profile"))
		t.Setenv(profileEnv, "environment-profile")
		viper.Set("profile", "flag-profile")

		profile, source := effectiveProfileSetting(cmd)

		assert.Equal(t, "flag-profile", profile)
		assert.Equal(t, "command-line flag", source)
	})

	t.Run("environment takes precedence over files", func(t *testing.T) {
		home := t.TempDir()
		repo := t.TempDir()
		withTempHomeAndCWD(t, home, repo)
		require.NoError(t, os.WriteFile(filepath.Join(repo, llm.RepoConfigFile), []byte("profile: repo-profile\n"), 0o644))
		t.Setenv(profileEnv, "environment-profile")
		viper.Set("profile", "environment-profile")

		profile, source := effectiveProfileSetting(&cobra.Command{})

		assert.Equal(t, "environment-profile", profile)
		assert.Equal(t, "environment", source)
	})

	t.Run("file source is retained without runtime override", func(t *testing.T) {
		home := t.TempDir()
		repo := t.TempDir()
		withTempHomeAndCWD(t, home, repo)
		require.NoError(t, os.WriteFile(filepath.Join(repo, llm.RepoConfigFile), []byte("profile: repo-profile\n"), 0o644))
		t.Setenv(profileEnv, "")
		viper.Set("profile", "repo-profile")

		profile, source := effectiveProfileSetting(&cobra.Command{})

		assert.Equal(t, "repo-profile", profile)
		assert.Equal(t, "repo config", source)
	})
}

func TestProfileCommandsUseOnlyNamedProfiles(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		require.NoError(t, viper.MergeConfigMap(previous))
	})
	home := t.TempDir()
	withTempHomeAndCWD(t, home, t.TempDir())
	t.Setenv(profileEnv, "")
	const content = `profile: flair
extensions:
  enabled: true
profiles:
  flair:
    provider: openai
    model: flair-model
  deep:
    provider: openai
    model: deep-model
`
	path := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	var settings map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &settings))
	require.NoError(t, viper.MergeConfigMap(settings))

	t.Run("list marks configured default separately from override", func(t *testing.T) {
		var output bytes.Buffer
		cmd := &cobra.Command{Use: "list"}
		cmd.SetOut(&output)
		cmd.Flags().String("profile", "", "")
		require.NoError(t, cmd.Flags().Set("profile", "deep"))
		viper.Set("profile", "deep")
		t.Cleanup(func() { viper.Set("profile", "flair") })

		require.NoError(t, profileListCmd.RunE(cmd, nil))
		assert.Regexp(t, `flair\s+global\s+Default`, output.String())
		assert.Regexp(t, `deep\s+global\s+Selected \(command-line flag\)`, output.String())
		assert.NotContains(t, output.String(), "built-in")
		assert.NotRegexp(t, `(?m)^default\s`, output.String())
	})

	for _, test := range []struct{ profile, wantError string }{
		{"", "set profile: <name>"},
		{"missing", "profile 'missing' not found"},
	} {
		t.Run("invalid selection "+test.profile, func(t *testing.T) {
			viper.Set("profile", test.profile)
			t.Cleanup(func() { viper.Set("profile", "flair") })
			require.ErrorContains(t, profileCurrentCmd.RunE(&cobra.Command{}, nil), test.wantError)
		})
	}

	t.Run("default is not an implicit profile", func(t *testing.T) {
		cmd := &cobra.Command{Use: "show"}
		cmd.Flags().String("format", "json", "")
		require.ErrorContains(t, profileShowCmd.RunE(cmd, []string{"default"}), "profile 'default' not found")
		cmd = &cobra.Command{Use: "use"}
		cmd.Flags().Bool("global", true, "")
		require.ErrorContains(t, profileUseCmd.RunE(cmd, []string{"default"}), "profile 'default' not found")
	})

	t.Run("explicit default profile can be shown", func(t *testing.T) {
		profiles := settings["profiles"].(map[string]any)
		profiles["default"] = map[string]any{"provider": "openai", "model": "named-default-model"}
		viper.Set("profiles", profiles)
		require.NoError(t, writeYAMLConfig(path, settings, true))
		var output bytes.Buffer
		cmd := &cobra.Command{Use: "show"}
		cmd.Flags().String("format", "json", "")
		cmd.SetOut(&output)
		require.NoError(t, profileShowCmd.RunE(cmd, []string{"default"}))
		var shown struct {
			Profile string `json:"profile"`
			Model   string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(output.Bytes(), &shown))
		assert.Equal(t, "default", shown.Profile)
		assert.Equal(t, "named-default-model", shown.Model)
	})
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
		require.NoError(t, os.WriteFile(path, []byte("extensions:\n  enabled: true\nprofile: old\n"), 0o644))

		require.NoError(t, updateProfileInConfig(true, "new"))

		var config map[string]any
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(data, &config))
		assert.Equal(t, "new", config["profile"])
		assert.Equal(t, map[string]any{"enabled": true}, config["extensions"])
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
