package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompactPrompt(t *testing.T) {
	t.Run("prompt is not empty", func(t *testing.T) {
		assert.NotEmpty(t, CompactPrompt, "CompactPrompt should not be empty")
	})

	t.Run("contains required sections", func(t *testing.T) {
		requiredSections := []string{
			"comprehensive summary",
			"conversation history",
			"Explicit Request and Intention",
			"Key Technical Concepts",
			"Files and Code Snippets Examined",
			"Errors and Fixes Applied",
			"Solved Problems and Ongoing Troubleshooting",
			"Non-Tool Use User Messages",
			"Pending Tasks",
			"Current Work in Progress",
		}

		for _, section := range requiredSections {
			assert.Contains(t, CompactPrompt, section,
				"CompactPrompt should contain section: %s", section)
		}
	})

	t.Run("contains structured format", func(t *testing.T) {
		structuredElements := []string{
			"<summary>",
			"</summary>",
			"### 1. Explicit Request and Intention",
			"### 2. Key Technical Concepts",
			"### 3. Files and Code Snippets Examined",
			"### 4. Errors and Fixes Applied",
			"### 5. Solved Problems and Ongoing Troubleshooting",
			"### 6. Non-Tool Use User Messages",
			"### 7. Pending Tasks",
			"### 8. Current Work in Progress",
		}

		for _, element := range structuredElements {
			assert.Contains(t, CompactPrompt, element,
				"CompactPrompt should contain structured element: %s", element)
		}
	})

	t.Run("contains important instructions", func(t *testing.T) {
		importantInstructions := []string{
			"IMPORTANT:",
			"Use the exact 8-section structure",
			"Include markdown formatting",
			"Use code blocks",
			"Be specific with file paths",
			"Include exact error messages",
			"Use bullet points",
			"chronological",
			"precise",
		}

		for _, instruction := range importantInstructions {
			assert.Contains(t, CompactPrompt, instruction,
				"CompactPrompt should contain important instruction: %s", instruction)
		}
	})

	t.Run("has reasonable length", func(t *testing.T) {
		// The prompt should be comprehensive but not excessively long
		// Expect at least 1000 characters but no more than 10000
		length := len(CompactPrompt)
		assert.GreaterOrEqual(t, length, 1000,
			"CompactPrompt should be comprehensive (at least 1000 chars)")
		assert.LessOrEqual(t, length, 10000,
			"CompactPrompt should not be excessively long (max 10000 chars)")
	})

	t.Run("properly formatted markdown", func(t *testing.T) {
		// Check for proper markdown formatting
		assert.True(t, strings.Contains(CompactPrompt, "##"),
			"Should contain markdown headers")
		assert.True(t, strings.Contains(CompactPrompt, "###"),
			"Should contain subheaders")
		assert.True(t, strings.Contains(CompactPrompt, "- ["),
			"Should contain bullet point examples")
	})
}

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
		// Short summary prompt should be brief relative to compact prompt
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
}

func TestPromptConsistency(t *testing.T) {
	t.Run("different purposes", func(t *testing.T) {
		// CompactPrompt and ShortSummaryPrompt should serve different purposes
		assert.NotEqual(t, CompactPrompt, ShortSummaryPrompt,
			"CompactPrompt and ShortSummaryPrompt should be different")

		// CompactPrompt should be longer than ShortSummaryPrompt (comprehensive vs concise)
		assert.Greater(t, len(CompactPrompt), len(ShortSummaryPrompt),
			"CompactPrompt should be longer than ShortSummaryPrompt")
	})

	t.Run("both prompts are well formed", func(t *testing.T) {
		prompts := map[string]string{
			"CompactPrompt":      CompactPrompt,
			"ShortSummaryPrompt": ShortSummaryPrompt,
		}

		for name, prompt := range prompts {
			// Should not be empty
			assert.NotEmpty(t, prompt, "%s should not be empty", name)

			// Should not contain placeholder text
			assert.NotContains(t, prompt, "TODO",
				"%s should not contain TODO placeholders", name)
			assert.NotContains(t, prompt, "FIXME",
				"%s should not contain FIXME placeholders", name)
		}
	})
}

func TestPromptValidation(t *testing.T) {
	t.Run("compact prompt structure validation", func(t *testing.T) {
		// Count the number of numbered sections
		sectionCount := 0
		for i := 1; i <= 10; i++ {
			sectionHeader := "### " + string(rune('0'+i)) + "."
			if strings.Contains(CompactPrompt, sectionHeader) {
				sectionCount++
			}
		}

		assert.Equal(t, 8, sectionCount,
			"CompactPrompt should contain exactly 8 numbered sections")
	})

	t.Run("no contradictory instructions", func(t *testing.T) {
		// CompactPrompt should not contain contradictory length requirements
		assert.False(t, strings.Contains(CompactPrompt, "brief") && strings.Contains(CompactPrompt, "comprehensive"),
			"CompactPrompt should not contain contradictory length instructions")
	})
}
