package groq_test

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/preset/groq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModels(t *testing.T) {
	require.NotNil(t, groq.Models, "Models should not be nil")

	t.Run("ReasoningModels", func(t *testing.T) {
		expectedReasoning := []string{
			"deepseek-r1-distill-llama-70b",
			"qwen/qwen3-32b",
		}
		assert.Equal(t, expectedReasoning, groq.Models.Reasoning, "Reasoning models should match expected list")
	})

	t.Run("NonReasoningModels", func(t *testing.T) {
		expectedNonReasoning := []string{
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
		}
		assert.Equal(t, expectedNonReasoning, groq.Models.NonReasoning, "Non-reasoning models should match expected list")
	})
}

func TestPricing(t *testing.T) {
	require.NotNil(t, groq.Pricing, "Pricing should not be nil")

	t.Run("AllModelsHavePricing", func(t *testing.T) {
		// Verify all reasoning models have pricing
		for _, model := range groq.Models.Reasoning {
			pricing, exists := groq.Pricing[model]
			assert.True(t, exists, "Reasoning model %s should have pricing", model)
			assert.Greater(t, pricing.Input, 0.0, "Input price for %s should be positive", model)
			assert.Greater(t, pricing.Output, 0.0, "Output price for %s should be positive", model)
			assert.Greater(t, pricing.ContextWindow, 0, "Context window for %s should be positive", model)
		}

		// Verify all non-reasoning models have pricing
		for _, model := range groq.Models.NonReasoning {
			pricing, exists := groq.Pricing[model]
			assert.True(t, exists, "Non-reasoning model %s should have pricing", model)
			assert.Greater(t, pricing.Input, 0.0, "Input price for %s should be positive", model)
			assert.Greater(t, pricing.Output, 0.0, "Output price for %s should be positive", model)
			assert.Greater(t, pricing.ContextWindow, 0, "Context window for %s should be positive", model)
		}
	})

	t.Run("SpecificModelPricing", func(t *testing.T) {
		// Test Llama 3.1 8B instant pricing
		llama31Pricing, exists := groq.Pricing["llama-3.1-8b-instant"]
		require.True(t, exists, "llama-3.1-8b-instant should exist in pricing")

		assert.Equal(t, 0.00000005, llama31Pricing.Input, "llama-3.1-8b-instant input pricing should match")
		assert.Equal(t, 0.00000008, llama31Pricing.Output, "llama-3.1-8b-instant output pricing should match")
		assert.Equal(t, 131072, llama31Pricing.ContextWindow, "llama-3.1-8b-instant context window should match")

		// Test Llama 3.3 70B versatile pricing
		llama33Pricing, exists := groq.Pricing["llama-3.3-70b-versatile"]
		require.True(t, exists, "llama-3.3-70b-versatile should exist in pricing")

		assert.Equal(t, 0.00000059, llama33Pricing.Input, "llama-3.3-70b-versatile input pricing should match")
		assert.Equal(t, 0.00000079, llama33Pricing.Output, "llama-3.3-70b-versatile output pricing should match")
		assert.Equal(t, 131072, llama33Pricing.ContextWindow, "llama-3.3-70b-versatile context window should match")

		// Test Gemma 2 9B pricing
		gemmaPricing, exists := groq.Pricing["gemma2-9b-it"]
		require.True(t, exists, "gemma2-9b-it should exist in pricing")

		assert.Equal(t, 0.0000002, gemmaPricing.Input, "gemma2-9b-it input pricing should match")
		assert.Equal(t, 0.0000002, gemmaPricing.Output, "gemma2-9b-it output pricing should match")
		assert.Equal(t, 8192, gemmaPricing.ContextWindow, "gemma2-9b-it context window should match")
	})
}

func TestBaseURL(t *testing.T) {
	expectedURL := "https://api.groq.com/openai/v1"
	assert.Equal(t, expectedURL, groq.BaseURL, "BaseURL should match expected Groq API endpoint")
}

func TestModelCount(t *testing.T) {
	// Ensure we have the expected number of models
	assert.Len(t, groq.Models.Reasoning, 2, "Should have 2 reasoning models")
	assert.Len(t, groq.Models.NonReasoning, 15, "Should have 15 non-reasoning models")
	
	// Check that we have pricing for all models in our lists
	totalModels := len(groq.Models.Reasoning) + len(groq.Models.NonReasoning)
	assert.Len(t, groq.Pricing, totalModels, "Should have pricing for all models")
}

func TestNoDuplicateModels(t *testing.T) {
	allModels := make(map[string]bool)

	// Check reasoning models for duplicates
	for _, model := range groq.Models.Reasoning {
		assert.False(t, allModels[model], "Model %s should not be duplicated", model)
		allModels[model] = true
	}

	// Check non-reasoning models for duplicates
	for _, model := range groq.Models.NonReasoning {
		assert.False(t, allModels[model], "Model %s should not be duplicated", model)
		allModels[model] = true
	}
}

func TestAPIKeyEnvVar(t *testing.T) {
	// Test that APIKeyEnvVar is correctly defined
	assert.Equal(t, "GROQ_API_KEY", groq.APIKeyEnvVar, "APIKeyEnvVar should be GROQ_API_KEY")

	// Test that it doesn't contain whitespace
	assert.NotContains(t, groq.APIKeyEnvVar, " ", "APIKeyEnvVar should not contain spaces")
	assert.NotContains(t, groq.APIKeyEnvVar, "\t", "APIKeyEnvVar should not contain tabs")
	assert.NotContains(t, groq.APIKeyEnvVar, "\n", "APIKeyEnvVar should not contain newlines")

	// Test that it's not empty
	assert.NotEmpty(t, groq.APIKeyEnvVar, "APIKeyEnvVar should not be empty")
}

func TestPricingValidation(t *testing.T) {
	// Test that all pricing entries have valid values
	for model, pricing := range groq.Pricing {
		assert.GreaterOrEqual(t, pricing.Input, 0.0, "Input pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.CachedInput, 0.0, "CachedInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.Output, 0.0, "Output pricing for %s should be >= 0", model)
		assert.Greater(t, pricing.ContextWindow, 0, "ContextWindow for %s should be > 0", model)
	}
}

func TestModelNames(t *testing.T) {
	// Test that model names are correctly formatted (basic validation)
	allModels := append(groq.Models.Reasoning, groq.Models.NonReasoning...)
	
	for _, model := range allModels {
		assert.NotEmpty(t, model, "Model name should not be empty")
		assert.NotContains(t, model, " ", "Model name should not contain spaces")
	}
}