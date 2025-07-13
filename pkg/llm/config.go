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

	config := llmtypes.Config{
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

	// Load OpenAI-specific configuration
	if viper.IsSet("openai") {
		openaiConfig := &llmtypes.OpenAIConfig{}

		// Load basic settings
		openaiConfig.Preset = viper.GetString("openai.preset")
		openaiConfig.BaseURL = viper.GetString("openai.base_url")

		// Load models configuration
		if viper.IsSet("openai.models") {
			openaiConfig.Models = &llmtypes.CustomModels{
				Reasoning:    viper.GetStringSlice("openai.models.reasoning"),
				NonReasoning: viper.GetStringSlice("openai.models.non_reasoning"),
			}
		}

		// Load pricing configuration
		if viper.IsSet("openai.pricing") {
			openaiConfig.Pricing = make(map[string]llmtypes.ModelPricing)
			pricingMap := viper.GetStringMap("openai.pricing")

			for model, pricingData := range pricingMap {
				if pricingSubMap, ok := pricingData.(map[string]interface{}); ok {
					pricing := llmtypes.ModelPricing{}

					if input, ok := pricingSubMap["input"].(float64); ok {
						pricing.Input = input
					}
					if cachedInput, ok := pricingSubMap["cached_input"].(float64); ok {
						pricing.CachedInput = cachedInput
					}
					if output, ok := pricingSubMap["output"].(float64); ok {
						pricing.Output = output
					}
					if contextWindow, ok := pricingSubMap["context_window"].(int); ok {
						pricing.ContextWindow = contextWindow
					}

					openaiConfig.Pricing[model] = pricing
				}
			}
		}

		config.OpenAI = openaiConfig
	}

	return config
}
