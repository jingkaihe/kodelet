package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ExtensionToolRenderer renders extension tool results.
type ExtensionToolRenderer struct{}

// RenderCLI renders extension tool execution results in CLI format.
func (r *ExtensionToolRenderer) RenderCLI(result tools.StructuredToolResult) string {
	var meta tools.ExtensionToolMetadata
	metadataOK := tools.ExtractMetadata(result.Metadata, &meta)
	if presentation, _, ok := tools.ExtractExtensionToolPresentation(&result); ok {
		var output strings.Builder
		output.WriteString(presentation.Summary)
		if !result.Success && strings.TrimSpace(result.Error) != "" {
			fmt.Fprintf(&output, "\n\nError: %s", result.Error)
		}
		body := presentation.Body
		if !presentation.HasBody() {
			body = meta.Output
		}
		if strings.TrimSpace(body) != "" && (result.Success || strings.TrimSpace(body) != strings.TrimSpace(result.Error)) {
			output.WriteString("\n\n")
			output.WriteString(body)
		}
		return output.String()
	}
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}
	if !metadataOK {
		return "Error: Invalid metadata type for extension tool"
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Extension Tool: %s", meta.ToolName)
	if meta.ExtensionID != "" {
		fmt.Fprintf(&output, " (%s)", meta.ExtensionID)
	}
	if meta.ExecutionTime > 0 {
		fmt.Fprintf(&output, " (executed in %v)", meta.ExecutionTime)
	}
	output.WriteString("\n")
	if meta.Output != "" {
		output.WriteString("\n")
		output.WriteString(meta.Output)
	}
	return output.String()
}
