package extensions

import (
	"context"
	"reflect"
	"strings"
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

func TestLoadConfigFromViperAppliesNestedEnvironmentOverrides(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer restoreViperSettings(originalSettings)
	t.Setenv("KODELET_EXTENSIONS_LOCAL_DIR", "/tmp/sdk-inline-extensions")
	t.Setenv("KODELET_EXTENSIONS_ALLOW", "/tmp/sdk-inline-extensions")
	t.Setenv("KODELET_EXTENSIONS_GLOBAL_DIR", "/tmp/global-extensions")
	viper.Reset()
	viper.SetEnvPrefix("KODELET")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("extensions.enabled", true)
	viper.SetDefault("extensions.local_dir", "./.kodelet/extensions")
	viper.SetDefault("extensions.global_dir", "~/.kodelet/extensions")
	viper.SetDefault("extensions.max_output_size", 102400)
	viper.SetDefault("extensions.allow", []string{})
	viper.SetDefault("extensions.deny", []string{})

	config := LoadConfigFromViper()

	assert.True(t, config.Enabled)
	assert.Equal(t, "/tmp/sdk-inline-extensions", config.LocalDir)
	assert.Equal(t, "/tmp/global-extensions", config.GlobalDir)
	assert.Equal(t, []string{"/tmp/sdk-inline-extensions"}, config.Allow)
	assert.Empty(t, config.Deny)
	assert.Equal(t, 102400, config.MaxOutputSize)
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
