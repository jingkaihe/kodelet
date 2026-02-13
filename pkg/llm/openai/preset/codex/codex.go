// Package codex provides preset configurations for Codex CLI models.
package codex

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the Codex model categorization for reasoning and non-reasoning models.
var Models = llm.CustomModels{
	Reasoning: []string{
		"gpt-5.3-codex",
		"gpt-5.3-codex-spark",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
	},
	NonReasoning: []string{},
}

// Pricing defines the pricing information for Codex models.
// Note: Codex subscription pricing is included in the ChatGPT subscription.
var Pricing = llm.CustomPricing{
	"gpt-5.3-codex": llm.ModelPricing{
		Input:         0.0, // Included in subscription
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.3-codex-spark": llm.ModelPricing{
		Input:         0.0, // Included in subscription
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.2-codex": llm.ModelPricing{
		Input:         0.0, // Included in subscription
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.2": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-max": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-mini": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
}

// BaseURL is the API endpoint for Codex models (via ChatGPT backend).
const BaseURL = "https://chatgpt.com/backend-api/codex"

// DefaultModel is the default model for Codex.
const DefaultModel = "gpt-5.3-codex"
