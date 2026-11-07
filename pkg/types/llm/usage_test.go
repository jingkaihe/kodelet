package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsageTotals(t *testing.T) {
	usage := &Usage{
		InputTokens:              10,
		OutputTokens:             20,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     40,
		InputCost:                0.10,
		OutputCost:               0.20,
		CacheCreationCost:        0.30,
		CacheReadCost:            0.40,
	}

	assert.Equal(t, 100, usage.TotalTokens())
	assert.InDelta(t, 1.00, usage.TotalCost(), 0.000001)
}

func TestUsageForDisplay(t *testing.T) {
	thinking := 80
	usage := Usage{
		InputTokens: 100, OutputTokens: 90, CurrentContextWindow: 190,
		ContextThinkingByResponse: map[string]int{"response-1": thinking},
		ContextBreakdown:          &ContextBreakdown{ThinkingTokens: &thinking, TotalTokens: 190},
	}
	display := usage.ForDisplay()
	assert.Nil(t, display.ContextThinkingByResponse)
	assert.Equal(t, usage.ContextBreakdown, display.ContextBreakdown)
	assert.Equal(t, usage.TotalTokens(), display.TotalTokens())
	assert.Equal(t, usage.CurrentContextWindow, display.CurrentContextWindow)
	assert.Equal(t, map[string]int{"response-1": thinking}, usage.ContextThinkingByResponse)
}
