package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

	for model, price := range Pricing {
		assert.Equal(t, 0.0, price.Input)
		assert.Equal(t, 0.0, price.Output)
		if model == "gpt-5.3-codex-spark" {
			assert.Equal(t, 128_000, price.ContextWindow)
			continue
		}
		assert.Equal(t, 272_000, price.ContextWindow)
	}
}
