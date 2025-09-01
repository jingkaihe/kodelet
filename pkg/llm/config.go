package llm

import (
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func GetConfigFromViper() (llmtypes.Config, error) {
	config, err := loadViperConfig()
	if err != nil {
		return config, err
	}

	// Clean up profiles - remove default profile if it exists
	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}

	// Apply active profile if set
	profileName := getActiveProfile()
	if profileName != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[profileName]; exists {
			if err := applyProfile(&config, profile); err != nil {
				return config, err
			}
		}
	}

	// Resolve model aliases
	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)

	return config, nil
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

func applyProfile(config *llmtypes.Config, profile llmtypes.ProfileConfig) error {
	// Use mapstructure to decode profile into config, merging values
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           config,
		WeaklyTypedInput: true,
		ZeroFields:       false, // Don't overwrite with zero values
	})
	if err != nil {
		return errors.Wrap(err, "failed to create profile decoder")
	}

	// Apply profile settings on top of existing config
	if err := decoder.Decode(profile); err != nil {
		return errors.Wrap(err, "failed to apply profile configuration")
	}

	return nil
}
