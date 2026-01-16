package prompts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortSummaryPrompt(t *testing.T) {
	t.Run("prompt is not empty", func(t *testing.T) {
		assert.NotEmpty(t, ShortSummaryPrompt, "ShortSummaryPrompt should not be empty")
	})

	t.Run("contains conciseness requirements", func(t *testing.T) {
		conciseElements := []string{
			"one sentence",
			"12 words",
			"short",
			"concise",
		}

		for _, element := range conciseElements {
			assert.Contains(t, ShortSummaryPrompt, element,
				"ShortSummaryPrompt should emphasize conciseness: %s", element)
		}
	})

	t.Run("has appropriate length", func(t *testing.T) {
		length := len(ShortSummaryPrompt)
		assert.GreaterOrEqual(t, length, 50,
			"ShortSummaryPrompt should have some content (at least 50 chars)")
		assert.LessOrEqual(t, length, 3000,
			"ShortSummaryPrompt should be reasonable in length (max 3000 chars)")
	})

	t.Run("provides clear instructions", func(t *testing.T) {
		assert.Contains(t, ShortSummaryPrompt, "Summarise",
			"Should contain clear instruction to summarize")
	})

	t.Run("is well formed", func(t *testing.T) {
		assert.NotContains(t, ShortSummaryPrompt, "TODO",
			"ShortSummaryPrompt should not contain TODO placeholders")
		assert.NotContains(t, ShortSummaryPrompt, "FIXME",
			"ShortSummaryPrompt should not contain FIXME placeholders")
	})
}
