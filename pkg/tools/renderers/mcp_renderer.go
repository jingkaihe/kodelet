package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// MCPToolRenderer renders MCP tool results
type MCPToolRenderer struct{}

func (r *MCPToolRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.MCPToolMetadata
	if !extractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for MCP tool"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("MCP Tool: %s", meta.MCPToolName))

	if meta.ServerName != "" {
		output.WriteString(fmt.Sprintf(" (server: %s)", meta.ServerName))
	}
	output.WriteString("\n")

	// Show parameters if present
	if len(meta.Parameters) > 0 {
		output.WriteString("\nParameters:\n")
		for k, v := range meta.Parameters {
			output.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
	}

	// Render structured content
	if len(meta.Content) > 0 {
		output.WriteString("\nContent:\n")
		for i, content := range meta.Content {
			if i > 0 {
				output.WriteString("\n")
			}

			switch content.Type {
			case "text":
				output.WriteString(content.Text)
			case "image":
				output.WriteString(fmt.Sprintf("[Image: %s, size: %d bytes]",
					content.MimeType, len(content.Data)))
			case "resource":
				output.WriteString(fmt.Sprintf("[Resource: %s (%s)]",
					content.URI, content.MimeType))
			default:
				output.WriteString(fmt.Sprintf("[%s content]", content.Type))
				if content.Text != "" {
					output.WriteString(": ")
					output.WriteString(content.Text)
				}
			}
		}
	} else if meta.ContentText != "" {
		// Fallback to concatenated text content
		output.WriteString("\n")
		output.WriteString(meta.ContentText)
	}

	if meta.ExecutionTime > 0 {
		output.WriteString(fmt.Sprintf("\n\nExecution time: %v", meta.ExecutionTime))
	}

	return output.String()
}
