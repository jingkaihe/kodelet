package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "File Read: /test/file.go") {
			t.Errorf("Expected file path in output, got: %s", output)
		}
		if !strings.Contains(output, "Offset: 1") {
			t.Errorf("Expected offset in output, got: %s", output)
		}
		if !strings.Contains(output, "package main") {
			t.Errorf("Expected file content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "[truncated]") {
			t.Errorf("Expected truncation indicator in output, got: %s", output)
		}
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   false,
			Error:     "File not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: File not found") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileWriteMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Invalid metadata type") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
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
		if output == "" {
			t.Errorf("Expected diff output, got empty string")
		}
		// Basic check that it looks like a diff (udiff will handle actual formatting)
		if !strings.Contains(output, "/test/file.go") {
			t.Errorf("Expected file path in diff output, got: %s", output)
		}
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

		if !strings.Contains(output, "(no changes)") {
			t.Errorf("Expected no changes message, got: %s", output)
		}
	})
}