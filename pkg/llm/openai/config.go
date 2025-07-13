package openai

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/grok"
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
	// Convert grok.Models to llmtypes.CustomModels
	models := &llmtypes.CustomModels{
		Reasoning:    grok.Models.Reasoning,
		NonReasoning: grok.Models.NonReasoning,
	}

	// Convert grok.Pricing to llmtypes.CustomPricing
	pricing := make(llmtypes.CustomPricing)
	for model, grokPricing := range grok.Pricing {
		pricing[model] = llmtypes.ModelPricing{
			Input:         grokPricing.Input,
			CachedInput:   grokPricing.CachedInput,
			Output:        grokPricing.Output,
			ContextWindow: grokPricing.ContextWindow,
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
		return grok.BaseURL
	default:
		return ""
	}
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
