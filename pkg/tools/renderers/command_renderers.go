package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BashRenderer renders bash command results
type BashRenderer struct{}

func (r *BashRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.BashMetadata
	if !extractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for bash"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Command: %s\n", meta.Command))
	output.WriteString(fmt.Sprintf("Exit Code: %d\n", meta.ExitCode))

	if meta.WorkingDir != "" {
		output.WriteString(fmt.Sprintf("Working Directory: %s\n", meta.WorkingDir))
	}

	output.WriteString(fmt.Sprintf("Execution Time: %v\n", meta.ExecutionTime))

	if meta.Output != "" {
		output.WriteString("\nOutput:\n")
		output.WriteString(meta.Output)
	}

	return output.String()
}
