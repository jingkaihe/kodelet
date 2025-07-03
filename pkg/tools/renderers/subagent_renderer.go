package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentRenderer renders subagent results
type SubAgentRenderer struct{}

func (r *SubAgentRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	meta, ok := result.Metadata.(*tools.SubAgentMetadata)
	if !ok {
		return "Error: Invalid metadata type for subagent"
	}

	output := "Subagent Response:\n"
	if meta.Question != "" {
		output += fmt.Sprintf("Question: %s\n", meta.Question)
	}
	if meta.ModelStrength != "" {
		output += fmt.Sprintf("Model: %s\n", meta.ModelStrength)
	}
	output += fmt.Sprintf("\n%s", meta.Response)

	return output
}
