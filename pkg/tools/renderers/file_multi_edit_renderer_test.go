package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestFileMultiEditRenderer(t *testing.T) {
	renderer := &FileMultiEditRenderer{}

	t.Run("Successful multi-edit with multiple edits", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileMultiEditMetadata{
				FilePath: "/test/file.go",
				Edits: []tools.Edit{
					{
						OldContent: "old function() {",
						NewContent: "new function() {",
					},
					{
						OldContent: "var x = 1",
						NewContent: "var x = 2",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "File Multi Edit: /test/file.go", "Expected file path in output")
		assert.Contains(t, output, "Total edits: 2", "Expected total edits count in output")
		assert.Contains(t, output, "Edit 1:", "Expected edit 1 in output")
		assert.Contains(t, output, "Edit 2:", "Expected edit 2 in output")
		assert.Contains(t, output, "- old function() {", "Expected old content in output")
		assert.Contains(t, output, "+ new function() {", "Expected new content in output")
	})

	t.Run("Single edit with multiline content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileMultiEditMetadata{
				FilePath: "/test/config.yaml",
				Edits: []tools.Edit{
					{
						OldContent: "# old config\nkey: value1\nother: value2",
						NewContent: "# new config\nkey: value3\nother: value4",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Total edits: 1", "Expected total edits count in output")
		assert.Contains(t, output, "- # old config", "Expected old content first line in output")
		assert.Contains(t, output, "- key: value1", "Expected old content second line in output")
		assert.Contains(t, output, "+ # new config", "Expected new content first line in output")
		assert.Contains(t, output, "+ key: value3", "Expected new content second line in output")
	})

	t.Run("Edit with only old content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileMultiEditMetadata{
				FilePath: "/test/file.txt",
				Edits: []tools.Edit{
					{
						OldContent: "line to be deleted",
						NewContent: "",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "- line to be deleted", "Expected old content in output")
		assert.NotContains(t, output, "+ ", "Should not have new content marker")
	})

	t.Run("Edit with only new content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileMultiEditMetadata{
				FilePath: "/test/file.txt",
				Edits: []tools.Edit{
					{
						OldContent: "",
						NewContent: "new line to be added",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "+ new line to be added", "Expected new content in output")
		assert.NotContains(t, output, "- ", "Should not have old content marker")
	})

	t.Run("No edits", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileMultiEditMetadata{
				FilePath: "/test/file.txt",
				Edits:    []tools.Edit{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Total edits: 0", "Expected zero edits count in output")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   false,
			Error:     "File not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: File not found", "Expected error message in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for file_multi_edit", "Expected invalid metadata error")
	})
}
