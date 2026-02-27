package sysprompt

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubAgentPrompt(t *testing.T) {
	prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	// SubAgentPrompt now uses SystemPrompt with IsSubAgent=true
	// It should contain the main system prompt content
	expectedFragments := []string{
		"You are an interactive CLI tool",
		"Tone and Style",
		"Be concise, direct and to the point",
		"Tool Usage",
		"invoke multiple INDEPENDENT tools",
		"System Information",
		"Current working directory",
		"Operating system",
	}

	for _, fragment := range expectedFragments {
		assert.Contains(t, prompt, fragment, "Expected subagent prompt to contain: %q", fragment)
	}

	// Subagent prompts should NOT contain subagent tool guidance (to prevent recursion)
	// or todo tool references (disabled for subagents)
	unexpectedFragments := []string{
		"ALWAYS prioritize `subagent`",
		"## Subagent tool usage examples",
		"todo_write",
		"todo_read",
	}

	for _, fragment := range unexpectedFragments {
		assert.NotContains(t, prompt, fragment, "Did not expect subagent prompt to contain: %q", fragment)
	}
}

func TestSubAgentPromptBashBannedCommands(t *testing.T) {
	prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected subagent prompt to contain 'Bash Command Restrictions' section")
	assert.Contains(t, prompt, "Banned Commands", "Expected subagent prompt to contain 'Banned Commands' section")
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect subagent prompt to contain 'Allowed Commands' section in default mode")

	for _, bannedCmd := range tools.BannedCommands {
		assert.Contains(t, prompt, bannedCmd, "Expected subagent prompt to contain banned command: %q", bannedCmd)
	}
}

func TestSubAgentPromptBashAllowedCommands(t *testing.T) {
	allowedCommands := []string{"find *", "grep *", "cat *", "head *", "tail *"}
	llmConfig := llm.Config{
		AllowedCommands: allowedCommands,
		IsSubAgent:      true,
	}

	prompt := SubAgentPrompt("claude-sonnet-4-6", llmConfig, nil)

	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected subagent prompt to contain 'Bash Command Restrictions' section")
	assert.Contains(t, prompt, "Allowed Commands", "Expected subagent prompt to contain 'Allowed Commands' section")
	assert.NotContains(t, prompt, "Banned Commands", "Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")

	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, prompt, allowedCmd, "Expected subagent prompt to contain allowed command: %q", allowedCmd)
	}

	assert.Contains(t, prompt, "Commands not matching these patterns will be rejected", "Expected subagent prompt to contain rejection message for non-matching commands")
}

func TestSubAgentPromptContextConsistency(t *testing.T) {
	promptCtx := NewPromptContext(nil)
	promptCtx.SubagentEnabled = true
	promptCtx.TodoToolsEnabled = false
	allowedCommands := []string{"test *", "verify *"}
	llmConfig := llm.Config{
		AllowedCommands: allowedCommands,
	}

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)

	systemPrompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	// Subagent prompt keeps allowed command behavior but excludes subagent guidance.
	subagentPrompt := SubAgentPrompt("claude-sonnet-4-6", llmConfig, nil)

	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, systemPrompt, allowedCmd, "Expected system prompt to contain allowed command: %q", allowedCmd)
		assert.Contains(t, subagentPrompt, allowedCmd, "Expected subagent prompt to contain allowed command: %q", allowedCmd)
	}

	assert.Contains(t, systemPrompt, "Allowed Commands", "Expected system prompt to contain 'Allowed Commands' section")
	assert.Contains(t, subagentPrompt, "Allowed Commands", "Expected subagent prompt to contain 'Allowed Commands' section")
	assert.Contains(t, systemPrompt, "ALWAYS prioritize `subagent`", "Expected system prompt to include subagent guidance")
	assert.NotContains(t, subagentPrompt, "ALWAYS prioritize `subagent`", "Expected subagent prompt to exclude subagent guidance")

	assert.NotContains(t, systemPrompt, "Banned Commands", "Did not expect system prompt to contain 'Banned Commands' section when allowed commands are configured")
	assert.NotContains(t, subagentPrompt, "Banned Commands", "Did not expect subagent prompt to contain 'Banned Commands' section when allowed commands are configured")
}

func TestSubAgentPrompt_WithContexts(t *testing.T) {
	contexts := map[string]string{
		"/path/to/project/AGENTS.md":    "# Project Context\nGeneral project guidelines and conventions.",
		"/home/user/.kodelet/AGENTS.md": "# User Preferences\nPersonal coding style and preferences.",
	}

	prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected subagent introduction")
	assert.Contains(t, prompt, "Here are some useful context to help you solve the user's problem.", "Expected context introduction")

	assert.Contains(t, prompt, `<context filename="/path/to/project/AGENTS.md", dir="/path/to/project">`, "Expected project AGENTS.md context with filename")
	assert.Contains(t, prompt, "# Project Context", "Expected project AGENTS.md content")
	assert.Contains(t, prompt, "General project guidelines and conventions.", "Expected project AGENTS.md content")

	assert.Contains(t, prompt, `<context filename="/home/user/.kodelet/AGENTS.md", dir="/home/user/.kodelet">`, "Expected home AGENTS.md context with filename")
	assert.Contains(t, prompt, "# User Preferences", "Expected home AGENTS.md content")
	assert.Contains(t, prompt, "Personal coding style and preferences.", "Expected home AGENTS.md content")

	assert.Contains(t, prompt, "</context>", "Expected context closing tags")
}

func TestSubAgentPrompt_WithEmptyContexts(t *testing.T) {
	emptyContexts := map[string]string{}
	prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, emptyContexts)

	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected subagent introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")
	assert.NotContains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Should not have context intro when no contexts")
}

func TestSubAgentPrompt_WithNilContexts(t *testing.T) {
	prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, nil)

	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected subagent introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")
}

func TestSubAgentPrompt_ContextFormattingConsistency(t *testing.T) {
	t.Run("context_with_code_blocks", func(t *testing.T) {
		contexts := map[string]string{
			"/project/docs/CODING_STYLE.md": "# Coding Style\n\n```go\nfunc Example() {\n    fmt.Println(\"hello\")\n}\n```\n\nUse proper indentation.",
		}

		prompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/project/docs/CODING_STYLE.md", dir="/project/docs">`, "Expected context file with full path")
		assert.Contains(t, prompt, "# Coding Style", "Expected markdown header")
		assert.Contains(t, prompt, "```go", "Expected code block start")
		assert.Contains(t, prompt, "func Example() {", "Expected code content")
		assert.Contains(t, prompt, "```", "Expected code block end")
		assert.Contains(t, prompt, "Use proper indentation.", "Expected text after code block")
	})

	t.Run("multiple_contexts_in_subagent", func(t *testing.T) {
		contexts := map[string]string{
			"/project/AGENTS.md":              "# Main Project\nThis is the main project context for subagents.",
			"/project/modules/auth/AGENTS.md": "# Auth Module\nAuthentication-specific guidelines for subagents.",
		}

		prompt := SubAgentPrompt("gpt-4", llm.Config{}, contexts)

		assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected subagent introduction")
		assert.Contains(t, prompt, "This is the main project context for subagents.", "Expected main project context")
		assert.Contains(t, prompt, "Authentication-specific guidelines for subagents.", "Expected auth module context")
		assert.Contains(t, prompt, `<context filename="/project/AGENTS.md", dir="/project">`, "Expected main project context file")
		assert.Contains(t, prompt, `<context filename="/project/modules/auth/AGENTS.md", dir="/project/modules/auth">`, "Expected auth module context file")
	})
}

func TestSubAgentPrompt_FeatureConsistency(t *testing.T) {
	contexts := map[string]string{
		"/shared/context.md": "# Shared Context\nThis content should appear in both system and subagent prompts.",
	}

	llmConfig := llm.Config{}

	systemPrompt := SystemPrompt("claude-sonnet-4-6", llmConfig, contexts)
	subagentPrompt := SubAgentPrompt("claude-sonnet-4-6", llmConfig, contexts)

	assert.Contains(t, systemPrompt, "# Shared Context", "Expected shared context in system prompt")
	assert.Contains(t, subagentPrompt, "# Shared Context", "Expected shared context in subagent prompt")
	assert.Contains(t, systemPrompt, "This content should appear in both", "Expected shared context content in system prompt")
	assert.Contains(t, subagentPrompt, "This content should appear in both", "Expected shared context content in subagent prompt")

	assert.Contains(t, systemPrompt, `<context filename="/shared/context.md", dir="/shared">`, "Expected context file formatting in system prompt")
	assert.Contains(t, subagentPrompt, `<context filename="/shared/context.md", dir="/shared">`, "Expected context file formatting in subagent prompt")

	assert.Contains(t, systemPrompt, "</context>", "Expected context closing tags in system prompt")
	assert.Contains(t, subagentPrompt, "</context>", "Expected context closing tags in subagent prompt")
}

func TestSubAgentPrompt_NoSubagentExamples(t *testing.T) {
	// Verify that subagent prompts don't include subagent tool usage examples (to prevent recursion)
	systemPrompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, nil)
	subagentPrompt := SubAgentPrompt("claude-sonnet-4-6", llm.Config{}, nil)

	// System prompt (not subagent) SHOULD have subagent examples
	assert.Contains(t, systemPrompt, "## Subagent tool usage examples", "Expected system prompt to contain subagent examples")

	// Subagent prompt should NOT have subagent examples
	assert.NotContains(t, subagentPrompt, "## Subagent tool usage examples", "Expected subagent prompt to NOT contain subagent examples")
}
