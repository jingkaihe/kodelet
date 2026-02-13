package sysprompt

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestSystemPrompt_ProviderSelection(t *testing.T) {
	t.Run("Anthropic provider uses templates", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderAnthropic,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("claude-sonnet-45", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
	})
}

func TestSubAgentPrompt_ProviderSelection(t *testing.T) {
	t.Run("Anthropic provider uses templates", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderAnthropic,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("claude-sonnet-45", config, contexts)
		assert.NotEmpty(t, prompt)
		// SubAgentPrompt now delegates to SystemPrompt with IsSubAgent=true
		assert.Contains(t, prompt, "interactive CLI tool")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		// SubAgentPrompt now delegates to SystemPrompt with IsSubAgent=true
		assert.Contains(t, prompt, "interactive CLI tool")
	})
}

func TestOpenAIPromptLoading(t *testing.T) {
	t.Run("OpenAI provider uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("OpenAI subagent prompt uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("OpenAI Responses API provider uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAIResponses,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("OpenAI Responses API subagent prompt uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAIResponses,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("OpenAI preset uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
			OpenAI: &llm.OpenAIConfig{
				Preset: "openai",
			},
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4.1", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
	})

	t.Run("Codex preset uses normal system prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
			OpenAI: &llm.OpenAIConfig{
				Preset: "codex",
			},
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-5.3-codex", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "interactive CLI tool")
		assert.NotContains(t, prompt, "coding agent")
		assert.NotContains(t, prompt, "You are Codex, based on GPT-5")
	})
}

func TestOpenAIConditionalSections(t *testing.T) {
	t.Run("Main agent OpenAI prompt includes subagent usage examples", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		assert.Contains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "The user's request is nuanced and cannot be described in regex")
		assert.NotContains(t, prompt, "## Subagent Response Guidelines")
	})

	t.Run("Subagent OpenAI prompt does not include subagent tool usage examples", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.NotContains(t, prompt, "## Subagent Response Guidelines")
	})

	t.Run("Subagent examples section appears after tools section", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		toolsPos := strings.Index(prompt, "# Tools")
		subagentExamplesPos := strings.Index(prompt, "## Subagent tool usage examples")
		taskManagementPos := strings.Index(prompt, "# Task Management")

		assert.Greater(t, subagentExamplesPos, toolsPos, "Subagent examples should come after tools")
		assert.Greater(t, taskManagementPos, subagentExamplesPos, "Task management should come after subagent examples")
	})

	t.Run("Template variables are properly substituted in OpenAI prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		assert.Contains(t, prompt, "`subagent`")
		assert.Contains(t, prompt, "`grep_tool`")
		assert.Contains(t, prompt, "`glob_tool`")
		assert.Contains(t, prompt, "`todo_write`")

		assert.NotContains(t, prompt, "{{.ToolNames.subagent}}")
		assert.NotContains(t, prompt, "{{.ToolNames.grep}}")
		assert.NotContains(t, prompt, "{{.ToolNames.glob}}")
		assert.NotContains(t, prompt, "{{.ToolNames.todo_write}}")
	})

	t.Run("Planning section is conditional on todoToolsEnabled for OpenAI prompts", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		mainPrompt := SystemPrompt("gpt-4", config, contexts)
		assert.Contains(t, mainPrompt, "# Task Management")
		assert.Contains(t, mainPrompt, "You have access to the `todo_write` and `todo_read` tools")

		subagentPrompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.Contains(t, subagentPrompt, "# Task Management")
		assert.NotContains(t, subagentPrompt, "You have access to the `todo_write` and `todo_read` tools")
	})

	t.Run("Subagent examples use XML format", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		assert.Contains(t, prompt, "<example>")
		assert.Contains(t, prompt, "</example>")
		assert.Contains(t, prompt, "<reasoning>")
		assert.NotContains(t, prompt, "**Core Authentication**")
	})
}

func TestDisableSubagent_SystemPrompt(t *testing.T) {
	t.Run("Anthropic prompt excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderAnthropic,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("claude-sonnet-45", config, map[string]string{})

		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.NotContains(t, prompt, "Subagent tool usage examples")
		assert.Contains(t, prompt, "interactive CLI tool", "Basic prompt content should still exist")
		assert.Contains(t, prompt, "parallel tool calling", "Non-subagent tool guidance should remain")
	})

	t.Run("Anthropic prompt includes subagent content when DisableSubagent is false", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderAnthropic,
			DisableSubagent: false,
		}

		prompt := SystemPrompt("claude-sonnet-45", config, map[string]string{})

		assert.Contains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.Contains(t, prompt, "Subagent tool usage examples")
	})

	t.Run("OpenAI prompt excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAI,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.Contains(t, prompt, "interactive CLI tool", "Basic prompt content should still exist")
	})

	t.Run("OpenAI prompt includes subagent content when DisableSubagent is false", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAI,
			DisableSubagent: false,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.Contains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "ALWAYS prioritize `subagent`")
	})

	t.Run("OpenAI Responses API excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAIResponses,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent Tool Usage")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
	})

	t.Run("DisableSubagent does not affect subagent prompt shape for actual subagents", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAI,
			IsSubAgent:      true,
			DisableSubagent: false,
		}

		prompt := SubAgentPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "# !!!VERY IMPORTANT!!! Tool Usage")
		assert.Contains(t, prompt, "# Task Management")
	})
}
