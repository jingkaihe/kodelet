package llm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBashConfigMarshalJSONUsesDurationString(t *testing.T) {
	data, err := json.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.JSONEq(t, `{"timeout":"5m0s"}`, string(data))
}

func TestBashConfigMarshalYAMLUsesDurationString(t *testing.T) {
	data, err := yaml.Marshal(BashConfig{Timeout: 5 * time.Minute})
	require.NoError(t, err)

	assert.Equal(t, "timeout: 5m0s\n", string(data))
}

func TestModelPricingForPromptTokens(t *testing.T) {
	pricing := ModelPricing{
		Input:                  1,
		CachedInput:            0.1,
		Output:                 2,
		LongContextInput:       3,
		LongContextCachedInput: 0.3,
		LongContextOutput:      4,
		LongContextThreshold:   272_000,
		ContextWindow:          1_050_000,
	}

	assert.Equal(t, 1.0, pricing.ForPromptTokens(272_000).Input)
	assert.Equal(t, 0.1, pricing.ForPromptTokens(272_000).CachedInput)
	assert.Equal(t, 2.0, pricing.ForPromptTokens(272_000).Output)

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.3, longContext.CachedInput)
	assert.Equal(t, 4.0, longContext.Output)
	assert.Equal(t, 1_050_000, longContext.ContextWindow)
}

func TestModelPricingForPromptTokensAllowsPartialLongContextRates(t *testing.T) {
	pricing := ModelPricing{
		Input:                1,
		CachedInput:          0,
		Output:               2,
		LongContextInput:     3,
		LongContextOutput:    4,
		LongContextThreshold: 272_000,
	}

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.0, longContext.CachedInput)
	assert.Equal(t, 4.0, longContext.Output)
}
