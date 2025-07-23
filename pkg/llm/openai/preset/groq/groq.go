// Package groq provides preset configurations for Groq models
package groq

import "github.com/jingkaihe/kodelet/pkg/types/llm"

// Models defines the Groq model categorization for reasoning and non-reasoning models
// Based on official Groq documentation: https://console.groq.com/docs/reasoning
var Models = llm.CustomModels{
	Reasoning: []string{
		"deepseek-r1-distill-llama-70b",
		"qwen/qwen3-32b",
	},
	NonReasoning: []string{
		"llama-3.1-8b-instant",
		"llama-3.3-70b-versatile",
		"llama3-8b-8192",
		"llama3-70b-8192",
		"gemma2-9b-it",
		"meta-llama/llama-guard-4-12b",
		"meta-llama/llama-prompt-guard-2-22m",
		"meta-llama/llama-prompt-guard-2-86m",
		"meta-llama/llama-4-maverick-17b-128e-instruct",
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"mistral-saba-24b",
		"moonshotai/kimi-k2-instruct",
		"whisper-large-v3",
		"whisper-large-v3-turbo",
		"distil-whisper-large-v3-en",
	},
}

// Pricing defines the pricing information for all Groq models
// Pricing based on Groq's official pricing as of the image data
var Pricing = llm.CustomPricing{
	// Llama 3.1 models
	"llama-3.1-8b-instant": llm.ModelPricing{
		Input:         0.00000005, // $0.05 per million tokens
		Output:        0.00000008, // $0.08 per million tokens
		ContextWindow: 131072,     // 131K tokens
	},
	"llama-3.3-70b-versatile": llm.ModelPricing{
		Input:         0.00000059, // $0.59 per million tokens
		Output:        0.00000079, // $0.79 per million tokens
		ContextWindow: 131072,     // 131K tokens
	},
	// Llama 3 models (legacy naming)
	"llama3-8b-8192": llm.ModelPricing{
		Input:         0.00000005, // $0.05 per million tokens
		Output:        0.00000008, // $0.08 per million tokens
		ContextWindow: 8192,       // 8K tokens
	},
	"llama3-70b-8192": llm.ModelPricing{
		Input:         0.00000059, // $0.59 per million tokens
		Output:        0.00000079, // $0.79 per million tokens
		ContextWindow: 8192,       // 8K tokens
	},
	// Gemma models
	"gemma2-9b-it": llm.ModelPricing{
		Input:         0.0000002, // $0.20 per million tokens
		Output:        0.0000002, // $0.20 per million tokens
		ContextWindow: 8192,      // 8K tokens
	},
	// Meta Llama Guard models
	"meta-llama/llama-guard-4-12b": llm.ModelPricing{
		Input:         0.0000002, // $0.20 per million tokens
		Output:        0.0000002, // $0.20 per million tokens
		ContextWindow: 131072,    // 131K tokens
	},
	"meta-llama/llama-prompt-guard-2-22m": llm.ModelPricing{
		Input:         0.0000001, // Estimated lower pricing for smaller model
		Output:        0.0000001,
		ContextWindow: 512,
	},
	"meta-llama/llama-prompt-guard-2-86m": llm.ModelPricing{
		Input:         0.0000001, // Estimated lower pricing for smaller model
		Output:        0.0000001,
		ContextWindow: 512,
	},
	// Reasoning models (preview)
	"deepseek-r1-distill-llama-70b": llm.ModelPricing{
		Input:         0.00000059, // Same as 70B model
		Output:        0.00000079,
		ContextWindow: 131072,
	},
	"meta-llama/llama-4-maverick-17b-128e-instruct": llm.ModelPricing{
		Input:         0.0000002, // Estimated pricing for 17B model
		Output:        0.0000003,
		ContextWindow: 131072,
	},
	"meta-llama/llama-4-scout-17b-16e-instruct": llm.ModelPricing{
		Input:         0.0000002, // Estimated pricing for 17B model
		Output:        0.0000003,
		ContextWindow: 131072,
	},
	// Other models
	"mistral-saba-24b": llm.ModelPricing{
		Input:         0.0000003, // Estimated pricing for 24B model
		Output:        0.0000004,
		ContextWindow: 32768,
	},
	"moonshotai/kimi-k2-instruct": llm.ModelPricing{
		Input:         0.0000002, // Estimated pricing
		Output:        0.0000003,
		ContextWindow: 131072,
	},
	"qwen/qwen3-32b": llm.ModelPricing{
		Input:         0.0000003, // Estimated pricing for 32B model
		Output:        0.0000004,
		ContextWindow: 131072,
	},
	// Audio models - typically priced differently, using conservative estimates
	"whisper-large-v3": llm.ModelPricing{
		Input:         0.0000001,
		Output:        0.0000001,
		ContextWindow: 25000, // Typical audio model context
	},
	"whisper-large-v3-turbo": llm.ModelPricing{
		Input:         0.0000001,
		Output:        0.0000001,
		ContextWindow: 25000,
	},
	"distil-whisper-large-v3-en": llm.ModelPricing{
		Input:         0.00000005,
		Output:        0.00000005,
		ContextWindow: 25000,
	},
}

// BaseURL is the API endpoint for Groq models
const BaseURL = "https://api.groq.com/openai/v1"

// APIKeyEnvVar is the environment variable name for the Groq API key
const APIKeyEnvVar = "GROQ_API_KEY"
