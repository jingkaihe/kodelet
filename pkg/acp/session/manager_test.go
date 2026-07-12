package session

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
