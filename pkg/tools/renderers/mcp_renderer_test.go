package renderers

import (
	"strings"
	"testing"
	"time"

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

		if !strings.Contains(output, "MCP Tool: mcp_definition") {
			t.Errorf("Expected MCP tool name in output, got: %s", output)
		}
		if !strings.Contains(output, "(server: language-server)") {
			t.Errorf("Expected server name in output, got: %s", output)
		}
		if !strings.Contains(output, "Parameters:") {
			t.Errorf("Expected parameters section in output, got: %s", output)
		}
		if !strings.Contains(output, "symbolName: main.go:TestFunction") {
			t.Errorf("Expected symbolName parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "project: kodelet") {
			t.Errorf("Expected project parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "Content:") {
			t.Errorf("Expected content section in output, got: %s", output)
		}
		if !strings.Contains(output, "func TestFunction(t *testing.T) {") {
			t.Errorf("Expected function content in output, got: %s", output)
		}
		if !strings.Contains(output, "Execution time: 100ms") {
			t.Errorf("Expected execution time in output, got: %s", output)
		}
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

		if !strings.Contains(output, "MCP Tool: mcp_hover") {
			t.Errorf("Expected MCP tool name in output, got: %s", output)
		}
		if !strings.Contains(output, "line: 42") {
			t.Errorf("Expected line parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "column: 10") {
			t.Errorf("Expected column parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "Variable: counter") {
			t.Errorf("Expected text content in output, got: %s", output)
		}
		if !strings.Contains(output, "[Image: image/png, size: 15 bytes]") {
			t.Errorf("Expected image content description in output, got: %s", output)
		}
		if !strings.Contains(output, "[Resource: file:///home/user/docs/counter.md (text/markdown)]") {
			t.Errorf("Expected resource content description in output, got: %s", output)
		}
		if !strings.Contains(output, "[code content]: var counter int = 0") {
			t.Errorf("Expected code content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "MCP Tool: mcp_references") {
			t.Errorf("Expected MCP tool name in output, got: %s", output)
		}
		if !strings.Contains(output, "symbolName: TestFunction") {
			t.Errorf("Expected symbolName parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "Found 3 references:") {
			t.Errorf("Expected fallback content text in output, got: %s", output)
		}
		if !strings.Contains(output, "1. main.go:10") {
			t.Errorf("Expected reference line in output, got: %s", output)
		}
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

		if !strings.Contains(output, "MCP Tool: mcp_diagnostics") {
			t.Errorf("Expected MCP tool name in output, got: %s", output)
		}
		if strings.Contains(output, "server:") {
			t.Errorf("Should not show server when not set, got: %s", output)
		}
		if strings.Contains(output, "Parameters:") {
			t.Errorf("Should not show parameters when empty, got: %s", output)
		}
		if !strings.Contains(output, "No issues found") {
			t.Errorf("Expected content text in output, got: %s", output)
		}
		if strings.Contains(output, "Execution time:") {
			t.Errorf("Should not show execution time when zero, got: %s", output)
		}
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

		if !strings.Contains(output, "MCP Tool: mcp_rename_symbol") {
			t.Errorf("Expected MCP tool name in output, got: %s", output)
		}
		if !strings.Contains(output, "oldName: oldFunction") {
			t.Errorf("Expected oldName parameter in output, got: %s", output)
		}
		if !strings.Contains(output, "newName: newFunction") {
			t.Errorf("Expected newName parameter in output, got: %s", output)
		}
		if strings.Contains(output, "Content:") {
			t.Errorf("Should not show content section when no content, got: %s", output)
		}
		if !strings.Contains(output, "Execution time: 50ms") {
			t.Errorf("Expected execution time in output, got: %s", output)
		}
	})

	t.Run("MCP tool error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_definition",
			Success:   false,
			Error:     "Symbol not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "Error: Symbol not found" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "mcp_definition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GrepMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if output != "Error: Invalid metadata type for MCP tool" {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}
