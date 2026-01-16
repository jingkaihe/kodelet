package hooks

import (
	"testing"

	"github.com/stretchr/testify/assert"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestTrigger_WithMessageOpt_DisablesAutoCompact(t *testing.T) {
	base := Trigger{
		AutoCompactEnabled:   true,
		AutoCompactThreshold: 0.8,
	}

	adjusted := base.WithMessageOpt(llmtypes.MessageOpt{
		DisableAutoCompact: true,
		CompactRatio:       0.6,
	})

	assert.False(t, adjusted.AutoCompactEnabled)
	assert.Equal(t, 0.8, adjusted.AutoCompactThreshold)
}

func TestTrigger_WithMessageOpt_OverridesCompactRatio(t *testing.T) {
	base := Trigger{
		AutoCompactEnabled:   true,
		AutoCompactThreshold: 0.8,
	}

	adjusted := base.WithMessageOpt(llmtypes.MessageOpt{
		CompactRatio: 0.6,
	})

	assert.True(t, adjusted.AutoCompactEnabled)
	assert.Equal(t, 0.6, adjusted.AutoCompactThreshold)
}
