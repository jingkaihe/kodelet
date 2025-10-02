package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// CustomToolRenderer renders custom tool results
type CustomToolRenderer struct{}

// RenderCLI renders custom tool execution results in CLI format, including the tool name,
// execution time, and output.
func (r *CustomToolRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.CustomToolMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for custom tool"
	}

	var output strings.Builder

	// Extract tool name without prefix for display
	toolDisplayName := strings.TrimPrefix(result.ToolName, "custom_tool_")
	output.WriteString(fmt.Sprintf("Custom Tool: %s", toolDisplayName))

	if meta.ExecutionTime > 0 {
		output.WriteString(fmt.Sprintf(" (executed in %v)", meta.ExecutionTime))
	}
	output.WriteString("\n")

	// Add the tool output
	if meta.Output != "" {
		output.WriteString("\n")
		output.WriteString(meta.Output)
	}

	return output.String()
}
