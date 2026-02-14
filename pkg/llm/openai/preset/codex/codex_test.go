package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModels(t *testing.T) {
	assert.Contains(t, Models.Reasoning, "gpt-5.3-codex")
	assert.Contains(t, Models.Reasoning, "gpt-5.3-codex-spark")
	assert.Contains(t, Models.Reasoning, "gpt-5.2-codex")
	assert.Contains(t, Models.Reasoning, "gpt-5.2")
	assert.Contains(t, Models.Reasoning, "gpt-5.1-codex-max")
	assert.Contains(t, Models.Reasoning, "gpt-5.1-codex-mini")
	assert.Empty(t, Models.NonReasoning)
}

func TestPricing(t *testing.T) {
	assert.Contains(t, Pricing, "gpt-5.3-codex")
	assert.Contains(t, Pricing, "gpt-5.3-codex-spark")
	assert.Contains(t, Pricing, "gpt-5.2-codex")
	assert.Contains(t, Pricing, "gpt-5.2")
	assert.Contains(t, Pricing, "gpt-5.1-codex-max")
	assert.Contains(t, Pricing, "gpt-5.1-codex-mini")

	for _, price := range Pricing {
		assert.Equal(t, 0.0, price.Input)
		assert.Equal(t, 0.0, price.Output)
		assert.Equal(t, 272_000, price.ContextWindow)
	}
}
