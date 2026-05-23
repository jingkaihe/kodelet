package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRecommendedSetupConfigYAML_OpenAIProfilesUsePatchMode(t *testing.T) {
	var config struct {
		Profiles map[string]map[string]any `yaml:"profiles"`
	}

	err := yaml.Unmarshal([]byte(recommendedSetupConfigYAML()), &config)
	require.NoError(t, err)

	openAIProfile, ok := config.Profiles["openai"]
	require.True(t, ok)
	assert.Equal(t, "patch", openAIProfile["tool_mode"])
	assert.Equal(t, true, openAIProfile["disable_fs_search_tools"])
	assert.Equal(t, "openai", openAIProfile["provider"])

	anthropicProfile, ok := config.Profiles["anthropic"]
	require.True(t, ok)
	assert.Equal(t, "anthropic", anthropicProfile["provider"])
	assert.Equal(t, "full", anthropicProfile["tool_mode"])
	assert.Equal(t, false, anthropicProfile["disable_fs_search_tools"])
	assert.Equal(t, 64000, anthropicProfile["max_tokens"])
	assert.Equal(t, "max", anthropicProfile["reasoning_effort"])
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
	assert.True(t, strings.Contains(string(updated), "profile: default"))
	assert.True(t, strings.Contains(string(updated), "reasoning_effort: xhigh"))
}

func runSetupCommandForTest(t *testing.T, override bool) {
	t.Helper()

	cmd := &cobra.Command{Use: "setup"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("override", override, "")
	setupCmd.Run(cmd, nil)
}
