package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BashRenderer renders bash command results
type BashRenderer struct{}

func (r *BashRenderer) RenderCLI(result tools.StructuredToolResult) string {
	var output strings.Builder
	if !result.Success {
		output.WriteString(fmt.Sprintf("Error: %s", result.Error))
	}

	// Try to extract regular BashMetadata first
	var bashMeta tools.BashMetadata
	if tools.ExtractMetadata(result.Metadata, &bashMeta) {
		return r.renderBashMetadata(bashMeta, &output)
	}

	// Try to extract BackgroundBashMetadata
	var bgBashMeta tools.BackgroundBashMetadata
	if tools.ExtractMetadata(result.Metadata, &bgBashMeta) {
		return r.renderBackgroundBashMetadata(bgBashMeta, &output)
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

func (r *BashRenderer) renderBackgroundBashMetadata(meta tools.BackgroundBashMetadata, output *strings.Builder) string {
	fmt.Fprintf(output, "Background Command: %s\n", meta.Command)
	fmt.Fprintf(output, "Process ID: %d\n", meta.PID)
	fmt.Fprintf(output, "Log File: %s\n", meta.LogPath)
	fmt.Fprintf(output, "Started: %s\n", meta.StartTime.Format("2006-01-02 15:04:05"))
	output.WriteString("\nThe process is running in the background. Check the log file for output.")

	return output.String()
}
