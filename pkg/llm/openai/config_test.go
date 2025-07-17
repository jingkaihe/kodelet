package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
			name:   "no custom config uses openai preset",
			config: llmtypes.Config{},
			expected: &llmtypes.CustomModels{
				Reasoning: []string{
					"o1", "o1-pro", "o1-mini", "o3", "o3-pro", "o3-mini",
					"o3-deep-research", "o4-mini", "o4-mini-deep-research",
				},
				NonReasoning: []string{
					"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4.5-preview",
					"gpt-4o", "gpt-4o-mini", "gpt-4o-audio-preview", "gpt-4o-realtime-preview",
					"gpt-4o-mini-audio-preview", "gpt-4o-mini-realtime-preview",
					"gpt-4o-mini-search-preview", "gpt-4o-search-preview",
					"computer-use-preview", "gpt-image-1", "codex-mini-latest",
				},
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "explicit openai preset",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "openai",
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning: []string{
					"o1", "o1-pro", "o1-mini", "o3", "o3-pro", "o3-mini",
					"o3-deep-research", "o4-mini", "o4-mini-deep-research",
				},
				NonReasoning: []string{
					"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4.5-preview",
					"gpt-4o", "gpt-4o-mini", "gpt-4o-audio-preview", "gpt-4o-realtime-preview",
					"gpt-4o-mini-audio-preview", "gpt-4o-mini-realtime-preview",
					"gpt-4o-mini-search-preview", "gpt-4o-search-preview",
					"computer-use-preview", "gpt-image-1", "codex-mini-latest",
				},
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "xai preset",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai",
				},
			},
			expected: &llmtypes.CustomModels{
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
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "custom models only (no preset)",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "", // Explicitly empty preset
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
			hasPricing: false, // No preset loaded since explicitly empty
		},
		{
			name: "preset with custom override",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai",
					Models: &llmtypes.CustomModels{
						Reasoning: []string{"custom-override-model"},
					},
				},
			},
			expected: &llmtypes.CustomModels{
				Reasoning: []string{"custom-override-model"},
				// Auto-populated from preset pricing since reasoning was overridden but non-reasoning wasn't
				NonReasoning: []string{"grok-4-0709", "grok-3", "grok-3-mini", "grok-3-fast", "grok-3-mini-fast", "grok-2-vision-1212"},
			},
			hasModels:  true,
			hasPricing: true,
		},
		{
			name: "auto-populate non-reasoning models",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "", // Explicitly empty preset to avoid default OpenAI preset loading
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

func TestLoadXAIGrokPreset(t *testing.T) {
	models, pricing := loadXAIGrokPreset()

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	// Check reasoning models
	expectedReasoning := []string{"grok-4-0709", "grok-3-mini", "grok-3-mini-fast"}
	assert.ElementsMatch(t, expectedReasoning, models.Reasoning)

	// Check non-reasoning models
	expectedNonReasoning := []string{"grok-3", "grok-3-fast", "grok-2-vision-1212"}
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
	assert.Equal(t, 0.0000009, grok3MiniPricing.Output)
	assert.Equal(t, 131072, grok3MiniPricing.ContextWindow)
}

func TestLoadOpenAIPreset(t *testing.T) {
	models, pricing := loadOpenAIPreset()

	require.NotNil(t, models)
	require.NotNil(t, pricing)

	// Check reasoning models
	expectedReasoning := []string{
		"o1", "o1-pro", "o1-mini", "o3", "o3-pro", "o3-mini",
		"o3-deep-research", "o4-mini", "o4-mini-deep-research",
	}
	assert.ElementsMatch(t, expectedReasoning, models.Reasoning)

	// Check non-reasoning models
	expectedNonReasoning := []string{
		"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4.5-preview",
		"gpt-4o", "gpt-4o-mini", "gpt-4o-audio-preview", "gpt-4o-realtime-preview",
		"gpt-4o-mini-audio-preview", "gpt-4o-mini-realtime-preview",
		"gpt-4o-mini-search-preview", "gpt-4o-search-preview",
		"computer-use-preview", "gpt-image-1", "codex-mini-latest",
	}
	assert.ElementsMatch(t, expectedNonReasoning, models.NonReasoning)

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

func TestGetPresetBaseURL(t *testing.T) {
	tests := []struct {
		preset   string
		expected string
	}{
		{
			preset:   "openai",
			expected: "https://api.openai.com/v1",
		},
		{
			preset:   "xai",
			expected: "https://api.x.ai/v1",
		},
		{
			preset:   "unknown-preset",
			expected: "",
		},
		{
			preset:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			result := getPresetBaseURL(tt.preset)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPresetAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		preset   string
		expected string
	}{
		{
			preset:   "openai",
			expected: "OPENAI_API_KEY",
		},
		{
			preset:   "xai",
			expected: "XAI_API_KEY",
		},
		{
			preset:   "unknown-preset",
			expected: "OPENAI_API_KEY", // fallback
		},
		{
			preset:   "",
			expected: "OPENAI_API_KEY", // fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			result := getPresetAPIKeyEnvVar(tt.preset)
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
			name: "openai preset uses OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-4.1",
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "openai",
				},
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "xai preset uses XAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "grok-3",
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai",
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
			name: "custom api_key_env_var overrides preset",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "grok-3",
				OpenAI: &llmtypes.OpenAIConfig{
					Preset:       "xai",
					APIKeyEnvVar: "MY_CUSTOM_XAI_KEY",
				},
			},
			expected: "MY_CUSTOM_XAI_KEY",
		},
		{
			name: "unknown preset falls back to OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "some-model",
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "unknown-preset",
				},
			},
			expected: "OPENAI_API_KEY",
		},
		{
			name: "empty preset with no custom env var falls back to OPENAI_API_KEY",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "some-model",
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "",
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
			name: "valid preset openai",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "openai",
				},
			},
			expectError: false,
		},
		{
			name: "valid preset xai",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai",
				},
			},
			expectError: false,
		},
		{
			name: "invalid preset",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "invalid-preset",
				},
			},
			expectError:   true,
			errorContains: "invalid preset",
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
