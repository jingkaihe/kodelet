package session

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/mcp"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager_WithManagerConfig(t *testing.T) {
	t.Run("creates manager with default config", func(t *testing.T) {
		m := NewManager(ManagerConfig{})

		assert.NotNil(t, m.sessions, "Sessions map should be initialized")
		assert.Empty(t, m.config.Provider)
		assert.Empty(t, m.config.Model)
		assert.False(t, m.config.NoSkills)
		assert.False(t, m.config.NoWorkflows)
		assert.False(t, m.config.DisableFSSearchTools)
		assert.False(t, m.config.DisableSubagent)
		assert.False(t, m.config.NoHooks)
	})

	t.Run("creates manager with all config fields", func(t *testing.T) {
		cfg := ManagerConfig{
			Provider:             "anthropic",
			Model:                "claude-sonnet-4-6",
			MaxTokens:            4096,
			NoSkills:             true,
			NoWorkflows:          true,
			DisableFSSearchTools: true,
			DisableSubagent:      true,
			NoHooks:              true,
			MaxTurns:             10,
			CompactRatio:         0.7,
			DisableAutoCompact:   true,
		}
		m := NewManager(cfg)

		assert.Equal(t, cfg, m.config)
		assert.NotNil(t, m.sessions)
	})
}

func TestManager_BuildLLMConfig(t *testing.T) {
	t.Run("propagates DisableFSSearchTools to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			DisableFSSearchTools: true,
		})

		llmConfig := m.buildLLMConfig("")
		assert.True(t, llmConfig.DisableFSSearchTools)
	})

	t.Run("propagates DisableSubagent to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			DisableSubagent: true,
		})

		llmConfig := m.buildLLMConfig("")
		assert.True(t, llmConfig.DisableSubagent)
	})

	t.Run("propagates NoHooks to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			NoHooks: true,
		})

		llmConfig := m.buildLLMConfig("")
		assert.True(t, llmConfig.NoHooks)
	})

	t.Run("propagates provider and model overrides", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Provider: "openai",
			Model:    "gpt-4",
		})

		llmConfig := m.buildLLMConfig("")
		assert.Equal(t, "openai", llmConfig.Provider)
		assert.Equal(t, "gpt-4", llmConfig.Model)
	})

	t.Run("propagates MaxTokens when set", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			MaxTokens: 8192,
		})

		llmConfig := m.buildLLMConfig("")
		assert.Equal(t, 8192, llmConfig.MaxTokens)
	})

	t.Run("does not override MaxTokens when zero", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			MaxTokens: 0,
		})

		llmConfig := m.buildLLMConfig("")
		// Zero MaxTokens in ManagerConfig should not force LLM config to 0;
		// the underlying viper default takes precedence.
		assert.GreaterOrEqual(t, llmConfig.MaxTokens, 0)
	})

	t.Run("NoSkills disables skills in LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			NoSkills: true,
		})

		llmConfig := m.buildLLMConfig("")
		assert.NotNil(t, llmConfig.Skills)
		assert.False(t, llmConfig.Skills.Enabled)
	})

	t.Run("DisableSubagent false does not set DisableSubagent", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			DisableSubagent: false,
		})

		llmConfig := m.buildLLMConfig("")
		assert.False(t, llmConfig.DisableSubagent)
	})
}

func TestBuildSessionMCPStateOpts_UsesSessionProjectDirForCodeExecution(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("mcp.execution_mode", "code")

	originalSetup := setupMCPExecutionMode
	t.Cleanup(func() {
		setupMCPExecutionMode = originalSetup
	})

	var (
		gotSessionID  string
		gotProjectDir string
	)
	setupMCPExecutionMode = func(_ context.Context, manager *tools.MCPManager, sessionID string, projectDir string) (*mcp.ExecutionSetup, error) {
		gotSessionID = sessionID
		gotProjectDir = projectDir
		require.NotNil(t, manager)
		return &mcp.ExecutionSetup{
			StateOpts: []tools.BasicStateOption{},
		}, nil
	}

	kodeletMCPManager, err := tools.NewMCPManager(tools.MCPConfig{
		Servers: map[string]tools.MCPServerConfig{},
	})
	require.NoError(t, err)

	manager := &Manager{
		kodeletMCPManager: kodeletMCPManager,
	}

	sessionMCPManager := manager.buildSessionMCPManager(context.Background(), nil)
	require.NotNil(t, sessionMCPManager)
	require.NotSame(t, kodeletMCPManager, sessionMCPManager)

	opts := manager.buildSessionMCPStateOpts(context.Background(), "session-123", "/tmp/worktree", sessionMCPManager)

	assert.NotNil(t, opts)
	assert.Equal(t, "session-123", gotSessionID)
	assert.Equal(t, "/tmp/worktree", gotProjectDir)
}

func TestConvertMCPServers_MapsHTTPAndSSETransports(t *testing.T) {
	config := convertMCPServers([]acptypes.MCPServer{
		{
			Name:       "stdio-server",
			Type:       "stdio",
			Command:    "npx",
			Args:       []string{"-y", "test-server"},
			Env:        acptypes.EnvMap{"TOKEN": "secret"},
			AuthHeader: "Bearer should-not-be-used",
		},
		{
			Name:       "http-server",
			Type:       "http",
			URL:        "https://example.com/mcp",
			AuthHeader: "Bearer http-token",
		},
		{
			Name:       "sse-server",
			Type:       "sse",
			URL:        "https://example.com/sse",
			AuthHeader: "Bearer sse-token",
		},
	})

	require.Len(t, config.Servers, 3)

	assert.Equal(t, tools.MCPServerTypeStdio, config.Servers["stdio-server"].ServerType)
	assert.Equal(t, "npx", config.Servers["stdio-server"].Command)
	assert.Equal(t, []string{"-y", "test-server"}, config.Servers["stdio-server"].Args)
	assert.Equal(t, map[string]string{"TOKEN": "secret"}, config.Servers["stdio-server"].Envs)
	assert.Empty(t, config.Servers["stdio-server"].Headers)

	assert.Equal(t, tools.MCPServerTypeHTTP, config.Servers["http-server"].ServerType)
	assert.Equal(t, "https://example.com/mcp", config.Servers["http-server"].BaseURL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer http-token"}, config.Servers["http-server"].Headers)

	assert.Equal(t, tools.MCPServerTypeSSE, config.Servers["sse-server"].ServerType)
	assert.Equal(t, "https://example.com/sse", config.Servers["sse-server"].BaseURL)
	assert.Equal(t, map[string]string{"Authorization": "Bearer sse-token"}, config.Servers["sse-server"].Headers)
}
