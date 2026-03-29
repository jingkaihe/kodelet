// Package renderers provides CLI output rendering functionality for tool results.
// It includes a registry system for managing tool renderers and supports
// pattern-based matching for custom and MCP tools.
package renderers

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// CLIRenderer interface for rendering structured tool results to CLI output
type CLIRenderer interface {
	RenderCLI(result tools.StructuredToolResult) string
}

// RendererRegistry manages tool renderers with pattern matching support
type RendererRegistry struct {
	renderers map[string]CLIRenderer
	patterns  map[string]CLIRenderer
}

// NewRendererRegistry creates and initializes a new renderer registry
func NewRendererRegistry() *RendererRegistry {
	registry := &RendererRegistry{
		renderers: make(map[string]CLIRenderer),
		patterns:  make(map[string]CLIRenderer),
	}

	// Register all tool renderers
	registry.Register("file_read", &FileReadRenderer{})
	registry.Register("file_write", &FileWriteRenderer{})
	registry.Register("file_edit", &FileEditRenderer{})
	registry.Register("apply_patch", &ApplyPatchRenderer{})

	registry.Register("bash", &BashRenderer{})
	registry.Register("grep_tool", &GrepRenderer{})
	registry.Register("glob_tool", &GlobRenderer{})
	registry.Register("todo_read", &TodoRenderer{})
	registry.Register("todo_write", &TodoRenderer{})
	registry.Register("subagent", &SubAgentRenderer{})
	registry.Register("image_recognition", &ImageRecognitionRenderer{})
	registry.Register("web_fetch", &WebFetchRenderer{})
	registry.Register("read_conversation", &ReadConversationRenderer{})
	registry.Register("code_execution", &CodeExecutionRenderer{})
	registry.Register("skill", &SkillRenderer{})

	// Register MCP tools - pattern matches any tool prefixed with "mcp_"
	registry.RegisterPattern("mcp_*", &MCPToolRenderer{})

	// Register Custom tools - pattern matches any tool prefixed with "custom_tool_"
	registry.RegisterPattern("custom_tool_*", &CustomToolRenderer{})

	return registry
}

// Register adds a renderer for a specific tool name
func (r *RendererRegistry) Register(toolName string, renderer CLIRenderer) {
	r.renderers[toolName] = renderer
}

// RegisterPattern adds a renderer for a pattern (e.g., "mcp_*")
func (r *RendererRegistry) RegisterPattern(pattern string, renderer CLIRenderer) {
	r.patterns[pattern] = renderer
}

// Render finds the appropriate renderer and renders the result
func (r *RendererRegistry) Render(result tools.StructuredToolResult) string {
	renderer, exists := r.resolveRenderer(result.ToolName)
	if exists {
		return renderer.RenderCLI(result)
	}

	// Fallback renderer for unknown tools
	return r.renderFallback(result)
}

// RenderMarkdown finds the appropriate renderer and renders the result as markdown.
func (r *RendererRegistry) RenderMarkdown(result tools.StructuredToolResult) string {
	renderer, exists := r.resolveRenderer(result.ToolName)
	if !exists {
		return r.renderFallbackMarkdown(result)
	}

	if markdownRenderer, ok := renderer.(MarkdownRenderer); ok {
		return markdownRenderer.RenderMarkdown(result)
	}

	return renderMarkdownFromCLI(result, renderer.RenderCLI(result))
}

func (r *RendererRegistry) resolveRenderer(toolName string) (CLIRenderer, bool) {
	// First try exact match
	renderer, exists := r.renderers[toolName]
	if exists {
		return renderer, true
	}

	// Then try pattern matching
	for pattern, patternRenderer := range r.patterns {
		if r.matchesPattern(toolName, pattern) {
			return patternRenderer, true
		}
	}

	return nil, false
}

func (r *RendererRegistry) matchesPattern(toolName, pattern string) bool {
	// Simple pattern matching for "*" suffix patterns
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(toolName, prefix)
	}
	return toolName == pattern
}

func (r *RendererRegistry) renderFallback(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error (%s): %s", result.ToolName, result.Error)
	}
	return fmt.Sprintf("Tool Result (%s):\nSuccess: %v\nTimestamp: %s",
		result.ToolName, result.Success, result.Timestamp.Format("2006-01-02 15:04:05"))
}

func (r *RendererRegistry) renderFallbackMarkdown(result tools.StructuredToolResult) string {
	return renderMarkdownFromCLI(result, r.renderFallback(result))
}
