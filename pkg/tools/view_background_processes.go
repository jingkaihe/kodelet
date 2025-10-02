package tools

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

// ViewBackgroundProcessesToolResult represents the result of viewing background processes
type ViewBackgroundProcessesToolResult struct {
	processes []tooltypes.BackgroundProcess
	statuses  []string
	err       string
}

// GetResult returns the formatted list of background processes
func (r *ViewBackgroundProcessesToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	return r.formatProcesses()
}

// GetError returns the error message
func (r *ViewBackgroundProcessesToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *ViewBackgroundProcessesToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *ViewBackgroundProcessesToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.GetResult(), r.GetError())
}

func (r *ViewBackgroundProcessesToolResult) formatProcesses() string {
	if len(r.processes) == 0 {
		return "No background processes running."
	}

	formatted := "Background Processes:\n"
	formatted += fmt.Sprintf("%-8s %-10s %-15s %-20s %s\n", "PID", "Status", "Start Time", "Log Path", "Command")
	formatted += fmt.Sprintf("%-8s %-10s %-15s %-20s %s\n", "---", "------", "----------", "--------", "-------")

	for i, process := range r.processes {
		status := r.statuses[i]
		startTime := process.StartTime.Format("15:04:05")
		formatted += fmt.Sprintf("%-8d %-10s %-15s %-20s %s\n",
			process.PID, status, startTime, process.LogPath, process.Command)
	}

	return formatted
}

// ViewBackgroundProcessesTool provides functionality to view background processes
type ViewBackgroundProcessesTool struct{}

// ViewBackgroundProcessesInput defines the input parameters for the tool
type ViewBackgroundProcessesInput struct{}

// Name returns the name of the tool
func (t *ViewBackgroundProcessesTool) Name() string {
	return "view_background_processes"
}

// Description returns the description of the tool
func (t *ViewBackgroundProcessesTool) Description() string {
	return `View all background processes currently tracked by the system.

This tool lists all background processes that have been started, showing their PID, 
current status (running/stopped), start time, log file path, and the command that was executed.

# Use Cases
* Check which background processes are currently running
* Monitor the status of previously started background tasks
* Get process information for debugging or management purposes
* Review log file locations for background processes
`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *ViewBackgroundProcessesTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ViewBackgroundProcessesInput]()
}

// ValidateInput validates the input parameters for the tool
func (t *ViewBackgroundProcessesTool) ValidateInput(_ tooltypes.State, _ string) error {
	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *ViewBackgroundProcessesTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	return []attribute.KeyValue{
		attribute.String("tool.name", "view_background_processes"),
	}, nil
}

// Execute retrieves and formats the list of background processes
func (t *ViewBackgroundProcessesTool) Execute(_ context.Context, state tooltypes.State, _ string) tooltypes.ToolResult {
	processes := state.GetBackgroundProcesses()
	statuses := make([]string, len(processes))

	for i, process := range processes {
		statuses[i] = getProcessStatus(process)
	}

	return &ViewBackgroundProcessesToolResult{
		processes: processes,
		statuses:  statuses,
	}
}

// getProcessStatus checks if a process is still running
func getProcessStatus(process tooltypes.BackgroundProcess) string {
	if process.Process == nil {
		return "unknown"
	}

	// Try to send signal 0 to check if process exists
	err := process.Process.Signal(syscall.Signal(0))
	if err != nil {
		// Check if it's a permission error vs process not found
		if os.IsPermission(err) {
			return "running"
		}
		return "stopped"
	}

	return "running"
}

// StructuredData returns structured metadata about the background processes
func (r *ViewBackgroundProcessesToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "view_background_processes",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Convert background processes to structured format
	processes := make([]tooltypes.BackgroundProcessInfo, 0, len(r.processes))
	for i, process := range r.processes {
		status := "unknown"
		if i < len(r.statuses) {
			status = r.statuses[i]
		}

		processes = append(processes, tooltypes.BackgroundProcessInfo{
			PID:       process.PID,
			Command:   process.Command,
			LogPath:   process.LogPath,
			StartTime: process.StartTime,
			Status:    status,
		})
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.ViewBackgroundProcessesMetadata{
		Processes: processes,
		Count:     len(processes),
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
