package llm

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetConfigFromViper(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("model", "test-model")
	viper.Set("max_tokens", 1234)

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	assert.Equal(t, "test-model", config.Model)
	assert.Equal(t, 1234, config.MaxTokens)
}

func TestGetConfigFromViperDefaults(t *testing.T) {
	// Setup
	viper.Reset()

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	assert.Empty(t, config.Model)
	assert.Zero(t, config.MaxTokens)
}

func TestGetConfigFromViperWithCmd_ExplicitContextPatternsOverrideProfile(t *testing.T) {
	originalConfig := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalConfig {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("profile", "work")
	viper.Set("profiles", map[string]interface{}{
		"work": map[string]interface{}{
			"context": map[string]interface{}{
				"patterns": []string{"README.md"},
			},
		},
	})

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringSlice("context-patterns", []string{"AGENTS.md"}, "Context file patterns")
	err := cmd.Flags().Set("context-patterns", "CODING.md,README.md")
	require.NoError(t, err)

	config, err := GetConfigFromViperWithCmd(cmd)
	require.NoError(t, err)

	require.NotNil(t, config.Context)
	assert.Equal(t, []string{"CODING.md", "README.md"}, config.Context.Patterns)
}

func TestGetConfigFromViperWithAliases(t *testing.T) {
	// Save original viper state
	originalConfig := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalConfig {
			viper.Set(key, value)
		}
	}()

	tests := []struct {
		name            string
		configData      map[string]interface{}
		expectedAliases map[string]string
		description     string
	}{
		{
			name: "loads aliases from config",
			configData: map[string]interface{}{
				"provider":   "anthropic",
				"model":      "claude-sonnet-4-5-20250929",
				"max_tokens": 8192,
				"aliases": map[string]interface{}{
					"sonnet-45": "claude-sonnet-4-5-20250929",
					"haiku-45":  "claude-haiku-4-5-20251001",
					"gpt41":     "gpt-4.1",
				},
			},
			expectedAliases: map[string]string{
				"sonnet-45": "claude-sonnet-4-5-20250929",
				"haiku-45":  "claude-haiku-4-5-20251001",
				"gpt41":     "gpt-4.1",
			},
			description: "should load aliases from config data",
		},
		{
			name: "handles missing aliases config",
			configData: map[string]interface{}{
				"provider":   "anthropic",
				"model":      "claude-sonnet-4-5-20250929",
				"max_tokens": 8192,
			},
			expectedAliases: nil,
			description:     "should handle missing aliases configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset viper
			viper.Reset()

			// Set config data
			for key, value := range tt.configData {
				viper.Set(key, value)
			}

			// Get config
			config, err := GetConfigFromViper()
			require.NoError(t, err)

			// Verify aliases
			assert.Equal(t, tt.expectedAliases, config.Aliases, tt.description)

			// Verify other config fields are preserved
			if provider, exists := tt.configData["provider"]; exists {
				assert.Equal(t, provider, config.Provider, "provider should be preserved")
			}
			if model, exists := tt.configData["model"]; exists {
				assert.Equal(t, model, config.Model, "model should be preserved")
			}
		})
	}
}

func TestConfigAliasIntegrationWithNewThread(t *testing.T) {
	// Save original viper state
	originalConfig := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalConfig {
			viper.Set(key, value)
		}
	}()

	// Reset viper and set config with alias
	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "sonnet-45") // This is an alias
	viper.Set("max_tokens", 8192)
	viper.Set("aliases", map[string]interface{}{
		"sonnet-45": "claude-sonnet-4-5-20250929",
		"haiku-45":  "claude-haiku-4-5-20251001",
	})

	// Get config and create thread
	config, err := GetConfigFromViper()
	require.NoError(t, err)
	originalModel := config.Model

	thread, err := NewThread(config)

	require.NoError(t, err, "should resolve alias from config through NewThread")
	require.NotNil(t, thread, "thread should not be nil")

	// Verify the original config was not modified (passed by value to NewThread)
	assert.Equal(t, originalModel, config.Model, "original config should not be modified")
}

func TestGetConfigFromViperOpenAINotSet(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4")

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	assert.Nil(t, config.OpenAI, "OpenAI config should be nil when not set")
}

func TestGetConfigFromViperOpenAIBasicConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("openai.preset", "xai")
	viper.Set("openai.base_url", "https://api.x.ai/v1")

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	assert.Equal(t, "xai", config.OpenAI.Preset)
	assert.Equal(t, "https://api.x.ai/v1", config.OpenAI.BaseURL)
	assert.Nil(t, config.OpenAI.Models, "Models should be nil when not set")
	assert.Nil(t, config.OpenAI.Pricing, "Pricing should be nil when not set")
}

func TestGetConfigFromViperOpenAIModelsConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("openai.models.reasoning", []string{"o1-preview", "o1-mini", "o3-mini"})
	viper.Set("openai.models.non_reasoning", []string{"gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"})

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	require.NotNil(t, config.OpenAI.Models, "Models config should not be nil")
	assert.Equal(t, []string{"o1-preview", "o1-mini", "o3-mini"}, config.OpenAI.Models.Reasoning)
	assert.Equal(t, []string{"gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"}, config.OpenAI.Models.NonReasoning)
}

func TestGetConfigFromViperOpenAIPartialModelsConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("openai.models.reasoning", []string{"o1-preview"})
	// Don't set non_reasoning

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	require.NotNil(t, config.OpenAI.Models, "Models config should not be nil")
	assert.Equal(t, []string{"o1-preview"}, config.OpenAI.Models.Reasoning)
	assert.Nil(t, config.OpenAI.Models.NonReasoning, "NonReasoning should be nil when not set")
}

func TestGetConfigFromViperOpenAIPricingConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")

	// Create complex pricing configuration
	pricingConfig := map[string]interface{}{
		"gpt-4": map[string]interface{}{
			"input":          0.00003,
			"cached_input":   0.000015,
			"output":         0.00006,
			"context_window": 128000,
		},
		"o1-preview": map[string]interface{}{
			"input":          0.000015,
			"output":         0.00006,
			"context_window": 32768,
		},
	}
	viper.Set("openai.pricing", pricingConfig)

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	require.NotNil(t, config.OpenAI.Pricing, "Pricing should not be nil")
	require.Len(t, config.OpenAI.Pricing, 2, "Should have 2 pricing entries")

	// Check gpt-4 pricing
	gpt4Pricing, exists := config.OpenAI.Pricing["gpt-4"]
	require.True(t, exists, "gpt-4 pricing should exist")
	assert.Equal(t, 0.00003, gpt4Pricing.Input)
	assert.Equal(t, 0.000015, gpt4Pricing.CachedInput)
	assert.Equal(t, 0.00006, gpt4Pricing.Output)
	assert.Equal(t, 128000, gpt4Pricing.ContextWindow)

	// Check o1-preview pricing
	o1Pricing, exists := config.OpenAI.Pricing["o1-preview"]
	require.True(t, exists, "o1-preview pricing should exist")
	assert.Equal(t, 0.000015, o1Pricing.Input)
	assert.Equal(t, 0.0, o1Pricing.CachedInput) // Not set, should be zero value
	assert.Equal(t, 0.00006, o1Pricing.Output)
	assert.Equal(t, 32768, o1Pricing.ContextWindow)
}

func TestGetConfigFromViperOpenAIPricingPartialConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")

	// Create pricing configuration with only some fields
	pricingConfig := map[string]interface{}{
		"gpt-4": map[string]interface{}{
			"input":  0.00003,
			"output": 0.00006,
			// Missing cached_input and context_window
		},
	}
	viper.Set("openai.pricing", pricingConfig)

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	require.NotNil(t, config.OpenAI.Pricing, "Pricing should not be nil")

	gpt4Pricing, exists := config.OpenAI.Pricing["gpt-4"]
	require.True(t, exists, "gpt-4 pricing should exist")
	assert.Equal(t, 0.00003, gpt4Pricing.Input)
	assert.Equal(t, 0.0, gpt4Pricing.CachedInput) // Should be zero value
	assert.Equal(t, 0.00006, gpt4Pricing.Output)
	assert.Equal(t, 0, gpt4Pricing.ContextWindow) // Should be zero value
}

func TestGetConfigFromViperOpenAIPricingInvalidTypes(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")

	// Create pricing configuration with invalid types
	pricingConfig := map[string]interface{}{
		"gpt-4": map[string]interface{}{
			"input":          "invalid", // Should be float64
			"cached_input":   0.000015,
			"output":         0.00006,
			"context_window": "invalid", // Should be int
		},
		"invalid-entry": "not-a-map", // Should be a map
	}
	viper.Set("openai.pricing", pricingConfig)

	// Execute - invalid types should cause configuration to fail
	_, err := GetConfigFromViper()

	// Verify that error is returned for invalid configuration
	assert.Error(t, err, "should return error for invalid pricing configuration types")
	assert.Contains(t, err.Error(), "failed to unmarshal configuration", "error should mention unmarshaling failure")
}

func TestGetConfigFromViperOpenAIFullConfig(t *testing.T) {
	// Setup
	viper.Reset()
	viper.Set("provider", "openai")
	viper.Set("model", "gpt-4")
	viper.Set("max_tokens", 4096)

	// Set full OpenAI configuration
	viper.Set("openai.preset", "custom")
	viper.Set("openai.base_url", "https://api.custom.ai/v1")
	viper.Set("openai.models.reasoning", []string{"o1-preview", "o1-mini"})
	viper.Set("openai.models.non_reasoning", []string{"gpt-4", "gpt-3.5-turbo"})

	pricingConfig := map[string]interface{}{
		"gpt-4": map[string]interface{}{
			"input":          0.00003,
			"cached_input":   0.000015,
			"output":         0.00006,
			"context_window": 128000,
		},
		"o1-preview": map[string]interface{}{
			"input":          0.000015,
			"output":         0.00006,
			"context_window": 32768,
		},
	}
	viper.Set("openai.pricing", pricingConfig)

	// Execute
	config, err := GetConfigFromViper()
	require.NoError(t, err)

	// Verify basic config
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "gpt-4", config.Model)
	assert.Equal(t, 4096, config.MaxTokens)

	// Verify OpenAI config
	require.NotNil(t, config.OpenAI, "OpenAI config should not be nil")
	assert.Equal(t, "custom", config.OpenAI.Preset)
	assert.Equal(t, "https://api.custom.ai/v1", config.OpenAI.BaseURL)

	// Verify models config
	require.NotNil(t, config.OpenAI.Models, "Models config should not be nil")
	assert.Equal(t, []string{"o1-preview", "o1-mini"}, config.OpenAI.Models.Reasoning)
	assert.Equal(t, []string{"gpt-4", "gpt-3.5-turbo"}, config.OpenAI.Models.NonReasoning)

	// Verify pricing config
	require.NotNil(t, config.OpenAI.Pricing, "Pricing should not be nil")
	require.Len(t, config.OpenAI.Pricing, 2, "Should have 2 pricing entries")

	gpt4Pricing := config.OpenAI.Pricing["gpt-4"]
	assert.Equal(t, 0.00003, gpt4Pricing.Input)
	assert.Equal(t, 0.000015, gpt4Pricing.CachedInput)
	assert.Equal(t, 0.00006, gpt4Pricing.Output)
	assert.Equal(t, 128000, gpt4Pricing.ContextWindow)

	o1Pricing := config.OpenAI.Pricing["o1-preview"]
	assert.Equal(t, 0.000015, o1Pricing.Input)
	assert.Equal(t, 0.0, o1Pricing.CachedInput)
	assert.Equal(t, 0.00006, o1Pricing.Output)
	assert.Equal(t, 32768, o1Pricing.ContextWindow)
}
