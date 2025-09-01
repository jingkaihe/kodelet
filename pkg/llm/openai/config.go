package openai

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/xai"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// loadCustomConfiguration loads custom models and pricing from configuration
// It processes presets first, then applies custom overrides if provided
func loadCustomConfiguration(config llmtypes.Config) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	var models *llmtypes.CustomModels
	var pricing llmtypes.CustomPricing

	// Determine which preset to use
	presetName := ""
	if config.OpenAI == nil {
		// No OpenAI config at all, use default preset
		presetName = "openai"
	} else if config.OpenAI.Preset != "" {
		// Explicit preset specified
		presetName = config.OpenAI.Preset
	} else {
		// OpenAI config exists but no preset (empty string means no preset)
		// Check if we have custom models/pricing, if not, use default preset
		if config.OpenAI.Models == nil && config.OpenAI.Pricing == nil {
			presetName = "openai" // Default preset when no custom config
		}
		// Otherwise, no preset (empty presetName)
	}

	// Load preset if one was determined
	if presetName != "" {
		presetModels, presetPricing := loadPreset(presetName)
		models = presetModels
		pricing = presetPricing
	}

	// Override with custom configuration if provided
	if config.OpenAI != nil {
		if config.OpenAI.Models != nil {
			if models == nil {
				models = &llmtypes.CustomModels{}
			}
			// If custom models are specified, override the preset completely
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

	// Auto-populate NonReasoning if not explicitly set
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
	switch presetName {
	case "openai":
		return loadOpenAIPreset()
	case "xai":
		return loadXAIGrokPreset()
	default:
		return nil, nil
	}
}

// loadOpenAIPreset loads the complete OpenAI configuration
func loadOpenAIPreset() (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	// Convert openaipreset.Models to llmtypes.CustomModels
	models := &llmtypes.CustomModels{
		Reasoning:    openaipreset.Models.Reasoning,
		NonReasoning: openaipreset.Models.NonReasoning,
	}

	// Convert openaipreset.Pricing to llmtypes.CustomPricing
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
	// Convert xai.Models to llmtypes.CustomModels
	models := &llmtypes.CustomModels{
		Reasoning:    xai.Models.Reasoning,
		NonReasoning: xai.Models.NonReasoning,
	}

	// Convert xai.Pricing to llmtypes.CustomPricing
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

// getPresetBaseURL returns the base URL for a given preset
func getPresetBaseURL(presetName string) string {
	switch presetName {
	case "openai":
		return openaipreset.BaseURL
	case "xai":
		return xai.BaseURL
	default:
		return ""
	}
}

// getPresetAPIKeyEnvVar returns the environment variable name for the API key for a given preset
func getPresetAPIKeyEnvVar(presetName string) string {
	switch presetName {
	case "openai":
		return openaipreset.APIKeyEnvVar
	case "xai":
		return xai.APIKeyEnvVar
	default:
		return "OPENAI_API_KEY" // default fallback
	}
}

// GetAPIKeyEnvVar returns the API key environment variable name from configuration
// Priority: custom api_key_env_var > preset default > fallback to OPENAI_API_KEY
func GetAPIKeyEnvVar(config llmtypes.Config) string {
	// Check for custom api_key_env_var first
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		return config.OpenAI.APIKeyEnvVar
	}

	// Check preset default
	if config.OpenAI != nil && config.OpenAI.Preset != "" {
		return getPresetAPIKeyEnvVar(config.OpenAI.Preset)
	}

	// Fallback to default
	return "OPENAI_API_KEY"
}

// validateCustomConfiguration validates the custom OpenAI configuration
func validateCustomConfiguration(config llmtypes.Config) error {
	if config.OpenAI == nil {
		return nil // No custom configuration to validate
	}

	// Validate preset name if specified
	if config.OpenAI.Preset != "" {
		validPresets := []string{"openai", "xai"}
		isValidPreset := false
		for _, preset := range validPresets {
			if config.OpenAI.Preset == preset {
				isValidPreset = true
				break
			}
		}
		if !isValidPreset {
			return fmt.Errorf("invalid preset '%s', valid presets are: %v", config.OpenAI.Preset, validPresets)
		}
	}

	// Validate base URL format if specified
	if config.OpenAI.BaseURL != "" {
		if !strings.HasPrefix(config.OpenAI.BaseURL, "http://") && !strings.HasPrefix(config.OpenAI.BaseURL, "https://") {
			return fmt.Errorf("base_url must start with http:// or https://")
		}
	}

	// Validate API key environment variable format if specified
	if config.OpenAI.APIKeyEnvVar != "" {
		if strings.TrimSpace(config.OpenAI.APIKeyEnvVar) == "" {
			return fmt.Errorf("api_key_env_var cannot be empty or whitespace")
		}
		// Check for common problematic characters that might cause issues
		if strings.ContainsAny(config.OpenAI.APIKeyEnvVar, " \t\n\r") {
			return fmt.Errorf("api_key_env_var cannot contain whitespace characters")
		}
	}

	// Validate pricing configuration if specified
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
