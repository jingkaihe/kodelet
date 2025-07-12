package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestLoadCustomConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected *CustomModels
		hasModels bool
		hasPricing bool
	}{
		{
			name:      "no custom config",
			config:    llmtypes.Config{},
			expected:  nil,
			hasModels: false,
			hasPricing: false,
		},
		{
			name: "xai-grok preset",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai-grok",
				},
			},
			expected: &CustomModels{
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
			hasModels: true,
			hasPricing: true,
		},
		{
			name: "custom models only",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Models: &llmtypes.OpenAIModelsConfig{
						Reasoning:    []string{"custom-reasoning-model"},
						NonReasoning: []string{"custom-regular-model"},
					},
				},
			},
			expected: &CustomModels{
				Reasoning:    []string{"custom-reasoning-model"},
				NonReasoning: []string{"custom-regular-model"},
			},
			hasModels: true,
			hasPricing: false,
		},
		{
			name: "preset with custom override",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai-grok",
					Models: &llmtypes.OpenAIModelsConfig{
						Reasoning: []string{"custom-override-model"},
					},
				},
			},
			expected: &CustomModels{
				Reasoning: []string{"custom-override-model"},
				// Auto-populated from preset pricing since reasoning was overridden but non-reasoning wasn't
				NonReasoning: []string{"grok-4-0709", "grok-3", "grok-3-mini", "grok-3-fast", "grok-3-mini-fast", "grok-2-vision-1212"},
			},
			hasModels: true,
			hasPricing: true,
		},
		{
			name: "auto-populate non-reasoning models",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Models: &llmtypes.OpenAIModelsConfig{
						Reasoning: []string{"model-a"},
					},
					Pricing: map[string]llmtypes.PricingConfig{
						"model-a": {Input: 0.001, Output: 0.002, ContextWindow: 128000},
						"model-b": {Input: 0.003, Output: 0.004, ContextWindow: 64000},
						"model-c": {Input: 0.005, Output: 0.006, ContextWindow: 32000},
					},
				},
			},
			expected: &CustomModels{
				Reasoning:    []string{"model-a"},
				NonReasoning: []string{"model-b", "model-c"}, // Auto-populated from pricing
			},
			hasModels: true,
			hasPricing: true,
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

func TestGetPresetBaseURL(t *testing.T) {
	tests := []struct {
		preset   string
		expected string
	}{
		{
			preset:   "xai-grok",
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

func TestValidateCustomConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		config      llmtypes.Config
		expectError bool
		errorContains string
	}{
		{
			name:        "no custom config",
			config:      llmtypes.Config{},
			expectError: false,
		},
		{
			name: "valid preset",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai-grok",
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
			expectError: true,
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
			expectError: true,
			errorContains: "base_url must start with",
		},
		{
			name: "valid pricing",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.PricingConfig{
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
					Pricing: map[string]llmtypes.PricingConfig{
						"test-model": {
							Input:         -0.001,
							Output:        0.002,
							ContextWindow: 128000,
						},
					},
				},
			},
			expectError: true,
			errorContains: "invalid input pricing",
		},
		{
			name: "invalid context window",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Pricing: map[string]llmtypes.PricingConfig{
						"test-model": {
							Input:         0.001,
							Output:        0.002,
							ContextWindow: 0,
						},
					},
				},
			},
			expectError: true,
			errorContains: "invalid context_window",
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