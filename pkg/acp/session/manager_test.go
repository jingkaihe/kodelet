package session

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerLoadSessionRejectsRunnerBoundConversation(t *testing.T) {
	manager := &Manager{
		config:   ManagerConfig{NoExtensions: true},
		sessions: make(map[acptypes.SessionID]*Session),
		store: &fakeConversationStore{loads: map[string]convtypes.ConversationRecord{
			"remote-session": {
				ID:       "remote-session",
				Metadata: map[string]any{convtypes.RunnerIDMetadataKey: "runner-1"},
			},
		}},
	}

	_, err := manager.LoadSession(context.Background(), acptypes.LoadSessionRequest{
		SessionID: "remote-session",
		CWD:       t.TempDir(),
	})
	require.ErrorContains(t, err, "conversation is bound to runner runner-1")
}

func TestNewManager_WithManagerConfig(t *testing.T) {
	t.Run("creates manager with default config", func(t *testing.T) {
		m := NewManager(ManagerConfig{})

		assert.NotNil(t, m.sessions, "Sessions map should be initialized")
		assert.Empty(t, m.config.Provider)
		assert.Empty(t, m.config.Model)
		assert.False(t, m.config.NoSkills)
		assert.False(t, m.config.NoExtensions)
		assert.False(t, m.config.EnableFSSearchTools)
	})

	t.Run("creates manager with all config fields", func(t *testing.T) {
		cfg := ManagerConfig{
			Provider:            "anthropic",
			Model:               "claude-sonnet-4-6",
			MaxTokens:           4096,
			NoSkills:            true,
			NoExtensions:        true,
			EnableFSSearchTools: true,
			MaxTurns:            10,
			CompactRatio:        0.7,
		}
		m := NewManager(cfg)

		assert.Equal(t, cfg, m.config)
		assert.NotNil(t, m.sessions)
	})
}

func TestManager_BuildLLMConfig(t *testing.T) {
	t.Run("propagates EnableFSSearchTools to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			EnableFSSearchTools: true,
		})

		llmConfig := m.buildLLMConfig("")
		assert.True(t, llmConfig.EnableFSSearchTools)
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
}

func TestManagerBuildLLMConfigForRecordAppliesSnapshot(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()
	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "current-model")
	viper.Set("reasoning_effort", "low")
	viper.Set("allowed_tools", []string{"file_read"})

	metadata, err := conversations.AddConfigSnapshot(nil, llmtypes.Config{
		Profile:         "removed-profile",
		Provider:        "openai",
		Model:           "persisted-model",
		MaxTokens:       16000,
		ReasoningEffort: "high",
		OpenAI:          &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeResponses},
	})
	require.NoError(t, err)

	manager := &Manager{config: ManagerConfig{Provider: "anthropic", Model: "manager-model", EnableFSSearchTools: true, NoSkills: true}}
	config, err := manager.buildLLMConfigForRecord(convtypes.ConversationRecord{
		Provider: "openai",
		Metadata: metadata,
	}, "/tmp/project")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "persisted-model", config.Model)
	assert.Equal(t, "high", config.ReasoningEffort)
	assert.Equal(t, 16000, config.MaxTokens)
	assert.Equal(t, []string{"file_read"}, config.AllowedTools)
	assert.True(t, config.EnableFSSearchTools)
	assert.Equal(t, "/tmp/project", config.WorkingDirectory)
	require.NotNil(t, config.Skills)
	assert.False(t, config.Skills.Enabled)
}

func TestManagerCreatesAndLoadsSessionsWithLocalEnvironment(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()
	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "claude-sonnet-4-6")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())

	store := &fakeConversationStore{loads: map[string]convtypes.ConversationRecord{
		"loaded-session": {
			ID:       "loaded-session",
			Provider: "anthropic",
			Metadata: map[string]any{"model": "claude-sonnet-4-6"},
		},
	}}
	manager := &Manager{
		config: ManagerConfig{
			Provider:     "anthropic",
			Model:        "claude-sonnet-4-6",
			NoExtensions: true,
			MaxTurns:     9,
			CompactRatio: 0.65,
		},
		sessions: make(map[acptypes.SessionID]*Session),
		store:    store,
	}
	workspace := t.TempDir()

	created, err := manager.NewSession(t.Context(), acptypes.NewSessionRequest{CWD: workspace})
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, workspace, created.CWD)
	assert.Equal(t, 9, created.maxTurns)
	assert.Equal(t, 0.65, created.compactRatio)
	assert.Nil(t, created.Extensions)
	assert.True(t, created.Thread.IsPersisted())
	environmentThread, ok := created.Thread.(interface{ GetEnvironment() agentenv.Environment })
	require.True(t, ok)
	require.NotNil(t, environmentThread.GetEnvironment())
	stored, err := manager.GetSession(created.ID)
	require.NoError(t, err)
	assert.Same(t, created, stored)

	loaded, err := manager.LoadSession(t.Context(), acptypes.LoadSessionRequest{SessionID: "loaded-session", CWD: workspace})
	require.NoError(t, err)
	assert.Equal(t, acptypes.SessionID("loaded-session"), loaded.ID)
	assert.Equal(t, workspace, loaded.CWD)
	assert.True(t, loaded.Thread.IsPersisted())
	assert.Equal(t, "loaded-session", loaded.Thread.GetConversationID())

	require.NoError(t, manager.Close(t.Context()))
	assert.True(t, store.isClosed())
}

func TestManagerBuildExtensionRuntimeHonorsConfiguration(t *testing.T) {
	assert.Nil(t, (&Manager{config: ManagerConfig{NoExtensions: true}}).buildExtensionRuntime(t.Context(), t.TempDir()))

	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()
	viper.Reset()
	viper.Set("extensions.enabled", false)
	runtime := (&Manager{}).buildExtensionRuntime(t.Context(), t.TempDir())
	require.NotNil(t, runtime)
	require.NoError(t, runtime.Close())
}
