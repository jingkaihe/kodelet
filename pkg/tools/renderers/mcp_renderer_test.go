package renderers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestMCPToolRenderer(t *testing.T) {
	renderer := &MCPToolRenderer{}

	t.Run("Successful MCP tool with parameters and content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_definition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.MCPToolMetadata{
				MCPToolName: "mcp_definition",
				ServerName:  "language-server",
				Parameters: map[string]any{
					"symbolName": "main.go:TestFunction",
					"project":    "kodelet",
				},
				Content: []tools.MCPContent{
					{
						Type: "text",
						Text: "func TestFunction(t *testing.T) {\n\t// Test implementation\n}",
					},
				},
				ContentText:   "func TestFunction(t *testing.T) {\n\t// Test implementation\n}",
				ExecutionTime: 100 * time.Millisecond,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "MCP Tool: mcp_definition", "Expected MCP tool name in output")
		assert.Contains(t, output, "(server: language-server)", "Expected server name in output")
		assert.Contains(t, output, "Parameters:", "Expected parameters section in output")
		assert.Contains(t, output, "symbolName: main.go:TestFunction", "Expected symbolName parameter in output")
		assert.Contains(t, output, "project: kodelet", "Expected project parameter in output")
		assert.Contains(t, output, "Content:", "Expected content section in output")
		assert.Contains(t, output, "func TestFunction(t *testing.T) {", "Expected function content in output")
		assert.Contains(t, output, "Execution time: 100ms", "Expected execution time in output")
	})

	t.Run("MCP tool with multiple content types", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_hover",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.MCPToolMetadata{
				MCPToolName: "mcp_hover",
				ServerName:  "language-server",
				Parameters: map[string]any{
					"line":   42,
					"column": 10,
				},
				Content: []tools.MCPContent{
					{
						Type: "text",
						Text: "Variable: counter",
					},
					{
						Type:     "image",
						MimeType: "image/png",
						Data:     "fake image data",
					},
					{
						Type:     "resource",
						URI:      "file:///home/user/docs/counter.md",
						MimeType: "text/markdown",
					},
					{
						Type: "code",
						Text: "var counter int = 0",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "MCP Tool: mcp_hover", "Expected MCP tool name in output")
		assert.Contains(t, output, "line: 42", "Expected line parameter in output")
		assert.Contains(t, output, "column: 10", "Expected column parameter in output")
		assert.Contains(t, output, "Variable: counter", "Expected text content in output")
		assert.Contains(t, output, "[Image: image/png, size: 15 bytes]", "Expected image content description in output")
		assert.Contains(t, output, "[Resource: file:///home/user/docs/counter.md (text/markdown)]", "Expected resource content description in output")
		assert.Contains(t, output, "[code content]: var counter int = 0", "Expected code content in output")
	})

	t.Run("MCP tool with fallback text content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_references",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.MCPToolMetadata{
				MCPToolName: "mcp_references",
				ServerName:  "language-server",
				Parameters: map[string]any{
					"symbolName": "TestFunction",
				},
				ContentText: "Found 3 references:\n1. main.go:10\n2. utils.go:25\n3. test.go:42",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "MCP Tool: mcp_references", "Expected MCP tool name in output")
		assert.Contains(t, output, "symbolName: TestFunction", "Expected symbolName parameter in output")
		assert.Contains(t, output, "Found 3 references:", "Expected fallback content text in output")
		assert.Contains(t, output, "1. main.go:10", "Expected reference line in output")
	})

	t.Run("MCP tool minimal metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_diagnostics",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.MCPToolMetadata{
				MCPToolName: "mcp_diagnostics",
				Content: []tools.MCPContent{
					{
						Type: "text",
						Text: "No issues found",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "MCP Tool: mcp_diagnostics", "Expected MCP tool name in output")
		assert.NotContains(t, output, "server:", "Should not show server when not set")
		assert.NotContains(t, output, "Parameters:", "Should not show parameters when empty")
		assert.Contains(t, output, "No issues found", "Expected content text in output")
		assert.NotContains(t, output, "Execution time:", "Should not show execution time when zero")
	})

	t.Run("MCP tool with no content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_rename_symbol",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.MCPToolMetadata{
				MCPToolName: "mcp_rename_symbol",
				ServerName:  "language-server",
				Parameters: map[string]any{
					"oldName": "oldFunction",
					"newName": "newFunction",
				},
				ExecutionTime: 50 * time.Millisecond,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "MCP Tool: mcp_rename_symbol", "Expected MCP tool name in output")
		assert.Contains(t, output, "oldName: oldFunction", "Expected oldName parameter in output")
		assert.Contains(t, output, "newName: newFunction", "Expected newName parameter in output")
		assert.NotContains(t, output, "Content:", "Should not show content section when no content")
		assert.Contains(t, output, "Execution time: 50ms", "Expected execution time in output")
	})

	t.Run("MCP tool error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_definition",
			Success:   false,
			Error:     "Symbol not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Symbol not found", output, "Expected error message")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_definition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GrepMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Invalid metadata type for MCP tool", output, "Expected invalid metadata error")
	})
}
