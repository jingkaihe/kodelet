package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestCustomToolRenderer(t *testing.T) {
	renderer := &CustomToolRenderer{}

	t.Run("Successful custom tool with output and execution time", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_hello",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CustomToolMetadata{
				ExecutionTime: 150 * time.Millisecond,
				Output:        "Hello, Alice! You are 30 years old.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Custom Tool: hello")
		assert.Contains(t, output, "(executed in 150ms)")
		assert.Contains(t, output, "Hello, Alice! You are 30 years old.")
	})

	t.Run("Custom tool with minimal metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_git_info",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CustomToolMetadata{
				ExecutionTime: 0,
				Output:        `{"branch": "main", "commit": "abc123", "uncommitted_changes": 0}`,
				CanonicalName: "git-info",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Custom Tool: git_info [git-info]")
		assert.NotContains(t, output, "executed in") // No execution time shown when 0
		assert.Contains(t, output, `{"branch": "main"`)
	})

	t.Run("Plugin tool shows canonical name", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "plugin_tool_jingkaihe_skills_waitrose_cli",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CustomToolMetadata{
				ExecutionTime: 10 * time.Millisecond,
				Output:        "ok",
				CanonicalName: "jingkaihe/skills/waitrose-cli",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Custom Tool: jingkaihe_skills_waitrose_cli [jingkaihe/skills/waitrose-cli]")
	})

	t.Run("Custom tool with no output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_test",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CustomToolMetadata{
				ExecutionTime: 50 * time.Millisecond,
				Output:        "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Custom Tool: test")
		assert.Contains(t, output, "(executed in 50ms)")
		assert.NotContains(t, output, "\n\n") // No extra newlines when no output
	})

	t.Run("Custom tool with multiline output", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_multiline",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.CustomToolMetadata{
				ExecutionTime: 200 * time.Millisecond,
				Output:        "Line 1\nLine 2\nLine 3",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Custom Tool: multiline")
		assert.Contains(t, output, "Line 1\nLine 2\nLine 3")
	})

	t.Run("Custom tool error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_error",
			Success:   false,
			Error:     "Tool execution failed: invalid input",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Tool execution failed: invalid input", output)
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_invalid",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{ // Wrong metadata type
				Command:  "echo test",
				ExitCode: 0,
				Output:   "test",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Invalid metadata type for custom tool", output)
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "custom_tool_nil",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Invalid metadata type for custom tool", output)
	})
}
