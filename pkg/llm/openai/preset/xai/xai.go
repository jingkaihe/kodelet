// Package xai provides preset configurations for xAI Grok models
package xai

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the xAI Grok model categorization for reasoning and non-reasoning models
var Models = llm.CustomModels{
	Reasoning: []string{
		"grok-code-fast-1",
		"grok-4-0709",
		"grok-3-mini",
	},
	NonReasoning: []string{
		"grok-3",
		"grok-2-image-1212",
	},
}

// Pricing defines the pricing information for all xAI Grok models
var Pricing = llm.CustomPricing{
	"grok-code-fast-1": llm.ModelPricing{
		Input:         0.0000002, // $0.20 per million tokens
		Output:        0.0000015, // $1.50 per million tokens
		ContextWindow: 256000,    // 256k tokens
	},
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
		Output:        0.0000005, // $0.50 per million tokens
		ContextWindow: 131072,    // 131k tokens
	},
	"grok-2-image-1212": llm.ModelPricing{
		Input:         0,       // Image generation model - no input token cost
		Output:        0.00007, // $0.07 per image output
		ContextWindow: 32768,   // Context window for image generation
	},
}

// BaseURL is the API endpoint for xAI Grok models
const BaseURL = "https://api.x.ai/v1"

// APIKeyEnvVar is the environment variable name for the xAI API key
const APIKeyEnvVar = "XAI_API_KEY"
