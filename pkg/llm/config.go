package llm

import (
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func GetConfigFromViper() llmtypes.Config {
	config := loadViperConfig()

	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}

	profileName := getActiveProfile()
	if profileName != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[profileName]; exists {
			applyProfile(&config, profile)
		}
	}

	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)

	return config
}

func loadViperConfig() llmtypes.Config {
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
		AllowedTools:         viper.GetStringSlice("allowed_tools"),
		AllowedCommands:      viper.GetStringSlice("allowed_commands"),
		AllowedDomainsFile:   viper.GetString("allowed_domains_file"),
		AnthropicAPIAccess:   llmtypes.AnthropicAPIAccess(anthropicAPIAccess),
		UseCopilot:           viper.GetBool("use_copilot"),
		Aliases:              viper.GetStringMapString("aliases"),
		Profile:              viper.GetString("profile"),
		Profiles:             loadProfiles(),
	}

	config.OpenAI = loadOpenAIConfig("openai")
	config.Retry = loadRetryConfig()

	if viper.IsSet("subagent") {
		config.SubAgent = &llmtypes.SubAgentConfigSettings{
			Provider:        viper.GetString("subagent.provider"),
			Model:           viper.GetString("subagent.model"),
			MaxTokens:       viper.GetInt("subagent.max_tokens"),
			ReasoningEffort: viper.GetString("subagent.reasoning_effort"),
			ThinkingBudget:  viper.GetInt("subagent.thinking_budget"),
			AllowedTools:    viper.GetStringSlice("subagent.allowed_tools"),
			OpenAI:          loadOpenAIConfig("subagent.openai"),
		}
	}

	return config
}

// loadOpenAIConfig loads OpenAI configuration from a given viper key prefix
func loadOpenAIConfig(keyPrefix string) *llmtypes.OpenAIConfig {
	if !viper.IsSet(keyPrefix) {
		return nil
	}

	config := &llmtypes.OpenAIConfig{
		Preset:       viper.GetString(keyPrefix + ".preset"),
		BaseURL:      viper.GetString(keyPrefix + ".base_url"),
		APIKeyEnvVar: viper.GetString(keyPrefix + ".api_key_env_var"),
	}

	modelsKey := keyPrefix + ".models"
	if viper.IsSet(modelsKey) {
		config.Models = &llmtypes.CustomModels{
			Reasoning:    viper.GetStringSlice(modelsKey + ".reasoning"),
			NonReasoning: viper.GetStringSlice(modelsKey + ".non_reasoning"),
		}
	}

	pricingKey := keyPrefix + ".pricing"
	if viper.IsSet(pricingKey) {
		config.Pricing = loadPricingConfig(pricingKey)
	}

	return config
}

// loadRetryConfig loads retry configuration from viper with defaults
func loadRetryConfig() llmtypes.RetryConfig {
	if !viper.IsSet("retry") {
		return llmtypes.DefaultRetryConfig
	}

	config := llmtypes.RetryConfig{
		Attempts:     viper.GetInt("retry.attempts"),
		InitialDelay: viper.GetInt("retry.initial_delay"),
		MaxDelay:     viper.GetInt("retry.max_delay"),
		BackoffType:  viper.GetString("retry.backoff_type"),
	}

	// Apply defaults for any zero values
	if config.Attempts == 0 {
		config.Attempts = llmtypes.DefaultRetryConfig.Attempts
	}
	if config.InitialDelay == 0 {
		config.InitialDelay = llmtypes.DefaultRetryConfig.InitialDelay
	}
	if config.MaxDelay == 0 {
		config.MaxDelay = llmtypes.DefaultRetryConfig.MaxDelay
	}
	if config.BackoffType == "" {
		config.BackoffType = llmtypes.DefaultRetryConfig.BackoffType
	}

	return config
}

func loadPricingConfig(keyPrefix string) map[string]llmtypes.ModelPricing {
	pricingMap := viper.GetStringMap(keyPrefix)
	pricing := make(map[string]llmtypes.ModelPricing)

	for model, pricingData := range pricingMap {
		if pricingSubMap, ok := pricingData.(map[string]interface{}); ok {
			pricing[model] = parseModelPricing(pricingSubMap)
		}
	}

	return pricing
}

func parseModelPricing(data map[string]interface{}) llmtypes.ModelPricing {
	pricing := llmtypes.ModelPricing{}

	if input, ok := data["input"].(float64); ok {
		pricing.Input = input
	}
	if cachedInput, ok := data["cached_input"].(float64); ok {
		pricing.CachedInput = cachedInput
	}
	if output, ok := data["output"].(float64); ok {
		pricing.Output = output
	}
	if contextWindow := data["context_window"]; contextWindow != nil {
		pricing.ContextWindow = toInt(contextWindow)
	}

	return pricing
}

func loadProfiles() map[string]llmtypes.ProfileConfig {
	if !viper.IsSet("profiles") {
		return nil
	}

	profilesMap := viper.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)

	for name, profileData := range profilesMap {
		if name == "default" {
			continue
		}

		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}

	return profiles
}

func getActiveProfile() string {
	profile := viper.GetString("profile")

	if profile == "default" || profile == "" {
		return ""
	}
	return profile
}

func applyProfile(config *llmtypes.Config, profile llmtypes.ProfileConfig) {
	applyStringField(profile, "provider", &config.Provider)
	applyStringField(profile, "model", &config.Model)
	applyStringField(profile, "weak_model", &config.WeakModel)
	applyStringField(profile, "reasoning_effort", &config.ReasoningEffort)
	applyStringField(profile, "allowed_domains_file", &config.AllowedDomainsFile)

	applyIntField(profile, "max_tokens", &config.MaxTokens)
	applyIntField(profile, "weak_model_max_tokens", &config.WeakModelMaxTokens)
	applyIntField(profile, "thinking_budget_tokens", &config.ThinkingBudgetTokens)
	applyIntField(profile, "cache_every", &config.CacheEvery)

	applyBoolField(profile, "use_copilot", &config.UseCopilot)

	applyStringSliceField(profile, "allowed_tools", &config.AllowedTools)
	applyStringSliceField(profile, "allowed_commands", &config.AllowedCommands)

	if anthropicAPIAccess, ok := profile["anthropic_api_access"].(string); ok {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccess(anthropicAPIAccess)
	}

	if aliases, ok := profile["aliases"]; ok {
		config.Aliases = toStringMap(aliases)
	}

	if openaiConfig, ok := profile["openai"]; ok {
		if openaiMap, ok := openaiConfig.(map[string]interface{}); ok {
			config.OpenAI = applyOpenAIConfigFromMap(config.OpenAI, openaiMap)
		}
	}

	if subagentConfig, ok := profile["subagent"]; ok {
		if subagentMap, ok := subagentConfig.(map[string]interface{}); ok {
			config.SubAgent = applySubAgentConfigFromMap(config.SubAgent, subagentMap)
		}
	}

	if retryConfig, ok := profile["retry"]; ok {
		if retryMap, ok := retryConfig.(map[string]interface{}); ok {
			config.Retry = applyRetryConfigFromMap(config.Retry, retryMap)
		}
	}
}

func applyOpenAIConfigFromMap(existing *llmtypes.OpenAIConfig, openaiMap map[string]interface{}) *llmtypes.OpenAIConfig {
	if existing == nil {
		existing = &llmtypes.OpenAIConfig{}
	}

	if preset, ok := openaiMap["preset"].(string); ok {
		existing.Preset = preset
	}
	if baseURL, ok := openaiMap["base_url"].(string); ok {
		existing.BaseURL = baseURL
	}
	if apiKeyEnvVar, ok := openaiMap["api_key_env_var"].(string); ok {
		existing.APIKeyEnvVar = apiKeyEnvVar
	}

	if models, ok := openaiMap["models"]; ok {
		if modelsMap, ok := models.(map[string]interface{}); ok {
			if existing.Models == nil {
				existing.Models = &llmtypes.CustomModels{}
			}

			if reasoning, ok := modelsMap["reasoning"]; ok {
				existing.Models.Reasoning = toStringSlice(reasoning)
			}
			if nonReasoning, ok := modelsMap["non_reasoning"]; ok {
				existing.Models.NonReasoning = toStringSlice(nonReasoning)
			}
		}
	}

	if pricing, ok := openaiMap["pricing"]; ok {
		if pricingMap, ok := pricing.(map[string]interface{}); ok {
			if existing.Pricing == nil {
				existing.Pricing = make(map[string]llmtypes.ModelPricing)
			}

			for model, modelPricing := range pricingMap {
				if pricingData, ok := modelPricing.(map[string]interface{}); ok {
					existing.Pricing[model] = parseModelPricing(pricingData)
				}
			}
		}
	}

	return existing
}

func applySubAgentConfigFromMap(existing *llmtypes.SubAgentConfigSettings, subagentMap map[string]interface{}) *llmtypes.SubAgentConfigSettings {
	if existing == nil {
		existing = &llmtypes.SubAgentConfigSettings{}
	}

	if provider, ok := subagentMap["provider"].(string); ok {
		existing.Provider = provider
	}
	if model, ok := subagentMap["model"].(string); ok {
		existing.Model = model
	}
	if reasoningEffort, ok := subagentMap["reasoning_effort"].(string); ok {
		existing.ReasoningEffort = reasoningEffort
	}

	if maxTokens, ok := subagentMap["max_tokens"]; ok {
		existing.MaxTokens = toInt(maxTokens)
	}
	if thinkingBudget, ok := subagentMap["thinking_budget"]; ok {
		existing.ThinkingBudget = toInt(thinkingBudget)
	}

	if allowedTools, ok := subagentMap["allowed_tools"]; ok {
		existing.AllowedTools = toStringSlice(allowedTools)
	}

	if openaiConfig, ok := subagentMap["openai"]; ok {
		if openaiMap, ok := openaiConfig.(map[string]interface{}); ok {
			existing.OpenAI = applyOpenAIConfigFromMap(existing.OpenAI, openaiMap)
		}
	}

	return existing
}

func applyRetryConfigFromMap(existing llmtypes.RetryConfig, retryMap map[string]interface{}) llmtypes.RetryConfig {
	config := existing

	if attempts, ok := retryMap["attempts"]; ok {
		config.Attempts = toInt(attempts)
	}
	if initialDelay, ok := retryMap["initial_delay"]; ok {
		config.InitialDelay = toInt(initialDelay)
	}
	if maxDelay, ok := retryMap["max_delay"]; ok {
		config.MaxDelay = toInt(maxDelay)
	}
	if backoffType, ok := retryMap["backoff_type"].(string); ok {
		config.BackoffType = backoffType
	}

	return config
}

func applyStringField(profile llmtypes.ProfileConfig, key string, target *string) {
	if value, ok := profile[key].(string); ok {
		*target = value
	}
}

func applyIntField(profile llmtypes.ProfileConfig, key string, target *int) {
	if value, ok := profile[key]; ok {
		*target = toInt(value)
	}
}

func applyBoolField(profile llmtypes.ProfileConfig, key string, target *bool) {
	if value, ok := profile[key].(bool); ok {
		*target = value
	}
}

func applyStringSliceField(profile llmtypes.ProfileConfig, key string, target *[]string) {
	if value, ok := profile[key]; ok {
		*target = toStringSlice(value)
	}
}

func toInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func toStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

func toStringMap(value interface{}) map[string]string {
	switch v := value.(type) {
	case map[string]string:
		return v
	case map[string]interface{}:
		result := make(map[string]string)
		for k, val := range v {
			if str, ok := val.(string); ok {
				result[k] = str
			}
		}
		return result
	default:
		return nil
	}
}
