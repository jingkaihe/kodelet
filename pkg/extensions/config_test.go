package extensions

import (
	"context"
	"reflect"
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
		"timeout":         "1s",
		"max_output_size": 2048,
		"allow":           []string{"./local-ext/weather"},
		"deny":            []string{"org@repo/blocked"},
		"events": map[string]any{
			"tool.call": map[string]any{"timeout": "2s"},
		},
		"commands": map[string]any{
			"research": map[string]any{"timeout": "30m"},
		},
		"tools": map[string]any{
			"get_weather": map[string]any{"enabled": false, "timeout": "10s"},
		},
	})

	config := LoadConfigFromViper()

	assert.False(t, config.Enabled)
	assert.Equal(t, "./local-ext", config.LocalDir)
	assert.Equal(t, "~/global-ext", config.GlobalDir)
	assert.Equal(t, 2048, config.MaxOutputSize)
	assert.Equal(t, []string{"./local-ext/weather"}, config.Allow)
	assert.Equal(t, []string{"org@repo/blocked"}, config.Deny)
	requireFalse := false
	assert.Equal(t, &requireFalse, config.Tools["get_weather"].Enabled)
}

func TestExtensionConfigHasNoTimeoutConfigSurface(t *testing.T) {
	typeOfConfig := reflect.TypeOf(Config{})
	_, hasTimeout := typeOfConfig.FieldByName("Timeout")
	_, hasEvents := typeOfConfig.FieldByName("Events")
	_, hasCommands := typeOfConfig.FieldByName("Commands")

	assert.False(t, hasTimeout)
	assert.False(t, hasEvents)
	assert.False(t, hasCommands)
}

func TestContextWithOptionalDuration(t *testing.T) {
	ctx, cancel := contextWithOptionalDuration(context.Background(), 0)
	defer cancel()
	_, hasDeadline := ctx.Deadline()
	assert.False(t, hasDeadline)

	ctx, cancel = contextWithOptionalDuration(context.Background(), time.Second)
	defer cancel()
	deadline, hasDeadline := ctx.Deadline()
	assert.True(t, hasDeadline)
	assert.WithinDuration(t, time.Now().Add(time.Second), deadline, 100*time.Millisecond)
}

func restoreViperSettings(settings map[string]any) {
	viper.Reset()
	for key, value := range settings {
		viper.Set(key, value)
	}
}
