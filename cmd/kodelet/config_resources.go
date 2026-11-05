package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func setResourceConfigDefaults(v *viper.Viper) {
	v.SetDefault("extensions.enabled", true)
	v.SetDefault("extensions.discover_path", true)
	v.SetDefault("extensions.global_dir", "~/.kodelet/extensions")
	v.SetDefault("extensions.local_dir", "./.kodelet/extensions")
	v.SetDefault("extensions.max_output_size", 102400)
	v.SetDefault("context.patterns", llmtypes.DefaultContextPatterns())
}

// newResourceSettingsLoader pins the trusted sources and process environment,
// not their file contents. Each call reads into a private Viper instance; it
// never mutates daemon configuration or consults repository files.
func newResourceSettingsLoader(flags *pflag.FlagSet) (runnerclient.ResourceSettingsLoader, error) {
	mode, err := configFileMode()
	if err != nil {
		return nil, err
	}
	override := strings.TrimSpace(os.Getenv(configFileEnv))
	if override != "" {
		override, err = filepath.Abs(override)
		if err != nil {
			return nil, errors.Wrap(err, "failed to resolve resource configuration override")
		}
	}
	var home string
	if override == "" || mode != configFileModeIsolate {
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, errors.Wrap(err, "failed to locate global resource configuration")
		}
	}
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "KODELET_") && value != "" {
			environment[key] = value
		}
	}
	var contextPatterns *[]string
	if flags != nil && flags.Changed("context-patterns") {
		patterns, err := flags.GetStringSlice("context-patterns")
		if err != nil {
			return nil, errors.Wrap(err, "failed to snapshot context-patterns flag")
		}
		contextPatterns = new(slices.Clone(patterns))
	}
	read := func() (*viper.Viper, error) {
		v := viper.New()
		setResourceConfigDefaults(v)
		if home != "" {
			v.SetConfigName("config")
			v.SetConfigType("yaml")
			v.AddConfigPath(filepath.Join(home, ".kodelet"))
			if err := v.ReadInConfig(); err != nil {
				var missing viper.ConfigFileNotFoundError
				if !errors.As(err, &missing) {
					return nil, errors.Wrap(err, "failed to read global resource configuration")
				}
			} else if err := validateTrustedConfigPermissions(v.ConfigFileUsed()); err != nil {
				return nil, err
			}
		}
		if override != "" {
			v.SetConfigFile(override)
			if err := v.MergeInConfig(); err != nil {
				return nil, errors.Wrap(err, "failed to read resource configuration override")
			}
			if err := validateTrustedConfigPermissions(override); err != nil {
				return nil, err
			}
		}
		return v, nil
	}
	initial, err := read()
	if err != nil {
		return nil, err
	}
	startupKeys := initial.AllKeys()
	return func() (map[string]any, error) {
		v, err := read()
		if err != nil {
			return nil, err
		}
		// Match startup AutomaticEnv precedence without rereading mutable process
		// state. Keep known environment overrides even if their file keys disappear.
		for _, key := range append(v.AllKeys(), startupKeys...) {
			name := "KODELET_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
			if value, ok := environment[name]; ok {
				v.Set(key, value)
			}
		}
		if contextPatterns != nil {
			v.Set("context.patterns", slices.Clone(*contextPatterns))
		}
		settings := make(map[string]any)
		values := v.AllSettings()
		for _, key := range []string{"extensions", "skills", "context"} {
			if value, ok := values[key]; ok {
				settings[key] = value
			}
		}
		return settings, nil
	}, nil
}
