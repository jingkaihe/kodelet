package llm

import (
	"testing"

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
	config := GetConfigFromViper()

	// Verify
	assert.Equal(t, "test-model", config.Model)
	assert.Equal(t, 1234, config.MaxTokens)
}

func TestGetConfigFromViperDefaults(t *testing.T) {
	// Setup
	viper.Reset()

	// Execute
	config := GetConfigFromViper()

	// Verify
	assert.Empty(t, config.Model)
	assert.Zero(t, config.MaxTokens)
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
				"model":      "claude-sonnet-4-20250514",
				"max_tokens": 8192,
				"aliases": map[string]interface{}{
					"sonnet-4": "claude-sonnet-4-20250514",
					"haiku-35": "claude-3-5-haiku-20241022",
					"gpt41":    "gpt-4.1",
				},
			},
			expectedAliases: map[string]string{
				"sonnet-4": "claude-sonnet-4-20250514",
				"haiku-35": "claude-3-5-haiku-20241022",
				"gpt41":    "gpt-4.1",
			},
			description: "should load aliases from config data",
		},
		{
			name: "handles missing aliases config",
			configData: map[string]interface{}{
				"provider":   "anthropic",
				"model":      "claude-sonnet-4-20250514",
				"max_tokens": 8192,
			},
			expectedAliases: map[string]string{},
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
			config := GetConfigFromViper()

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
	viper.Set("model", "sonnet-4") // This is an alias
	viper.Set("max_tokens", 8192)
	viper.Set("aliases", map[string]interface{}{
		"sonnet-4": "claude-sonnet-4-20250514",
		"haiku-35": "claude-3-5-haiku-20241022",
	})

	// Get config and create thread
	config := GetConfigFromViper()
	originalModel := config.Model

	thread, err := NewThread(config)

	require.NoError(t, err, "should resolve alias from config through NewThread")
	require.NotNil(t, thread, "thread should not be nil")

	// Verify the original config was not modified (passed by value to NewThread)
	assert.Equal(t, originalModel, config.Model, "original config should not be modified")
}
