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
