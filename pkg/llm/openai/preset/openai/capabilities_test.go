package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResponsesModelCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		responses   bool
		rejectsNone bool
	}{
		{name: "gpt-6.2 Astra", model: "gpt-6.2-astra", responses: true, rejectsNone: true},
		{name: "gpt-6.2 Sol normalized", model: " GPT-6.2-SOL ", responses: true, rejectsNone: true},
		{name: "gpt-6.1 Terra", model: "gpt-6.1-terra", responses: true, rejectsNone: true},
		{name: "gpt-6.1 Sol", model: "gpt-6.1-sol", responses: true, rejectsNone: true},
		{name: "gpt-6 Astra", model: "gpt-6-astra", responses: true, rejectsNone: true},
		{name: "gpt-6 Sol", model: "gpt-6-sol", responses: true},
		{name: "gpt-6 Luna", model: "gpt-6-luna", responses: true},
		{name: "gpt-5.6 alias", model: "gpt-5.6", responses: true},
		{name: "gpt-5.6 family normalized", model: " GPT-5.6-SOL ", responses: true},
		{name: "gpt-5.5", model: "gpt-5.5"},
		{name: "gpt-4.1", model: "gpt-4.1"},
		{name: "custom model", model: "custom-model"},
		{name: "empty", model: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.responses, RequiresResponsesAPI(tt.model))
			assert.Equal(t, tt.responses, SupportsPromptCacheOptions(tt.model))
			assert.Equal(t, tt.rejectsNone, RejectsNoneReasoningEffort(tt.model))
		})
	}
}

func TestResponsesModelsHaveCatalogEntries(t *testing.T) {
	for model := range responsesModels {
		assert.Contains(t, Models.Reasoning, model)
		assert.Contains(t, Pricing, model)
		assert.Contains(t, PriorityPricing, model)
	}
}
