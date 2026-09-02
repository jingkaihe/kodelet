package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractExtensionToolPresentationValidatesAndNormalizes(t *testing.T) {
	result := &StructuredToolResult{Metadata: &ExtensionToolMetadata{Data: map[string]any{
		"presentation": map[string]any{
			"summary": "  Follow up\nparser-reviewer  ",
			"body":    "Review the parser",
			"format":  "MARKDOWN",
		},
	}}}

	presentation, _, ok := ExtractExtensionToolPresentation(result)
	require.True(t, ok)
	assert.Equal(t, "Follow up parser-reviewer", presentation.Summary)
	assert.Equal(t, "Review the parser", presentation.Body)
	assert.Equal(t, "markdown", presentation.Format)
	assert.True(t, presentation.HasBody())

	result.Metadata.(*ExtensionToolMetadata).Data["presentation"] = map[string]any{
		"summary": "Wait for parser-reviewer",
	}
	presentation, _, ok = ExtractExtensionToolPresentation(result)
	require.True(t, ok)
	assert.Equal(t, "text", presentation.Format)
	assert.False(t, presentation.HasBody())

	result.Metadata.(*ExtensionToolMetadata).Data["presentation"] = map[string]any{
		"summary": "Agent update",
		"body":    "",
	}
	presentation, _, ok = ExtractExtensionToolPresentation(result)
	require.True(t, ok)
	assert.True(t, presentation.HasBody())

	for _, invalid := range []any{
		map[string]any{},
		map[string]any{"summary": "Agent update", "format": "html"},
		map[string]any{"summary": "Agent\x1b[31m update"},
		map[string]any{"summary": "Agent \u202eupdate"},
		map[string]any{"summary": strings.Repeat("x", MaxExtensionToolPresentationSummaryRunes+1)},
		map[string]any{"summary": "Agent update", "body": strings.Repeat("界", MaxExtensionToolPresentationBodyBytes/3+1)},
		map[string]any{"summary": "Agent update", "body": nil},
		map[string]any{"summary": "Agent update", "format": nil},
		"invalid",
	} {
		result.Metadata.(*ExtensionToolMetadata).Data["presentation"] = invalid
		_, _, ok = ExtractExtensionToolPresentation(result)
		assert.False(t, ok)
	}
}
