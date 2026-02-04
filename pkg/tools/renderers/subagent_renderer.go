package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentRenderer renders subagent results
type SubAgentRenderer struct{}

// RenderCLI renders subagent execution results in CLI format, showing the question
// (if available) and the response.
func (r *SubAgentRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.SubAgentMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for subagent"
	}

	output := "Subagent Response:\n"
	if meta.Workflow != "" {
		output += fmt.Sprintf("Workflow: %s\n", meta.Workflow)
	}
	if meta.Cwd != "" {
		output += fmt.Sprintf("Directory: %s\n", meta.Cwd)
	}
	if meta.Question != "" {
		output += fmt.Sprintf("Question: %s\n", meta.Question)
	}
	output += fmt.Sprintf("\n%s", meta.Response)

	return output
}
