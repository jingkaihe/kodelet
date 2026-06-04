// Package extensions implements Kodelet's long-running extension runtime.
package extensions

import (
	"context"
	"math"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/spf13/viper"
)

// ToolConfig controls runtime behavior for a specific extension-provided tool.
type ToolConfig struct {
	Enabled *bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
}

// ExtensionConfig controls behavior for a specific extension process.
type ExtensionConfig struct {
	Env map[string]*string `mapstructure:"env" json:"env" yaml:"env"`
}

// Config contains extension runtime configuration.
type Config struct {
	Enabled       bool                       `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	GlobalDir     string                     `mapstructure:"global_dir" json:"global_dir" yaml:"global_dir"`
	LocalDir      string                     `mapstructure:"local_dir" json:"local_dir" yaml:"local_dir"`
	MaxOutputSize int                        `mapstructure:"max_output_size" json:"max_output_size" yaml:"max_output_size"`
	Allow         []string                   `mapstructure:"allow" json:"allow" yaml:"allow"`
	Deny          []string                   `mapstructure:"deny" json:"deny" yaml:"deny"`
	Tools         map[string]ToolConfig      `mapstructure:"tools" json:"tools" yaml:"tools"`
	Processes     map[string]ExtensionConfig `mapstructure:"processes" json:"processes" yaml:"processes"`
}

// DefaultConfig returns the default extension runtime configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		GlobalDir:     "~/.kodelet/extensions",
		LocalDir:      "./.kodelet/extensions",
		MaxOutputSize: 102400,
	}
}

// LoadConfigFromViper loads extension configuration from viper.
func LoadConfigFromViper() Config {
	config := DefaultConfig()
	if viper.IsSet("extensions") {
		if err := viper.UnmarshalKey("extensions", &config, viper.DecodeHook(extensionConfigDecodeHook())); err != nil {
			logger.G(context.Background()).WithError(err).Warn("failed to load extensions config, using defaults")
		}
	}
	applyExtensionConfigOverrides(&config)
	return config
}

func applyExtensionConfigOverrides(config *Config) {
	if viper.IsSet("extensions.enabled") {
		config.Enabled = viper.GetBool("extensions.enabled")
	}
	if viper.IsSet("extensions.global_dir") {
		config.GlobalDir = viper.GetString("extensions.global_dir")
	}
	if viper.IsSet("extensions.local_dir") {
		config.LocalDir = viper.GetString("extensions.local_dir")
	}
	if viper.IsSet("extensions.max_output_size") {
		config.MaxOutputSize = viper.GetInt("extensions.max_output_size")
	}
	if viper.IsSet("extensions.allow") {
		config.Allow = viper.GetStringSlice("extensions.allow")
	}
	if viper.IsSet("extensions.deny") {
		config.Deny = viper.GetStringSlice("extensions.deny")
	}
}

func extensionConfigDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToSliceHookFunc(","),
	)
}

func timeoutInSecDuration(timeoutInSec *float64) time.Duration {
	if timeoutInSec == nil || *timeoutInSec < 0 || math.IsNaN(*timeoutInSec) || math.IsInf(*timeoutInSec, 0) {
		return 0
	}
	return time.Duration(*timeoutInSec * float64(time.Second))
}

func contextWithOptionalDuration(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
