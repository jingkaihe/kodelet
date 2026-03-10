package llm

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// GetConfigFromViper loads the LLM configuration from Viper, applies the active profile if set,
// and resolves any model aliases.
func GetConfigFromViper() (llmtypes.Config, error) {
	return GetConfigFromViperWithCmd(nil)
}

// GetConfigFromViperWithProfile loads configuration from Viper while applying the
// provided profile name instead of the globally active viper profile. This is
// useful for request-scoped profile selection (for example, in the web UI)
// without mutating shared process-wide viper state.
func GetConfigFromViperWithProfile(profileName string) (llmtypes.Config, error) {
	return getConfigFromViperWithProfileAndCmd(profileName, nil)
}

// GetConfigFromViperWithCmd loads the LLM configuration from Viper with command context.
// When a cobra.Command is provided, CLI flags that were explicitly changed take priority
// over profile settings.
func GetConfigFromViperWithCmd(cmd *cobra.Command) (llmtypes.Config, error) {
	return getConfigFromViperWithProfileAndCmd("", cmd)
}

func getConfigFromViperWithProfileAndCmd(profileName string, cmd *cobra.Command) (llmtypes.Config, error) {
	settings := cloneSettings(viper.AllSettings())
	config, err := loadConfigFromSettings(settings)
	if err != nil {
		return config, err
	}

	// Clean up profiles - remove default profile if it exists
	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}

	activeProfile := profileName
	if activeProfile == "" {
		activeProfile = getActiveProfile()
	}

	// Apply active profile to viper if set
	if activeProfile != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[activeProfile]; exists {
			applyProfileToSettings(settings, profile)
		} else if profileName != "" {
			return config, errors.Errorf("failed to apply configuration profile: profile '%s' not found", profileName)
		}
	}

	// Apply explicitly changed CLI flags to viper (highest priority)
	if cmd != nil {
		applyExplicitFlagsToSettings(cmd, settings)
	}

	// Re-load config with all overrides applied
	config, err = loadConfigFromSettings(settings)
	if err != nil {
		return config, err
	}

	if activeProfile != "" {
		config.Profile = activeProfile
	}

	// Resolve model aliases
	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)

	return config, nil
}

// applyExplicitFlagsToSettings sets explicitly changed CLI flag values into a local settings map.
func applyExplicitFlagsToSettings(cmd *cobra.Command, settings map[string]any) {
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Changed {
			viperKey := explicitFlagViperKey(flag.Name)
			if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
				setSetting(settings, viperKey, sliceValue.GetSlice())
				return
			}
			if flag.Value.Type() == "stringToString" {
				if mapValue, err := cmd.Flags().GetStringToString(flag.Name); err == nil {
					setSetting(settings, viperKey, mapValue)
					return
				}
			}
			setSetting(settings, viperKey, flag.Value.String())
		}
	})
}

func explicitFlagViperKey(flagName string) string {
	if viperKey, ok := explicitFlagKeyOverrides[flagName]; ok {
		return viperKey
	}
	return strings.ReplaceAll(flagName, "-", "_")
}

var explicitFlagKeyOverrides = map[string]string{
	"context-patterns": "context.patterns",
	"tracing-enabled":  "tracing.enabled",
	"tracing-sampler":  "tracing.sampler",
	"tracing-ratio":    "tracing.ratio",
	"sysprompt":        "sysprompt",
	"sysprompt-arg":    "sysprompt_args",
}

// applyProfileToSettings applies profile settings to a local settings map.
func applyProfileToSettings(settings map[string]any, profile llmtypes.ProfileConfig) {
	for key, value := range profile {
		if value != nil {
			setSetting(settings, key, value)
		}
	}
}

func loadConfigFromSettings(settings map[string]any) (llmtypes.Config, error) {
	var config llmtypes.Config
	v := viper.New()
	for key, value := range settings {
		v.Set(key, value)
	}

	// Use viper's automatic unmarshaling with mapstructure tags
	if err := v.Unmarshal(&config); err != nil {
		return config, errors.Wrap(err, "failed to unmarshal configuration")
	}

	// Set default anthropic_api_access if empty
	if config.AnthropicAPIAccess == "" {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAuto
	}

	// Apply retry defaults if not set
	if config.Retry.Attempts == 0 {
		config.Retry = llmtypes.DefaultRetryConfig
	}

	return config, nil
}

func cloneSettings(settings map[string]any) map[string]any {
	cloned := make(map[string]any, len(settings))
	for key, value := range settings {
		cloned[key] = cloneSettingValue(value)
	}
	return cloned
}

func cloneSettingValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneSettings(typed)
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for k, v := range typed {
			cloned[k] = v
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneSettingValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func setSetting(settings map[string]any, key string, value any) {
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		settings[key] = cloneSettingValue(value)
		return
	}

	current := settings
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok || next == nil {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}

	current[parts[len(parts)-1]] = cloneSettingValue(value)
}

func getActiveProfile() string {
	profile := viper.GetString("profile")
	if profile == "default" || profile == "" {
		return ""
	}
	return profile
}
