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

// GetConfigFromViperWithCmd loads the LLM configuration from Viper with command context.
// When a cobra.Command is provided, CLI flags that were explicitly changed take priority
// over profile settings.
func GetConfigFromViperWithCmd(cmd *cobra.Command) (llmtypes.Config, error) {
	config, err := loadViperConfig()
	if err != nil {
		return config, err
	}

	// Clean up profiles - remove default profile if it exists
	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}

	// Apply active profile to viper if set
	profileName := getActiveProfile()
	if profileName != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[profileName]; exists {
			applyProfileToViper(profile)
		}
	}

	// Apply explicitly changed CLI flags to viper (highest priority)
	if cmd != nil {
		applyExplicitFlagsToViper(cmd)
	}

	// Re-load config with all overrides applied
	config, err = loadViperConfig()
	if err != nil {
		return config, err
	}

	// Resolve model aliases
	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)

	return config, nil
}

// applyExplicitFlagsToViper sets explicitly changed CLI flag values in viper
func applyExplicitFlagsToViper(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Changed {
			viperKey := strings.ReplaceAll(flag.Name, "-", "_")
			// Must use flag.Value directly, not viper.Get(), because
			// profile settings use viper.Set() which has highest priority
			viper.Set(viperKey, flag.Value.String())
		}
	})
}

// applyProfileToViper applies profile settings to viper
func applyProfileToViper(profile llmtypes.ProfileConfig) {
	for key, value := range profile {
		if value != nil {
			viper.Set(key, value)
		}
	}
}

func loadViperConfig() (llmtypes.Config, error) {
	var config llmtypes.Config

	// Use viper's automatic unmarshaling with mapstructure tags
	if err := viper.Unmarshal(&config); err != nil {
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

func getActiveProfile() string {
	profile := viper.GetString("profile")
	if profile == "default" || profile == "" {
		return ""
	}
	return profile
}
