package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestSkillRenderer_RenderCLI(t *testing.T) {
	renderer := &SkillRenderer{}

	t.Run("successful skill invocation", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "skill",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SkillMetadata{
				SkillName: "pdf",
				Directory: "/home/user/.kodelet/skills/pdf",
			},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "pdf")
		assert.Contains(t, output, "/home/user/.kodelet/skills/pdf")
	})

	t.Run("error result", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "skill",
			Success:   false,
			Error:     "skill 'unknown' not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Error")
		assert.Contains(t, output, "not found")
	})

	t.Run("invalid metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "skill",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Error")
		assert.Contains(t, output, "Invalid metadata")
	})
}
