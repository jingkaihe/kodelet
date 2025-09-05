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
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, map[string]string{})

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
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, map[string]string{})

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

// TestSubAgentPrompt_WithContexts verifies that provided contexts are properly included in subagent prompt
func TestSubAgentPrompt_WithContexts(t *testing.T) {
	contexts := map[string]string{
		"/path/to/project/AGENTS.md":    "# Project Context\nGeneral project guidelines and conventions.",
		"/home/user/.kodelet/AGENTS.md": "# User Preferences\nPersonal coding style and preferences.",
	}

	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

	// Verify this is a subagent prompt (contains subagent-specific elements)
	assert.Contains(t, prompt, "You are an AI SWE Agent", "Expected subagent introduction")

	// Verify context section exists
	assert.Contains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Expected context introduction")

	// Verify both context files are included with proper formatting
	assert.Contains(t, prompt, `<context filename="/path/to/project/AGENTS.md">`, "Expected project AGENTS.md context with filename")
	assert.Contains(t, prompt, "# Project Context", "Expected project AGENTS.md content")
	assert.Contains(t, prompt, "General project guidelines and conventions.", "Expected project AGENTS.md content")

	assert.Contains(t, prompt, `<context filename="/home/user/.kodelet/AGENTS.md">`, "Expected home AGENTS.md context with filename")
	assert.Contains(t, prompt, "# User Preferences", "Expected home AGENTS.md content")
	assert.Contains(t, prompt, "Personal coding style and preferences.", "Expected home AGENTS.md content")

	// Verify context sections are properly closed
	assert.Contains(t, prompt, "</context>", "Expected context closing tags")
}

// TestSubAgentPrompt_WithEmptyContexts verifies fallback behavior with empty contexts
func TestSubAgentPrompt_WithEmptyContexts(t *testing.T) {
	emptyContexts := map[string]string{}
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, emptyContexts)

	// Should still generate a valid subagent prompt
	assert.Contains(t, prompt, "You are an AI SWE Agent", "Expected subagent introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")

	// Should not contain context section when no contexts provided
	assert.NotContains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Should not have context intro when no contexts")
}

// TestSubAgentPrompt_WithNilContexts verifies fallback behavior with nil contexts
func TestSubAgentPrompt_WithNilContexts(t *testing.T) {
	prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, nil)

	// Should still generate a valid subagent prompt and use default context loading
	assert.Contains(t, prompt, "You are an AI SWE Agent", "Expected subagent introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")

	// When nil contexts are passed, it should fall back to the default loadContexts() behavior
	// which may or may not find context files in the current directory
}

// TestSubAgentPrompt_ContextFormattingConsistency tests that contexts are formatted the same as system prompt
func TestSubAgentPrompt_ContextFormattingConsistency(t *testing.T) {
	t.Run("context_with_code_blocks", func(t *testing.T) {
		contexts := map[string]string{
			"/project/docs/CODING_STYLE.md": "# Coding Style\n\n```go\nfunc Example() {\n    fmt.Println(\"hello\")\n}\n```\n\nUse proper indentation.",
		}

		prompt := SubAgentPrompt("claude-sonnet-4-20250514", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/project/docs/CODING_STYLE.md">`, "Expected context file with full path")
		assert.Contains(t, prompt, "# Coding Style", "Expected markdown header")
		assert.Contains(t, prompt, "```go", "Expected code block start")
		assert.Contains(t, prompt, "func Example() {", "Expected code content")
		assert.Contains(t, prompt, "```", "Expected code block end")
		assert.Contains(t, prompt, "Use proper indentation.", "Expected text after code block")
	})

	t.Run("multiple_contexts_in_subagent", func(t *testing.T) {
		contexts := map[string]string{
			"/project/AGENTS.md":               "# Main Project\nThis is the main project context for subagents.",
			"/project/modules/auth/KODELET.md": "# Auth Module\nAuthentication-specific guidelines for subagents.",
		}

		prompt := SubAgentPrompt("gpt-4", llm.Config{}, contexts)

		// Verify subagent-specific content is present
		assert.Contains(t, prompt, "You are an AI SWE Agent", "Expected subagent introduction")

		// Verify both contexts are included
		assert.Contains(t, prompt, "This is the main project context for subagents.", "Expected main project context")
		assert.Contains(t, prompt, "Authentication-specific guidelines for subagents.", "Expected auth module context")

		// Verify proper context formatting
		assert.Contains(t, prompt, `<context filename="/project/AGENTS.md">`, "Expected main project context file")
		assert.Contains(t, prompt, `<context filename="/project/modules/auth/KODELET.md">`, "Expected auth module context file")
	})
}

// TestSubAgentPrompt_FeatureConsistency tests that subagent prompts maintain consistency with system prompts for context handling
func TestSubAgentPrompt_FeatureConsistency(t *testing.T) {
	contexts := map[string]string{
		"/shared/context.md": "# Shared Context\nThis content should appear in both system and subagent prompts.",
	}

	llmConfig := llm.Config{}

	// Generate both prompts with the same contexts
	systemPrompt := SystemPrompt("claude-sonnet-4-20250514", llmConfig, contexts)
	subagentPrompt := SubAgentPrompt("claude-sonnet-4-20250514", llmConfig, contexts)

	// Both should include the same context content
	assert.Contains(t, systemPrompt, "# Shared Context", "Expected shared context in system prompt")
	assert.Contains(t, subagentPrompt, "# Shared Context", "Expected shared context in subagent prompt")
	assert.Contains(t, systemPrompt, "This content should appear in both", "Expected shared context content in system prompt")
	assert.Contains(t, subagentPrompt, "This content should appear in both", "Expected shared context content in subagent prompt")

	// Both should format contexts the same way
	assert.Contains(t, systemPrompt, `<context filename="/shared/context.md">`, "Expected context file formatting in system prompt")
	assert.Contains(t, subagentPrompt, `<context filename="/shared/context.md">`, "Expected context file formatting in subagent prompt")

	// Both should close context tags properly
	assert.Contains(t, systemPrompt, "</context>", "Expected context closing tags in system prompt")
	assert.Contains(t, subagentPrompt, "</context>", "Expected context closing tags in subagent prompt")
}
