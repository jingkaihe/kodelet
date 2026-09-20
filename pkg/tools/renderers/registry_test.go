package renderers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{"Thinking", "thinking", true, false},
		{"Batch", "batch", true, false},
		{"View Image", "view_image", true, false},
		{"Web Fetch", "web_fetch", true, false},

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

func TestRendererRegistry_ExtensionToolMetadata(t *testing.T) {
	registry := NewRendererRegistry()
	for _, name := range []string{"get_weather", "read_conversation"} {
		t.Run(name, func(t *testing.T) {
			result := tools.StructuredToolResult{
				ToolName: name,
				Success:  true,
				Metadata: &tools.ExtensionToolMetadata{
					ExtensionID: name,
					ToolName:    name,
					Output:      "Extracted evidence",
				},
			}
			assert.Contains(t, registry.Render(result), "Extracted evidence")
			// Persisted results must use extension metadata, even for a former built-in name.
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &result))
			for _, render := range []func(tools.StructuredToolResult) string{
				registry.Render, registry.RenderMarkdown, registry.RenderMergedMarkdown,
			} {
				output := render(result)
				assert.Contains(t, output, "Extension Tool: "+name)
				assert.Contains(t, output, "Extracted evidence")
			}
		})
	}
}

func TestRendererRegistry_BrowserActionsRetainOutputAndErrors(t *testing.T) {
	registry := NewRendererRegistry()
	for _, tc := range []struct {
		name, error string
		meta        tools.BrowserMetadata
		want        string
	}{
		{"open", "", tools.BrowserMetadata{Action: "open", Output: `{"sessionId":"session-1"}`}, "Browser: Open\n\n" + `{"sessionId":"session-1"}`},
		{"navigate", "", tools.BrowserMetadata{Action: "navigate", URL: "http://localhost:1234", Output: `{"frameId":"page"}`}, "Browser: Go to http://localhost:1234\n\n" + `{"frameId":"page"}`},
		{"evaluate", "", tools.BrowserMetadata{Action: "evaluate", Expression: "const title = document.title;\ntitle", Output: "Page ready"}, "Browser: Run code\n\nconst title = document.title;\ntitle\n\nPage ready"},
		{"evaluation failure", "ReferenceError", tools.BrowserMetadata{Action: "evaluate", Expression: "missing()", Output: "partial output"}, "Browser: Run code\n\nmissing()\n\npartial output\n\nError: ReferenceError"},
		{"stop", "", tools.BrowserMetadata{Action: "stop", SessionID: "session-1", Output: "Conversation browser stopped."}, "Browser: Stop\n\nSession: session-1\n\nConversation browser stopped."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tools.StructuredToolResult{ToolName: "browser", Success: tc.error == "", Error: tc.error, Metadata: tc.meta}
			assert.Equal(t, tc.want, registry.Render(result))
		})
	}
	result := tools.StructuredToolResult{
		ToolName: "browser", Success: true,
		Metadata:    tools.BrowserMetadata{Action: "screenshot", Path: "/workspace/page.png", Output: "Viewed image page.png"},
		Attachments: []tools.ToolAttachment{{Type: "image", ArtifactID: "internal-artifact", ShortCode: "screenshot", ViewURL: "https://kodelet.example/i/screenshot"}},
	}
	output := registry.Render(result)
	assert.Contains(t, output, "Browser: Screenshot /workspace/page.png")
	assert.Equal(t, 1, strings.Count(output, "https://kodelet.example/i/screenshot"))
	assert.NotContains(t, output, "Viewed image")
	assert.NotContains(t, output, "internal-artifact")
}

func TestRendererRegistry_ExtensionToolPresentation(t *testing.T) {
	registry := NewRendererRegistry()
	result := tools.StructuredToolResult{
		ToolName: "send_instruction",
		Success:  true,
		Metadata: &tools.ExtensionToolMetadata{
			ExtensionID: "worker-tools",
			ToolName:    "send_instruction",
			Output:      "Started run_456 for agt_123",
			Data: map[string]any{"presentation": map[string]any{
				"summary": "Follow up parser-reviewer",
				"body":    "Review the parser",
			}},
		},
	}

	output := registry.Render(result)

	assert.Equal(t, "Follow up parser-reviewer\n\nReview the parser", output)
	assert.NotContains(t, output, "send_instruction")
	assert.NotContains(t, output, "run_456")
	assert.NotContains(t, output, "agt_123")
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
