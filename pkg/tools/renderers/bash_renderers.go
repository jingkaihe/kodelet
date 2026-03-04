package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BashRenderer renders bash command results
type BashRenderer struct{}

// RenderCLI renders bash command execution results in CLI format, including command details,
// exit code, execution time, and output.
func (r *BashRenderer) RenderCLI(result tools.StructuredToolResult) string {
	var output strings.Builder
	if !result.Success {
		output.WriteString(fmt.Sprintf("Error: %s\n", result.Error))
	}

	// Try to extract regular BashMetadata first
	var bashMeta tools.BashMetadata
	if tools.ExtractMetadata(result.Metadata, &bashMeta) {
		return r.renderBashMetadata(bashMeta, &output)
	}

	return "Error: Invalid metadata type for bash"
}

func (r *BashRenderer) renderBashMetadata(meta tools.BashMetadata, output *strings.Builder) string {
	fmt.Fprintf(output, "Command: %s\n", meta.Command)
	fmt.Fprintf(output, "Exit Code: %d\n", meta.ExitCode)

	if meta.WorkingDir != "" {
		fmt.Fprintf(output, "Working Directory: %s\n", meta.WorkingDir)
	}

	fmt.Fprintf(output, "Execution Time: %v\n", meta.ExecutionTime)

	if meta.Output != "" {
		output.WriteString("\nOutput:\n")
		output.WriteString(meta.Output)
	}

	return output.String()
}
