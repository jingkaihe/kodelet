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

type ViewBackgroundProcessesToolResult struct {
	processes []tooltypes.BackgroundProcess
	statuses  []string
	err       string
}

func (r *ViewBackgroundProcessesToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	return r.formatProcesses()
}

func (r *ViewBackgroundProcessesToolResult) GetError() string {
	return r.err
}

func (r *ViewBackgroundProcessesToolResult) IsError() bool {
	return r.err != ""
}

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

type ViewBackgroundProcessesTool struct{}

type ViewBackgroundProcessesInput struct{}

func (t *ViewBackgroundProcessesTool) Name() string {
	return "view_background_processes"
}

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

func (t *ViewBackgroundProcessesTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ViewBackgroundProcessesInput]()
}

func (t *ViewBackgroundProcessesTool) ValidateInput(state tooltypes.State, parameters string) error {
	return nil
}

func (t *ViewBackgroundProcessesTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	return []attribute.KeyValue{
		attribute.String("tool.name", "view_background_processes"),
	}, nil
}

func (t *ViewBackgroundProcessesTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
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

func (r *ViewBackgroundProcessesToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "view_background_processes",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
		return result
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

	result.Metadata = &tooltypes.ViewBackgroundProcessesMetadata{
		Processes: processes,
		Count:     len(processes),
	}

	return result
}
