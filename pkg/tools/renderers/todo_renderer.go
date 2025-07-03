package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// TodoRenderer renders todo list results
type TodoRenderer struct{}

func (r *TodoRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.TodoMetadata)
	if !ok {
		return "Error: Invalid metadata type for todo"
	}

	var output strings.Builder

	if meta.Action == "read" {
		output.WriteString("Todo List:\n")
	} else {
		output.WriteString("Todo List Updated:\n")
	}

	// Show statistics if available
	if meta.Statistics.Total > 0 {
		output.WriteString(fmt.Sprintf("\nTotal: %d | Completed: %d | In Progress: %d | Pending: %d\n\n",
			meta.Statistics.Total,
			meta.Statistics.Completed,
			meta.Statistics.InProgress,
			meta.Statistics.Pending))
	}

	// Show todo items
	for _, item := range meta.TodoList {
		statusIcon := "○"
		switch item.Status {
		case "completed":
			statusIcon = "✓"
		case "in_progress":
			statusIcon = "→"
		}

		output.WriteString(fmt.Sprintf("%s [%s] %s - %s\n",
			statusIcon, item.Priority, item.ID, item.Content))
	}

	return output.String()
}
