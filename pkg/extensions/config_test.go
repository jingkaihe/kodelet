package extensions

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestLoadConfigFromViperUsesDefaultsWhenExtensionsUnset(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer restoreViperSettings(originalSettings)
	viper.Reset()

	config := LoadConfigFromViper()

	assert.Equal(t, DefaultConfig(), config)
}

func TestLoadConfigFromViperLoadsExtensionConfig(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer restoreViperSettings(originalSettings)
	viper.Reset()
	viper.Set("extensions", map[string]any{
		"enabled":         false,
		"local_dir":       "./local-ext",
		"global_dir":      "~/global-ext",
		"timeout":         "7s",
		"tool_timeout":    "9s",
		"max_output_size": 2048,
		"allow":           []string{"./local-ext/weather"},
		"deny":            []string{"org@repo/blocked"},
		"events": map[string]any{
			"tool.call": map[string]any{"timeout": "2s"},
		},
		"tools": map[string]any{
			"get_weather": map[string]any{"enabled": false, "timeout": "3s"},
		},
	})

	config := LoadConfigFromViper()

	assert.False(t, config.Enabled)
	assert.Equal(t, "./local-ext", config.LocalDir)
	assert.Equal(t, "~/global-ext", config.GlobalDir)
	assert.Equal(t, 7*time.Second, config.Timeout)
	assert.Equal(t, 9*time.Second, config.ToolTimeout)
	assert.Equal(t, 2048, config.MaxOutputSize)
	assert.Equal(t, []string{"./local-ext/weather"}, config.Allow)
	assert.Equal(t, []string{"org@repo/blocked"}, config.Deny)
	assert.Equal(t, 2*time.Second, config.Events["tool.call"].Timeout)
	requireFalse := false
	assert.Equal(t, &requireFalse, config.Tools["get_weather"].Enabled)
	assert.Equal(t, 3*time.Second, config.Tools["get_weather"].Timeout)
}

func restoreViperSettings(settings map[string]any) {
	viper.Reset()
	for key, value := range settings {
		viper.Set(key, value)
	}
}
