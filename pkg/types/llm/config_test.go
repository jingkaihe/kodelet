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

func TestToolModeIsPatchMode(t *testing.T) {
	assert.True(t, ToolModePatch.IsPatchMode())
	assert.False(t, ToolModeFull.IsPatchMode())
	assert.False(t, ToolMode("").IsPatchMode())
}

func TestConversationSummaryModeUsesLLM(t *testing.T) {
	assert.True(t, ConversationSummaryMode("").UsesLLM())
	assert.True(t, ConversationSummaryModeLLM.UsesLLM())
	assert.False(t, ConversationSummaryModeFirstMessage.UsesLLM())
}

func TestConfigBashTimeout(t *testing.T) {
	assert.Equal(t, DefaultBashTimeout, Config{}.BashTimeout())
	assert.Equal(t, DefaultBashTimeout, Config{Bash: &BashConfig{}}.BashTimeout())
	assert.Equal(t, 30*time.Second, Config{Bash: &BashConfig{Timeout: 30 * time.Second}}.BashTimeout())
}

func TestOpenAIServiceTierParsingAndWireValue(t *testing.T) {
	tier, ok := ParseOpenAIServiceTier(" FAST ")
	require.True(t, ok)
	assert.Equal(t, OpenAIServiceTierFast, tier)
	assert.Equal(t, "priority", tier.WireValue())

	tier, ok = ParseOpenAIServiceTier("scale")
	require.True(t, ok)
	assert.Equal(t, OpenAIServiceTierScale, tier)
	assert.Equal(t, "scale", tier.WireValue())

	tier, ok = ParseOpenAIServiceTier("")
	assert.False(t, ok)
	assert.Empty(t, tier)

	tier, ok = ParseOpenAIServiceTier("unknown")
	assert.False(t, ok)
	assert.Empty(t, tier)
	assert.Empty(t, OpenAIServiceTier("unknown").WireValue())
}

func TestDefaultContextPatterns(t *testing.T) {
	patterns := DefaultContextPatterns()
	assert.Equal(t, []string{"AGENTS.md"}, patterns)

	patterns[0] = "changed"
	assert.Equal(t, []string{"AGENTS.md"}, DefaultContextPatterns())
}

func TestModelPricingForPromptTokens(t *testing.T) {
	pricing := ModelPricing{
		Input:                      1,
		CachedInput:                0.1,
		CacheWriteInput:            1.25,
		Output:                     2,
		LongContextInput:           3,
		LongContextCachedInput:     0.3,
		LongContextCacheWriteInput: 3.75,
		LongContextOutput:          4,
		LongContextThreshold:       272_000,
		ContextWindow:              1_050_000,
	}

	assert.Equal(t, 1.0, pricing.ForPromptTokens(272_000).Input)
	assert.Equal(t, 0.1, pricing.ForPromptTokens(272_000).CachedInput)
	assert.Equal(t, 1.25, pricing.ForPromptTokens(272_000).CacheWriteInput)
	assert.Equal(t, 2.0, pricing.ForPromptTokens(272_000).Output)

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.3, longContext.CachedInput)
	assert.Equal(t, 3.75, longContext.CacheWriteInput)
	assert.Equal(t, 4.0, longContext.Output)
	assert.Equal(t, 1_050_000, longContext.ContextWindow)
}

func TestModelPricingForPromptTokensAllowsPartialLongContextRates(t *testing.T) {
	pricing := ModelPricing{
		Input:                1,
		CachedInput:          0,
		CacheWriteInput:      1.25,
		Output:               2,
		LongContextInput:     3,
		LongContextOutput:    4,
		LongContextThreshold: 272_000,
	}

	longContext := pricing.ForPromptTokens(272_001)
	assert.Equal(t, 3.0, longContext.Input)
	assert.Equal(t, 0.0, longContext.CachedInput)
	assert.Equal(t, 1.25, longContext.CacheWriteInput)
	assert.Equal(t, 4.0, longContext.Output)
}
