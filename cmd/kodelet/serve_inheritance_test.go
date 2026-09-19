package main

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/browser"
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
	t.Setenv("KODELET_BROWSER_EXECUTABLE", "/host/chrome")
	t.Setenv("KODELET_BROWSER_DEVTOOLS_DIR", "/host/devtools")
	t.Setenv("KODELET_BROWSER_IDLE_TIMEOUT", "7m")
	viper.Set("profile", "deep")
	viper.Set("tool_mode", "full")
	viper.Set("openai", map[string]any{"api_key": "daemon-only-secret"})
	viper.Set("context.patterns", []string{"TEAM.md"})
	viper.Set("profiles", map[string]any{
		"deep": map[string]any{
			"provider": "openai", "model": "daemon-only-model",
			"tool_mode": "patch", "bash": map[string]any{"timeout": "30s"},
		},
		"flair": map[string]any{
			"provider": "openai", "model": "flair-model",
			"tool_mode": "full", "enable_fs_search_tools": true,
		},
		"default": map[string]any{
			"provider": "openai", "model": "named-default-model",
			"tool_mode": "patch", "enable_fs_search_tools": true,
		},
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
	assert.Equal(t, "/host/chrome", server.EmbeddedRunner.ServiceOptions.Browser.Executable)
	assert.Equal(t, "/host/devtools", server.EmbeddedRunner.ServiceOptions.Browser.DevToolsDir)
	assert.Equal(t, 7*time.Minute, server.EmbeddedRunner.ServiceOptions.Browser.IdleTimeout)
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
		{"", llmtypes.ToolModeFull, false},
		{"deep", llmtypes.ToolModePatch, false},
		{"flair", llmtypes.ToolModeFull, true},
		{"default", llmtypes.ToolModePatch, true},
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

func TestServePinsBrowserConfigAtStartup(t *testing.T) {
	previous := viper.AllSettings()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		require.NoError(t, viper.MergeConfigMap(previous))
	})
	t.Setenv("KODELET_BROWSER_EXECUTABLE", "")
	t.Setenv("KODELET_BROWSER_DEVTOOLS_DIR", "")
	t.Setenv("KODELET_BROWSER_IDLE_TIMEOUT", "")
	viper.Set("browser", map[string]any{"executable": "/yaml/chrome", "devtools_dir": "/yaml/devtools", "idle_timeout": "7m"})
	viper.Set("profiles.deep.browser.executable", "/profile/chrome")
	viper.Set("profiles.deep.provider", "openai")
	viper.Set("profiles.deep.model", "gpt-4.1")
	viper.Set("profile", "deep")
	viper.Set("serve.runner_settings.browser.executable", "/workspace/chrome")
	cmd := newServeCommandForTest()
	config := getServeConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	assert.Equal(t, "/yaml/chrome", config.Browser.Executable, "profiles and runner_settings cannot override the host browser")
	assert.False(t, config.BrowserEnabled, "configuring Chrome does not grant server access")
	require.NoError(t, cmd.Flags().Set("browser-executable", "/flag/chrome"))
	config = getServeConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	want := browser.Config{Executable: "/flag/chrome", DevToolsDir: "/yaml/devtools", IdleTimeout: 7 * time.Minute}
	assert.Equal(t, want, config.Browser)

	viper.Set("browser.executable", "/changed/chrome")
	t.Setenv("KODELET_BROWSER_EXECUTABLE", "/changed-env/chrome")
	server, err := buildControlPlaneServerConfig(config)
	require.NoError(t, err)
	require.NotNil(t, server.EmbeddedRunner)
	assert.Equal(t, want, server.EmbeddedRunner.ServiceOptions.Browser)

	// A server without an embedded runner does not consume local browser settings.
	t.Setenv("KODELET_BROWSER_IDLE_TIMEOUT", "invalid")
	config = getServeConfigFromFlags(cmd)
	require.ErrorContains(t, config.ConfigError, "positive duration")
	_, err = buildControlPlaneServerConfig(config)
	require.ErrorContains(t, err, "positive duration")
	require.NoError(t, cmd.Flags().Set("embedded-runner", "false"))
	config = getServeConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	server, err = buildControlPlaneServerConfig(config)
	require.NoError(t, err)
	assert.Nil(t, server.EmbeddedRunner)
}
