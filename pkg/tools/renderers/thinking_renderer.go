package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ThinkingRenderer renders thinking results
type ThinkingRenderer struct{}

func (r *ThinkingRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.ThinkingMetadata
	if !extractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for thinking"
	}

	var output strings.Builder
	output.WriteString("Thinking")

	if meta.Category != "" {
		output.WriteString(fmt.Sprintf(" [%s]", meta.Category))
	}

	output.WriteString(":\n")
	output.WriteString(meta.Thought)

	return output.String()
}
