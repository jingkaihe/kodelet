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

func TestTodoRenderer(t *testing.T) {
	renderer := &TodoRenderer{}

	t.Run("Todo list with items", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "todo",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.TodoMetadata{
				Action: "read",
				TodoList: []tools.TodoItem{
					{
						ID:       "1",
						Content:  "Fix the bug",
						Status:   "pending",
						Priority: "high",
					},
					{
						ID:       "2",
						Content:  "Add tests",
						Status:   "completed",
						Priority: "medium",
					},
					{
						ID:       "3",
						Content:  "Update docs",
						Status:   "in_progress",
						Priority: "low",
					},
				},
				Statistics: tools.TodoStats{
					Total:      3,
					Pending:    1,
					InProgress: 1,
					Completed:  1,
				},
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Todo List:") {
			t.Errorf("Expected todo list header, got: %s", output)
		}
		if !strings.Contains(output, "Total: 3") {
			t.Errorf("Expected statistics, got: %s", output)
		}
		if !strings.Contains(output, "Fix the bug") {
			t.Errorf("Expected todo content, got: %s", output)
		}
		if !strings.Contains(output, "✓") {
			t.Errorf("Expected completed icon, got: %s", output)
		}
		if !strings.Contains(output, "→") {
			t.Errorf("Expected in progress icon, got: %s", output)
		}
		if !strings.Contains(output, "○") {
			t.Errorf("Expected pending icon, got: %s", output)
		}
	})

	t.Run("Todo update action", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "todo",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.TodoMetadata{
				Action:   "write",
				TodoList: []tools.TodoItem{},
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Todo List Updated:") {
			t.Errorf("Expected todo list updated header, got: %s", output)
		}
	})
}

func TestBatchRenderer(t *testing.T) {
	renderer := &BatchRenderer{}

	t.Run("Batch execution with sub-results", func(t *testing.T) {
		subResult := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath: "/test/file.txt",
				Lines:    []string{"content"},
			},
		}

		result := tools.StructuredToolResult{
			ToolName:  "batch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BatchMetadata{
				Description:   "Test batch",
				SuccessCount:  1,
				FailureCount:  0,
				ExecutionTime: time.Second,
				SubResults:    []tools.StructuredToolResult{subResult},
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Batch Execution: Test batch") {
			t.Errorf("Expected batch description, got: %s", output)
		}
		if !strings.Contains(output, "Success: 1") {
			t.Errorf("Expected success count, got: %s", output)
		}
		if !strings.Contains(output, "Task 1: file_read") {
			t.Errorf("Expected sub-task info, got: %s", output)
		}
	})
}

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

func TestWebFetchRenderer(t *testing.T) {
	renderer := &WebFetchRenderer{}

	t.Run("Web fetch with all metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				ProcessedType: "html",
				SavedPath:     "/tmp/content.html",
				Prompt:        "Extract main content",
				Size:          1024,
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Web Fetch: https://example.com") {
			t.Errorf("Expected URL in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: html") {
			t.Errorf("Expected type in output, got: %s", output)
		}
		if !strings.Contains(output, "Saved to: /tmp/content.html") {
			t.Errorf("Expected saved path in output, got: %s", output)
		}
		if !strings.Contains(output, "Prompt: Extract main content") {
			t.Errorf("Expected prompt in output, got: %s", output)
		}
		if !strings.Contains(output, "Size: 1024 bytes") {
			t.Errorf("Expected size in output, got: %s", output)
		}
	})

	t.Run("Web fetch minimal metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				ProcessedType: "text",
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Web Fetch: https://example.com") {
			t.Errorf("Expected URL in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: text") {
			t.Errorf("Expected type in output, got: %s", output)
		}
		// Should not contain optional fields
		if strings.Contains(output, "Saved to:") {
			t.Errorf("Should not show empty saved path, got: %s", output)
		}
	})
}
