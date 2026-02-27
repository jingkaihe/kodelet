package sysprompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemPrompt verifies that key elements from templates appear in the generated system prompt
func TestSystemPrompt(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	expectedFragments := []string{
		"You are an interactive CLI tool",
		"Tone and Style",
		"Be concise, direct and to the point",
		"Tool Usage",
		"invoke multiple INDEPENDENT tools",
		"Context",
		"file, it will be automatically loaded",
		"System Information",
		"Current working directory",
		"Operating system",
	}

	for _, fragment := range expectedFragments {
		assert.Contains(t, prompt, fragment, "Expected system prompt to contain: %q", fragment)
	}
}

// TestSystemPromptBashBannedCommands verifies that banned commands appear in the default system prompt
func TestSystemPromptBashBannedCommands(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected system prompt to contain 'Bash Command Restrictions' section")
	assert.Contains(t, prompt, "Banned Commands", "Expected system prompt to contain 'Banned Commands' section")
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect system prompt to contain 'Allowed Commands' section in default mode")

	// Verify all banned commands from tools package are present
	for _, bannedCmd := range tools.BannedCommands {
		assert.Contains(t, prompt, bannedCmd, "Expected system prompt to contain banned command: %q", bannedCmd)
	}
}

// TestSystemPromptBashAllowedCommands verifies that allowed commands work correctly
func TestSystemPromptBashAllowedCommands(t *testing.T) {
	promptCtx := newPromptContext(nil)
	promptCtx.SubagentEnabled = true
	promptCtx.TodoToolsEnabled = false
	allowedCommands := []string{"ls *", "pwd", "git status", "echo *"}
	llmConfig := &llm.Config{
		AllowedCommands: allowedCommands,
	}

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	assert.Contains(t, prompt, "Bash Command Restrictions", "Expected system prompt to contain 'Bash Command Restrictions' section")
	assert.Contains(t, prompt, "Allowed Commands", "Expected system prompt to contain 'Allowed Commands' section")
	assert.NotContains(t, prompt, "Banned Commands", "Did not expect system prompt to contain 'Banned Commands' section when allowed commands are configured")

	for _, allowedCmd := range allowedCommands {
		assert.Contains(t, prompt, allowedCmd, "Expected system prompt to contain allowed command: %q", allowedCmd)
	}

	assert.Contains(t, prompt, "Commands not matching these patterns will be rejected", "Expected system prompt to contain rejection message for non-matching commands")
}

// TestSystemPromptBashEmptyAllowedCommands verifies behavior with empty allowed commands
func TestSystemPromptBashEmptyAllowedCommands(t *testing.T) {
	// Empty allowed commands should fall back to banned commands behavior
	promptCtx := newPromptContext(nil)
	promptCtx.SubagentEnabled = true
	promptCtx.TodoToolsEnabled = false
	llmConfig := &llm.Config{
		AllowedCommands: []string{},
	}

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer := NewRenderer(TemplateFS)
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	require.NoError(t, err, "Failed to render system prompt")

	assert.Contains(t, prompt, "Banned Commands", "Expected system prompt to fall back to 'Banned Commands' section when allowed commands is empty")
	assert.NotContains(t, prompt, "Allowed Commands", "Did not expect system prompt to contain 'Allowed Commands' section when allowed commands is empty")
}

// TestSystemPrompt_WithContexts verifies that provided contexts are properly included in system prompt
func TestSystemPrompt_WithContexts(t *testing.T) {
	contexts := map[string]string{
		"/path/to/project/AGENTS.md":        "# Project Guidelines\nThis is the main project context.",
		"/path/to/project/module/AGENTS.md": "# Module Specific\nThis module handles authentication.",
	}

	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

	assert.Contains(t, prompt, "Here are some useful context to help you solve the user's problem.", "Expected context introduction")

	assert.Contains(t, prompt, `<context filename="/path/to/project/AGENTS.md", dir="/path/to/project">`, "Expected AGENTS.md context with filename")
	assert.Contains(t, prompt, "# Project Guidelines", "Expected AGENTS.md content")
	assert.Contains(t, prompt, "This is the main project context.", "Expected AGENTS.md content")

	assert.Contains(t, prompt, `<context filename="/path/to/project/module/AGENTS.md", dir="/path/to/project/module">`, "Expected module AGENTS.md context with filename")
	assert.Contains(t, prompt, "# Module Specific", "Expected module AGENTS.md content")
	assert.Contains(t, prompt, "This module handles authentication.", "Expected module AGENTS.md content")

	assert.Contains(t, prompt, "</context>", "Expected context closing tags")
}

// TestSystemPrompt_WithEmptyContexts verifies fallback behavior with empty contexts
func TestSystemPrompt_WithEmptyContexts(t *testing.T) {
	emptyContexts := map[string]string{}
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, emptyContexts)

	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected basic kodelet introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")
	assert.NotContains(t, prompt, "Here are some useful context to help you solve the user's problem:", "Should not have context intro when no contexts")
}

// TestSystemPrompt_WithNilContexts verifies fallback behavior with nil contexts
func TestSystemPrompt_WithNilContexts(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, nil)

	assert.Contains(t, prompt, "You are an interactive CLI tool", "Expected basic kodelet introduction")
	assert.Contains(t, prompt, "System Information", "Expected system information section")

	// When nil contexts are passed, it should initialize with empty map
}

// TestSystemPrompt_ContextFormattingEdgeCases tests edge cases in context formatting
func TestSystemPrompt_ContextFormattingEdgeCases(t *testing.T) {
	t.Run("context_with_special_characters", func(t *testing.T) {
		contexts := map[string]string{
			"/path/with spaces/AGENTS.md": "Content with <tags> & special chars: quotes \"test\" and 'test'",
		}

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/path/with spaces/AGENTS.md", dir="/path/with spaces">`, "Expected path with spaces")
		assert.Contains(t, prompt, "Content with <tags> & special chars", "Expected content with special characters")
		assert.Contains(t, prompt, `quotes "test" and 'test'`, "Expected quotes preserved")
	})

	t.Run("empty_context_content", func(t *testing.T) {
		contexts := map[string]string{
			"/empty/AGENTS.md": "",
		}

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

		assert.Contains(t, prompt, `<context filename="/empty/AGENTS.md", dir="/empty">`, "Expected empty context file to be included")
		assert.Contains(t, prompt, "</context>", "Expected context to be properly closed even when empty")
	})

	t.Run("multiple_contexts_ordering", func(t *testing.T) {
		contexts := map[string]string{
			"/z/last.md":   "Last content",
			"/a/first.md":  "First content",
			"/m/middle.md": "Middle content",
		}

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

		// All contexts should be included regardless of order
		assert.Contains(t, prompt, "First content", "Expected first context")
		assert.Contains(t, prompt, "Middle content", "Expected middle context")
		assert.Contains(t, prompt, "Last content", "Expected last context")

		assert.Contains(t, prompt, `<context filename="/a/first.md", dir="/a">`, "Expected first context file")
		assert.Contains(t, prompt, `<context filename="/m/middle.md", dir="/m">`, "Expected middle context file")
		assert.Contains(t, prompt, `<context filename="/z/last.md", dir="/z">`, "Expected last context file")
	})
}

func TestSystemPrompt_UsesConfiguredContextPatterns(t *testing.T) {
	contexts := map[string]string{
		"/path/to/project/README.md": "# README\nProject overview.",
	}
	llmConfig := llm.Config{
		Context: &llm.ContextConfig{
			Patterns: []string{"README.md", "AGENTS.md"},
		},
	}

	prompt := SystemPrompt("claude-sonnet-4-6", llmConfig, contexts)

	assert.Contains(t, prompt, "If the current working directory contains a `README.md` file")
	assert.NotContains(t, prompt, "If the current working directory contains a `AGENTS.md` file")
}

func TestSystemPrompt_TodoToolsFeatureFlag(t *testing.T) {
	t.Run("todo tools disabled by default", func(t *testing.T) {
		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

		assert.NotContains(t, prompt, "# Task Management")
		assert.NotContains(t, prompt, "You have access to the `todo_write` and `todo_read` tools")
		assert.NotContains(t, prompt, "## Task management examples")
	})

	t.Run("todo tools enabled with flag", func(t *testing.T) {
		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{EnableTodos: true}, map[string]string{})

		assert.Contains(t, prompt, "# Task Management")
		assert.Contains(t, prompt, "You have access to the `todo_write` and `todo_read` tools")
		assert.Contains(t, prompt, "## Task management examples")
	})
}

func TestSystemPrompt_CustomTemplate(t *testing.T) {
	t.Run("uses custom template with built-in include", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "sysprompt.tmpl")
		err := os.WriteFile(tmplPath, []byte("CUSTOM-PREFIX\n{{include \"templates/sections/tooling.tmpl\" .}}"), 0o644)
		require.NoError(t, err)

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{Sysprompt: tmplPath}, nil)
		assert.Contains(t, prompt, "CUSTOM-PREFIX")
		assert.Contains(t, prompt, "# Tool Usage")
	})

	t.Run("falls back to default prompt on invalid custom template", func(t *testing.T) {
		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{Sysprompt: "/does/not/exist.tmpl"}, nil)
		assert.Contains(t, prompt, "You are an interactive CLI tool")
		assert.Contains(t, prompt, "# Tool Usage")
	})

	t.Run("supports sysprompt args in custom template", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "sysprompt-args.tmpl")
		err := os.WriteFile(tmplPath, []byte("Project={{default .Args.project \"unknown\"}}"), 0o644)
		require.NoError(t, err)

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{
			Sysprompt:     tmplPath,
			SyspromptArgs: map[string]string{"project": "kodelet"},
		}, nil)
		assert.Contains(t, prompt, "Project=kodelet")
	})
}

func TestSystemPrompt_TemplateSelection(t *testing.T) {
	t.Run("uses codex template for gpt codex model suffix", func(t *testing.T) {
		prompt := SystemPrompt("gpt-5.3-codex", llm.Config{Provider: "openai"}, nil)

		assert.Contains(t, prompt, "Within this context, Codex refers to the open-source agentic coding interface")
		assert.Contains(t, prompt, "## Personality")
	})

	t.Run("uses codex template for codex model variants", func(t *testing.T) {
		prompt := SystemPrompt("gpt-5.3-codex-spark", llm.Config{Provider: "openai"}, nil)

		assert.Contains(t, prompt, "Within this context, Codex refers to the open-source agentic coding interface")
		assert.Contains(t, prompt, "## Personality")
	})

	t.Run("keeps default template for non-codex model", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4.1", llm.Config{Provider: "openai"}, nil)

		assert.Contains(t, prompt, "Tone and Style")
		assert.NotContains(t, prompt, "Within this context, Codex refers to the open-source agentic coding interface")
	})

	t.Run("custom sysprompt still takes precedence", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom-codex.tmpl")
		err := os.WriteFile(tmplPath, []byte("CUSTOM-CODEX-TEMPLATE"), 0o644)
		require.NoError(t, err)

		prompt := SystemPrompt("gpt-5.3-codex", llm.Config{Provider: "openai", Sysprompt: tmplPath}, nil)

		assert.Contains(t, prompt, "CUSTOM-CODEX-TEMPLATE")
		assert.NotContains(t, prompt, "Within this context, Codex refers to the open-source agentic coding interface")
	})
}
