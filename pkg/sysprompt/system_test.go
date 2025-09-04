package sysprompt

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemPrompt verifies that key elements from templates appear in the generated system prompt
func TestSystemPrompt(t *testing.T) {
	// Generate a system prompt
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, map[string]tooltypes.ContextInfo{})

	// Define expected fragments that should appear in the prompt
	expectedFragments := []string{
		// Main introduction
		"You are an interactive CLI tool",

		// Tone and style sections
		"Tone and Style",
		"Be concise, direct and to the point",

		// Tool usage section
		"Tool Usage",
		"invoke multiple INDEPENDENT tools",

		// Task management section
		"Task Management",
		"todo_write",
		"todo_read",

		// Context section
		"Context",
		"file, it will be automatically loaded", // Should mention the file loading

		// System information section
		"System Information",
		"Current working directory",
		"Operating system",
	}

	// Verify each fragment appears in the prompt
	for _, fragment := range expectedFragments {
		assert.Contains(t, prompt, fragment, "Expected system prompt to contain: %q", fragment)
	}
}

// TestSystemPromptBashBannedCommands verifies that banned commands appear in the default system prompt
func TestSystemPromptBashBannedCommands(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, map[string]tooltypes.ContextInfo{})

	// Should contain bash command restrictions section
	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected system prompt to contain 'Bash Command Restrictions' section")

	// Should contain banned commands section (default behavior)
	assert.Contains(t, prompt, "Banned Commands", "Expected system prompt to contain 'Banned Commands' section")

	// Should NOT contain allowed commands section in default mode
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect system prompt to contain 'Allowed Commands' section in default mode")

	// Verify all banned commands from tools package are present
	for _, bannedCmd := range tools.BannedCommands {
		assert.Contains(t, prompt, bannedCmd, "Expected system prompt to contain banned command: %q", bannedCmd)
	}
}

// TestSystemPromptBashAllowedCommands verifies that allowed commands work correctly
func TestSystemPromptBashAllowedCommands(t *testing.T) {
	// Create a prompt context with allowed commands
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-sonnet-4-20250514")
	allowedCommands := []string{"ls *", "pwd", "git status", "echo *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	// Should contain bash command restrictions section
	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected system prompt to contain 'Bash Command Restrictions' section")

	// Should contain allowed commands section
	assert.Contains(t, prompt, "Allowed Commands", "Expected system prompt to contain 'Allowed Commands' section")

	// Should NOT contain banned commands section when allowed commands are set
	assert.NotContains(t, prompt, "Banned Commands", "Did not expect system prompt to contain 'Banned Commands' section when allowed commands are configured")

	// Verify all allowed commands are present
	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, prompt, allowedCmd, "Expected system prompt to contain allowed command: %q", allowedCmd)
	}

	// Should contain the rejection message
	assert.Contains(t, prompt, "Commands not matching these patterns will be rejected", "Expected system prompt to contain rejection message for non-matching commands")
}

// TestSystemPromptBashEmptyAllowedCommands verifies behavior with empty allowed commands
func TestSystemPromptBashEmptyAllowedCommands(t *testing.T) {
	// Create a prompt context with empty allowed commands (should fall back to banned commands)
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-sonnet-4-20250514")
	llmConfig := &llm.Config{
		AllowedCommands: []string{}, // Empty slice
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	// Should fall back to banned commands behavior
	assert.Contains(t, prompt, "Banned Commands", "Expected system prompt to fall back to 'Banned Commands' section when allowed commands is empty")

	// Should NOT contain allowed commands section
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect system prompt to contain 'Allowed Commands' section when allowed commands is empty")
}

// TestSystemPrompt_WithContexts verifies that provided contexts are properly included in system prompt
func TestSystemPrompt_WithContexts(t *testing.T) {
	contexts := map[string]tooltypes.ContextInfo{
		"/path/to/project/AGENTS.md": {
			Content:      "# Project Guidelines\nThis is the main project context.",
			Path:         "/path/to/project/AGENTS.md",
			LastModified: time.Now(),
		},
		"/path/to/project/module/KODELET.md": {
			Content:      "# Module Specific\nThis module handles authentication.",
			Path:         "/path/to/project/module/KODELET.md", 
			LastModified: time.Now(),
		},
	}

	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

	// Verify context section exists
	assert.Contains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Expected context introduction")

	// Verify both context files are included with proper formatting
	assert.Contains(t, prompt, `<context filename="/path/to/project/AGENTS.md">`, "Expected AGENTS.md context with filename")
	assert.Contains(t, prompt, "# Project Guidelines", "Expected AGENTS.md content")
	assert.Contains(t, prompt, "This is the main project context.", "Expected AGENTS.md content")

	assert.Contains(t, prompt, `<context filename="/path/to/project/module/KODELET.md">`, "Expected KODELET.md context with filename") 
	assert.Contains(t, prompt, "# Module Specific", "Expected KODELET.md content")
	assert.Contains(t, prompt, "This module handles authentication.", "Expected KODELET.md content")

	// Verify context sections are properly closed
	assert.Contains(t, prompt, "</context>", "Expected context closing tags")
}

// TestSystemPrompt_WithEmptyContexts verifies fallback behavior with empty contexts
func TestSystemPrompt_WithEmptyContexts(t *testing.T) {
	emptyContexts := map[string]tooltypes.ContextInfo{}
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, emptyContexts)

	// Should still generate a valid prompt 
	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected basic kodelet introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")

	// Should not contain context section when no contexts provided
	assert.NotContains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Should not have context intro when no contexts")
}

// TestSystemPrompt_WithNilContexts verifies fallback behavior with nil contexts
func TestSystemPrompt_WithNilContexts(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, nil)

	// Should still generate a valid prompt and use default context loading
	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected basic kodelet introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")

	// When nil contexts are passed, it should fall back to the default loadContexts() behavior
	// which may or may not find context files in the current directory
}

// TestSystemPrompt_ContextFormattingEdgeCases tests edge cases in context formatting
func TestSystemPrompt_ContextFormattingEdgeCases(t *testing.T) {
	t.Run("context_with_special_characters", func(t *testing.T) {
		contexts := map[string]tooltypes.ContextInfo{
			"/path/with spaces/AGENTS.md": {
				Content: "Content with <tags> & special chars: quotes \"test\" and 'test'",
				Path:    "/path/with spaces/AGENTS.md",
			},
		}

		prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/path/with spaces/AGENTS.md">`, "Expected path with spaces")
		assert.Contains(t, prompt, "Content with <tags> & special chars", "Expected content with special characters")
		assert.Contains(t, prompt, `quotes "test" and 'test'`, "Expected quotes preserved")
	})

	t.Run("empty_context_content", func(t *testing.T) {
		contexts := map[string]tooltypes.ContextInfo{
			"/empty/AGENTS.md": {
				Content: "",
				Path:    "/empty/AGENTS.md",
			},
		}

		prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/empty/AGENTS.md">`, "Expected empty context file to be included")
		assert.Contains(t, prompt, "</context>", "Expected context to be properly closed even when empty")
	})

	t.Run("multiple_contexts_ordering", func(t *testing.T) {
		contexts := map[string]tooltypes.ContextInfo{
			"/z/last.md": {
				Content: "Last content",
				Path:    "/z/last.md",
			},
			"/a/first.md": {
				Content: "First content", 
				Path:    "/a/first.md",
			},
			"/m/middle.md": {
				Content: "Middle content",
				Path:    "/m/middle.md",
			},
		}

		prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

		// All contexts should be included regardless of order
		assert.Contains(t, prompt, "First content", "Expected first context")
		assert.Contains(t, prompt, "Middle content", "Expected middle context")
		assert.Contains(t, prompt, "Last content", "Expected last context")

		// All context files should have proper formatting
		assert.Contains(t, prompt, `<context filename="/a/first.md">`, "Expected first context file")
		assert.Contains(t, prompt, `<context filename="/m/middle.md">`, "Expected middle context file")
		assert.Contains(t, prompt, `<context filename="/z/last.md">`, "Expected last context file")
	})
}
