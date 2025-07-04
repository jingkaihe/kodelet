package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "File Multi Edit: /test/file.go") {
			t.Errorf("Expected file path in output, got: %s", output)
		}
		if !strings.Contains(output, "Total edits: 2") {
			t.Errorf("Expected total edits count in output, got: %s", output)
		}
		if !strings.Contains(output, "Edit 1:") {
			t.Errorf("Expected edit 1 in output, got: %s", output)
		}
		if !strings.Contains(output, "Edit 2:") {
			t.Errorf("Expected edit 2 in output, got: %s", output)
		}
		if !strings.Contains(output, "- old function() {") {
			t.Errorf("Expected old content in output, got: %s", output)
		}
		if !strings.Contains(output, "+ new function() {") {
			t.Errorf("Expected new content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Total edits: 1") {
			t.Errorf("Expected total edits count in output, got: %s", output)
		}
		if !strings.Contains(output, "- # old config") {
			t.Errorf("Expected old content first line in output, got: %s", output)
		}
		if !strings.Contains(output, "- key: value1") {
			t.Errorf("Expected old content second line in output, got: %s", output)
		}
		if !strings.Contains(output, "+ # new config") {
			t.Errorf("Expected new content first line in output, got: %s", output)
		}
		if !strings.Contains(output, "+ key: value3") {
			t.Errorf("Expected new content second line in output, got: %s", output)
		}
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

		if !strings.Contains(output, "- line to be deleted") {
			t.Errorf("Expected old content in output, got: %s", output)
		}
		if strings.Contains(output, "+ ") {
			t.Errorf("Should not have new content marker, got: %s", output)
		}
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

		if !strings.Contains(output, "+ new line to be added") {
			t.Errorf("Expected new content in output, got: %s", output)
		}
		if strings.Contains(output, "- ") {
			t.Errorf("Should not have old content marker, got: %s", output)
		}
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

		if !strings.Contains(output, "Total edits: 0") {
			t.Errorf("Expected zero edits count in output, got: %s", output)
		}
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_multi_edit",
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
			ToolName:  "file_multi_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for file_multi_edit") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}
