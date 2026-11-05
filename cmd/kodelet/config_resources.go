package main

import (
	"os"
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

// newResourceSettings pins the trusted sources and process environment, not
// their file contents. Each read uses a private Viper instance; it never
// mutates daemon configuration or consults repository files. The initial read
// is strict; later invalid files fall back to the last valid settings except
// when a reload refreshes them explicitly.
func newResourceSettings(flags *pflag.FlagSet) (*runnerclient.ResourceSettings, error) {
	readConfig, err := trustedConfigReader()
	if err != nil {
		return nil, err
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
		if err := readConfig(v); err != nil {
			return nil, err
		}
		return v, nil
	}
	initial, err := read()
	if err != nil {
		return nil, err
	}
	startupKeys := initial.AllKeys()
	return runnerclient.NewResourceSettings(func() (map[string]any, error) {
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
		return v.AllSettings(), nil
	})
}
