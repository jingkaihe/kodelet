package llm

import (
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// GetConfigFromViper returns a Config object based on the current Viper settings
func GetConfigFromViper() llmtypes.Config {
	var config llmtypes.Config
	
	// Load base configuration using viper's Unmarshal
	if err := viper.Unmarshal(&config); err != nil {
		// If unmarshaling fails, fall back to manual loading
		config = loadConfigManually()
	} else {
		// Fill in values that Unmarshal might miss
		fillMissingConfigValues(&config)
	}
	
	// Validate that no profile is named "default" (reserved)
	if config.Profiles != nil {
		delete(config.Profiles, "default")
	}
	
	// Apply profile if specified
	profileName := getActiveProfile()
	if profileName != "" && config.Profiles != nil {
		if profile, exists := config.Profiles[profileName]; exists {
			applyProfile(&config, profile)
		}
	}
	
	// Apply model aliases after profile application
	config.Model = resolveModelAlias(config.Model, config.Aliases)
	config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)
	
	return config
}

// loadConfigManually loads configuration manually as a fallback
func loadConfigManually() llmtypes.Config {
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
		AllowedTools:         viper.GetStringSlice("allowed_tools"),
		AllowedCommands:      viper.GetStringSlice("allowed_commands"),
		AllowedDomainsFile:   viper.GetString("allowed_domains_file"),
		AnthropicAPIAccess:   llmtypes.AnthropicAPIAccess(anthropicAPIAccess),
		UseCopilot:           viper.GetBool("use_copilot"),
		Aliases:              viper.GetStringMapString("aliases"),
		Profile:              viper.GetString("profile"),
		Profiles:             loadProfiles(),
	}

	// Load OpenAI-specific configuration
	config.OpenAI = loadOpenAIConfig("openai")

	// Load subagent configuration
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

	// Load models configuration
	modelsKey := keyPrefix + ".models"
	if viper.IsSet(modelsKey) {
		config.Models = &llmtypes.CustomModels{
			Reasoning:    viper.GetStringSlice(modelsKey + ".reasoning"),
			NonReasoning: viper.GetStringSlice(modelsKey + ".non_reasoning"),
		}
	}

	// Load pricing configuration
	pricingKey := keyPrefix + ".pricing"
	if viper.IsSet(pricingKey) {
		config.Pricing = loadPricingConfig(pricingKey)
	}

	return config
}

// loadPricingConfig loads pricing configuration from viper
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

// parseModelPricing parses a single model's pricing from a map
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

// fillMissingConfigValues fills values that Unmarshal might miss
func fillMissingConfigValues(config *llmtypes.Config) {
	// Set default for anthropic API access if not specified
	if string(config.AnthropicAPIAccess) == "" {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAuto
	}
	
	// Load profiles if not already loaded
	if config.Profiles == nil {
		config.Profiles = loadProfiles()
	}
}

// loadProfiles loads profile configurations from viper
func loadProfiles() map[string]llmtypes.ProfileConfig {
	if !viper.IsSet("profiles") {
		return nil
	}
	
	profilesMap := viper.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)
	
	for name, profileData := range profilesMap {
		if name == "default" {
			// Skip reserved profile name
			continue
		}
		
		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}
	
	return profiles
}

// getActiveProfile determines the active profile name based on priority order
func getActiveProfile() string {
	// Priority order:
	// 1. Command-line flag (--profile) via viper
	// 2. Environment variable (KODELET_PROFILE) via viper
	// 3. Config file setting (profile: "name") via viper
	
	profile := viper.GetString("profile")
	
	// "default" is a reserved name that means no profile
	if profile == "default" || profile == "" {
		return ""
	}
	return profile
}

// applyProfile applies a profile configuration to the base config
func applyProfile(config *llmtypes.Config, profile llmtypes.ProfileConfig) {
	// Apply basic configuration fields
	applyStringField(profile, "provider", &config.Provider)
	applyStringField(profile, "model", &config.Model)
	applyStringField(profile, "weak_model", &config.WeakModel)
	applyStringField(profile, "reasoning_effort", &config.ReasoningEffort)
	applyStringField(profile, "allowed_domains_file", &config.AllowedDomainsFile)
	
	// Apply integer fields
	applyIntField(profile, "max_tokens", &config.MaxTokens)
	applyIntField(profile, "weak_model_max_tokens", &config.WeakModelMaxTokens)
	applyIntField(profile, "thinking_budget_tokens", &config.ThinkingBudgetTokens)
	applyIntField(profile, "cache_every", &config.CacheEvery)
	
	// Apply boolean fields
	applyBoolField(profile, "use_copilot", &config.UseCopilot)
	
	// Apply slice fields
	applyStringSliceField(profile, "allowed_tools", &config.AllowedTools)
	applyStringSliceField(profile, "allowed_commands", &config.AllowedCommands)
	
	// Apply special fields
	if anthropicAPIAccess, ok := profile["anthropic_api_access"].(string); ok {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccess(anthropicAPIAccess)
	}
	
	// Handle aliases
	if aliases, ok := profile["aliases"]; ok {
		config.Aliases = toStringMap(aliases)
	}
	
	// Handle OpenAI configuration
	if openaiConfig, ok := profile["openai"]; ok {
		if openaiMap, ok := openaiConfig.(map[string]interface{}); ok {
			config.OpenAI = applyOpenAIConfigFromMap(config.OpenAI, openaiMap)
		}
	}
	
	// Handle SubAgent configuration
	if subagentConfig, ok := profile["subagent"]; ok {
		if subagentMap, ok := subagentConfig.(map[string]interface{}); ok {
			config.SubAgent = applySubAgentConfigFromMap(config.SubAgent, subagentMap)
		}
	}
}

// applyOpenAIConfigFromMap applies OpenAI configuration from a map
func applyOpenAIConfigFromMap(existing *llmtypes.OpenAIConfig, openaiMap map[string]interface{}) *llmtypes.OpenAIConfig {
	if existing == nil {
		existing = &llmtypes.OpenAIConfig{}
	}
	
	// Apply basic fields
	if preset, ok := openaiMap["preset"].(string); ok {
		existing.Preset = preset
	}
	if baseURL, ok := openaiMap["base_url"].(string); ok {
		existing.BaseURL = baseURL
	}
	if apiKeyEnvVar, ok := openaiMap["api_key_env_var"].(string); ok {
		existing.APIKeyEnvVar = apiKeyEnvVar
	}
	
	// Handle models configuration
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
	
	// Handle pricing configuration
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

// applySubAgentConfigFromMap applies SubAgent configuration from a map
func applySubAgentConfigFromMap(existing *llmtypes.SubAgentConfigSettings, subagentMap map[string]interface{}) *llmtypes.SubAgentConfigSettings {
	if existing == nil {
		existing = &llmtypes.SubAgentConfigSettings{}
	}
	
	// Apply basic fields
	if provider, ok := subagentMap["provider"].(string); ok {
		existing.Provider = provider
	}
	if model, ok := subagentMap["model"].(string); ok {
		existing.Model = model
	}
	if reasoningEffort, ok := subagentMap["reasoning_effort"].(string); ok {
		existing.ReasoningEffort = reasoningEffort
	}
	
	// Apply integer fields
	if maxTokens, ok := subagentMap["max_tokens"]; ok {
		existing.MaxTokens = toInt(maxTokens)
	}
	if thinkingBudget, ok := subagentMap["thinking_budget"]; ok {
		existing.ThinkingBudget = toInt(thinkingBudget)
	}
	
	// Apply slice fields
	if allowedTools, ok := subagentMap["allowed_tools"]; ok {
		existing.AllowedTools = toStringSlice(allowedTools)
	}
	
	// Handle OpenAI configuration for subagent
	if openaiConfig, ok := subagentMap["openai"]; ok {
		if openaiMap, ok := openaiConfig.(map[string]interface{}); ok {
			existing.OpenAI = applyOpenAIConfigFromMap(existing.OpenAI, openaiMap)
		}
	}
	
	return existing
}

// Utility functions for type conversions and field applications

// applyStringField applies a string field from profile to config
func applyStringField(profile llmtypes.ProfileConfig, key string, target *string) {
	if value, ok := profile[key].(string); ok {
		*target = value
	}
}

// applyIntField applies an integer field from profile to config
func applyIntField(profile llmtypes.ProfileConfig, key string, target *int) {
	if value, ok := profile[key]; ok {
		*target = toInt(value)
	}
}

// applyBoolField applies a boolean field from profile to config
func applyBoolField(profile llmtypes.ProfileConfig, key string, target *bool) {
	if value, ok := profile[key].(bool); ok {
		*target = value
	}
}

// applyStringSliceField applies a string slice field from profile to config
func applyStringSliceField(profile llmtypes.ProfileConfig, key string, target *[]string) {
	if value, ok := profile[key]; ok {
		*target = toStringSlice(value)
	}
}

// toInt converts interface{} to int, handling both int and float64
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

// toStringSlice converts interface{} to []string, handling various slice types
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

// toStringMap converts interface{} to map[string]string, handling various map types
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


