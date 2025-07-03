package renderers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestRendererWithJSONUnmarshal(t *testing.T) {
	registry := NewRendererRegistry()

	t.Run("WebFetchRenderer after JSON unmarshal", func(t *testing.T) {
		// Create a structured result with WebFetchMetadata
		original := tools.StructuredToolResult{
			ToolName:  "web_fetch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.WebFetchMetadata{
				URL:           "https://example.com",
				SavedPath:     "/tmp/file.txt",
				Content:       "This is the content",
				ProcessedType: "saved",
			},
		}

		// Marshal to JSON
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}

		// Unmarshal back
		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		if err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}

		// Try to render - this will panic or fail with current implementation
		output := registry.Render(unmarshaled)

		// Should produce the expected output
		expected := "Web Fetch: https://example.com\nSaved to: /tmp/file.txt\nThis is the content"
		if output != expected {
			t.Errorf("Expected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("FileReadRenderer after JSON unmarshal", func(t *testing.T) {
		original := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath:  "/etc/hosts",
				Offset:    0,
				Lines:     []string{"line1", "line2", "line3"},
				Language:  "text",
				Truncated: false,
			},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		if err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}

		output := registry.Render(unmarshaled)

		// Should contain file path
		if !strings.Contains(output, "/etc/hosts") {
			t.Errorf("Expected file path in output, got: %s", output)
		}
	})

	t.Run("BashRenderer after JSON unmarshal", func(t *testing.T) {
		original := tools.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BashMetadata{
				Command:       "ls -la",
				Output:        "total 8\ndrwxr-xr-x 2 user user 4096",
				ExitCode:      0,
				ExecutionTime: 100 * time.Millisecond,
			},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		if err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}

		output := registry.Render(unmarshaled)

		// Should contain command
		if !strings.Contains(output, "ls -la") {
			t.Errorf("Expected command in output, got: %s", output)
		}
	})

	t.Run("TodoRenderer after JSON unmarshal", func(t *testing.T) {
		original := tools.StructuredToolResult{
			ToolName:  "todo_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.TodoMetadata{
				Action: "read",
				TodoList: []tools.TodoItem{
					{ID: "1", Content: "Task 1", Status: "pending", Priority: "high"},
					{ID: "2", Content: "Task 2", Status: "completed", Priority: "low"},
				},
				Statistics: tools.TodoStats{
					Total:      2,
					Completed:  1,
					InProgress: 0,
					Pending:    1,
				},
			},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		if err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}

		output := registry.Render(unmarshaled)

		// Should contain todo content
		if !strings.Contains(output, "Task 1") {
			t.Errorf("Expected todo content in output, got: %s", output)
		}
	})
}
