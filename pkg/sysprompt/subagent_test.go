package sysprompt

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubAgentPrompt verifies that key elements from templates appear in the generated subagent prompt
func TestSubAgentPrompt(t *testing.T) {
	// Generate a subagent prompt
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{})

	// Define expected fragments that should appear in the prompt
	expectedFragments := []string{
		// Main introduction
		"You are an AI SWE Agent",
		"open ended code search, architecture analysis",

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

		// System information section
		"System Information",
		"Current working directory",
		"Operating system",
	}

	// Verify each fragment appears in the prompt
	for _, fragment := range expectedFragments {
		assert.Contains(t, prompt, fragment, "Expected subagent prompt to contain: %q", fragment)
	}

	unexpectedFragments := []string{
		"## Subagent tool usage examples",
		"- **ALWAYS prioritize",
		"for open-ended code search",
	}

	for _, fragment := range unexpectedFragments {
		assert.NotContains(t, prompt, fragment, "Did not expect subagent prompt to contain: %q", fragment)
	}
}

// TestSubAgentPromptBashBannedCommands verifies that banned commands appear in the default subagent prompt
func TestSubAgentPromptBashBannedCommands(t *testing.T) {
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{})

	// Should contain bash command restrictions section
	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected subagent prompt to contain 'Bash Command Restrictions' section")

	// Should contain banned commands section (default behavior)
	assert.Contains(t, prompt, "Banned Commands", "Expected subagent prompt to contain 'Banned Commands' section")

	// Should NOT contain allowed commands section in default mode
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect subagent prompt to contain 'Allowed Commands' section in default mode")

	// Verify all banned commands from tools package are present
	for _, bannedCmd := range tools.BannedCommands {
		assert.Contains(t, prompt, bannedCmd, "Expected subagent prompt to contain banned command: %q", bannedCmd)
	}
}

// TestSubAgentPromptBashAllowedCommands verifies that allowed commands work correctly in subagent prompts
func TestSubAgentPromptBashAllowedCommands(t *testing.T) {
	// Create a prompt context with allowed commands
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-sonnet-4-20250514")
	allowedCommands := []string{"find *", "grep *", "cat *", "head *", "tail *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSubagentPrompt(promptCtx)
	require.NoError(t, err, "Failed to render subagent prompt")

	// Should contain bash command restrictions section
	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected subagent prompt to contain 'Bash Command Restrictions' section")

	// Should contain allowed commands section
	assert.Contains(t, prompt, "Allowed Commands", "Expected subagent prompt to contain 'Allowed Commands' section")

	// Should NOT contain banned commands section when allowed commands are set
	assert.NotContains(t, prompt, "Banned Commands", "Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")

	// Verify all allowed commands are present
	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, prompt, allowedCmd, "Expected subagent prompt to contain allowed command: %q", allowedCmd)
	}

	// Should contain the rejection message
	assert.Contains(t, prompt, "Commands not matching these patterns will be rejected", "Expected subagent prompt to contain rejection message for non-matching commands")
}

// TestSubAgentPromptContextConsistency verifies that both system and subagent prompts have consistent bash restrictions
func TestSubAgentPromptContextConsistency(t *testing.T) {
	// Test that both system and subagent prompts render the same bash restrictions with the same context
	promptCtx := NewPromptContext()
	config := NewDefaultConfig().WithModel("claude-sonnet-4-20250514")
	allowedCommands := []string{"test *", "verify *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)

	systemPrompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	subagentPrompt, err := renderer.RenderSubagentPrompt(promptCtx)
	require.NoError(t, err, "Failed to render subagent prompt")

	// Both should contain the same allowed commands
	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, systemPrompt, allowedCmd, "Expected system prompt to contain allowed command: %q", allowedCmd)
		assert.Contains(t, subagentPrompt, allowedCmd, "Expected subagent prompt to contain allowed command: %q", allowedCmd)
	}

	// Both should contain allowed commands section and NOT banned commands section
	assert.Contains(t, systemPrompt, "Allowed Commands", "Expected system prompt to contain 'Allowed Commands' section")
	assert.Contains(t, subagentPrompt, "Allowed Commands", "Expected subagent prompt to contain 'Allowed Commands' section")

	assert.NotContains(t, systemPrompt, "Banned Commands", "Did not expect system prompt to contain 'Banned Commands' section when allowed commands are configured")
	assert.NotContains(t, subagentPrompt, "Banned Commands", "Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")
}
