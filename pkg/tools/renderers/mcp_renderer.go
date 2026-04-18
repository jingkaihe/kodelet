package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// MCPToolRenderer renders MCP tool results
type MCPToolRenderer struct{}

// RenderCLI renders MCP tool execution results in CLI format, including the tool name,
// server name, parameters, structured content, and execution time.
func (r *MCPToolRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.MCPToolMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for MCP tool"
	}

	var output strings.Builder
	fmt.Fprintf(&output, "MCP Tool: %s", meta.MCPToolName)

	if meta.ServerName != "" {
		fmt.Fprintf(&output, " (server: %s)", meta.ServerName)
	}
	output.WriteString("\n")

	// Show parameters if present
	if len(meta.Parameters) > 0 {
		output.WriteString("\nParameters:\n")
		for k, v := range meta.Parameters {
			fmt.Fprintf(&output, "  %s: %v\n", k, v)
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
				fmt.Fprintf(&output, "[Image: %s, size: %d bytes]",
					content.MimeType, len(content.Data))
			case "resource":
				fmt.Fprintf(&output, "[Resource: %s (%s)]",
					content.URI, content.MimeType)
			default:
				fmt.Fprintf(&output, "[%s content]", content.Type)
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
		fmt.Fprintf(&output, "\n\nExecution time: %v", meta.ExecutionTime)
	}

	return output.String()
}
