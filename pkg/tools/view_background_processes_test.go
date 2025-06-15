package tools

import (
	"context"
	"os"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestViewBackgroundProcessesTool_Name(t *testing.T) {
	tool := &ViewBackgroundProcessesTool{}
	assert.Equal(t, "view_background_processes", tool.Name())
}

func TestViewBackgroundProcessesTool_Description(t *testing.T) {
	tool := &ViewBackgroundProcessesTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "View all background processes")
	assert.Contains(t, desc, "current status")
}

func TestViewBackgroundProcessesTool_ValidateInput(t *testing.T) {
	tool := &ViewBackgroundProcessesTool{}
	state := NewBasicState(context.Background())

	err := tool.ValidateInput(state, "{}")
	assert.NoError(t, err)
}

func TestViewBackgroundProcessesTool_Execute_NoProcesses(t *testing.T) {
	tool := &ViewBackgroundProcessesTool{}
	state := NewBasicState(context.Background())

	result := tool.Execute(context.Background(), state, "{}")

	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "No background processes running")
}

func TestViewBackgroundProcessesTool_Execute_WithProcesses(t *testing.T) {
	tool := &ViewBackgroundProcessesTool{}
	state := NewBasicState(context.Background())

	// Add a mock background process
	mockProcess := tooltypes.BackgroundProcess{
		PID:       1234,
		Command:   "test command",
		LogPath:   "/tmp/test.log",
		StartTime: time.Now(),
		Process:   &os.Process{Pid: 1234}, // Mock process
	}

	err := state.AddBackgroundProcess(mockProcess)
	assert.NoError(t, err)

	result := tool.Execute(context.Background(), state, "{}")

	assert.False(t, result.IsError())
	output := result.GetResult()
	assert.Contains(t, output, "Background Processes:")
	assert.Contains(t, output, "1234")
	assert.Contains(t, output, "test command")
	assert.Contains(t, output, "/tmp/test.log")
}

func TestViewBackgroundProcessesToolResult_FormatProcesses(t *testing.T) {
	processes := []tooltypes.BackgroundProcess{
		{
			PID:       1234,
			Command:   "long command that might wrap",
			LogPath:   "/tmp/test.log",
			StartTime: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	statuses := []string{"running"}

	result := &ViewBackgroundProcessesToolResult{
		processes: processes,
		statuses:  statuses,
	}

	formatted := result.formatProcesses()
	assert.Contains(t, formatted, "Background Processes:")
	assert.Contains(t, formatted, "PID")
	assert.Contains(t, formatted, "Status")
	assert.Contains(t, formatted, "1234")
	assert.Contains(t, formatted, "running")
	assert.Contains(t, formatted, "12:00:00")
}

func TestGetProcessStatus(t *testing.T) {
	// Test with nil process
	process := tooltypes.BackgroundProcess{
		PID:     1234,
		Process: nil,
	}
	status := getProcessStatus(process)
	assert.Equal(t, "unknown", status)

	// Test with valid process (current process should be running)
	currentProcess, err := os.FindProcess(os.Getpid())
	assert.NoError(t, err)

	process = tooltypes.BackgroundProcess{
		PID:     os.Getpid(),
		Process: currentProcess,
	}
	status = getProcessStatus(process)
	assert.Equal(t, "running", status)
}
