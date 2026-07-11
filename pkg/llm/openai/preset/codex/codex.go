// Package codex provides preset configurations for Codex CLI models.
package codex

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the Codex model categorization for reasoning and non-reasoning models.
var Models = llm.CustomModels{
	Reasoning: []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4-mini",
		"gpt-5.4",
		"gpt-5.3-codex-spark",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-mini",
		"gpt-5.1-codex-max",
	},
	NonReasoning: []string{},
}

// Pricing defines the standard-tier pricing information for Codex models.
// Token rates mirror OpenAI API pricing in USD per token. The ChatGPT-backed
// Codex endpoint uses the flat short-context band, so no long-context fields are
// set here even for underlying models that have OpenAI API long-context rates.
var Pricing = llm.CustomPricing{
	"gpt-5.6-sol": llm.ModelPricing{
		Input:           0.000005,   // $5.00 per million tokens
		CachedInput:     0.0000005,  // $0.50 per million tokens
		CacheWriteInput: 0.00000625, // $6.25 per million tokens
		Output:          0.00003,    // $30.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.6-terra": llm.ModelPricing{
		Input:           0.0000025,   // $2.50 per million tokens
		CachedInput:     0.00000025,  // $0.25 per million tokens
		CacheWriteInput: 0.000003125, // $3.125 per million tokens
		Output:          0.000015,    // $15.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.6-luna": llm.ModelPricing{
		Input:           0.000001,   // $1.00 per million tokens
		CachedInput:     0.0000001,  // $0.10 per million tokens
		CacheWriteInput: 0.00000125, // $1.25 per million tokens
		Output:          0.000006,   // $6.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.5": llm.ModelPricing{
		Input:         0.000005,  // $5.00 per million tokens
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.00003,   // $30.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.4-mini": llm.ModelPricing{
		Input:         0.00000075,  // $0.75 per million tokens
		CachedInput:   0.000000075, // $0.075 per million tokens
		Output:        0.0000045,   // $4.50 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.4": llm.ModelPricing{
		Input:         0.0000025,  // $2.50 per million tokens
		CachedInput:   0.00000025, // $0.25 per million tokens
		Output:        0.000015,   // $15.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.3-codex-spark": llm.ModelPricing{
		Input:         0.00000175,  // $1.75 per million tokens
		CachedInput:   0.000000175, // $0.175 per million tokens
		Output:        0.000014,    // $14.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-5.3-codex": llm.ModelPricing{
		Input:         0.00000175,  // $1.75 per million tokens
		CachedInput:   0.000000175, // $0.175 per million tokens
		Output:        0.000014,    // $14.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.2-codex": llm.ModelPricing{
		Input:         0.00000175,  // $1.75 per million tokens
		CachedInput:   0.000000175, // $0.175 per million tokens
		Output:        0.000014,    // $14.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.2": llm.ModelPricing{
		Input:         0.00000175,  // $1.75 per million tokens
		CachedInput:   0.000000175, // $0.175 per million tokens
		Output:        0.000014,    // $14.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-mini": llm.ModelPricing{
		Input:         0.00000025,  // $0.25 per million tokens
		CachedInput:   0.000000025, // $0.025 per million tokens
		Output:        0.000002,    // $2.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-max": llm.ModelPricing{
		Input:         0.00000125,  // $1.25 per million tokens
		CachedInput:   0.000000125, // $0.125 per million tokens
		Output:        0.00001,     // $10.00 per million tokens
		ContextWindow: 272_000,
	},
}

// PriorityPricing defines the fast/priority-tier pricing information for Codex
// models. The `fast` service tier is sent upstream as OpenAI `priority`.
var PriorityPricing = llm.CustomPricing{
	"gpt-5.6-sol": llm.ModelPricing{
		Input:           0.00001,   // $10.00 per million tokens
		CachedInput:     0.000001,  // $1.00 per million tokens
		CacheWriteInput: 0.0000125, // $12.50 per million tokens
		Output:          0.00006,   // $60.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.6-terra": llm.ModelPricing{
		Input:           0.000005,   // $5.00 per million tokens
		CachedInput:     0.0000005,  // $0.50 per million tokens
		CacheWriteInput: 0.00000625, // $6.25 per million tokens
		Output:          0.00003,    // $30.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.6-luna": llm.ModelPricing{
		Input:           0.000002,  // $2.00 per million tokens
		CachedInput:     0.0000002, // $0.20 per million tokens
		CacheWriteInput: 0.0000025, // $2.50 per million tokens
		Output:          0.000012,  // $12.00 per million tokens
		ContextWindow:   372_000,
	},
	"gpt-5.5": llm.ModelPricing{
		Input:         0.0000125,  // $12.50 per million tokens
		CachedInput:   0.00000125, // $1.25 per million tokens
		Output:        0.000075,   // $75.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.4-mini": llm.ModelPricing{
		Input:         0.0000015,  // $1.50 per million tokens
		CachedInput:   0.00000015, // $0.15 per million tokens
		Output:        0.000009,   // $9.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.4": llm.ModelPricing{
		Input:         0.000005,  // $5.00 per million tokens
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.00003,   // $30.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.3-codex-spark": llm.ModelPricing{
		Input:         0.0000035,  // $3.50 per million tokens
		CachedInput:   0.00000035, // $0.35 per million tokens
		Output:        0.000028,   // $28.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-5.3-codex": llm.ModelPricing{
		Input:         0.0000035,  // $3.50 per million tokens
		CachedInput:   0.00000035, // $0.35 per million tokens
		Output:        0.000028,   // $28.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.2-codex": llm.ModelPricing{
		Input:         0.0000035,  // $3.50 per million tokens
		CachedInput:   0.00000035, // $0.35 per million tokens
		Output:        0.000028,   // $28.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.2": llm.ModelPricing{
		Input:         0.0000035,  // $3.50 per million tokens
		CachedInput:   0.00000035, // $0.35 per million tokens
		Output:        0.000028,   // $28.00 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-mini": llm.ModelPricing{
		Input:         0.00000045,  // $0.45 per million tokens
		CachedInput:   0.000000045, // $0.045 per million tokens
		Output:        0.0000036,   // $3.60 per million tokens
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-max": llm.ModelPricing{
		Input:         0.0000025,  // $2.50 per million tokens
		CachedInput:   0.00000025, // $0.25 per million tokens
		Output:        0.00002,    // $20.00 per million tokens
		ContextWindow: 272_000,
	},
}

func PricingForServiceTier(serviceTier llm.OpenAIServiceTier) llm.CustomPricing {
	pricing := make(llm.CustomPricing, len(Pricing))
	for model, modelPricing := range Pricing {
		pricing[model] = modelPricing
	}

	tier, ok := llm.ParseOpenAIServiceTier(string(serviceTier))
	if !ok || (tier != llm.OpenAIServiceTierFast && tier != llm.OpenAIServiceTierPriority) {
		return pricing
	}

	for model, modelPricing := range PriorityPricing {
		pricing[model] = modelPricing
	}

	return pricing
}

// BaseURL is the API endpoint for Codex models (via ChatGPT backend).
const BaseURL = "https://chatgpt.com/backend-api/codex"

// DefaultModel is the default model for Codex.
const DefaultModel = "gpt-5.6-sol"
