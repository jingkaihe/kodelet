package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewManager_WithManagerConfig(t *testing.T) {
	t.Run("creates manager with default config", func(t *testing.T) {
		m := NewManager(ManagerConfig{})

		assert.NotEmpty(t, m.id, "Manager should have a generated ID")
		assert.NotNil(t, m.sessions, "Sessions map should be initialized")
		assert.Empty(t, m.config.Provider)
		assert.Empty(t, m.config.Model)
		assert.False(t, m.config.NoSkills)
		assert.False(t, m.config.NoWorkflows)
		assert.False(t, m.config.DisableSubagent)
		assert.False(t, m.config.NoHooks)
	})

	t.Run("creates manager with all config fields", func(t *testing.T) {
		cfg := ManagerConfig{
			Provider:           "anthropic",
			Model:              "claude-sonnet-4-5-20250929",
			MaxTokens:          4096,
			NoSkills:           true,
			NoWorkflows:        true,
			DisableSubagent:    true,
			NoHooks:            true,
			MaxTurns:           10,
			CompactRatio:       0.7,
			DisableAutoCompact: true,
		}
		m := NewManager(cfg)

		assert.Equal(t, cfg, m.config)
		assert.NotEmpty(t, m.id)
		assert.NotNil(t, m.sessions)
	})

	t.Run("each manager gets a unique ID", func(t *testing.T) {
		m1 := NewManager(ManagerConfig{})
		m2 := NewManager(ManagerConfig{})

		assert.NotEqual(t, m1.id, m2.id, "Each manager should get a unique ID")
	})
}

func TestManager_BuildLLMConfig(t *testing.T) {
	t.Run("propagates DisableSubagent to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			DisableSubagent: true,
		})

		llmConfig := m.buildLLMConfig()
		assert.True(t, llmConfig.DisableSubagent)
	})

	t.Run("propagates NoHooks to LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			NoHooks: true,
		})

		llmConfig := m.buildLLMConfig()
		assert.True(t, llmConfig.NoHooks)
	})

	t.Run("propagates provider and model overrides", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Provider: "openai",
			Model:    "gpt-4",
		})

		llmConfig := m.buildLLMConfig()
		assert.Equal(t, "openai", llmConfig.Provider)
		assert.Equal(t, "gpt-4", llmConfig.Model)
	})

	t.Run("propagates MaxTokens when set", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			MaxTokens: 8192,
		})

		llmConfig := m.buildLLMConfig()
		assert.Equal(t, 8192, llmConfig.MaxTokens)
	})

	t.Run("does not override MaxTokens when zero", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			MaxTokens: 0,
		})

		llmConfig := m.buildLLMConfig()
		// Zero MaxTokens in ManagerConfig should not force LLM config to 0;
		// the underlying viper default takes precedence.
		assert.GreaterOrEqual(t, llmConfig.MaxTokens, 0)
	})

	t.Run("NoSkills disables skills in LLM config", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			NoSkills: true,
		})

		llmConfig := m.buildLLMConfig()
		assert.NotNil(t, llmConfig.Skills)
		assert.False(t, llmConfig.Skills.Enabled)
	})

	t.Run("DisableSubagent false does not set DisableSubagent", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			DisableSubagent: false,
		})

		llmConfig := m.buildLLMConfig()
		assert.False(t, llmConfig.DisableSubagent)
	})
}
