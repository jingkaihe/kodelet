package google

import (
	"strings"
)

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	Input             float64
	InputHigh         float64
	Output            float64
	OutputHigh        float64
	AudioInput        float64
	ContextWindow     int
	HasThinking       bool
	TieredPricing     bool
	HighTierThreshold int
}

// ModelPricingMap contains pricing information for Google GenAI models
// Based on current Vertex AI pricing for Gemini 2.5 models
var ModelPricingMap = map[string]ModelPricing{
	// Gemini 2.5 Pro - Tiered pricing based on input tokens
	"gemini-2.5-pro": {
		Input:             0.00125,   // $1.25 per 1M tokens (<=200K input)
		InputHigh:         0.0025,    // $2.50 per 1M tokens (>200K input)
		Output:            0.01,      // $10 per 1M tokens (<=200K input)
		OutputHigh:        0.015,     // $15 per 1M tokens (>200K input)
		ContextWindow:     2_097_152, // 2M tokens
		HasThinking:       true,
		TieredPricing:     true,
		HighTierThreshold: 200_000, // 200K tokens
	},

	// Gemini 2.5 Flash - Standard multi-modal model
	"gemini-2.5-flash": {
		Input:         0.0003,    // $0.30 per 1M tokens (text, image, video)
		AudioInput:    0.001,     // $1 per 1M tokens (audio)
		Output:        0.0025,    // $2.50 per 1M tokens
		ContextWindow: 1_048_576, // 1M tokens
		HasThinking:   false,
		TieredPricing: false,
	},

	// Gemini 2.5 Flash Lite - Lightweight model
	"gemini-2.5-flash-lite": {
		Input:         0.0001,    // $0.10 per 1M tokens (text, image, video)
		AudioInput:    0.0003,    // $0.30 per 1M tokens (audio)
		Output:        0.0004,    // $0.40 per 1M tokens
		ContextWindow: 1_048_576, // 1M tokens
		HasThinking:   false,
		TieredPricing: false,
	},

	// Aliases for common model names
	"gemini-pro": {
		Input:             0.00125,
		InputHigh:         0.0025,
		Output:            0.01,
		OutputHigh:        0.015,
		ContextWindow:     2_097_152,
		HasThinking:       true,
		TieredPricing:     true,
		HighTierThreshold: 200_000,
	},

	"gemini-flash": {
		Input:         0.0003,
		AudioInput:    0.001,
		Output:        0.0025,
		ContextWindow: 1_048_576,
		HasThinking:   false,
		TieredPricing: false,
	},
}

// calculateCost calculates the actual cost based on usage and model pricing
func calculateCost(modelName string, inputTokens, outputTokens int, hasAudio bool) (inputCost, outputCost float64) {
	pricing, exists := ModelPricingMap[modelName]
	if !exists {
		// Try to find a match with different casing or partial match
		for key, value := range ModelPricingMap {
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(key)) {
				pricing = value
				exists = true
				break
			}
		}
		if !exists {
			return 0, 0
		}
	}

	// Handle tiered pricing for Pro models
	if pricing.TieredPricing && inputTokens > pricing.HighTierThreshold {
		lowTierTokens := pricing.HighTierThreshold
		highTierTokens := inputTokens - pricing.HighTierThreshold

		inputCost = (float64(lowTierTokens) * pricing.Input / 1000000) + (float64(highTierTokens) * pricing.InputHigh / 1000000)

		// Output pricing also depends on input token count
		if inputTokens <= pricing.HighTierThreshold {
			outputCost = float64(outputTokens) * pricing.Output / 1000000
		} else {
			outputCost = float64(outputTokens) * pricing.OutputHigh / 1000000
		}
	} else {
		// Standard pricing
		if hasAudio && pricing.AudioInput > 0 {
			inputCost = float64(inputTokens) * pricing.AudioInput / 1000000
		} else {
			inputCost = float64(inputTokens) * pricing.Input / 1000000
		}
		outputCost = float64(outputTokens) * pricing.Output / 1000000
	}

	return inputCost, outputCost
}

// getContextWindow returns the context window size for a given model
func getContextWindow(modelName string) int {
	pricing, exists := ModelPricingMap[modelName]
	if !exists {
		for key, value := range ModelPricingMap {
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(key)) {
				return value.ContextWindow
			}
		}
		return 1_048_576 // Default to 1M tokens
	}
	return pricing.ContextWindow
}
