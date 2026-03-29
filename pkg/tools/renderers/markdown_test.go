package renderers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFencedCodeBlockUsesFenceLongerThanContent(t *testing.T) {
	content := "before\n```\ninside\n```\nafter"

	rendered := FencedCodeBlock("text", content)

	assert.Contains(t, rendered, "````text\nbefore\n```\ninside\n```\nafter\n````")
}

func TestStripLeadingMarkdownMetadata(t *testing.T) {
	input := "- **Tool:** `bash`\n- **Call ID:** `call-1`\n- **Status:** success\n\n**Output**\n\n```text\nhello\n```"

	rendered := stripLeadingMarkdownMetadata(input, map[string]struct{}{
		"Tool":    {},
		"Call ID": {},
	})

	assert.NotContains(t, rendered, "- **Tool:**")
	assert.NotContains(t, rendered, "- **Call ID:**")
	assert.Contains(t, rendered, "- **Status:** success")
	assert.Contains(t, rendered, "**Output**")
}
