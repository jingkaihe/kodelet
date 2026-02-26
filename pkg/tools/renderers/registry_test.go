package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestRendererRegistry_ExactMatches(t *testing.T) {
	registry := NewRendererRegistry()

	tests := []struct {
		name         string
		toolName     string
		expectRender bool
		expectError  bool
	}{
		{"File Read", "file_read", true, false},
		{"File Write", "file_write", true, false},
		{"File Edit", "file_edit", true, false},
		{"Apply Patch", "apply_patch", true, false},
		{"Glob", "glob_tool", true, false},
		{"Grep", "grep_tool", true, false},
		{"Bash", "bash", true, false},
		{"Background Bash", "bash_background", true, false},
		{"Todo", "todo_read", true, false},
		{"Thinking", "thinking", true, false},
		{"Batch", "batch", true, false},
		{"Image Recognition", "image_recognition", true, false},
		{"Subagent", "subagent", true, false},
		{"Web Fetch", "web_fetch", true, false},
		{"View Background Processes", "view_background_processes", true, false},
		{"Code Execution", "code_execution", true, false},

		{"Unknown Tool", "unknown_tool", true, false}, // Should fallback to default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tools.StructuredToolResult{
				ToolName:  tt.toolName,
				Success:   true,
				Timestamp: time.Now(),
			}

			output := registry.Render(result)

			if tt.expectRender {
				assert.NotEmpty(t, output, "Expected render output for %s", tt.toolName)
			} else {
				assert.Empty(t, output, "Expected no render output for %s", tt.toolName)
			}
		})
	}
}

func TestRendererRegistry_PatternMatches(t *testing.T) {
	registry := NewRendererRegistry()

	tests := []struct {
		name     string
		toolName string
		expected string
	}{
		{"MCP Definition", "mcp_definition", "MCP Tool: mcp_definition"},
		{"MCP References", "mcp_references", "MCP Tool: mcp_references"},
		{"MCP Hover", "mcp_hover", "MCP Tool: mcp_hover"},
		{"MCP Rename", "mcp_rename_symbol", "MCP Tool: mcp_rename_symbol"},
		{"MCP Edit", "mcp_edit_file", "MCP Tool: mcp_edit_file"},
		{"MCP Diagnostics", "mcp_diagnostics", "MCP Tool: mcp_diagnostics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tools.StructuredToolResult{
				ToolName:  tt.toolName,
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tools.MCPToolMetadata{
					MCPToolName: tt.toolName,
					ServerName:  "test-server",
					Parameters:  map[string]any{"test": "value"},
					Content: []tools.MCPContent{
						{Type: "text", Text: "MCP response"},
					},
					ContentText: "MCP response",
				},
			}

			output := registry.Render(result)

			assert.Contains(t, output, tt.expected, "Expected output to contain %q for %s", tt.expected, tt.toolName)
		})
	}
}

func TestRendererRegistry_ErrorHandling(t *testing.T) {
	registry := NewRendererRegistry()

	result := tools.StructuredToolResult{
		ToolName:  "file_read",
		Success:   false,
		Error:     "File not found",
		Timestamp: time.Now(),
	}

	output := registry.Render(result)

	assert.Contains(t, output, "Error: File not found", "Expected error message in output")
}

func TestRendererRegistry_FallbackRenderer(t *testing.T) {
	registry := NewRendererRegistry()

	result := tools.StructuredToolResult{
		ToolName:  "completely_unknown_tool",
		Success:   true,
		Timestamp: time.Now(),
	}

	output := registry.Render(result)

	// Should use the default renderer and include tool name
	assert.Contains(t, output, "completely_unknown_tool", "Expected fallback renderer output to contain tool name")
	assert.Contains(t, output, "Success: true", "Expected fallback renderer output to contain success status")
}

func TestRendererRegistry_CustomRenderer(t *testing.T) {
	registry := NewRendererRegistry()

	// Create a custom renderer
	customRenderer := &TestRenderer{message: "Custom Test Renderer"}

	// Register it
	registry.Register("test_tool", customRenderer)

	result := tools.StructuredToolResult{
		ToolName:  "test_tool",
		Success:   true,
		Timestamp: time.Now(),
	}

	output := registry.Render(result)

	assert.Contains(t, output, "Custom Test Renderer", "Expected custom renderer output")
}

func TestRendererRegistry_PatternRegistration(t *testing.T) {
	registry := NewRendererRegistry()

	// Create a custom renderer for a pattern
	customRenderer := &TestRenderer{message: "Custom Pattern Renderer"}

	// Register it with a pattern
	registry.RegisterPattern("test_*", customRenderer)

	tests := []struct {
		toolName string
		expected string
	}{
		{"test_one", "Custom Pattern Renderer"},
		{"test_two", "Custom Pattern Renderer"},
		{"test_", "Custom Pattern Renderer"},
		{"not_test", "not_test"}, // Should use default and contain tool name
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := tools.StructuredToolResult{
				ToolName:  tt.toolName,
				Success:   true,
				Timestamp: time.Now(),
			}

			output := registry.Render(result)

			assert.Contains(t, output, tt.expected, "Expected output to contain %q for %s", tt.expected, tt.toolName)
		})
	}
}

// TestRenderer is a simple test renderer for testing purposes
type TestRenderer struct {
	message string
}

func (r *TestRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return "Error: " + result.Error
	}
	return r.message
}
