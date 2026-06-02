package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ExtensionToolRenderer renders extension tool results.
type ExtensionToolRenderer struct{}

// RenderCLI renders extension tool execution results in CLI format.
func (r *ExtensionToolRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ExtensionToolMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for extension tool"
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Extension Tool: %s", meta.ToolName)
	if meta.ExtensionID != "" {
		fmt.Fprintf(&output, " (%s)", meta.ExtensionID)
	}
	if meta.ExecutionTime > 0 {
		fmt.Fprintf(&output, " (executed in %v)", meta.ExecutionTime)
	}
	output.WriteString("\n")
	if meta.Output != "" {
		output.WriteString("\n")
		output.WriteString(meta.Output)
	}
	return output.String()
}
