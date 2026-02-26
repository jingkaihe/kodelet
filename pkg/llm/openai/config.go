package openai

import (
	"fmt"
	"os"
	"strings"

	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/xai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const defaultOpenAIPlatform = "openai"

func normalizePlatformName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func resolvePlatformName(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	return defaultOpenAIPlatform
}

func resolvePlatformForLoading(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	if config.OpenAI.Models == nil && config.OpenAI.Pricing == nil {
		return defaultOpenAIPlatform
	}

	return ""
}

func parseAPIMode(raw string) (llmtypes.OpenAIAPIMode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")

	switch normalized {
	case "chat", "chat_completions", "chatcompletions":
		return llmtypes.OpenAIAPIModeChatCompletions, true
	case "responses", "responses_api", "response":
		return llmtypes.OpenAIAPIModeResponses, true
	default:
		return "", false
	}
}

func resolveAPIMode(config llmtypes.Config) llmtypes.OpenAIAPIMode {
	if resolvePlatformName(config) == "codex" {
		return llmtypes.OpenAIAPIModeResponses
	}

	if envMode := os.Getenv("KODELET_OPENAI_API_MODE"); envMode != "" {
		if mode, ok := parseAPIMode(envMode); ok {
			return mode
		}
	}

	if envValue := os.Getenv("KODELET_OPENAI_USE_RESPONSES_API"); envValue != "" {
		if strings.EqualFold(envValue, "true") || envValue == "1" {
			return llmtypes.OpenAIAPIModeResponses
		}
		return llmtypes.OpenAIAPIModeChatCompletions
	}

	if config.OpenAI == nil {
		return llmtypes.OpenAIAPIModeChatCompletions
	}

	if mode, ok := parseAPIMode(string(config.OpenAI.APIMode)); ok {
		return mode
	}

	if config.OpenAI.ResponsesAPI != nil {
		if *config.OpenAI.ResponsesAPI {
			return llmtypes.OpenAIAPIModeResponses
		}
		return llmtypes.OpenAIAPIModeChatCompletions
	}

	if config.OpenAI.UseResponsesAPI {
		return llmtypes.OpenAIAPIModeResponses
	}

	return llmtypes.OpenAIAPIModeChatCompletions
}

// loadCustomConfiguration loads custom models and pricing from configuration.
// It loads platform defaults first, then applies custom overrides if provided.
func loadCustomConfiguration(config llmtypes.Config) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	var models *llmtypes.CustomModels
	var pricing llmtypes.CustomPricing

	platformName := resolvePlatformForLoading(config)
	if platformName != "" {
		platformModels, platformPricing := loadPlatformDefaults(platformName)
		models = platformModels
		pricing = platformPricing
	}

	if config.OpenAI != nil {
		if config.OpenAI.Models != nil {
			if models == nil {
				models = &llmtypes.CustomModels{}
			}
			models.Reasoning = config.OpenAI.Models.Reasoning
			models.NonReasoning = config.OpenAI.Models.NonReasoning
		}

		if config.OpenAI.Pricing != nil {
			if pricing == nil {
				pricing = make(llmtypes.CustomPricing)
			}
			for model, p := range config.OpenAI.Pricing {
				pricing[model] = p
			}
		}
	}

	if models != nil && len(models.NonReasoning) == 0 && len(models.Reasoning) > 0 && pricing != nil {
		reasoningSet := make(map[string]bool)
		for _, model := range models.Reasoning {
			reasoningSet[model] = true
		}

		for model := range pricing {
			if !reasoningSet[model] {
				models.NonReasoning = append(models.NonReasoning, model)
			}
		}
	}

	return models, pricing
}

// loadPlatformDefaults loads built-in defaults for known OpenAI-compatible platforms.
func loadPlatformDefaults(platformName string) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	switch normalizePlatformName(platformName) {
	case "openai":
		return loadOpenAIPlatformDefaults()
	case "xai":
		return loadXAIPlatformDefaults()
	case "codex":
		return loadCodexPlatformDefaults()
	default:
		return nil, nil
	}
}

// loadOpenAIPlatformDefaults loads the complete OpenAI platform defaults.
func loadOpenAIPlatformDefaults() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	models := &llmtypes.CustomModels{
		Reasoning:    openaipreset.Models.Reasoning,
		NonReasoning: openaipreset.Models.NonReasoning,
	}

	pricing := make(llmtypes.CustomPricing)
	for model, openaiPricing := range openaipreset.Pricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         openaiPricing.Input,
			CachedInput:   openaiPricing.CachedInput,
			Output:        openaiPricing.Output,
			ContextWindow: openaiPricing.ContextWindow,
		}
	}

	return models, pricing
}

// loadXAIPlatformDefaults loads the complete xAI platform defaults.
func loadXAIPlatformDefaults() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	models := &llmtypes.CustomModels{
		Reasoning:    xai.Models.Reasoning,
		NonReasoning: xai.Models.NonReasoning,
	}

	pricing := make(llmtypes.CustomPricing)
	for model, xaiPricing := range xai.Pricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         xaiPricing.Input,
			CachedInput:   xaiPricing.CachedInput,
			Output:        xaiPricing.Output,
			ContextWindow: xaiPricing.ContextWindow,
		}
	}

	return models, pricing
}

func loadCodexPlatformDefaults() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	models := &llmtypes.CustomModels{
		Reasoning:    codexpreset.Models.Reasoning,
		NonReasoning: codexpreset.Models.NonReasoning,
	}

	pricing := make(llmtypes.CustomPricing)
	for model, codexPricing := range codexpreset.Pricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         codexPricing.Input,
			CachedInput:   codexPricing.CachedInput,
			Output:        codexPricing.Output,
			ContextWindow: codexPricing.ContextWindow,
		}
	}

	return models, pricing
}

// getPlatformBaseURL returns the default base URL for a given platform.
func getPlatformBaseURL(platformName string) string {
	switch normalizePlatformName(platformName) {
	case "openai":
		return openaipreset.BaseURL
	case "xai":
		return xai.BaseURL
	case "codex":
		return codexpreset.BaseURL
	default:
		return ""
	}
}

// getPlatformAPIKeyEnvVar returns the default API key environment variable for a given platform.
func getPlatformAPIKeyEnvVar(platformName string) string {
	switch normalizePlatformName(platformName) {
	case "openai", "codex":
		return openaipreset.APIKeyEnvVar
	case "xai":
		return xai.APIKeyEnvVar
	default:
		return "OPENAI_API_KEY"
	}
}

// GetAPIKeyEnvVar returns the API key environment variable name from configuration.
// Priority: custom api_key_env_var > platform default > fallback to OPENAI_API_KEY.
func GetAPIKeyEnvVar(config llmtypes.Config) string {
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		return config.OpenAI.APIKeyEnvVar
	}

	return getPlatformAPIKeyEnvVar(resolvePlatformName(config))
}

// GetBaseURL returns the base URL resolved from environment, config, and platform defaults.
func GetBaseURL(config llmtypes.Config) string {
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		return baseURL
	}

	if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		return config.OpenAI.BaseURL
	}

	return getPlatformBaseURL(resolvePlatformName(config))
}

// validateCustomConfiguration validates the custom OpenAI configuration
func validateCustomConfiguration(config llmtypes.Config) error {
	if config.OpenAI == nil {
		return nil
	}

	platform := normalizePlatformName(config.OpenAI.Platform)

	if config.OpenAI.Platform != "" && platform == "" {
		return fmt.Errorf("platform cannot be empty or whitespace")
	}

	if config.OpenAI.APIMode != "" {
		if _, ok := parseAPIMode(string(config.OpenAI.APIMode)); !ok {
			return fmt.Errorf("invalid api_mode '%s', valid values are: chat_completions, responses", config.OpenAI.APIMode)
		}
	}

	if config.OpenAI.BaseURL != "" {
		if !strings.HasPrefix(config.OpenAI.BaseURL, "http://") && !strings.HasPrefix(config.OpenAI.BaseURL, "https://") {
			return fmt.Errorf("base_url must start with http:// or https://")
		}
	}

	if config.OpenAI.APIKeyEnvVar != "" {
		if strings.TrimSpace(config.OpenAI.APIKeyEnvVar) == "" {
			return fmt.Errorf("api_key_env_var cannot be empty or whitespace")
		}
		if strings.ContainsAny(config.OpenAI.APIKeyEnvVar, " \t\n\r") {
			return fmt.Errorf("api_key_env_var cannot contain whitespace characters")
		}
	}

	if config.OpenAI.Pricing != nil {
		for model, pricing := range config.OpenAI.Pricing {
			if pricing.Input < 0 {
				return fmt.Errorf("invalid input pricing for model '%s': must be >= 0", model)
			}
			if pricing.Output < 0 {
				return fmt.Errorf("invalid output pricing for model '%s': must be >= 0", model)
			}
			if pricing.CachedInput < 0 {
				return fmt.Errorf("invalid cached_input pricing for model '%s': must be >= 0", model)
			}
			if pricing.ContextWindow <= 0 {
				return fmt.Errorf("invalid context_window for model '%s': must be > 0", model)
			}
		}
	}

	return nil
}
