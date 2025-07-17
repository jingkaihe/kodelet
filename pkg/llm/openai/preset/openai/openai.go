// Package openai provides preset configurations for OpenAI models
package openai

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the OpenAI model categorization for reasoning and non-reasoning models
var Models = llm.CustomModels{
	Reasoning: []string{
		"o1",
		"o1-pro",
		"o1-mini",
		"o3",
		"o3-pro",
		"o3-mini",
		"o3-deep-research",
		"o4-mini",
		"o4-mini-deep-research",
	},
	NonReasoning: []string{
		"gpt-4.1",
		"gpt-4.1-mini",
		"gpt-4.1-nano",
		"gpt-4.5-preview",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4o-audio-preview",
		"gpt-4o-realtime-preview",
		"gpt-4o-mini-audio-preview",
		"gpt-4o-mini-realtime-preview",
		"gpt-4o-mini-search-preview",
		"gpt-4o-search-preview",
		"computer-use-preview",
		"gpt-image-1",
		"codex-mini-latest",
	},
}

// Pricing defines the pricing information for all OpenAI models
var Pricing = llm.CustomPricing{
	"gpt-4.1": llm.ModelPricing{
		Input:         0.000002,  // $2.00 per million tokens
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.000008,  // $8.00 per million tokens
		ContextWindow: 1047576,
	},
	"gpt-4.1-mini": llm.ModelPricing{
		Input:         0.0000004, // $0.40 per million tokens
		CachedInput:   0.0000001, // $0.10 per million tokens
		Output:        0.0000016, // $1.60 per million tokens
		ContextWindow: 1047576,
	},
	"gpt-4.1-nano": llm.ModelPricing{
		Input:         0.0000001,   // $0.10 per million tokens
		CachedInput:   0.000000025, // $0.025 per million tokens
		Output:        0.0000004,   // $0.40 per million tokens
		ContextWindow: 1047576,
	},
	"gpt-4.5-preview": llm.ModelPricing{
		Input:         0.000075,  // $75.00 per million tokens
		CachedInput:   0.0000375, // $37.50 per million tokens
		Output:        0.00015,   // $150.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o": llm.ModelPricing{
		Input:         0.0000025,  // $2.50 per million tokens
		CachedInput:   0.00000125, // $1.25 per million tokens
		Output:        0.00001,    // $10.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-audio-preview": llm.ModelPricing{
		Input:         0.0000025, // $2.50 per million tokens
		Output:        0.00001,   // $10.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-realtime-preview": llm.ModelPricing{
		Input:         0.000005,  // $5.00 per million tokens
		CachedInput:   0.0000025, // $2.50 per million tokens
		Output:        0.00002,   // $20.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-mini": llm.ModelPricing{
		Input:         0.00000015,  // $0.15 per million tokens
		CachedInput:   0.000000075, // $0.075 per million tokens
		Output:        0.0000006,   // $0.60 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-mini-audio-preview": llm.ModelPricing{
		Input:         0.00000015, // $0.15 per million tokens
		Output:        0.0000006,  // $0.60 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-mini-realtime-preview": llm.ModelPricing{
		Input:         0.0000006, // $0.60 per million tokens
		CachedInput:   0.0000003, // $0.30 per million tokens
		Output:        0.0000024, // $2.40 per million tokens
		ContextWindow: 128_000,
	},
	"o1": llm.ModelPricing{
		Input:         0.000015,  // $15.00 per million tokens
		CachedInput:   0.0000075, // $7.50 per million tokens
		Output:        0.00006,   // $60.00 per million tokens
		ContextWindow: 128_000,
	},
	"o1-pro": llm.ModelPricing{
		Input:         0.00015, // $150.00 per million tokens
		Output:        0.0006,  // $600.00 per million tokens
		ContextWindow: 128_000,
	},
	"o3": llm.ModelPricing{
		Input:         0.000002,  // $2.00 per million tokens
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.000008,  // $8.00 per million tokens
		ContextWindow: 200_000,
	},
	"o4-mini": llm.ModelPricing{
		Input:         0.0000011,   // $1.10 per million tokens
		CachedInput:   0.000000275, // $0.275 per million tokens
		Output:        0.0000044,   // $4.40 per million tokens
		ContextWindow: 200_000,
	},
	"o3-mini": llm.ModelPricing{
		Input:         0.0000011,  // $1.10 per million tokens
		CachedInput:   0.00000055, // $0.55 per million tokens
		Output:        0.0000044,  // $4.40 per million tokens
		ContextWindow: 200_000,
	},
	"o1-mini": llm.ModelPricing{
		Input:         0.0000011,  // $1.10 per million tokens
		CachedInput:   0.00000055, // $0.55 per million tokens
		Output:        0.0000044,  // $4.40 per million tokens
		ContextWindow: 128_000,
	},
	"codex-mini-latest": llm.ModelPricing{
		Input:         0.0000015,   // $1.50 per million tokens
		CachedInput:   0.000000375, // $0.375 per million tokens
		Output:        0.000006,    // $6.00 per million tokens
		ContextWindow: 200_000,
	},
	"gpt-4o-mini-search-preview": llm.ModelPricing{
		Input:         0.00000015, // $0.15 per million tokens
		Output:        0.0000006,  // $0.60 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-4o-search-preview": llm.ModelPricing{
		Input:         0.0000025, // $2.50 per million tokens
		Output:        0.00001,   // $10.00 per million tokens
		ContextWindow: 128_000,
	},
	"computer-use-preview": llm.ModelPricing{
		Input:         0.000003, // $3.00 per million tokens
		Output:        0.000012, // $12.00 per million tokens
		ContextWindow: 128_000,
	},
	"gpt-image-1": llm.ModelPricing{
		Input:         0.000005,   // $5.00 per million tokens
		CachedInput:   0.00000125, // $1.25 per million tokens
		ContextWindow: 128_000,
	},
	"o3-pro": llm.ModelPricing{
		Input:         0.00002, // $20.00 per million tokens
		Output:        0.00008, // $80.00 per million tokens
		ContextWindow: 200_000,
	},
	"o3-deep-research": llm.ModelPricing{
		Input:         0.00001,   // $10.00 per million tokens
		CachedInput:   0.0000025, // $2.50 per million tokens
		Output:        0.00004,   // $40.00 per million tokens
		ContextWindow: 200_000,
	},
	"o4-mini-deep-research": llm.ModelPricing{
		Input:         0.000002,  // $2.00 per million tokens
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.000008,  // $8.00 per million tokens
		ContextWindow: 200_000,
	},
}

// BaseURL is the API endpoint for OpenAI models
const BaseURL = "https://api.openai.com/v1"

// APIKeyEnvVar is the environment variable name for the OpenAI API key
const APIKeyEnvVar = "OPENAI_API_KEY"
