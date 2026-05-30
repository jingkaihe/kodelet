// Package extensions implements Kodelet's long-running extension runtime.
package extensions

import (
	"context"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/spf13/viper"
)

// EventConfig controls runtime behavior for a specific extension event.
type EventConfig struct {
	Timeout time.Duration `mapstructure:"timeout" json:"timeout" yaml:"timeout"`
}

// ToolConfig controls runtime behavior for a specific extension-provided tool.
type ToolConfig struct {
	Enabled *bool         `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	Timeout time.Duration `mapstructure:"timeout" json:"timeout" yaml:"timeout"`
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
	Timeout       time.Duration              `mapstructure:"timeout" json:"timeout" yaml:"timeout"`
	ToolTimeout   time.Duration              `mapstructure:"tool_timeout" json:"tool_timeout" yaml:"tool_timeout"`
	MaxOutputSize int                        `mapstructure:"max_output_size" json:"max_output_size" yaml:"max_output_size"`
	Allow         []string                   `mapstructure:"allow" json:"allow" yaml:"allow"`
	Deny          []string                   `mapstructure:"deny" json:"deny" yaml:"deny"`
	Events        map[string]EventConfig     `mapstructure:"events" json:"events" yaml:"events"`
	Tools         map[string]ToolConfig      `mapstructure:"tools" json:"tools" yaml:"tools"`
	Processes     map[string]ExtensionConfig `mapstructure:"processes" json:"processes" yaml:"processes"`
}

// DefaultConfig returns the default extension runtime configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		GlobalDir:     "~/.kodelet/extensions",
		LocalDir:      "./.kodelet/extensions",
		Timeout:       30 * time.Second,
		ToolTimeout:   120 * time.Second,
		MaxOutputSize: 102400,
	}
}

// LoadConfigFromViper loads extension configuration from viper.
func LoadConfigFromViper() Config {
	config := DefaultConfig()
	if viper.IsSet("extensions") {
		if err := viper.UnmarshalKey("extensions", &config); err != nil {
			logger.G(context.Background()).WithError(err).Warn("failed to load extensions config, using defaults")
		}
	}
	return config
}
