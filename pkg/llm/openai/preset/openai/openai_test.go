package openai

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModels(t *testing.T) {
	// Test that Models is properly defined
	require.NotNil(t, Models)

	// Test reasoning models
	assert.Contains(t, Models.Reasoning, "gpt-5.6-sol")
	assert.Contains(t, Models.Reasoning, "gpt-5.6-terra")
	assert.Contains(t, Models.Reasoning, "gpt-5.6-luna")
	assert.Contains(t, Models.Reasoning, "gpt-5.5")
	assert.Contains(t, Models.Reasoning, "gpt-5.5-pro")
	assert.Contains(t, Models.Reasoning, "gpt-5.4")
	assert.Contains(t, Models.Reasoning, "gpt-5.4-pro")
	assert.Contains(t, Models.Reasoning, "gpt-5.4-mini")
	assert.Contains(t, Models.Reasoning, "gpt-5.4-nano")
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
	gpt56Sol, exists := Pricing["gpt-5.6-sol"]
	require.True(t, exists, "gpt-5.6-sol pricing should exist")
	assert.Equal(t, 0.000005, gpt56Sol.Input)
	assert.Equal(t, 0.0000005, gpt56Sol.CachedInput)
	assert.Equal(t, 0.00000625, gpt56Sol.CacheWriteInput)
	assert.Equal(t, 0.00003, gpt56Sol.Output)
	assert.Equal(t, 0.00001, gpt56Sol.LongContextInput)
	assert.Equal(t, 0.000001, gpt56Sol.LongContextCachedInput)
	assert.Equal(t, 0.0000125, gpt56Sol.LongContextCacheWriteInput)
	assert.Equal(t, 0.000045, gpt56Sol.LongContextOutput)
	assert.Equal(t, 272_000, gpt56Sol.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt56Sol.ContextWindow)

	gpt56Terra, exists := Pricing["gpt-5.6-terra"]
	require.True(t, exists, "gpt-5.6-terra pricing should exist")
	assert.Equal(t, 0.0000025, gpt56Terra.Input)
	assert.Equal(t, 0.00000025, gpt56Terra.CachedInput)
	assert.Equal(t, 0.000003125, gpt56Terra.CacheWriteInput)
	assert.Equal(t, 0.000015, gpt56Terra.Output)
	assert.Equal(t, 0.000005, gpt56Terra.LongContextInput)
	assert.Equal(t, 0.0000005, gpt56Terra.LongContextCachedInput)
	assert.Equal(t, 0.00000625, gpt56Terra.LongContextCacheWriteInput)
	assert.Equal(t, 0.0000225, gpt56Terra.LongContextOutput)
	assert.Equal(t, 272_000, gpt56Terra.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt56Terra.ContextWindow)

	gpt56Luna, exists := Pricing["gpt-5.6-luna"]
	require.True(t, exists, "gpt-5.6-luna pricing should exist")
	assert.Equal(t, 0.000001, gpt56Luna.Input)
	assert.Equal(t, 0.0000001, gpt56Luna.CachedInput)
	assert.Equal(t, 0.00000125, gpt56Luna.CacheWriteInput)
	assert.Equal(t, 0.000006, gpt56Luna.Output)
	assert.Equal(t, 0.000002, gpt56Luna.LongContextInput)
	assert.Equal(t, 0.0000002, gpt56Luna.LongContextCachedInput)
	assert.Equal(t, 0.0000025, gpt56Luna.LongContextCacheWriteInput)
	assert.Equal(t, 0.000009, gpt56Luna.LongContextOutput)
	assert.Equal(t, 272_000, gpt56Luna.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt56Luna.ContextWindow)

	gpt55, exists := Pricing["gpt-5.5"]
	require.True(t, exists, "gpt-5.5 pricing should exist")
	assert.Equal(t, 0.000005, gpt55.Input)
	assert.Equal(t, 0.0000005, gpt55.CachedInput)
	assert.Equal(t, 0.00003, gpt55.Output)
	assert.Equal(t, 0.00001, gpt55.LongContextInput)
	assert.Equal(t, 0.000001, gpt55.LongContextCachedInput)
	assert.Equal(t, 0.000045, gpt55.LongContextOutput)
	assert.Equal(t, 272_000, gpt55.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt55.ContextWindow)

	gpt55Pro, exists := Pricing["gpt-5.5-pro"]
	require.True(t, exists, "gpt-5.5-pro pricing should exist")
	assert.Equal(t, 0.00003, gpt55Pro.Input)
	assert.Equal(t, 0.00018, gpt55Pro.Output)
	assert.Equal(t, 0.00006, gpt55Pro.LongContextInput)
	assert.Equal(t, 0.00027, gpt55Pro.LongContextOutput)
	assert.Equal(t, 272_000, gpt55Pro.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt55Pro.ContextWindow)

	gpt54, exists := Pricing["gpt-5.4"]
	require.True(t, exists, "gpt-5.4 pricing should exist")
	assert.Equal(t, 0.0000025, gpt54.Input)
	assert.Equal(t, 0.00000025, gpt54.CachedInput)
	assert.Equal(t, 0.000015, gpt54.Output)
	assert.Equal(t, 0.000005, gpt54.LongContextInput)
	assert.Equal(t, 0.0000005, gpt54.LongContextCachedInput)
	assert.Equal(t, 0.0000225, gpt54.LongContextOutput)
	assert.Equal(t, 272_000, gpt54.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt54.ContextWindow)

	gpt54Pro, exists := Pricing["gpt-5.4-pro"]
	require.True(t, exists, "gpt-5.4-pro pricing should exist")
	assert.Equal(t, 0.00003, gpt54Pro.Input)
	assert.Equal(t, 0.00018, gpt54Pro.Output)
	assert.Equal(t, 0.00006, gpt54Pro.LongContextInput)
	assert.Equal(t, 0.00027, gpt54Pro.LongContextOutput)
	assert.Equal(t, 272_000, gpt54Pro.LongContextThreshold)
	assert.Equal(t, 1_050_000, gpt54Pro.ContextWindow)

	gpt54Mini, exists := Pricing["gpt-5.4-mini"]
	require.True(t, exists, "gpt-5.4-mini pricing should exist")
	assert.Equal(t, 0.00000075, gpt54Mini.Input)
	assert.Equal(t, 0.000000075, gpt54Mini.CachedInput)
	assert.Equal(t, 0.0000045, gpt54Mini.Output)
	assert.Equal(t, 400_000, gpt54Mini.ContextWindow)

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
		assert.GreaterOrEqual(t, pricing.CacheWriteInput, 0.0, "CacheWriteInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.Output, 0.0, "Output pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.LongContextInput, 0.0, "LongContextInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.LongContextCachedInput, 0.0, "LongContextCachedInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.LongContextCacheWriteInput, 0.0, "LongContextCacheWriteInput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.LongContextOutput, 0.0, "LongContextOutput pricing for %s should be >= 0", model)
		assert.GreaterOrEqual(t, pricing.LongContextThreshold, 0, "LongContextThreshold for %s should be >= 0", model)
		assert.Greater(t, pricing.ContextWindow, 0, "ContextWindow for %s should be > 0", model)
	}
}

func TestPricingForServiceTier(t *testing.T) {
	standard := PricingForServiceTier(llmtypes.OpenAIServiceTierDefault)
	priority := PricingForServiceTier(llmtypes.OpenAIServiceTierPriority)
	fast := PricingForServiceTier(llmtypes.OpenAIServiceTierFast)

	require.Equal(t, priority["gpt-5.6-sol"], fast["gpt-5.6-sol"])

	assert.Equal(t, 0.000005, standard["gpt-5.6-sol"].Input)
	assert.Equal(t, 0.0000005, standard["gpt-5.6-sol"].CachedInput)
	assert.Equal(t, 0.00000625, standard["gpt-5.6-sol"].CacheWriteInput)
	assert.Equal(t, 0.00003, standard["gpt-5.6-sol"].Output)
	assert.Equal(t, 0.0000125, standard["gpt-5.6-sol"].LongContextCacheWriteInput)

	assert.Equal(t, 0.00001, priority["gpt-5.6-sol"].Input)
	assert.Equal(t, 0.000001, priority["gpt-5.6-sol"].CachedInput)
	assert.Equal(t, 0.0000125, priority["gpt-5.6-sol"].CacheWriteInput)
	assert.Equal(t, 0.00006, priority["gpt-5.6-sol"].Output)
	assert.Equal(t, 0, priority["gpt-5.6-sol"].LongContextThreshold)

	assert.Equal(t, 0.000005, priority["gpt-5.6-terra"].Input)
	assert.Equal(t, 0.0000005, priority["gpt-5.6-terra"].CachedInput)
	assert.Equal(t, 0.00000625, priority["gpt-5.6-terra"].CacheWriteInput)
	assert.Equal(t, 0.00003, priority["gpt-5.6-terra"].Output)

	assert.Equal(t, 0.000002, priority["gpt-5.6-luna"].Input)
	assert.Equal(t, 0.0000002, priority["gpt-5.6-luna"].CachedInput)
	assert.Equal(t, 0.0000025, priority["gpt-5.6-luna"].CacheWriteInput)
	assert.Equal(t, 0.000012, priority["gpt-5.6-luna"].Output)

	assert.Equal(t, Pricing["gpt-4.1"], priority["gpt-4.1"])
}

func TestBaseURL(t *testing.T) {
	// Test that BaseURL is correctly defined
	assert.Equal(t, "https://api.openai.com/v1", BaseURL)
}

func TestAPIKeyEnvVar(t *testing.T) {
	// Test that APIKeyEnvVar is correctly defined
	assert.Equal(t, "OPENAI_API_KEY", APIKeyEnvVar)

	// Test that it doesn't contain whitespace
	assert.NotContains(t, APIKeyEnvVar, " ", "APIKeyEnvVar should not contain spaces")
	assert.NotContains(t, APIKeyEnvVar, "\t", "APIKeyEnvVar should not contain tabs")
	assert.NotContains(t, APIKeyEnvVar, "\n", "APIKeyEnvVar should not contain newlines")

	// Test that it's not empty
	assert.NotEmpty(t, APIKeyEnvVar, "APIKeyEnvVar should not be empty")
}
