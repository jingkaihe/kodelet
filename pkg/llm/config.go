package llm

import (
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// GetConfigFromViper returns a Config object based on the current Viper settings
func GetConfigFromViper() llmtypes.Config {
	// Set default to auto if not specified
	anthropicAPIAccess := viper.GetString("anthropic_api_access")
	if anthropicAPIAccess == "" {
		anthropicAPIAccess = string(llmtypes.AnthropicAPIAccessAuto)
	}

	return llmtypes.Config{
		Provider:             viper.GetString("provider"),
		Model:                viper.GetString("model"),
		MaxTokens:            viper.GetInt("max_tokens"),
		WeakModel:            viper.GetString("weak_model"),
		WeakModelMaxTokens:   viper.GetInt("weak_model_max_tokens"),
		ThinkingBudgetTokens: viper.GetInt("thinking_budget_tokens"),
		ReasoningEffort:      viper.GetString("reasoning_effort"),
		CacheEvery:           viper.GetInt("cache_every"),
		AllowedCommands:      viper.GetStringSlice("allowed_commands"),
		AllowedDomainsFile:   viper.GetString("allowed_domains_file"),
		AnthropicAPIAccess:   llmtypes.AnthropicAPIAccess(anthropicAPIAccess),
		Aliases:              viper.GetStringMapString("aliases"),
	}
}
