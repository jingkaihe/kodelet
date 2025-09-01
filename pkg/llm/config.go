package llm

import (
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func GetConfigFromViper() llmtypes.Config {
	config := loadViperConfig()

	// Clean up profiles - remove default profile if it exists
	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}

	// Apply active profile if set
	profileName := getActiveProfile()
	if profileName != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[profileName]; exists {
			applyProfile(&config, profile)
		}
	}

	// Resolve model aliases
	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)

	return config
}

func loadViperConfig() llmtypes.Config {
	var config llmtypes.Config
	
	// Use viper's automatic unmarshaling with mapstructure tags
	if err := viper.Unmarshal(&config); err != nil {
		// Fallback to defaults if unmarshaling fails
		config = llmtypes.Config{
			Provider:           "anthropic",
			Model:              "claude-sonnet-4-20250514",
			WeakModel:          "claude-3-5-haiku-20241022",
			MaxTokens:          16000,
			WeakModelMaxTokens: 8192,
			AnthropicAPIAccess: llmtypes.AnthropicAPIAccessAuto,
			Retry:              llmtypes.DefaultRetryConfig,
		}
	}

	// Apply retry defaults if not set
	if config.Retry.Attempts == 0 {
		config.Retry = llmtypes.DefaultRetryConfig
	}

	// Set default anthropic_api_access if empty
	if config.AnthropicAPIAccess == "" {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAuto
	}

	return config
}

func getActiveProfile() string {
	profile := viper.GetString("profile")
	if profile == "default" || profile == "" {
		return ""
	}
	return profile
}

func applyProfile(config *llmtypes.Config, profile llmtypes.ProfileConfig) {
	// Use mapstructure to decode profile into config, merging values
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           config,
		WeaklyTypedInput: true,
		ZeroFields:       false, // Don't overwrite with zero values
	})
	if err != nil {
		return
	}

	// Apply profile settings on top of existing config
	_ = decoder.Decode(profile)
}
