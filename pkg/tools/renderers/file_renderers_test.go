package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestFileReadRenderer(t *testing.T) {
	renderer := &FileReadRenderer{}

	t.Run("Successful file read", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath:  "/test/file.go",
				Lines:     []string{"package main", "func main() {", "}"},
				Offset:    1,
				Truncated: false,
				Language:  "go",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "File Read: /test/file.go", "Expected file path in output")
		assert.Contains(t, output, "Offset: 1", "Expected offset in output")
		assert.Contains(t, output, "package main", "Expected file content in output")
	})

	t.Run("Truncated file read", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath:  "/test/large.txt",
				Lines:     []string{"line1", "line2"},
				Offset:    0,
				Truncated: true,
				Language:  "text",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "[truncated]", "Expected truncation indicator in output")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   false,
			Error:     "File not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: File not found", "Expected error message in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileWriteMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Invalid metadata type", "Expected invalid metadata error")
	})
}

func TestFileEditRenderer(t *testing.T) {
	renderer := &FileEditRenderer{}

	t.Run("Successful file edit", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath: "/test/file.go",
				Language: "go",
				Edits: []tools.Edit{
					{
						StartLine:  5,
						EndLine:    7,
						OldContent: "old code here",
						NewContent: "new code here",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		// Should contain unified diff output
		assert.NotEmpty(t, output, "Expected diff output")
		// Basic check that it looks like a diff (udiff will handle actual formatting)
		assert.Contains(t, output, "/test/file.go", "Expected file path in diff output")
	})

	t.Run("No edits", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath: "/test/file.go",
				Language: "go",
				Edits:    []tools.Edit{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "(no changes)", "Expected no changes message")
	})
}
