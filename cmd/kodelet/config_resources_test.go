package main

import (
	"os"
	"path/filepath"
	"testing"

	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceSettingsLoaderRefreshesOnlyResources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	t.Setenv("KODELET_EXTENSIONS_MAX_OUTPUT_SIZE", "2048")
	t.Setenv("KODELET_SKILLS_ENABLED", "false")
	path := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`
extensions:
  config:
    subagent:
      mailbox: enabled
skills:
  enabled: false
context:
  patterns: [TEAM.md]
profiles:
  deep:
    model: not-a-resource
serve:
  port: 1234
`), 0o600))
	before := viper.AllSettings()
	load, err := newResourceSettingsLoader(nil)
	require.NoError(t, err)
	initial, err := load()
	require.NoError(t, err)
	assert.Len(t, initial, 3)
	extensions := initial["extensions"].(map[string]any)
	assert.Equal(t, "2048", extensions["max_output_size"])
	assert.Equal(t, true, extensions["enabled"])
	assert.Equal(t, map[string]any{"subagent": map[string]any{"mailbox": "enabled"}}, extensions["config"])
	assert.Equal(t, map[string]any{"patterns": []any{"TEAM.md"}}, initial["context"])
	assert.Equal(t, map[string]any{"enabled": "false"}, initial["skills"])
	assert.Equal(t, before, viper.AllSettings())

	// File deletion resets defaults, while startup sources/environment stay fixed.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configFileEnv, filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("KODELET_EXTENSIONS_MAX_OUTPUT_SIZE", "8192")
	require.NoError(t, os.Remove(path))
	updated, err := load()
	require.NoError(t, err)
	assert.Len(t, updated, 3)
	assert.Equal(t, map[string]any{"enabled": "false"}, updated["skills"])
	assert.Equal(t, map[string]any{"patterns": []string{"AGENTS.md"}}, updated["context"])
	extensions = updated["extensions"].(map[string]any)
	assert.NotContains(t, extensions, "config")
	assert.Equal(t, "2048", extensions["max_output_size"])
	assert.Contains(t, initial, "context", "old snapshots remain intact")

	require.NoError(t, os.WriteFile(path, []byte("extensions: ["), 0o600))
	_, err = load()
	require.ErrorContains(t, err, "global resource configuration")
	require.NoError(t, os.WriteFile(path, []byte("serve:\n  auth_token: secret\n"), 0o600))
	require.NoError(t, os.Chmod(path, 0o644))
	_, err = load()
	require.ErrorContains(t, err, "static authentication tokens")
}

func TestResourceSettingsLoaderConfigFileModes(t *testing.T) {
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
			load, err := newResourceSettingsLoader(nil)
			require.NoError(t, err)
			t.Chdir(t.TempDir())
			for _, mailbox := range []string{"enabled", "disabled"} {
				settings, err := load()
				require.NoError(t, err)
				config := settings["extensions"].(map[string]any)["config"].(map[string]any)
				assert.Equal(t, map[string]any{"mailbox": mailbox}, config["subagent"])
				if mode == configFileModeMerge {
					assert.Contains(t, config, "other")
					assert.Contains(t, settings, "context")
				} else {
					assert.NotContains(t, config, "other")
					assert.Equal(t, map[string]any{"patterns": []string{"AGENTS.md"}}, settings["context"])
				}
				require.NoError(t, os.WriteFile(path, []byte(`{"extensions": {"config": {"subagent": {"mailbox": "disabled"}}}}`), 0o600))
			}
			require.NoError(t, os.Remove(path))
			_, err = load()
			require.ErrorContains(t, err, "configuration override")
		})
	}
}

func TestResourceSettingsLoaderPreservesContextOverrides(t *testing.T) {
	for _, explicitFlag := range []bool{false, true} {
		name := "environment"
		if explicitFlag {
			name = "flag"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(configFileEnv, "")
			t.Setenv(configFileModeEnv, configFileModeMerge)
			t.Setenv("KODELET_CONTEXT_PATTERNS", "ENV.md")
			flags := pflag.NewFlagSet("resources", pflag.ContinueOnError)
			flags.StringSlice("context-patterns", []string{"AGENTS.md"}, "")
			want := []string{"ENV.md"}
			if explicitFlag {
				require.NoError(t, flags.Set("context-patterns", "CLI.md"))
				want = []string{"CLI.md"}
			}
			resources, err := newResourceSettingsLoader(flags)
			require.NoError(t, err)
			standalone, err := runnerclient.NewWorkspaceConfigLoader(nil, resources)
			require.NoError(t, err)
			embedded, err := runnerclient.NewEmbeddedConfigLoader(nil, nil, resources)
			require.NoError(t, err)
			cwd := t.TempDir()
			for range 2 {
				config, err := standalone(cwd, "")
				require.NoError(t, err)
				assert.Equal(t, want, config.Context.Patterns)
				config, err = embedded(cwd, "", "")
				require.NoError(t, err)
				assert.Equal(t, want, config.Context.Patterns)
				require.NoError(t, flags.Set("context-patterns", "CHANGED.md"))
				t.Setenv("KODELET_CONTEXT_PATTERNS", "CHANGED.md")
				dir := filepath.Join(os.Getenv("HOME"), ".kodelet")
				require.NoError(t, os.MkdirAll(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("context:\n  patterns: [FILE.md]\n"), 0o600))
			}
		})
	}
}
