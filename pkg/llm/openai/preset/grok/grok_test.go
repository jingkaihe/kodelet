package grok_test

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/grok"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModels(t *testing.T) {
	require.NotNil(t, grok.Models, "Models should not be nil")

	t.Run("ReasoningModels", func(t *testing.T) {
		expectedReasoning := []string{
			"grok-code-fast-1",
			"grok-4-0709",
			"grok-3-mini",
		}
		assert.Equal(t, expectedReasoning, grok.Models.Reasoning, "Reasoning models should match expected list")
	})

	t.Run("NonReasoningModels", func(t *testing.T) {
		expectedNonReasoning := []string{
			"grok-3",
			"grok-2-image-1212",
		}
		assert.Equal(t, expectedNonReasoning, grok.Models.NonReasoning, "Non-reasoning models should match expected list")
	})
}

func TestPricing(t *testing.T) {
	require.NotNil(t, grok.Pricing, "Pricing should not be nil")

	t.Run("AllModelsHavePricing", func(t *testing.T) {
		// Verify all reasoning models have pricing
		for _, model := range grok.Models.Reasoning {
			pricing, exists := grok.Pricing[model]
			assert.True(t, exists, "Reasoning model %s should have pricing", model)
			assert.Greater(t, pricing.Input, 0.0, "Input price for %s should be positive", model)
			assert.Greater(t, pricing.Output, 0.0, "Output price for %s should be positive", model)
			assert.Greater(t, pricing.ContextWindow, 0, "Context window for %s should be positive", model)
		}

		// Verify all non-reasoning models have pricing
		for _, model := range grok.Models.NonReasoning {
			pricing, exists := grok.Pricing[model]
			assert.True(t, exists, "Non-reasoning model %s should have pricing", model)
			
			// Special case for image generation models that don't charge for input tokens
			if model == "grok-2-image-1212" {
				assert.Equal(t, 0.0, pricing.Input, "Image generation model %s should have no input cost", model)
			} else {
				assert.Greater(t, pricing.Input, 0.0, "Input price for %s should be positive", model)
			}
			
			assert.Greater(t, pricing.Output, 0.0, "Output price for %s should be positive", model)
			assert.Greater(t, pricing.ContextWindow, 0, "Context window for %s should be positive", model)
		}
	})

	t.Run("SpecificModelPricing", func(t *testing.T) {
		// Test a specific model's pricing
		grok4Pricing, exists := grok.Pricing["grok-4-0709"]
		require.True(t, exists, "grok-4-0709 should exist in pricing")

		assert.Equal(t, 0.000003, grok4Pricing.Input, "grok-4-0709 input pricing should match")
		assert.Equal(t, 0.000015, grok4Pricing.Output, "grok-4-0709 output pricing should match")
		assert.Equal(t, 256000, grok4Pricing.ContextWindow, "grok-4-0709 context window should match")

		// Test image generation model pricing
		imagePricing, exists := grok.Pricing["grok-2-image-1212"]
		require.True(t, exists, "grok-2-image-1212 should exist in pricing")

		assert.Equal(t, 0.0, imagePricing.Input, "grok-2-image-1212 input pricing should match")
		assert.Equal(t, 0.00007, imagePricing.Output, "grok-2-image-1212 output pricing should match")
		assert.Equal(t, 32768, imagePricing.ContextWindow, "grok-2-image-1212 context window should match")
	})
}

func TestBaseURL(t *testing.T) {
	expectedURL := "https://api.x.ai/v1"
	assert.Equal(t, expectedURL, grok.BaseURL, "BaseURL should match expected xAI API endpoint")
}

func TestModelCount(t *testing.T) {
	// Ensure we have the expected number of models
	assert.Len(t, grok.Models.Reasoning, 3, "Should have 3 reasoning models")
	assert.Len(t, grok.Models.NonReasoning, 2, "Should have 2 non-reasoning models")
	assert.Len(t, grok.Pricing, 5, "Should have pricing for 5 total models")
}

func TestNoDuplicateModels(t *testing.T) {
	allModels := make(map[string]bool)

	// Check reasoning models for duplicates
	for _, model := range grok.Models.Reasoning {
		assert.False(t, allModels[model], "Model %s should not be duplicated", model)
		allModels[model] = true
	}

	// Check non-reasoning models for duplicates
	for _, model := range grok.Models.NonReasoning {
		assert.False(t, allModels[model], "Model %s should not be duplicated", model)
		allModels[model] = true
	}
}

func TestAPIKeyEnvVar(t *testing.T) {
	// Test that APIKeyEnvVar is correctly defined
	assert.Equal(t, "XAI_API_KEY", grok.APIKeyEnvVar, "APIKeyEnvVar should be XAI_API_KEY")

	// Test that it doesn't contain whitespace
	assert.NotContains(t, grok.APIKeyEnvVar, " ", "APIKeyEnvVar should not contain spaces")
	assert.NotContains(t, grok.APIKeyEnvVar, "\t", "APIKeyEnvVar should not contain tabs")
	assert.NotContains(t, grok.APIKeyEnvVar, "\n", "APIKeyEnvVar should not contain newlines")

	// Test that it's not empty
	assert.NotEmpty(t, grok.APIKeyEnvVar, "APIKeyEnvVar should not be empty")
}
