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
	// Latest models
	anthropic.ModelClaudeSonnet4_0: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeSonnet4_20250514: {
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
	anthropic.ModelClaude4Opus20250514: {
		Input:              0.000015,   // $15.00 per million tokens
		Output:             0.000075,   // $75.00 per million tokens
		PromptCachingWrite: 0.00001875, // $18.75 per million tokens
		PromptCachingRead:  0.0000015,  // $1.50 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaudeOpus4_0: {
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
	anthropic.ModelClaude3_5HaikuLatest: {
		Input:              0.0000008,  // $0.80 per million tokens
		Output:             0.000004,   // $4.00 per million tokens
		PromptCachingWrite: 0.000001,   // $1.00 per million tokens
		PromptCachingRead:  0.00000008, // $0.08 per million tokens
		ContextWindow:      200_000,
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
	// Legacy models
	anthropic.ModelClaude_3_Haiku_20240307: {
		Input:              0.00000025, // $0.25 per million tokens
		Output:             0.00000125, // $1.25 per million tokens
		PromptCachingWrite: 0.0000003,  // $0.30 per million tokens
		PromptCachingRead:  0.00000003, // $0.03 per million tokens
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
	lowerModel := strings.ToLower(string(model))
	if strings.Contains(lowerModel, "claude-sonnet-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeSonnet4_5]
	} else if strings.Contains(lowerModel, "claude-4-sonnet") || strings.Contains(lowerModel, "claude-sonnet-4") {
		return ModelPricingMap[anthropic.ModelClaudeSonnet4_0]
	} else if strings.Contains(lowerModel, "claude-opus-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_5_20251101]
	} else if strings.Contains(lowerModel, "claude-opus-4-1") {
		return ModelPricingMap[anthropic.ModelClaudeOpus4_1_20250805]
	} else if strings.Contains(lowerModel, "claude-4-opus") || strings.Contains(lowerModel, "claude-opus-4") {
		return ModelPricingMap[anthropic.ModelClaude4Opus20250514]
	} else if strings.Contains(lowerModel, "claude-haiku-4-5") {
		return ModelPricingMap[anthropic.ModelClaudeHaiku4_5]
	} else if strings.Contains(lowerModel, "claude-3-5-haiku") {
		return ModelPricingMap[anthropic.ModelClaude3_5HaikuLatest]
	} else if strings.Contains(lowerModel, "claude-3-haiku") {
		return ModelPricingMap["claude-3-haiku-20240307"]
	}

	// Default to Claude Sonnet 4.5 pricing if no match
	return ModelPricingMap[anthropic.ModelClaudeSonnet4_5_20250929]
}
