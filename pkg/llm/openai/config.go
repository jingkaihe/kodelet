package openai

import (
	"fmt"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// CustomModels holds model categorization for custom configurations
type CustomModels struct {
	Reasoning    []string
	NonReasoning []string
}

// CustomPricing maps model names to their pricing information
type CustomPricing map[string]ModelPricing

// loadCustomConfiguration loads custom models and pricing from configuration
// It processes presets first, then applies custom overrides if provided
func loadCustomConfiguration(config llmtypes.Config) (*CustomModels, CustomPricing) {
	if config.OpenAI == nil {
		return nil, nil
	}

	var models *CustomModels
	var pricing CustomPricing

	// Load preset if specified
	if config.OpenAI.Preset != "" {
		presetModels, presetPricing := loadPreset(config.OpenAI.Preset)
		models = presetModels
		pricing = presetPricing
	}

	// Override with custom configuration if provided
	if config.OpenAI.Models != nil {
		if models == nil {
			models = &CustomModels{}
		}
		// If custom models are specified, override the preset completely
		models.Reasoning = config.OpenAI.Models.Reasoning
		models.NonReasoning = config.OpenAI.Models.NonReasoning
	}

	if config.OpenAI.Pricing != nil {
		if pricing == nil {
			pricing = make(CustomPricing)
		}
		for model, p := range config.OpenAI.Pricing {
			pricing[model] = ModelPricing{
				Input:         p.Input,
				CachedInput:   p.CachedInput,
				Output:        p.Output,
				ContextWindow: p.ContextWindow,
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
func loadPreset(presetName string) (*CustomModels, CustomPricing) {
	switch presetName {
	case "xai-grok":
		return loadXAIGrokPreset()
	default:
		return nil, nil
	}
}

// loadXAIGrokPreset loads the complete xAI Grok configuration
func loadXAIGrokPreset() (*CustomModels, CustomPricing) {
	models := &CustomModels{
		Reasoning: []string{
			"grok-4-0709",
			"grok-3-mini",
			"grok-3-mini-fast",
		},
		NonReasoning: []string{
			"grok-3",
			"grok-3-fast",
			"grok-2-vision-1212",
		},
	}

	pricing := CustomPricing{
		"grok-4-0709": ModelPricing{
			Input:         0.000003,  // $3 per million tokens
			Output:        0.000015,  // $15 per million tokens
			ContextWindow: 256000,    // 256k tokens
		},
		"grok-3": ModelPricing{
			Input:         0.000003,  // $3 per million tokens
			Output:        0.000015,  // $15 per million tokens
			ContextWindow: 131072,    // 131k tokens
		},
		"grok-3-mini": ModelPricing{
			Input:         0.0000003, // $0.30 per million tokens
			Output:        0.0000009, // $0.90 per million tokens
			ContextWindow: 131072,    // 131k tokens
		},
		"grok-3-fast": ModelPricing{
			Input:         0.000005,  // $5 per million tokens
			Output:        0.000025,  // $25 per million tokens
			ContextWindow: 131072,    // 131k tokens
		},
		"grok-3-mini-fast": ModelPricing{
			Input:         0.0000006, // $0.60 per million tokens
			Output:        0.000004,  // $4 per million tokens
			ContextWindow: 131072,    // 131k tokens
		},
		"grok-2-vision-1212": ModelPricing{
			Input:         0.000002,  // $2 per million tokens
			Output:        0.00001,   // $10 per million tokens
			ContextWindow: 32768,     // 32k tokens (vision model)
		},
	}

	return models, pricing
}

// getPresetBaseURL returns the base URL for a given preset
func getPresetBaseURL(presetName string) string {
	switch presetName {
	case "xai-grok":
		return "https://api.x.ai/v1"
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
		validPresets := []string{"xai-grok"}
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