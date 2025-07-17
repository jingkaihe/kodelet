// Package grok provides preset configurations for xAI Grok models
package grok

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the xAI Grok model categorization for reasoning and non-reasoning models
var Models = llm.CustomModels{
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

// Pricing defines the pricing information for all xAI Grok models
var Pricing = llm.CustomPricing{
	"grok-4-0709": llm.ModelPricing{
		Input:         0.000003, // $3 per million tokens
		Output:        0.000015, // $15 per million tokens
		ContextWindow: 256000,   // 256k tokens
	},
	"grok-3": llm.ModelPricing{
		Input:         0.000003, // $3 per million tokens
		Output:        0.000015, // $15 per million tokens
		ContextWindow: 131072,   // 131k tokens
	},
	"grok-3-mini": llm.ModelPricing{
		Input:         0.0000003, // $0.30 per million tokens
		Output:        0.0000009, // $0.90 per million tokens
		ContextWindow: 131072,    // 131k tokens
	},
	"grok-3-fast": llm.ModelPricing{
		Input:         0.000005, // $5 per million tokens
		Output:        0.000025, // $25 per million tokens
		ContextWindow: 131072,   // 131k tokens
	},
	"grok-3-mini-fast": llm.ModelPricing{
		Input:         0.0000006, // $0.60 per million tokens
		Output:        0.000004,  // $4 per million tokens
		ContextWindow: 131072,    // 131k tokens
	},
	"grok-2-vision-1212": llm.ModelPricing{
		Input:         0.000002, // $2 per million tokens
		Output:        0.00001,  // $10 per million tokens
		ContextWindow: 32768,    // 32k tokens (vision model)
	},
}

// BaseURL is the API endpoint for xAI Grok models
const BaseURL = "https://api.x.ai/v1"

// APIKeyEnvVar is the environment variable name for the xAI API key
const APIKeyEnvVar = "XAI_API_KEY"
