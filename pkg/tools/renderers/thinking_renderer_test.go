package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestThinkingRenderer(t *testing.T) {
	renderer := &ThinkingRenderer{}

	t.Run("Thinking with category", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "thinking",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ThinkingMetadata{
				Category: "analysis",
				Thought:  "I need to think about this problem carefully.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Thinking [analysis]:", "Expected thinking header with category")
		assert.Contains(t, output, "I need to think about this problem carefully.", "Expected thought content")
	})

	t.Run("Thinking without category", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "thinking",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ThinkingMetadata{
				Thought: "Simple thought.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Thinking:", "Expected thinking header without category")
		assert.False(t, strings.Contains(output, "[]"), "Should not show empty category brackets")
	})
}