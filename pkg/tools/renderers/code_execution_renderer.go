package renderers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// CodeExecutionRenderer renders one parent card and bounded child summaries.
type CodeExecutionRenderer struct{}

// CodeExecutionSummary returns a compact label shared by CLI and TUI.
func CodeExecutionSummary(meta tools.CodeExecutionMetadata) string {
	completed, failed, running := 0, 0, 0
	for _, call := range meta.Calls {
		switch call.Status {
		case "completed":
			completed++
		case "queued", "running":
			running++
		default:
			failed++
		}
	}
	parts := []string{"Code execution"}
	for i, count := range []int{completed, failed, running} {
		if count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, []string{"succeeded", "failed", "running"}[i]))
		}
	}
	return strings.Join(parts, " · ")
}

// RenderCLI omits child inputs and result bodies, preserving selected output.
func (*CodeExecutionRenderer) RenderCLI(result tools.StructuredToolResult) string {
	var meta tools.CodeExecutionMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Code execution: " + result.Error
	}
	var output strings.Builder
	output.WriteString(CodeExecutionSummary(meta))
	fmt.Fprintf(&output, "\n%s · %d ms", meta.Status, meta.DurationMs)
	for _, call := range meta.Calls {
		fmt.Fprintf(&output, "\n  %s  %s · %d ms", call.Status, call.ToolName, call.DurationMs)
		if call.ErrorKind != "" {
			fmt.Fprintf(&output, " · %s", call.ErrorKind)
		}
	}
	if selected := CodeExecutionOutput(meta); selected != "" {
		output.WriteString("\n\n" + selected)
	}
	if result.Error != "" {
		output.WriteString("\n\nError: " + result.Error)
	}
	return output.String()
}

// CodeExecutionOutput renders selected values without duplicating the call summary.
func CodeExecutionOutput(meta tools.CodeExecutionMetadata) string {
	var output strings.Builder
	items := meta.Items
	if items == nil {
		for _, value := range meta.Outputs {
			items = append(items, tools.CodeExecutionOutput{Type: "json", Value: value})
		}
	}
	for _, item := range items {
		switch item.Type {
		case "json":
			var decoded any
			if json.Unmarshal(item.Value, &decoded) == nil {
				if text, ok := decoded.(string); ok {
					output.WriteString("\n\n" + text)
					continue
				}
			}
			output.WriteString("\n\n" + string(item.Value))
		case "image":
			fmt.Fprintf(&output, "\n\nImage sent to model: %s", item.ArtifactID)
			if item.Detail == "original" {
				output.WriteString(" (original detail)")
			}
		case "artifact":
			fmt.Fprintf(&output, "\n\nRetained artifact (not sent to model): %s", item.ArtifactID)
		}
	}
	return strings.TrimPrefix(output.String(), "\n\n")
}
