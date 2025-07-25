package renderers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, err, "Failed to marshal")

		// Unmarshal back
		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err, "Failed to unmarshal")

		// Try to render - this will panic or fail with current implementation
		output := registry.Render(unmarshaled)

		// Should produce the expected output
		expected := "Web Fetch: https://example.com\nSaved to: /tmp/file.txt\nThis is the content"
		assert.Equal(t, expected, output)
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
		require.NoError(t, err, "Failed to marshal")

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err, "Failed to unmarshal")

		output := registry.Render(unmarshaled)

		// Should contain file path
		assert.Contains(t, output, "/etc/hosts", "Expected file path in output")
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
		require.NoError(t, err, "Failed to marshal")

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err, "Failed to unmarshal")

		output := registry.Render(unmarshaled)

		// Should contain command
		assert.Contains(t, output, "ls -la", "Expected command in output")
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
		require.NoError(t, err, "Failed to marshal")

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err, "Failed to unmarshal")

		output := registry.Render(unmarshaled)

		// Should contain todo content
		assert.Contains(t, output, "Task 1", "Expected todo content in output")
	})

	t.Run("BackgroundBashRenderer after JSON unmarshal", func(t *testing.T) {
		original := tools.StructuredToolResult{
			ToolName:  "bash_background",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BackgroundBashMetadata{
				Command:   "python -m http.server 8000",
				PID:       12345,
				LogPath:   "/tmp/.kodelet/12345/out.log",
				StartTime: time.Date(2023, 1, 1, 10, 30, 45, 0, time.UTC),
			},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err, "Failed to marshal")

		var unmarshaled tools.StructuredToolResult
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err, "Failed to unmarshal")

		output := registry.Render(unmarshaled)

		// Should contain background command details
		assert.Contains(t, output, "Background Command: python -m http.server 8000", "Expected background command in output")
		assert.Contains(t, output, "Process ID: 12345", "Expected process ID in output")
		assert.Contains(t, output, "Log File: /tmp/.kodelet/12345/out.log", "Expected log file path in output")
		assert.Contains(t, output, "Started: 2023-01-01 10:30:45", "Expected start time in output")
	})
}
