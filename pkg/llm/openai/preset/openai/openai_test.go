package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModels(t *testing.T) {
	// Test that Models is properly defined
	require.NotNil(t, Models)
	
	// Test reasoning models
	assert.Contains(t, Models.Reasoning, "o1")
	assert.Contains(t, Models.Reasoning, "o1-pro")
	assert.Contains(t, Models.Reasoning, "o1-mini")
	assert.Contains(t, Models.Reasoning, "o3")
	assert.Contains(t, Models.Reasoning, "o3-pro")
	assert.Contains(t, Models.Reasoning, "o3-mini")
	assert.Contains(t, Models.Reasoning, "o3-deep-research")
	assert.Contains(t, Models.Reasoning, "o4-mini")
	assert.Contains(t, Models.Reasoning, "o4-mini-deep-research")
	
	// Test non-reasoning models
	assert.Contains(t, Models.NonReasoning, "gpt-4.1")
	assert.Contains(t, Models.NonReasoning, "gpt-4.1-mini")
	assert.Contains(t, Models.NonReasoning, "gpt-4.1-nano")
	assert.Contains(t, Models.NonReasoning, "gpt-4.5-preview")
	assert.Contains(t, Models.NonReasoning, "gpt-4o")
	assert.Contains(t, Models.NonReasoning, "gpt-4o-mini")
	
	// Ensure no overlap between reasoning and non-reasoning models
	reasoningSet := make(map[string]bool)
	for _, model := range Models.Reasoning {
		reasoningSet[model] = true
	}
	
	for _, model := range Models.NonReasoning {
		assert.False(t, reasoningSet[model], "Model %s should not be in both reasoning and non-reasoning lists", model)
	}
}

func TestPricing(t *testing.T) {
	// Test that Pricing is properly defined
	require.NotNil(t, Pricing)
	
	// Test some key models have pricing
	gpt41, exists := Pricing["gpt-4.1"]
	require.True(t, exists, "gpt-4.1 pricing should exist")
	assert.Equal(t, 0.000002, gpt41.Input)
	assert.Equal(t, 0.0000005, gpt41.CachedInput)
	assert.Equal(t, 0.000008, gpt41.Output)
	assert.Equal(t, 1047576, gpt41.ContextWindow)
	
	gpt4o, exists := Pricing["gpt-4o"]
	require.True(t, exists, "gpt-4o pricing should exist")
	assert.Equal(t, 0.0000025, gpt4o.Input)
	assert.Equal(t, 0.00000125, gpt4o.CachedInput)
	assert.Equal(t, 0.00001, gpt4o.Output)
	assert.Equal(t, 128_000, gpt4o.ContextWindow)
	
	o3, exists := Pricing["o3"]
	require.True(t, exists, "o3 pricing should exist")
	assert.Equal(t, 0.000002, o3.Input)
	assert.Equal(t, 0.0000005, o3.CachedInput)
	assert.Equal(t, 0.000008, o3.Output)
	assert.Equal(t, 200_000, o3.ContextWindow)
	
	// Test that all models in Models have pricing
	allModels := append(Models.Reasoning, Models.NonReasoning...)
	for _, model := range allModels {
		_, exists := Pricing[model]
		assert.True(t, exists, "Model %s should have pricing information", model)
	}
	
	// Test that all pricing entries have valid values
	for model, pricing := range Pricing {
		assert.GreaterOrEqual(t, pricing.Input, 0.0, "Input pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.CachedInput, 0.0, "CachedInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.Output, 0.0, "Output pricing for %s should be >= 0", model)
		assert.Greater(t, pricing.ContextWindow, 0, "ContextWindow for %s should be > 0", model)
	}
}

func TestBaseURL(t *testing.T) {
	// Test that BaseURL is correctly defined
	assert.Equal(t, "https://api.openai.com/v1", BaseURL)
}