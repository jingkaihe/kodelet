package extensions

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"discover_path":   false,
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
	assert.False(t, config.DiscoverPath)
	assert.Equal(t, "./local-ext", config.LocalDir)
	assert.Equal(t, "~/global-ext", config.GlobalDir)
	assert.Equal(t, 2048, config.MaxOutputSize)
	assert.Equal(t, []string{"./local-ext/weather"}, config.Allow)
	assert.Equal(t, []string{"org@repo/blocked"}, config.Deny)
	requireFalse := false
	assert.Equal(t, &requireFalse, config.Tools["get_weather"].Enabled)
}

func TestExtensionConfigPreservesStructuredValues(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer restoreViperSettings(originalSettings)
	viper.Reset()
	viper.SetConfigType("yaml")
	require.NoError(t, viper.ReadConfig(strings.NewReader(`
extensions:
  config:
    Subagent:
      Enabled: true
      Empty: ""
      Literal: '$HOME/CaseSensitive,a,b'
      Retries: 3
      Ratio: 1.5
      Nested:
        Mode: Review
      Values: [false, 2, CaseSensitive, null, {Key: Value}]
    Org@Repo/Worker:
      Token: SecretCase
`)))
	expected := map[string]map[string]any{
		"subagent": {
			"enabled": true,
			"empty":   "",
			"literal": "$HOME/CaseSensitive,a,b",
			"retries": 3,
			"ratio":   1.5,
			"nested":  map[string]any{"mode": "Review"},
			"values":  []any{false, 2, "CaseSensitive", nil, map[string]any{"key": "Value"}},
		},
		"org@repo/worker": {"token": "SecretCase"},
	}
	assert.Equal(t, expected, LoadConfigFromViper().Config)
	config, err := LoadConfigFromSettings(viper.GetStringMap("extensions"))
	require.NoError(t, err)
	assert.Equal(t, expected, config.Config)
}

func TestExtensionConfigRequiresObjects(t *testing.T) {
	for _, value := range []any{"invalid", true, 3, []any{"invalid"}} {
		_, err := LoadConfigFromSettings(map[string]any{"config": map[string]any{"subagent": value}})
		require.Error(t, err)
	}
}

func TestExtensionConfigHasNoTimeoutConfigSurface(t *testing.T) {
	typeOfConfig := reflect.TypeOf(Config{})
	_, hasTimeout := typeOfConfig.FieldByName("Timeout")
	_, hasEvents := typeOfConfig.FieldByName("Events")
	_, hasCommands := typeOfConfig.FieldByName("Commands")
	_, hasProcesses := typeOfConfig.FieldByName("Processes")

	assert.False(t, hasTimeout)
	assert.False(t, hasEvents)
	assert.False(t, hasCommands)
	assert.False(t, hasProcesses)
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
