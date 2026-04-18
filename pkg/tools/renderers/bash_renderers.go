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
		fmt.Fprintf(&output, "Error: %s\n", result.Error)
	}

	// Try to extract regular BashMetadata first
	var bashMeta tools.BashMetadata
	if tools.ExtractMetadata(result.Metadata, &bashMeta) {
		return r.renderBashMetadata(bashMeta, &output)
	}

	return "Error: Invalid metadata type for bash"
}

// RenderMarkdown renders bash results in markdown format.
func (r *BashRenderer) RenderMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, true)
}

// RenderToolUseMarkdown renders bash invocation inputs in markdown format.
func (r *BashRenderer) RenderToolUseMarkdown(rawInput string) string {
	var input tools.BashInput
	if !decodeToolInput(rawInput, &input) {
		return ""
	}

	var output strings.Builder
	if input.Description != "" {
		fmt.Fprintf(&output, "- **Description:** %s\n", sanitizeMarkdownText(input.Description))
	}
	if input.Timeout > 0 {
		fmt.Fprintf(&output, "- **Timeout:** %d seconds\n", input.Timeout)
	}

	output.WriteString("\n**Command**\n\n")
	output.WriteString(fencedCodeBlock("bash", input.Command))

	return strings.TrimSpace(output.String())
}

// RenderMergedMarkdown renders bash results for the merged tool-call view.
func (r *BashRenderer) RenderMergedMarkdown(result tools.StructuredToolResult) string {
	return r.renderMarkdown(result, false)
}

func (r *BashRenderer) renderMarkdown(result tools.StructuredToolResult, includeCommand bool) string {
	var meta tools.BashMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return renderMarkdownFromCLI(result, r.RenderCLI(result))
	}

	var output strings.Builder
	status := "success"
	if !result.Success {
		status = "failed"
	}

	fmt.Fprintf(&output, "- **Status:** %s\n", status)
	fmt.Fprintf(&output, "- **Exit code:** %d\n", meta.ExitCode)
	if meta.WorkingDir != "" {
		fmt.Fprintf(&output, "- **Working directory:** %s\n", inlineCode(meta.WorkingDir))
	}
	fmt.Fprintf(&output, "- **Execution time:** %s\n", inlineCode(meta.ExecutionTime.String()))
	if result.Error != "" {
		fmt.Fprintf(&output, "- **Error:** %s\n", inlineCode(result.Error))
	}

	if includeCommand {
		output.WriteString("\n**Command**\n\n")
		output.WriteString(fencedCodeBlock("bash", meta.Command))
	}

	if strings.TrimSpace(meta.Output) != "" {
		if includeCommand {
			output.WriteString("\n\n**Output**\n\n")
		} else {
			output.WriteString("\n**Output**\n\n")
		}
		output.WriteString(fencedCodeBlock("text", meta.Output))
	}

	return strings.TrimSpace(output.String())
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
