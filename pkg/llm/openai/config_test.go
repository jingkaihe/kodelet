package openai

import (
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// Define expected OpenAI platform defaults once to avoid duplication
var (
	expectedOpenAIReasoningModels = []string{
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.5-pro",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano",
		"gpt-5.4-pro",
		"gpt-5.2", "gpt-5.2-pro",
		"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-chat-latest",
		"gpt-5.3-codex", "gpt-5.2-codex",
		"gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
		"o1", "o1-pro", "o1-mini", "o3", "o3-pro", "o3-mini",
		"o3-deep-research", "o4-mini", "o4-mini-deep-research",
	}

	expectedOpenAINonReasoningModels = []string{
		"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4.5-preview",
		"gpt-4o", "gpt-4o-2024-05-13", "gpt-4o-mini", "gpt-4o-audio-preview", "gpt-4o-realtime-preview",
		"gpt-4o-mini-audio-preview", "gpt-4o-mini-realtime-preview",
		"gpt-4o-mini-search-preview", "gpt-4o-search-preview",
		"computer-use-preview", "gpt-image-1", "codex-mini-latest",
	}
)

func TestLoadCustomConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		config     llmtypes.Config
		expected   *llmtypes.CustomModels
		hasModels  bool
		hasPricing bool
	}{
		{
			name:   "no custom config uses openai platform defaults",
			config: llmtypes.Config{},
			expected: &llmtypes.CustomModels{
				Reasoning:    expectedOpenAIReasoningModels,
				NonReasoning: expectedOpenAINonReasoningModels,
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "explicit openai platform",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "openai",
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning:    expectedOpenAIReasoningModels,
				NonReasoning: expectedOpenAINonReasoningModels,
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "custom models only (no platform defaults)",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "", // Explicitly empty platform
					Models: &llmtypes.CustomModels{
						Reasoning:    []string{"custom-reasoning-model"},
						NonReasoning: []string{"custom-regular-model"},
					},
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning:    []string{"custom-reasoning-model"},
				NonReasoning: []string{"custom-regular-model"},
			},
			hasModels:  true,
			hasPricing: false, // No platform defaults loaded when platform is explicitly empty
		},
		{
			name: "custom platform with custom override",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "custom-provider",
					Models: &llmtypes.CustomModels{
						Reasoning: []string{"custom-override-model"},
					},
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning:    []string{"custom-override-model"},
				NonReasoning: nil,
			},
			hasModels:  true,
			hasPricing: false,
		},
		{
			name: "auto-populate non-reasoning models",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "", // Explicitly empty platform to avoid default OpenAI platform loading
					Models: &llmtypes.CustomModels{
						Reasoning: []string{"model-a"},
					},
					Pricing: map[string]llmtypes.ModelPricing{
						"model-a": {Input: 0.001, Output: 0.002, ContextWindow: 128000},
						"model-b": {Input: 0.003, Output: 0.004, ContextWindow: 64000},
						"model-c": {Input: 0.005, Output: 0.006, ContextWindow: 32000},
					},
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning:    []string{"model-a"},
				NonReasoning: []string{"model-b", "model-c"}, // Auto-populated from pricing
			},
			hasModels:  true,
			hasPricing: true, // Has pricing because custom pricing is provided
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, pricing := loadCustomConfiguration(tt.config)

			if !tt.hasModels {
				assert.Nil(t, models)
			} else {
				require.NotNil(t, models)
				assert.ElementsMatch(t, tt.expected.Reasoning, models.Reasoning)
				assert.ElementsMatch(t, tt.expected.NonReasoning, models.NonReasoning)
			}

			if !tt.hasPricing {
				assert.Nil(t, pricing)
			} else {
				assert.NotNil(t, pricing)
			}
		})
	}
}

func TestLoadCodexPlatformDefaults(t *testing.T) {
	models, pricing := loadCodexPlatformDefaults()

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	expectedReasoning := []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.3-codex",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
	}
	assert.ElementsMatch(t, expectedReasoning, models.Reasoning)
	assert.Empty(t, models.NonReasoning)

	gpt56SolPricing, exists := pricing["gpt-5.6-sol"]
	require.True(t, exists)
	assert.Equal(t, 0.000005, gpt56SolPricing.Input)
	assert.Equal(t, 0.0000005, gpt56SolPricing.CachedInput)
	assert.Equal(t, 0.00000625, gpt56SolPricing.CacheWriteInput)
	assert.Equal(t, 0.00003, gpt56SolPricing.Output)
	assert.Equal(t, 372_000, gpt56SolPricing.ContextWindow)

	gpt55Pricing, exists := pricing["gpt-5.5"]
	require.True(t, exists)
	assert.Equal(t, 0.000005, gpt55Pricing.Input)
	assert.Equal(t, 0.0000005, gpt55Pricing.CachedInput)
	assert.Equal(t, 0.00003, gpt55Pricing.Output)
	assert.Equal(t, 0, gpt55Pricing.LongContextThreshold)
	assert.Equal(t, 272_000, gpt55Pricing.ContextWindow)

	gpt54Pricing, exists := pricing["gpt-5.4"]
	require.True(t, exists)
	assert.Equal(t, 0.0000025, gpt54Pricing.Input)
	assert.Equal(t, 0.00000025, gpt54Pricing.CachedInput)
	assert.Equal(t, 0.000015, gpt54Pricing.Output)
	assert.Equal(t, 0, gpt54Pricing.LongContextThreshold)
	assert.Equal(t, 272_000, gpt54Pricing.ContextWindow)

	miniPricing, exists := pricing["gpt-5.4-mini"]
	require.True(t, exists)
	assert.Equal(t, 0.00000075, miniPricing.Input)
	assert.Equal(t, 0.000000075, miniPricing.CachedInput)
	assert.Equal(t, 0.0000045, miniPricing.Output)
	assert.Equal(t, 272_000, miniPricing.ContextWindow)

	sparkPricing, exists := pricing["gpt-5.3-codex-spark"]
	require.True(t, exists)
	assert.Equal(t, 0.00000175, sparkPricing.Input)
	assert.Equal(t, 0.000000175, sparkPricing.CachedInput)
	assert.Equal(t, 0.000014, sparkPricing.Output)
	assert.Equal(t, 128_000, sparkPricing.ContextWindow)

	legacyPricing, exists := pricing["gpt-5.1-codex-mini"]
	require.True(t, exists)
	assert.Equal(t, 0.00000025, legacyPricing.Input)
	assert.Equal(t, 0.000000025, legacyPricing.CachedInput)
	assert.Equal(t, 0.000002, legacyPricing.Output)
	assert.Equal(t, 272_000, legacyPricing.ContextWindow)

	for _, model := range models.Reasoning {
		_, exists := pricing[model]
		assert.True(t, exists, "Model %s should have pricing information", model)
	}
}

func TestLoadCodexPlatformDefaultsFastTier(t *testing.T) {
	models, pricing := loadCustomConfiguration(llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "codex",
			ServiceTier: llmtypes.OpenAIServiceTierFast,
		},
	})

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	gpt53CodexPricing, exists := pricing["gpt-5.3-codex"]
	require.True(t, exists)
	assert.Equal(t, 0.0000035, gpt53CodexPricing.Input)
	assert.Equal(t, 0.00000035, gpt53CodexPricing.CachedInput)
	assert.Equal(t, 0.000028, gpt53CodexPricing.Output)
	assert.Equal(t, 272_000, gpt53CodexPricing.ContextWindow)

	gpt55Pricing, exists := pricing["gpt-5.5"]
	require.True(t, exists)
	assert.Equal(t, 0.0000125, gpt55Pricing.Input)
	assert.Equal(t, 0.00000125, gpt55Pricing.CachedInput)
	assert.Equal(t, 0.000075, gpt55Pricing.Output)
	assert.Equal(t, 0, gpt55Pricing.LongContextThreshold)

	gpt56SolPricing, exists := pricing["gpt-5.6-sol"]
	require.True(t, exists)
	assert.Equal(t, 0.00001, gpt56SolPricing.Input)
	assert.Equal(t, 0.000001, gpt56SolPricing.CachedInput)
	assert.Equal(t, 0.0000125, gpt56SolPricing.CacheWriteInput)
	assert.Equal(t, 0.00006, gpt56SolPricing.Output)
	assert.Equal(t, 372_000, gpt56SolPricing.ContextWindow)
}

func TestBuildCopilotPlatformDefaults(t *testing.T) {
	models, pricing := buildCopilotPlatformDefaults([]auth.CopilotModelCatalogEntry{
		{
			ID: "gpt-5.4",
			Capabilities: auth.CopilotModelCapabilities{
				Limits:   auth.CopilotModelLimits{MaxContextWindowTokens: 400000},
				Supports: auth.CopilotModelSupport{ReasoningEffort: []string{"low", "medium", "high"}},
			},
		},
		{
			ID: "gpt-4.1",
			Capabilities: auth.CopilotModelCapabilities{
				Limits: auth.CopilotModelLimits{MaxContextWindowTokens: 1047576},
			},
		},
	})

	require.NotNil(t, models)
	assert.Equal(t, []string{"gpt-5.4"}, models.Reasoning)
	assert.Equal(t, []string{"gpt-4.1"}, models.NonReasoning)
	require.Contains(t, pricing, "gpt-5.4")
	assert.Equal(t, 0.0, pricing["gpt-5.4"].Input)
	assert.Equal(t, 400000, pricing["gpt-5.4"].ContextWindow)
	require.Contains(t, pricing, "gpt-4.1")
	assert.Equal(t, 1047576, pricing["gpt-4.1"].ContextWindow)
}

func TestLoadOpenAIPlatformDefaults(t *testing.T) {
	models, pricing := loadOpenAIPlatformDefaults()

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	// Check reasoning models
	assert.ElementsMatch(t, expectedOpenAIReasoningModels, models.Reasoning)

	// Check non-reasoning models
	assert.ElementsMatch(t, expectedOpenAINonReasoningModels, models.NonReasoning)

	// Check pricing for key models
	gpt56SolPricing, exists := pricing["gpt-5.6-sol"]
	require.True(t, exists)
	assert.Equal(t, 0.000005, gpt56SolPricing.Input)
	assert.Equal(t, 0.0000005, gpt56SolPricing.CachedInput)
	assert.Equal(t, 0.00000625, gpt56SolPricing.CacheWriteInput)
	assert.Equal(t, 0.00003, gpt56SolPricing.Output)
	assert.Equal(t, 0.0000125, gpt56SolPricing.LongContextCacheWriteInput)
	assert.Equal(t, 1_050_000, gpt56SolPricing.ContextWindow)

	gpt41Pricing, exists := pricing["gpt-4.1"]
	require.True(t, exists)
	assert.Equal(t, 0.000002, gpt41Pricing.Input)
	assert.Equal(t, 0.0000005, gpt41Pricing.CachedInput)
	assert.Equal(t, 0.000008, gpt41Pricing.Output)
	assert.Equal(t, 1047576, gpt41Pricing.ContextWindow)

	gpt4oPricing, exists := pricing["gpt-4o"]
	require.True(t, exists)
	assert.Equal(t, 0.0000025, gpt4oPricing.Input)
	assert.Equal(t, 0.00000125, gpt4oPricing.CachedInput)
	assert.Equal(t, 0.00001, gpt4oPricing.Output)
	assert.Equal(t, 128_000, gpt4oPricing.ContextWindow)

	o3Pricing, exists := pricing["o3"]
	require.True(t, exists)
	assert.Equal(t, 0.000002, o3Pricing.Input)
	assert.Equal(t, 0.0000005, o3Pricing.CachedInput)
	assert.Equal(t, 0.000008, o3Pricing.Output)
	assert.Equal(t, 200_000, o3Pricing.ContextWindow)

	// Test all models have pricing
	allModels := append(models.Reasoning, models.NonReasoning...)
	for _, model := range allModels {
		_, exists := pricing[model]
		assert.True(t, exists, "Model %s should have pricing information", model)
	}
}

func TestLoadOpenAIPlatformDefaultsUsesConfiguredServiceTierPricing(t *testing.T) {
	_, standardPricing := loadOpenAIPlatformDefaults()
	_, priorityPricing := loadOpenAIPlatformDefaultsForServiceTier(llmtypes.OpenAIServiceTierPriority)

	standardGPT56Sol, exists := standardPricing["gpt-5.6-sol"]
	require.True(t, exists)
	assert.Equal(t, 0.000005, standardGPT56Sol.Input)
	assert.Equal(t, 0.00000625, standardGPT56Sol.CacheWriteInput)
	assert.Equal(t, 0.0000125, standardGPT56Sol.LongContextCacheWriteInput)

	priorityGPT56Sol, exists := priorityPricing["gpt-5.6-sol"]
	require.True(t, exists)
	assert.Equal(t, 0.00001, priorityGPT56Sol.Input)
	assert.Equal(t, 0.0000125, priorityGPT56Sol.CacheWriteInput)
	assert.Equal(t, 0, priorityGPT56Sol.LongContextThreshold)

	assert.Equal(t, standardPricing["gpt-4.1"], priorityPricing["gpt-4.1"])
}

func TestGetPlatformBaseURL(t *testing.T) {
	tests := []struct {
		platform string
		expected string
	}{
		{
			platform: "openai",
			expected: "https://api.openai.com/v1",
		},
		{
			platform: "codex",
			expected: "https://chatgpt.com/backend-api/codex",
		},
		{
			platform: "fireworks",
			expected: "",
		},
		{
			platform: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			result := getPlatformBaseURL(tt.platform)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPlatformAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		platform string
		expected string
	}{
		{
			platform: "openai",
			expected: "OPENAI_API_KEY",
		},
		{
			platform: "codex",
			expected: "OPENAI_API_KEY",
		},
		{
			platform: "fireworks",
			expected: "OPENAI_API_KEY",
		},
		{
			platform: "",
			expected: "OPENAI_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			result := getPlatformAPIKeyEnvVar(tt.platform)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected string
	}{
		{
			name: "default config uses OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-4.1",
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "openai platform uses OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-4.1",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "openai",
				},
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "custom api_key_env_var overrides default",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-4.1",
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "MY_CUSTOM_API_KEY",
				},
			},
			expected: "MY_CUSTOM_API_KEY",
		},
		{
			name: "custom api_key_env_var overrides custom platform",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "custom-model",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:     "custom-provider",
					APIKeyEnvVar: "MY_CUSTOM_API_KEY",
				},
			},
			expected: "MY_CUSTOM_API_KEY",
		},
		{
			name: "codex platform uses OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.2-codex",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "codex",
				},
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "unknown platform falls back to OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "some-model",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "unknown-platform",
				},
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "empty platform with no custom env var falls back to OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "some-model",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "",
				},
			},
			expected: "OPENAI_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAPIKeyEnvVar(tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveAPIMode(t *testing.T) {
	resetEnv := func() {
		os.Unsetenv("KODELET_OPENAI_API_MODE")
	}
	resetEnv()
	defer resetEnv()

	tests := []struct {
		name     string
		config   llmtypes.Config
		envMode  string
		expected llmtypes.OpenAIAPIMode
	}{
		{
			name:     "default is chat completions",
			config:   llmtypes.Config{},
			expected: llmtypes.OpenAIAPIModeChatCompletions,
		},
		{
			name:     "gpt-5.6 model family defaults to responses",
			config:   llmtypes.Config{Model: "gpt-5.6-sol"},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "gpt-5.6 ignores explicit chat completions api_mode",
			config:   llmtypes.Config{Model: "gpt-5.6-sol", OpenAI: &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeChatCompletions}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "gpt-5.6 ignores chat completions env override",
			config:   llmtypes.Config{Model: "gpt-5.6-luna"},
			envMode:  "chat_completions",
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "api_mode responses",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeResponses}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "env api mode overrides config",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeChatCompletions}},
			envMode:  "responses",
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "platform codex always responses",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEnv()
			if tt.envMode != "" {
				os.Setenv("KODELET_OPENAI_API_MODE", tt.envMode)
			}

			assert.Equal(t, tt.expected, resolveAPIMode(tt.config))
		})
	}
}

func TestNormalizeServiceTier(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected llmtypes.OpenAIServiceTier
	}{
		{
			name:     "missing openai config",
			config:   llmtypes.Config{},
			expected: "",
		},
		{
			name:     "fast tier preserved as config value",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{ServiceTier: llmtypes.OpenAIServiceTierFast}},
			expected: llmtypes.OpenAIServiceTierFast,
		},
		{
			name:     "whitespace and casing normalize",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{ServiceTier: llmtypes.OpenAIServiceTier(" Flex ")}},
			expected: llmtypes.OpenAIServiceTierFlex,
		},
		{
			name:     "invalid value ignored",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{ServiceTier: llmtypes.OpenAIServiceTier("turbo")}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeServiceTier(tt.config))
		})
	}
}

func TestOpenAIReasoningEffortForChatRequest(t *testing.T) {
	assert.Equal(t, "max", openAIReasoningEffortForChatRequest(" MAX "))
	assert.Equal(t, "xhigh", openAIReasoningEffortForChatRequest("xhigh"))
	assert.Equal(t, "", openAIReasoningEffortForChatRequest(""))
}

func TestGetBaseURL(t *testing.T) {
	os.Unsetenv("OPENAI_API_BASE")
	defer os.Unsetenv("OPENAI_API_BASE")

	tests := []struct {
		name     string
		config   llmtypes.Config
		envBase  string
		expected string
	}{
		{
			name:     "default openai base",
			config:   llmtypes.Config{},
			expected: "https://api.openai.com/v1",
		},
		{
			name:     "codex platform base",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: "https://chatgpt.com/backend-api/codex",
		},
		{
			name:     "custom base overrides platform",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "custom-provider", BaseURL: "https://custom.example/v1"}},
			expected: "https://custom.example/v1",
		},
		{
			name:     "env base overrides everything",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"}},
			envBase:  "https://env.example/v1",
			expected: "https://env.example/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("OPENAI_API_BASE")
			if tt.envBase != "" {
				os.Setenv("OPENAI_API_BASE", tt.envBase)
			}
			assert.Equal(t, tt.expected, GetBaseURL(tt.config))
		})
	}
}

func TestGetConfiguredBaseURL(t *testing.T) {
	os.Unsetenv("OPENAI_API_BASE")
	defer os.Unsetenv("OPENAI_API_BASE")

	tests := []struct {
		name     string
		config   llmtypes.Config
		envBase  string
		expected string
	}{
		{
			name:     "no explicit base url",
			config:   llmtypes.Config{},
			expected: "",
		},
		{
			name:     "platform default is not treated as explicit override",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: "",
		},
		{
			name:     "config base url override",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"}},
			expected: "https://custom.example/v1",
		},
		{
			name:     "env base url override",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"}},
			envBase:  "https://env.example/v1",
			expected: "https://env.example/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("OPENAI_API_BASE")
			if tt.envBase != "" {
				os.Setenv("OPENAI_API_BASE", tt.envBase)
			}
			assert.Equal(t, tt.expected, GetConfiguredBaseURL(tt.config))
		})
	}
}

func TestResolveClientBaseURL(t *testing.T) {
	os.Unsetenv("OPENAI_API_BASE")
	defer os.Unsetenv("OPENAI_API_BASE")

	tests := []struct {
		name       string
		config     llmtypes.Config
		useCopilot bool
		envBase    string
		expected   string
	}{
		{
			name:       "copilot uses copilot endpoint by default",
			config:     llmtypes.Config{},
			useCopilot: true,
			expected:   "https://api.githubcopilot.com",
		},
		{
			name:       "copilot ignores platform default endpoint",
			config:     llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			useCopilot: true,
			expected:   "https://api.githubcopilot.com",
		},
		{
			name:       "copilot respects explicit config base override",
			config:     llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"}},
			useCopilot: true,
			expected:   "https://custom.example/v1",
		},
		{
			name:       "copilot respects env base override",
			config:     llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"}},
			useCopilot: true,
			envBase:    "https://env.example/v1",
			expected:   "https://env.example/v1",
		},
		{
			name:       "non-copilot uses resolved provider base",
			config:     llmtypes.Config{},
			useCopilot: false,
			expected:   "https://api.openai.com/v1",
		},
		{
			name:       "copilot platform uses copilot endpoint",
			config:     llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot"}},
			useCopilot: true,
			expected:   "https://api.githubcopilot.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("OPENAI_API_BASE")
			if tt.envBase != "" {
				os.Setenv("OPENAI_API_BASE", tt.envBase)
			}
			assert.Equal(t, tt.expected, resolveClientBaseURL(tt.config, tt.useCopilot))
		})
	}
}

func TestValidateCustomConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		config        llmtypes.Config
		expectError   bool
		errorContains string
	}{
		{
			name:        "no custom config",
			config:      llmtypes.Config{},
			expectError: false,
		},
		{
			name: "valid built-in platform openai",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "openai",
				},
			},
			expectError: false,
		},
		{
			name: "valid built-in platform codex",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "codex",
				},
			},
			expectError: false,
		},
		{
			name: "valid built-in platform copilot",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "copilot",
				},
			},
			expectError: false,
		},
		{
			name: "valid custom platform fireworks",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "fireworks",
				},
			},
			expectError: false,
		},
		{
			name: "valid api mode",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIMode: llmtypes.OpenAIAPIModeResponses,
				},
			},
			expectError: false,
		},
		{
			name: "valid service tier fast",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					ServiceTier: llmtypes.OpenAIServiceTierFast,
				},
			},
			expectError: false,
		},
		{
			name: "valid service tier priority",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					ServiceTier: llmtypes.OpenAIServiceTierPriority,
				},
			},
			expectError: false,
		},
		{
			name: "invalid service tier",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					ServiceTier: llmtypes.OpenAIServiceTier("turbo"),
				},
			},
			expectError:   true,
			errorContains: "invalid service_tier",
		},
		{
			name: "invalid api mode",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIMode: "bad-mode",
				},
			},
			expectError:   true,
			errorContains: "invalid api_mode",
		},
		{
			name: "valid base URL https",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					BaseURL: "https://api.example.com/v1",
				},
			},
			expectError: false,
		},
		{
			name: "valid base URL http",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					BaseURL: "http://localhost:8080/v1",
				},
			},
			expectError: false,
		},
		{
			name: "invalid base URL",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					BaseURL: "invalid-url",
				},
			},
			expectError:   true,
			errorContains: "base_url must start with",
		},
		{
			name: "valid pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:                      0.001,
							Output:                     0.002,
							CachedInput:                0.0005,
							CacheWriteInput:            0.00125,
							LongContextInput:           0.002,
							LongContextOutput:          0.003,
							LongContextCachedInput:     0.001,
							LongContextCacheWriteInput: 0.0025,
							LongContextThreshold:       272000,
							ContextWindow:              128000,
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid long context pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:            0.001,
							Output:           0.002,
							LongContextInput: -0.002,
							ContextWindow:    128000,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid long_context_input pricing",
		},
		{
			name: "invalid long context threshold",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:                0.001,
							Output:               0.002,
							LongContextThreshold: -1,
							ContextWindow:        128000,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid long_context_threshold",
		},
		{
			name: "invalid input pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:         -0.001,
							Output:        0.002,
							ContextWindow: 128000,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid input pricing",
		},
		{
			name: "invalid cache write pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:           0.001,
							CacheWriteInput: -0.00125,
							Output:          0.002,
							ContextWindow:   128000,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid cache_write_input pricing",
		},
		{
			name: "invalid long context cache write pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:                      0.001,
							LongContextCacheWriteInput: -0.0025,
							Output:                     0.002,
							ContextWindow:              128000,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid long_context_cache_write_input pricing",
		},
		{
			name: "invalid context window",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.ModelPricing{
						"test-model": {
							Input:         0.001,
							Output:        0.002,
							ContextWindow: 0,
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid context_window",
		},
		{
			name: "valid api_key_env_var",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "MY_CUSTOM_API_KEY",
				},
			},
			expectError: false,
		},
		{
			name: "empty api_key_env_var",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "",
				},
			},
			expectError: false, // Empty string is allowed, will use default
		},
		{
			name: "api_key_env_var with only whitespace",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "   ",
				},
			},
			expectError:   true,
			errorContains: "api_key_env_var cannot be empty or whitespace",
		},
		{
			name: "api_key_env_var with space",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "MY API KEY",
				},
			},
			expectError:   true,
			errorContains: "api_key_env_var cannot contain whitespace characters",
		},
		{
			name: "api_key_env_var with tab",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "MY_API\tKEY",
				},
			},
			expectError:   true,
			errorContains: "api_key_env_var cannot contain whitespace characters",
		},
		{
			name: "api_key_env_var with newline",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIKeyEnvVar: "MY_API_KEY\n",
				},
			},
			expectError:   true,
			errorContains: "api_key_env_var cannot contain whitespace characters",
		},
		{
			name: "copilot platform rejects api_key_env_var",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:     "copilot",
					APIKeyEnvVar: "OPENAI_API_KEY",
				},
			},
			expectError:   true,
			errorContains: "api_key_env_var is not supported when openai.platform is copilot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCustomConfiguration(tt.config)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
