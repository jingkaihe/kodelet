package anthropic

import (
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	Input              float64
	Output             float64
	PromptCachingWrite float64
	PromptCachingRead  float64
	ContextWindow      int
}

// ModelPricingMap maps model names to their pricing information
var ModelPricingMap = map[anthropic.Model]ModelPricing{
	anthropic.ModelClaudeSonnet4_6: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      1_000_000,
	},
	anthropic.ModelClaudeSonnet4_5: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeSonnet4_5_20250929: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_1: {
		Input:              0.000015,   // $15.00 per million tokens
		Output:             0.000075,   // $75.00 per million tokens
		PromptCachingWrite: 0.00001875, // $18.75 per million tokens
		PromptCachingRead:  0.0000015,  // $1.50 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_1_20250805: {
		Input:              0.000015,   // $15.00 per million tokens
		Output:             0.000075,   // $75.00 per million tokens
		PromptCachingWrite: 0.00001875, // $18.75 per million tokens
		PromptCachingRead:  0.0000015,  // $1.50 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_5_20251101: {
		Input:              0.000005,   // $5.00 per million tokens
		Output:             0.000025,   // $25.00 per million tokens
		PromptCachingWrite: 0.00000625, // $6.25 per million tokens
		PromptCachingRead:  0.0000005,  // $0.50 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_5: {
		Input:              0.000005,   // $5.00 per million tokens
		Output:             0.000025,   // $25.00 per million tokens
		PromptCachingWrite: 0.00000625, // $6.25 per million tokens
		PromptCachingRead:  0.0000005,  // $0.50 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_7: {
		Input:              0.000005,   // $5.00 per million tokens
		Output:             0.000025,   // $25.00 per million tokens
		PromptCachingWrite: 0.00000625, // $6.25 per million tokens
		PromptCachingRead:  0.0000005,  // $0.50 per million tokens
		ContextWindow:      1_000_000,
	},
	anthropic.ModelClaudeOpus4_6: {
		Input:              0.000005,   // $5.00 per million tokens
		Output:             0.000025,   // $25.00 per million tokens
		PromptCachingWrite: 0.00000625, // $6.25 per million tokens
		PromptCachingRead:  0.0000005,  // $0.50 per million tokens
		ContextWindow:      1_000_000,
	},
	anthropic.ModelClaudeHaiku4_5: {
		Input:              0.000001,   // $1.00 per million tokens
		Output:             0.000005,   // $5.00 per million tokens
		PromptCachingWrite: 0.00000125, // $1.25 per million tokens
		PromptCachingRead:  0.0000001,  // $0.10 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeHaiku4_5_20251001: {
		Input:              0.000001,   // $1.00 per million tokens
		Output:             0.000005,   // $5.00 per million tokens
		PromptCachingWrite: 0.00000125, // $1.25 per million tokens
		PromptCachingRead:  0.0000001,  // $0.10 per million tokens
		ContextWindow:      200_000,
	},
}

// getModelPricing returns the pricing information for a given model
func getModelPricing(model anthropic.Model) ModelPricing {
	// First try exact match
	if pricing, ok := ModelPricingMap[model]; ok {
		return pricing
	}
	// Try to find a match based on model family
	lowerModel := strings.ToLower(model)
	if strings.Contains(lowerModel, "claude-sonnet-4-6") {
		return ModelPricingMap[anthropic.ModelClaudeSonnet4_6]
	} else if strings.Contains(lowerModel, "claude-sonnet-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeSonnet4_5]
	} else if strings.Contains(lowerModel, "claude-opus-4-7") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_7]
	} else if strings.Contains(lowerModel, "claude-opus-4-6") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_6]
	} else if strings.Contains(lowerModel, "claude-opus-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_5_20251101]
	} else if strings.Contains(lowerModel, "claude-opus-4-1") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_1_20250805]
	} else if strings.Contains(lowerModel, "claude-haiku-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeHaiku4_5]
	}

	// Default to Claude Sonnet 4.6 pricing if no match
	return ModelPricingMap[anthropic.ModelClaudeSonnet4_6]
}
