package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// CodeExecutionRenderer renders code execution results
type CodeExecutionRenderer struct{}

// RenderCLI renders code execution results in CLI format, showing the runtime,
// code executed, and the output produced.
func (r *CodeExecutionRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.CodeExecutionMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for code_execution"
	}

	var output strings.Builder

	if meta.Runtime != "" {
		fmt.Fprintf(&output, "Runtime: %s\n", meta.Runtime)
	}

	if meta.Code != "" {
		output.WriteString("\nCode:\n")
		output.WriteString(meta.Code)
		output.WriteString("\n")
	}

	if meta.Output != "" {
		output.WriteString("\nOutput:\n")
		output.WriteString(meta.Output)
	}

	return output.String()
}
