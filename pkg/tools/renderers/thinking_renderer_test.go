package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Thinking [analysis]:") {
			t.Errorf("Expected thinking header with category, got: %s", output)
		}
		if !strings.Contains(output, "I need to think about this problem carefully.") {
			t.Errorf("Expected thought content, got: %s", output)
		}
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

		if !strings.Contains(output, "Thinking:") {
			t.Errorf("Expected thinking header without category, got: %s", output)
		}
		if strings.Contains(output, "[]") {
			t.Errorf("Should not show empty category brackets, got: %s", output)
		}
	})
}
