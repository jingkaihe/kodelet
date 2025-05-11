package llm

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
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
