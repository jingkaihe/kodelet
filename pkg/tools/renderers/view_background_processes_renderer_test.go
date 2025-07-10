package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestViewBackgroundProcessesRenderer(t *testing.T) {
	renderer := &ViewBackgroundProcessesRenderer{}

	t.Run("Background processes with multiple entries", func(t *testing.T) {
		testTime := time.Date(2023, 12, 1, 14, 30, 0, 0, time.UTC)

		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ViewBackgroundProcessesMetadata{
				Count: 3,
				Processes: []tools.BackgroundProcessInfo{
					{
						PID:       12345,
						Status:    "running",
						StartTime: testTime,
						LogPath:   "/tmp/log1.log",
						Command:   "python server.py",
					},
					{
						PID:       12346,
						Status:    "stopped",
						StartTime: testTime.Add(5 * time.Minute),
						LogPath:   "/tmp/log2.log",
						Command:   "npm start",
					},
					{
						PID:       12347,
						Status:    "running",
						StartTime: testTime.Add(10 * time.Minute),
						LogPath:   "/tmp/log3.log",
						Command:   "go run main.go",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Background Processes:")
		assert.Contains(t, output, "PID")
		assert.Contains(t, output, "Status")
		assert.Contains(t, output, "Start Time")
		assert.Contains(t, output, "Log Path")
		assert.Contains(t, output, "Command")
		assert.Contains(t, output, "12345")
		assert.Contains(t, output, "running")
		assert.Contains(t, output, "python server.py")
		assert.Contains(t, output, "npm start")
		assert.Contains(t, output, "go run main.go")
		assert.Contains(t, output, "/tmp/log1.log")
	})

	t.Run("Single background process", func(t *testing.T) {
		testTime := time.Date(2023, 12, 1, 10, 0, 0, 0, time.UTC)

		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ViewBackgroundProcessesMetadata{
				Count: 1,
				Processes: []tools.BackgroundProcessInfo{
					{
						PID:       9999,
						Status:    "running",
						StartTime: testTime,
						LogPath:   "/var/log/service.log",
						Command:   "./my-service --port=8080",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Background Processes:")
		assert.Contains(t, output, "9999")
		assert.Contains(t, output, "./my-service --port=8080")
		assert.Contains(t, output, "/var/log/service.log")
	})

	t.Run("No background processes", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ViewBackgroundProcessesMetadata{
				Count:     0,
				Processes: []tools.BackgroundProcessInfo{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "No background processes running.")
		assert.NotContains(t, output, "Background Processes:")
		assert.NotContains(t, output, "PID")
	})

	t.Run("Processes with different statuses", func(t *testing.T) {
		testTime := time.Date(2023, 12, 1, 12, 0, 0, 0, time.UTC)

		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ViewBackgroundProcessesMetadata{
				Count: 4,
				Processes: []tools.BackgroundProcessInfo{
					{
						PID:       1001,
						Status:    "running",
						StartTime: testTime,
						LogPath:   "/logs/app1.log",
						Command:   "app1",
					},
					{
						PID:       1002,
						Status:    "stopped",
						StartTime: testTime.Add(time.Hour),
						LogPath:   "/logs/app2.log",
						Command:   "app2 --config=prod",
					},
					{
						PID:       1003,
						Status:    "failed",
						StartTime: testTime.Add(2 * time.Hour),
						LogPath:   "/logs/app3.log",
						Command:   "app3 --verbose",
					},
					{
						PID:       1004,
						Status:    "pending",
						StartTime: testTime.Add(3 * time.Hour),
						LogPath:   "/logs/app4.log",
						Command:   "app4 --daemon",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "1001")
		assert.Contains(t, output, "running")
		assert.Contains(t, output, "stopped")
		assert.Contains(t, output, "failed")
		assert.Contains(t, output, "pending")
		assert.Contains(t, output, "app1")
		assert.Contains(t, output, "app2 --config=prod")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   false,
			Error:     "Failed to retrieve background processes",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Failed to retrieve background processes")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for view_background_processes")
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for view_background_processes")
	})
}
