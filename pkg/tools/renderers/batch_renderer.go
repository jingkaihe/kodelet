package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BatchRenderer renders batch tool results
type BatchRenderer struct {
	registry *RendererRegistry
}

func (r *BatchRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.BatchMetadata
	if !extractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for batch"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Batch Execution: %s\n", meta.Description))
	output.WriteString(fmt.Sprintf("Success: %d | Failed: %d | Time: %v\n\n",
		meta.SuccessCount, meta.FailureCount, meta.ExecutionTime))

	// Initialize registry if needed
	if r.registry == nil {
		r.registry = NewRendererRegistry()
	}

	// Render each sub-result
	for i, subResult := range meta.SubResults {
		if i > 0 {
			output.WriteString("\n" + strings.Repeat("-", 40) + "\n\n")
		}
		output.WriteString(fmt.Sprintf("Task %d: %s\n", i+1, subResult.ToolName))
		output.WriteString(r.registry.Render(subResult))
	}

	return output.String()
}
