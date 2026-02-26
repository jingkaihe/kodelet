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

	if preset := normalizePlatformName(config.OpenAI.Preset); preset != "" {
		return preset
	}

	return defaultOpenAIPlatform
}

func resolvePresetForLoading(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	if preset := normalizePlatformName(config.OpenAI.Preset); preset != "" {
		return preset
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

// loadCustomConfiguration loads custom models and pricing from configuration
// It processes presets first, then applies custom overrides if provided
func loadCustomConfiguration(config llmtypes.Config) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	var models *llmtypes.CustomModels
	var pricing llmtypes.CustomPricing

	presetName := resolvePresetForLoading(config)
	if presetName != "" {
		presetModels, presetPricing := loadPreset(presetName)
		models = presetModels
		pricing = presetPricing
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

// loadPreset loads a built-in preset configuration for popular providers
func loadPreset(presetName string) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	switch normalizePlatformName(presetName) {
	case "openai":
		return loadOpenAIPreset()
	case "xai":
		return loadXAIGrokPreset()
	case "codex":
		return loadCodexPreset()
	default:
		return nil, nil
	}
}

// loadOpenAIPreset loads the complete OpenAI configuration
func loadOpenAIPreset() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
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

// loadXAIGrokPreset loads the complete xAI Grok configuration
func loadXAIGrokPreset() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
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

func loadCodexPreset() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
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

// getPresetBaseURL returns the base URL for a given preset
func getPresetBaseURL(presetName string) string {
	switch normalizePlatformName(presetName) {
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

// getPresetAPIKeyEnvVar returns the environment variable name for the API key for a given preset
func getPresetAPIKeyEnvVar(presetName string) string {
	switch normalizePlatformName(presetName) {
	case "openai", "codex":
		return openaipreset.APIKeyEnvVar
	case "xai":
		return xai.APIKeyEnvVar
	default:
		return "OPENAI_API_KEY"
	}
}

// GetAPIKeyEnvVar returns the API key environment variable name from configuration
// Priority: custom api_key_env_var > platform default > fallback to OPENAI_API_KEY
func GetAPIKeyEnvVar(config llmtypes.Config) string {
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		return config.OpenAI.APIKeyEnvVar
	}

	return getPresetAPIKeyEnvVar(resolvePlatformName(config))
}

// GetBaseURL returns the base URL resolved from environment, config, and platform defaults.
func GetBaseURL(config llmtypes.Config) string {
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		return baseURL
	}

	if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		return config.OpenAI.BaseURL
	}

	return getPresetBaseURL(resolvePlatformName(config))
}

func isKnownPreset(name string) bool {
	switch normalizePlatformName(name) {
	case "openai", "xai", "codex":
		return true
	default:
		return false
	}
}

// validateCustomConfiguration validates the custom OpenAI configuration
func validateCustomConfiguration(config llmtypes.Config) error {
	if config.OpenAI == nil {
		return nil
	}

	platform := normalizePlatformName(config.OpenAI.Platform)
	preset := normalizePlatformName(config.OpenAI.Preset)

	if config.OpenAI.Platform != "" && platform == "" {
		return fmt.Errorf("platform cannot be empty or whitespace")
	}

	if config.OpenAI.Preset != "" {
		if preset == "" {
			return fmt.Errorf("preset cannot be empty or whitespace")
		}
		if !isKnownPreset(preset) {
			return fmt.Errorf("invalid preset '%s', valid presets are: [openai xai codex]", config.OpenAI.Preset)
		}
	}

	if platform != "" && preset != "" && platform != preset {
		return fmt.Errorf("openai.platform and openai.preset conflict: '%s' != '%s'", config.OpenAI.Platform, config.OpenAI.Preset)
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
