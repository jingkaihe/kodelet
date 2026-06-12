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

// Config contains extension runtime configuration.
type Config struct {
	Enabled       bool                  `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	GlobalDir     string                `mapstructure:"global_dir" json:"global_dir" yaml:"global_dir"`
	LocalDir      string                `mapstructure:"local_dir" json:"local_dir" yaml:"local_dir"`
	MaxOutputSize int                   `mapstructure:"max_output_size" json:"max_output_size" yaml:"max_output_size"`
	Allow         []string              `mapstructure:"allow" json:"allow" yaml:"allow"`
	Deny          []string              `mapstructure:"deny" json:"deny" yaml:"deny"`
	Tools         map[string]ToolConfig `mapstructure:"tools" json:"tools" yaml:"tools"`
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
	return config
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
