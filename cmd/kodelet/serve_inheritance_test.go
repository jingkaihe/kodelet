package main

import (
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeInstallsPinnedEmbeddedProfileLoader(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		require.NoError(t, viper.MergeConfigMap(previous))
	})
	workspace := t.TempDir()
	viper.Set("profile", "deep")
	viper.Set("tool_mode", "full")
	viper.Set("model", "daemon-only-model")
	viper.Set("openai", map[string]any{"api_key": "daemon-only-secret"})
	viper.Set("context.patterns", []string{"TEAM.md"})
	viper.Set("profiles", map[string]any{
		"deep":  map[string]any{"tool_mode": "patch", "bash": map[string]any{"timeout": "30s"}},
		"flair": map[string]any{"tool_mode": "full", "enable_fs_search_tools": true},
	})
	viper.Set("environment_profiles", map[string]any{
		"review": map[string]any{"allowed_tools": []string{"bash"}},
	})
	viper.Set("serve", map[string]any{
		"embedded_runner": true, "runner_workspace": workspace,
		"runner_settings": map[string]any{"bash": map[string]any{"timeout": "17s"}},
	})
	server, err := buildControlPlaneServerConfig(getServeConfigFromFlags(newServeCommandForTest()))
	require.NoError(t, err)
	require.NotNil(t, server.EmbeddedRunner)
	loader := server.EmbeddedRunner.ServiceOptions.ProfileConfigLoader
	require.NotNil(t, loader)
	assert.Nil(t, server.EmbeddedRunner.ServiceOptions.ConfigLoader)
	assert.Nil(t, server.EmbeddedRunner.ServiceOptions.WorkspaceConfigLoader)

	// Later global changes cannot silently reconfigure the embedded runner.
	viper.Set("profile", "flair")
	viper.Set("profiles.deep.tool_mode", "full")
	viper.Set("serve.runner_settings.bash.timeout", "1s")
	for _, test := range []struct {
		profile string
		mode    llmtypes.ToolMode
		search  bool
	}{
		{"", llmtypes.ToolModePatch, false},
		{"deep", llmtypes.ToolModePatch, false},
		{"flair", llmtypes.ToolModeFull, true},
		{"default", llmtypes.ToolModeFull, false},
	} {
		config, err := loader(workspace, test.profile, "review")
		require.NoError(t, err)
		assert.Equal(t, test.mode, config.ToolMode)
		assert.Equal(t, test.search, config.EnableFSSearchTools)
		assert.Equal(t, 17*time.Second, config.BashTimeout())
		require.NotNil(t, config.Context)
		assert.Equal(t, []string{"TEAM.md"}, config.Context.Patterns)
		assert.Equal(t, []string{"bash"}, config.AllowedTools)
		assert.Empty(t, config.Model)
		assert.Empty(t, config.Provider)
		assert.Nil(t, config.OpenAI)
	}
}
