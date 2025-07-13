package openai

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/grok"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// loadCustomConfiguration loads custom models and pricing from configuration
// It processes presets first, then applies custom overrides if provided
func loadCustomConfiguration(config llmtypes.Config) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	if config.OpenAI == nil {
		return nil, nil
	}

	var models *llmtypes.CustomModels
	var pricing llmtypes.CustomPricing

	// Load preset if specified
	if config.OpenAI.Preset != "" {
		presetModels, presetPricing := loadPreset(config.OpenAI.Preset)
		models = presetModels
		pricing = presetPricing
	}

	// Override with custom configuration if provided
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
	case "xai":
		return loadXAIGrokPreset()
	default:
		return nil, nil
	}
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
		validPresets := []string{"xai"}
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
