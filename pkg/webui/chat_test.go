package webui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildChatState_BindsTodoPathToConversationID(t *testing.T) {
	customManager, err := tools.NewCustomToolManager()
	require.NoError(t, err)

	state, err := buildChatState(
		context.Background(),
		llmtypes.Config{
			DisableSubagent: true,
		},
		"conv-web-123",
		nil,
		customManager,
	)
	require.NoError(t, err)

	todoPath, err := state.TodoFilePath()
	require.NoError(t, err)

	assert.Equal(t, "conv-web-123.json", filepath.Base(todoPath))
	assert.Equal(t, "todos", filepath.Base(filepath.Dir(todoPath)))
}

func TestResolveWebChatConfigForExistingConversation_UsesStoredProfileAndMetadata(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "base-model")
	viper.Set("profiles", map[string]any{
		"premium": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
			"openai": map[string]any{
				"platform": "openai",
			},
		},
	})

	config, err := resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-123",
		Provider: "openai",
		Metadata: map[string]any{
			"profile":  "premium",
			"model":    "accounts/fireworks/models/kimi-k2",
			"platform": "fireworks",
			"api_mode": "responses",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "premium", config.Profile)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "accounts/fireworks/models/kimi-k2", config.Model)
	require.NotNil(t, config.OpenAI)
	assert.Equal(t, "fireworks", config.OpenAI.Platform)
	assert.Equal(t, llmtypes.OpenAIAPIMode("responses"), config.OpenAI.APIMode)
}

func TestResolveWebChatConfigForNewConversation_DefaultProfileNameIgnoresActiveProfile(t *testing.T) {
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
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
		},
	})

	config, err := resolveWebChatConfigForNewConversation("default")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "claude-sonnet-4-6", config.Model)
	assert.Equal(t, "default", config.Profile)
}

func TestResolveWebChatConfigForExistingConversation_DefaultProfileIgnoresActiveProfile(t *testing.T) {
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
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"provider": "openai",
			"model":    "gpt-4.1",
		},
	})

	config, err := resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-default",
		Provider: "anthropic",
		Metadata: map[string]any{
			"profile": "default",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "claude-sonnet-4-6", config.Model)
	assert.Equal(t, "default", config.Profile)
}
