package sysprompt

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemPrompt verifies that key elements from templates appear in the generated system prompt
func TestSystemPrompt(t *testing.T) {
	// Generate a system prompt
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{})

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
		"KODELET.md",

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
	prompt := SystemPrompt("claude-sonnet-4-20250514", llm.Config{})

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
