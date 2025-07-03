package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ViewBackgroundProcessesRenderer renders background process list
type ViewBackgroundProcessesRenderer struct{}

func (r *ViewBackgroundProcessesRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.ViewBackgroundProcessesMetadata)
	if !ok {
		return "Error: Invalid metadata type for view_background_processes"
	}

	if meta.Count == 0 {
		return "No background processes running."
	}

	var output strings.Builder
	output.WriteString("Background Processes:\n")
	output.WriteString(fmt.Sprintf("%-8s %-10s %-15s %-20s %s\n", "PID", "Status", "Start Time", "Log Path", "Command"))
	output.WriteString(fmt.Sprintf("%-8s %-10s %-15s %-20s %s\n", "---", "------", "----------", "--------", "-------"))

	for _, process := range meta.Processes {
		startTime := process.StartTime.Format("15:04:05")
		output.WriteString(fmt.Sprintf("%-8d %-10s %-15s %-20s %s\n",
			process.PID, process.Status, startTime, process.LogPath, process.Command))
	}

	return output.String()
}
