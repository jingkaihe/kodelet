package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceSettingsRefreshOnlyResources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	t.Setenv("KODELET_SKILLS_ENABLED", "false")
	path := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`
extensions:
  config:
    subagent:
      mailbox: enabled
skills:
  enabled: true
context:
  patterns: [TEAM.md]
profiles:
  deep:
    model: not-a-resource
serve:
  port: 1234
`), 0o600))
	before := viper.AllSettings()
	resources, err := newResourceSettings(nil)
	require.NoError(t, err)
	initial, err := resources.Load()
	require.NoError(t, err)
	assert.Len(t, initial, 3)
	extensions := initial["extensions"].(map[string]any)
	assert.Equal(t, map[string]any{"subagent": map[string]any{"mailbox": "enabled"}}, extensions["config"])
	assert.Equal(t, map[string]any{"patterns": []any{"TEAM.md"}}, initial["context"])
	assert.Equal(t, map[string]any{"enabled": "false"}, initial["skills"])
	assert.Equal(t, before, viper.AllSettings())

	// File deletion resets defaults, while startup sources/environment stay fixed.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configFileEnv, filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("KODELET_SKILLS_ENABLED", "true")
	require.NoError(t, os.Remove(path))
	updated, err := resources.Load()
	require.NoError(t, err)
	assert.Len(t, updated, 3)
	assert.Equal(t, map[string]any{"enabled": "false"}, updated["skills"])
	assert.Equal(t, map[string]any{"patterns": []string{"AGENTS.md"}}, updated["context"])
	extensions = updated["extensions"].(map[string]any)
	assert.NotContains(t, extensions, "config")

	// Invalid files fail strict refreshes but routine loads keep the last valid settings.
	require.NoError(t, os.WriteFile(path, []byte("extensions: ["), 0o600))
	require.ErrorContains(t, resources.Refresh(), "global config file")
	fallback, err := resources.Load()
	require.NoError(t, err)
	assert.Equal(t, updated, fallback)
	require.NoError(t, os.WriteFile(path, []byte("serve:\n  auth_token: secret\n"), 0o600))
	require.NoError(t, os.Chmod(path, 0o644))
	require.ErrorContains(t, resources.Refresh(), "static authentication tokens")
	fallback, err = resources.Load()
	require.NoError(t, err)
	assert.Equal(t, updated, fallback)
}

func TestResourceSettingsRejectInvalidStartupConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	path := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("extensions: ["), 0o600))
	_, err := newResourceSettings(nil)
	require.ErrorContains(t, err, path)
}

func TestResourceSettingsConfigFileModes(t *testing.T) {
	for _, mode := range []string{configFileModeMerge, configFileModeIsolate} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv(configFileModeEnv, mode)
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o700))
			global := filepath.Join(home, ".kodelet", "config.yaml")
			require.NoError(t, os.WriteFile(global, []byte(`
extensions:
  config:
    subagent:
      mailbox: disabled
    other:
      value: global
context:
  patterns: [GLOBAL.md]
`), 0o600))
			// Freeze a relative override against the startup CWD, not execution CWD.
			dir := t.TempDir()
			t.Chdir(dir)
			path := filepath.Join(dir, "override.json")
			t.Setenv(configFileEnv, "override.json")
			require.NoError(t, os.WriteFile(path, []byte(`{"extensions": {"config": {"subagent": {"mailbox": "enabled"}}}}`), 0o600))
			if mode == configFileModeIsolate {
				t.Setenv("HOME", "")
			}
			resources, err := newResourceSettings(nil)
			require.NoError(t, err)
			t.Chdir(t.TempDir())
			settings, err := resources.Load()
			require.NoError(t, err)
			config := settings["extensions"].(map[string]any)["config"].(map[string]any)
			assert.Equal(t, map[string]any{"mailbox": "enabled"}, config["subagent"])
			if mode == configFileModeMerge {
				assert.Contains(t, config, "other")
				assert.Equal(t, map[string]any{"patterns": []any{"GLOBAL.md"}}, settings["context"])
			} else {
				assert.NotContains(t, config, "other")
				assert.Equal(t, map[string]any{"patterns": []string{"AGENTS.md"}}, settings["context"])
			}
			require.NoError(t, os.WriteFile(path, []byte(`{"extensions": {"config": {"subagent": {"mailbox": "disabled"}}}}`), 0o600))
			settings, err = resources.Load()
			require.NoError(t, err)
			config = settings["extensions"].(map[string]any)["config"].(map[string]any)
			assert.Equal(t, map[string]any{"mailbox": "disabled"}, config["subagent"])
			require.NoError(t, os.Remove(path))
			require.ErrorContains(t, resources.Refresh(), "explicit config file")
			fallback, err := resources.Load()
			require.NoError(t, err)
			assert.Equal(t, settings, fallback)
		})
	}
}

func TestResourceSettingsApplyEnvironmentToKeysAddedAfterStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	t.Setenv("KODELET_EXTENSIONS_CONFIG_SUBAGENT_MAILBOX", "env")
	resources, err := newResourceSettings(nil)
	require.NoError(t, err)
	initial, err := resources.Load()
	require.NoError(t, err)
	assert.NotContains(t, initial["extensions"], "config", "like AutomaticEnv, environment variables do not invent keys")

	path := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`
extensions:
  config:
    subagent:
      mailbox: file
      other: file
`), 0o600))
	updated, err := resources.Load()
	require.NoError(t, err)
	config := updated["extensions"].(map[string]any)["config"].(map[string]any)
	assert.Equal(t, map[string]any{"mailbox": "env", "other": "file"}, config["subagent"])
}

func TestResourceSettingsPreserveContextOverrides(t *testing.T) {
	for _, name := range []string{"environment", "flag"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(configFileEnv, "")
			t.Setenv(configFileModeEnv, configFileModeMerge)
			t.Setenv("KODELET_CONTEXT_PATTERNS", "ENV.md")
			flags := pflag.NewFlagSet("resources", pflag.ContinueOnError)
			flags.StringSlice("context-patterns", []string{"AGENTS.md"}, "")
			var want any = "ENV.md"
			if name == "flag" {
				require.NoError(t, flags.Set("context-patterns", "CLI.md"))
				want = []string{"CLI.md"}
			}
			resources, err := newResourceSettings(flags)
			require.NoError(t, err)
			initial, err := resources.Load()
			require.NoError(t, err)
			assert.Equal(t, want, initial["context"].(map[string]any)["patterns"])

			require.NoError(t, flags.Set("context-patterns", "CHANGED.md"))
			t.Setenv("KODELET_CONTEXT_PATTERNS", "CHANGED.md")
			dir := filepath.Join(os.Getenv("HOME"), ".kodelet")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
context:
  patterns: [FILE.md]
`), 0o600))
			updated, err := resources.Load()
			require.NoError(t, err)
			assert.Equal(t, want, updated["context"].(map[string]any)["patterns"])
		})
	}
}
