package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Background Processes:") {
			t.Errorf("Expected background processes header in output, got: %s", output)
		}
		if !strings.Contains(output, "PID") {
			t.Errorf("Expected PID column header in output, got: %s", output)
		}
		if !strings.Contains(output, "Status") {
			t.Errorf("Expected Status column header in output, got: %s", output)
		}
		if !strings.Contains(output, "Start Time") {
			t.Errorf("Expected Start Time column header in output, got: %s", output)
		}
		if !strings.Contains(output, "Log Path") {
			t.Errorf("Expected Log Path column header in output, got: %s", output)
		}
		if !strings.Contains(output, "Command") {
			t.Errorf("Expected Command column header in output, got: %s", output)
		}
		if !strings.Contains(output, "12345") {
			t.Errorf("Expected first PID in output, got: %s", output)
		}
		if !strings.Contains(output, "running") {
			t.Errorf("Expected running status in output, got: %s", output)
		}
		if !strings.Contains(output, "python server.py") {
			t.Errorf("Expected first command in output, got: %s", output)
		}
		if !strings.Contains(output, "npm start") {
			t.Errorf("Expected second command in output, got: %s", output)
		}
		if !strings.Contains(output, "go run main.go") {
			t.Errorf("Expected third command in output, got: %s", output)
		}
		if !strings.Contains(output, "/tmp/log1.log") {
			t.Errorf("Expected first log path in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Background Processes:") {
			t.Errorf("Expected background processes header in output, got: %s", output)
		}
		if !strings.Contains(output, "9999") {
			t.Errorf("Expected PID in output, got: %s", output)
		}
		if !strings.Contains(output, "./my-service --port=8080") {
			t.Errorf("Expected command in output, got: %s", output)
		}
		if !strings.Contains(output, "/var/log/service.log") {
			t.Errorf("Expected log path in output, got: %s", output)
		}
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

		if !strings.Contains(output, "No background processes running.") {
			t.Errorf("Expected no processes message in output, got: %s", output)
		}
		if strings.Contains(output, "Background Processes:") {
			t.Errorf("Should not show table header when no processes, got: %s", output)
		}
		if strings.Contains(output, "PID") {
			t.Errorf("Should not show column headers when no processes, got: %s", output)
		}
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

		if !strings.Contains(output, "1001") {
			t.Errorf("Expected first PID in output, got: %s", output)
		}
		if !strings.Contains(output, "running") {
			t.Errorf("Expected running status in output, got: %s", output)
		}
		if !strings.Contains(output, "stopped") {
			t.Errorf("Expected stopped status in output, got: %s", output)
		}
		if !strings.Contains(output, "failed") {
			t.Errorf("Expected failed status in output, got: %s", output)
		}
		if !strings.Contains(output, "pending") {
			t.Errorf("Expected pending status in output, got: %s", output)
		}
		if !strings.Contains(output, "app1") {
			t.Errorf("Expected first command in output, got: %s", output)
		}
		if !strings.Contains(output, "app2 --config=prod") {
			t.Errorf("Expected second command with args in output, got: %s", output)
		}
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   false,
			Error:     "Failed to retrieve background processes",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Failed to retrieve background processes") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for view_background_processes") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "view_background_processes",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for view_background_processes") {
			t.Errorf("Expected invalid metadata error for nil metadata, got: %s", output)
		}
	})
}
