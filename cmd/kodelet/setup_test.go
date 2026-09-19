package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecommendedSetupConfigYAML_SeparatesModelProfilesFromRunnerDefaults(t *testing.T) {
	sample, err := os.ReadFile("../../config.sample.yaml")
	require.NoError(t, err)
	for name, content := range map[string]string{
		"setup":  recommendedSetupConfigYAML(),
		"sample": string(sample),
	} {
		t.Run(name, func(t *testing.T) {
			previous := viper.AllSettings()
			viper.Reset()
			t.Cleanup(func() {
				viper.Reset()
				require.NoError(t, viper.MergeConfigMap(previous))
			})
			viper.SetConfigType("yaml")
			require.NoError(t, viper.ReadConfig(strings.NewReader(content)))
			require.NoError(t, llm.ValidateModelProfiles())
			config, err := llm.GetConfigFromViper()
			require.NoError(t, err)
			assert.Equal(t, viper.GetString("profile"), config.Profile)
			if name == "setup" {
				assert.Equal(t, "openai", config.Profile)
				assert.Equal(t, "patch", viper.GetString("tool_mode"))
				assert.False(t, viper.GetBool("enable_fs_search_tools"))
				require.Len(t, config.Profiles, 2)
				for _, profile := range []string{"openai", "anthropic"} {
					definition := config.Profiles[profile]
					require.NotNil(t, definition)
					assert.NotContains(t, definition, "tool_mode")
					assert.NotContains(t, definition, "enable_fs_search_tools")
					assert.Equal(t, profile, definition["provider"])
				}
			}
		})
	}
}

func TestSetupCommandCreatesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	runSetupCommandForTest(t, false)

	configPath := filepath.Join(home, ".kodelet", "config.yaml")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "provider: openai")
	assert.Contains(t, string(data), "profiles:")
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(configPath)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestSetupCommandSkipsExistingConfigWithoutOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte("profile: keep\n"), 0o644))

	runSetupCommandForTest(t, false)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "profile: keep\n", string(data))
}

func TestSetupCommandOverrideBacksUpExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	configPath := filepath.Join(home, ".kodelet", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte("profile: old\n"), 0o644))

	runSetupCommandForTest(t, true)

	backup, err := os.ReadFile(filepath.Join(home, ".kodelet", "config.yaml.bak"))
	require.NoError(t, err)
	assert.Equal(t, "profile: old\n", string(backup))

	updated, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "profile: openai")
	assert.Contains(t, string(updated), "reasoning_effort: xhigh")
	if runtime.GOOS != "windows" {
		for _, path := range []string{configPath, filepath.Join(home, ".kodelet", "config.yaml.bak")} {
			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		}
	}
}

func runSetupCommandForTest(t *testing.T, override bool) {
	t.Helper()

	cmd := &cobra.Command{Use: "setup"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("override", override, "")
	setupCmd.Run(cmd, nil)
}
