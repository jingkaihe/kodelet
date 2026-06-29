package codex

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModels(t *testing.T) {
	assert.Equal(t, "gpt-5.4", DefaultModel)
	assert.Contains(t, Models.Reasoning, "gpt-5.5")
	assert.Contains(t, Models.Reasoning, "gpt-5.3-codex")
	assert.Contains(t, Models.Reasoning, "gpt-5.4")
	assert.Contains(t, Models.Reasoning, "gpt-5.4-mini")
	assert.Contains(t, Models.Reasoning, "gpt-5.3-codex-spark")
	assert.Contains(t, Models.Reasoning, "gpt-5.2-codex")
	assert.Contains(t, Models.Reasoning, "gpt-5.2")
	assert.Contains(t, Models.Reasoning, "gpt-5.1-codex-max")
	assert.Contains(t, Models.Reasoning, "gpt-5.1-codex-mini")
	assert.Empty(t, Models.NonReasoning)
}

func TestPricing(t *testing.T) {
	assert.Contains(t, Pricing, "gpt-5.5")
	assert.Contains(t, Pricing, "gpt-5.3-codex")
	assert.Contains(t, Pricing, "gpt-5.4")
	assert.Contains(t, Pricing, "gpt-5.4-mini")
	assert.Contains(t, Pricing, "gpt-5.3-codex-spark")
	assert.Contains(t, Pricing, "gpt-5.2-codex")
	assert.Contains(t, Pricing, "gpt-5.2")
	assert.Contains(t, Pricing, "gpt-5.1-codex-max")
	assert.Contains(t, Pricing, "gpt-5.1-codex-mini")

	gpt55 := Pricing["gpt-5.5"]
	assert.Equal(t, 0.000005, gpt55.Input)
	assert.Equal(t, 0.0000005, gpt55.CachedInput)
	assert.Equal(t, 0.00003, gpt55.Output)
	assert.Equal(t, 0, gpt55.LongContextThreshold)

	gpt53Codex := Pricing["gpt-5.3-codex"]
	assert.Equal(t, 0.00000175, gpt53Codex.Input)
	assert.Equal(t, 0.000000175, gpt53Codex.CachedInput)
	assert.Equal(t, 0.000014, gpt53Codex.Output)

	gpt54Mini := Pricing["gpt-5.4-mini"]
	assert.Equal(t, 0.00000075, gpt54Mini.Input)
	assert.Equal(t, 0.000000075, gpt54Mini.CachedInput)
	assert.Equal(t, 0.0000045, gpt54Mini.Output)

	legacyMini := Pricing["gpt-5.1-codex-mini"]
	assert.Equal(t, 0.00000025, legacyMini.Input)
	assert.Equal(t, 0.000000025, legacyMini.CachedInput)
	assert.Equal(t, 0.000002, legacyMini.Output)

	for model, price := range Pricing {
		assert.Greater(t, price.Input, 0.0)
		assert.Greater(t, price.CachedInput, 0.0)
		assert.Greater(t, price.Output, 0.0)
		assert.Equal(t, 0, price.LongContextThreshold, "Codex pricing for %s should stay in the flat context band", model)
		assert.Equal(t, price.Input, price.ForPromptTokens(1_000_000).Input)
		assert.Equal(t, price.CachedInput, price.ForPromptTokens(1_000_000).CachedInput)
		assert.Equal(t, price.Output, price.ForPromptTokens(1_000_000).Output)
		if model == "gpt-5.3-codex-spark" {
			assert.Equal(t, 128_000, price.ContextWindow)
			continue
		}
		assert.Equal(t, 272_000, price.ContextWindow)
	}
}

func TestPricingForServiceTier(t *testing.T) {
	standard := PricingForServiceTier(llmtypes.OpenAIServiceTierDefault)
	priority := PricingForServiceTier(llmtypes.OpenAIServiceTierPriority)
	fast := PricingForServiceTier(llmtypes.OpenAIServiceTierFast)

	require.Equal(t, Pricing["gpt-5.3-codex"], standard["gpt-5.3-codex"])
	require.Equal(t, priority["gpt-5.3-codex"], fast["gpt-5.3-codex"])
	assert.Equal(t, 0.0000035, priority["gpt-5.3-codex"].Input)
	assert.Equal(t, 0.00000035, priority["gpt-5.3-codex"].CachedInput)
	assert.Equal(t, 0.000028, priority["gpt-5.3-codex"].Output)

	assert.Equal(t, 0.0000125, priority["gpt-5.5"].Input)
	assert.Equal(t, 0.00000125, priority["gpt-5.5"].CachedInput)
	assert.Equal(t, 0.000075, priority["gpt-5.5"].Output)

	assert.Equal(t, 272_000, priority["gpt-5.5"].ContextWindow)
	assert.Equal(t, 0, priority["gpt-5.5"].LongContextThreshold)
}
