package sysprompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemPrompt(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	assert.Contains(t, prompt, "You are an interactive CLI tool")
	assert.Contains(t, prompt, "Tone and Style")
	assert.Contains(t, prompt, "Context")
	assert.Contains(t, prompt, "System Information")
}

func TestSystemPrompt_WithContexts(t *testing.T) {
	contexts := map[string]string{
		"/path/to/project/AGENTS.md":        "# Project Guidelines\nThis is the main project context.",
		"/path/to/project/module/AGENTS.md": "# Module Specific\nThis module handles authentication.",
	}

	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, contexts)

	assert.Contains(t, prompt, "Here are some useful context to help you solve the user's problem.")
	assert.Contains(t, prompt, `<context filename="/path/to/project/AGENTS.md", dir="/path/to/project">`)
	assert.Contains(t, prompt, `<context filename="/path/to/project/module/AGENTS.md", dir="/path/to/project/module">`)
}

func TestSystemPrompt_WithEmptyContexts(t *testing.T) {
	prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{}, map[string]string{})

	assert.Contains(t, prompt, "You are an interactive CLI tool")
	assert.NotContains(t, prompt, "Here are some useful context to help you solve the user's problem:")
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

func TestSystemPrompt_CustomTemplate(t *testing.T) {
	t.Run("uses custom template with built-in include", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "sysprompt.tmpl")
		err := os.WriteFile(tmplPath, []byte("CUSTOM-PREFIX\n{{include \"templates/sections/behavior.tmpl\" .}}"), 0o644)
		require.NoError(t, err)

		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{Sysprompt: tmplPath}, nil)
		assert.Contains(t, prompt, "CUSTOM-PREFIX")
		assert.Contains(t, prompt, "# Tone and Style")
	})

	t.Run("falls back to default prompt on invalid custom template", func(t *testing.T) {
		prompt := SystemPrompt("claude-sonnet-4-6", llm.Config{Sysprompt: "/does/not/exist.tmpl"}, nil)
		assert.Contains(t, prompt, "You are an interactive CLI tool")
		assert.Contains(t, prompt, "# Context")
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

		assert.Contains(t, prompt, "Your capabilities:")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("uses codex template for codex model variants", func(t *testing.T) {
		prompt := SystemPrompt("gpt-5.3-codex-spark", llm.Config{Provider: "openai"}, nil)

		assert.Contains(t, prompt, "Your capabilities:")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("keeps default template for non-codex model", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4.1", llm.Config{Provider: "openai"}, nil)

		assert.Contains(t, prompt, "Tone and Style")
		assert.NotContains(t, prompt, "Your capabilities:")
	})

	t.Run("custom sysprompt still takes precedence", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmplPath := filepath.Join(tmpDir, "custom-codex.tmpl")
		err := os.WriteFile(tmplPath, []byte("CUSTOM-CODEX-TEMPLATE"), 0o644)
		require.NoError(t, err)

		prompt := SystemPrompt("gpt-5.3-codex", llm.Config{Provider: "openai", Sysprompt: tmplPath}, nil)

		assert.Contains(t, prompt, "CUSTOM-CODEX-TEMPLATE")
		assert.NotContains(t, prompt, "Your capabilities:")
	})
}
