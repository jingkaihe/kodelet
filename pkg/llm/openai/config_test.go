package openai

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// Define expected OpenAI platform defaults once to avoid duplication
var (
	expectedOpenAIReasoningModels = []string{
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
			name: "xai platform",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "xai",
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning: []string{
					"grok-code-fast-1",
					"grok-4-0709",
					"grok-3-mini",
				},
				NonReasoning: []string{
					"grok-3",
					"grok-2-image-1212",
				},
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
			name: "xai platform with custom override",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "xai",
					Models: &llmtypes.CustomModels{
						Reasoning: []string{"custom-override-model"},
					},
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning: []string{"custom-override-model"},
				// Auto-populated from platform pricing since reasoning was overridden but non-reasoning wasn't
				NonReasoning: []string{"grok-3", "grok-3-mini", "grok-2-image-1212", "grok-code-fast-1", "grok-4-0709"},
			},
			hasModels:  true,
			hasPricing: true,
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

func TestLoadXAIPlatformDefaults(t *testing.T) {
	models, pricing := loadXAIPlatformDefaults()

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	// Check reasoning models
	expectedReasoning := []string{"grok-code-fast-1", "grok-4-0709", "grok-3-mini"}
	assert.ElementsMatch(t, expectedReasoning, models.Reasoning)

	// Check non-reasoning models
	expectedNonReasoning := []string{"grok-3", "grok-2-image-1212"}
	assert.ElementsMatch(t, expectedNonReasoning, models.NonReasoning)

	// Check pricing for a few key models
	grok4Pricing, exists := pricing["grok-4-0709"]
	require.True(t, exists)
	assert.Equal(t, 0.000003, grok4Pricing.Input)
	assert.Equal(t, 0.000015, grok4Pricing.Output)
	assert.Equal(t, 256000, grok4Pricing.ContextWindow)

	grok3MiniPricing, exists := pricing["grok-3-mini"]
	require.True(t, exists)
	assert.Equal(t, 0.0000003, grok3MiniPricing.Input)
	assert.Equal(t, 0.0000005, grok3MiniPricing.Output)
	assert.Equal(t, 131072, grok3MiniPricing.ContextWindow)
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
			platform: "xai",
			expected: "https://api.x.ai/v1",
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
			platform: "xai",
			expected: "XAI_API_KEY",
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
			name: "xai platform uses XAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "grok-3",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "xai",
				},
			},
			expected: "XAI_API_KEY",
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
			name: "custom api_key_env_var overrides platform",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "grok-3",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:     "xai",
					APIKeyEnvVar: "MY_CUSTOM_XAI_KEY",
				},
			},
			expected: "MY_CUSTOM_XAI_KEY",
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
		os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
	}
	resetEnv()
	defer resetEnv()

	trueValue := true
	falseValue := false

	tests := []struct {
		name     string
		config   llmtypes.Config
		envMode  string
		envBool  string
		expected llmtypes.OpenAIAPIMode
	}{
		{
			name:     "default is chat completions",
			config:   llmtypes.Config{},
			expected: llmtypes.OpenAIAPIModeChatCompletions,
		},
		{
			name:     "api_mode responses",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeResponses}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "legacy responses_api true",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{ResponsesAPI: &trueValue}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "legacy responses_api false",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{ResponsesAPI: &falseValue}},
			expected: llmtypes.OpenAIAPIModeChatCompletions,
		},
		{
			name:     "legacy use_responses_api true",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{UseResponsesAPI: true}},
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "env api mode overrides config",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{APIMode: llmtypes.OpenAIAPIModeChatCompletions}},
			envMode:  "responses",
			expected: llmtypes.OpenAIAPIModeResponses,
		},
		{
			name:     "legacy env bool true",
			config:   llmtypes.Config{},
			envBool:  "true",
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
			if tt.envBool != "" {
				os.Setenv("KODELET_OPENAI_USE_RESPONSES_API", tt.envBool)
			}

			assert.Equal(t, tt.expected, resolveAPIMode(tt.config))
		})
	}
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
			name:     "xai platform base",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "xai"}},
			expected: "https://api.x.ai/v1",
		},
		{
			name:     "codex platform base",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: "https://chatgpt.com/backend-api/codex",
		},
		{
			name:     "custom base overrides platform",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "xai", BaseURL: "https://custom.example/v1"}},
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
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "xai"}},
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
			config:     llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "xai"}},
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
			name: "valid built-in platform xai",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "xai",
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
							Input:         0.001,
							Output:        0.002,
							CachedInput:   0.0005,
							ContextWindow: 128000,
						},
					},
				},
			},
			expectError: false,
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
