package prompts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompactPrompt(t *testing.T) {
	t.Run("prompt is not empty", func(t *testing.T) {
		assert.NotEmpty(t, CompactPrompt, "CompactPrompt should not be empty")
	})

	t.Run("contains required summary sections", func(t *testing.T) {
		sections := []string{
			"Explicit Request and Intention",
			"Key Technical Concepts",
			"Files and Code Snippets Examined",
			"Errors and Fixes Applied",
			"Pending Tasks",
			"Current Work in Progress",
		}

		for _, section := range sections {
			assert.Contains(t, CompactPrompt, section)
		}
	})
}
