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
