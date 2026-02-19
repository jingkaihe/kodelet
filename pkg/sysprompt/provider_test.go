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

		prompt := SystemPrompt("claude-sonnet-46", config, contexts)
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

		prompt := SubAgentPrompt("claude-sonnet-46", config, contexts)
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

func TestUnifiedSystemPrompt_ForOpenAIProvidersAndPresets(t *testing.T) {
	testCases := []struct {
		name         string
		model        string
		config       llm.Config
		useSubagent  bool
		expectsCodex bool
	}{
		{
			name:  "OpenAI provider",
			model: "gpt-4",
			config: llm.Config{
				Provider: ProviderOpenAI,
			},
		},
		{
			name:  "OpenAI provider subagent",
			model: "gpt-4",
			config: llm.Config{
				Provider: ProviderOpenAI,
			},
			useSubagent: true,
		},
		{
			name:  "OpenAI Responses provider",
			model: "gpt-4",
			config: llm.Config{
				Provider: ProviderOpenAIResponses,
			},
		},
		{
			name:  "OpenAI Responses provider subagent",
			model: "gpt-4",
			config: llm.Config{
				Provider: ProviderOpenAIResponses,
			},
			useSubagent: true,
		},
		{
			name:  "OpenAI preset",
			model: "gpt-4.1",
			config: llm.Config{
				Provider: ProviderOpenAI,
				OpenAI: &llm.OpenAIConfig{
					Preset: "openai",
				},
			},
		},
		{
			name:  "Codex preset",
			model: "gpt-5.3-codex",
			config: llm.Config{
				Provider: ProviderOpenAI,
				OpenAI: &llm.OpenAIConfig{
					Preset: "codex",
				},
			},
			expectsCodex: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := ""
			if tc.useSubagent {
				prompt = SubAgentPrompt(tc.model, tc.config, map[string]string{})
			} else {
				prompt = SystemPrompt(tc.model, tc.config, map[string]string{})
			}

			assert.NotEmpty(t, prompt)
			assert.Contains(t, prompt, "interactive CLI tool")
			assert.NotContains(t, prompt, "coding agent")
			if tc.expectsCodex {
				assert.NotContains(t, prompt, "You are Codex, based on GPT-5")
			}
		})
	}
}

func TestPromptTemplateConditionalSections(t *testing.T) {
	baseConfig := llm.Config{Provider: ProviderOpenAI}

	t.Run("Main agent prompt includes subagent usage examples", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "The user's request is nuanced and cannot be described in regex")
	})

	t.Run("Subagent prompt excludes subagent usage examples", func(t *testing.T) {
		prompt := SubAgentPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.NotContains(t, prompt, "## Subagent tool usage examples")
	})

	t.Run("Subagent examples section appears after tools section", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)

		toolsPos := strings.Index(prompt, "# !!!VERY IMPORTANT!!! Tool Usage")
		subagentExamplesPos := strings.Index(prompt, "## Subagent tool usage examples")
		taskManagementPos := strings.Index(prompt, "# Task Management")

		assert.Greater(t, subagentExamplesPos, toolsPos)
		assert.Greater(t, taskManagementPos, subagentExamplesPos)
	})

	t.Run("Template variables are substituted", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "`subagent`")
		assert.Contains(t, prompt, "`grep_tool`")
		assert.Contains(t, prompt, "`glob_tool`")
		assert.NotContains(t, prompt, "{{.ToolNames.subagent}}")
		assert.NotContains(t, prompt, "{{.ToolNames.grep}}")
		assert.NotContains(t, prompt, "{{.ToolNames.glob}}")
		assert.NotContains(t, prompt, "{{.ToolNames.todo_write}}")
	})

	t.Run("Task management content depends on todo tools flag", func(t *testing.T) {
		mainPrompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.Contains(t, mainPrompt, "# Task Management")
		assert.NotContains(t, mainPrompt, "You have access to the `todo_write` and `todo_read` tools")

		mainPromptWithTodos := SystemPrompt("gpt-4", llm.Config{Provider: ProviderOpenAI, EnableTodos: true}, map[string]string{})
		assert.Contains(t, mainPromptWithTodos, "You have access to the `todo_write` and `todo_read` tools")

		subagentPrompt := SubAgentPrompt("gpt-4", llm.Config{Provider: ProviderOpenAI, EnableTodos: true}, map[string]string{})
		assert.Contains(t, subagentPrompt, "# Task Management")
		assert.NotContains(t, subagentPrompt, "You have access to the `todo_write` and `todo_read` tools")
	})

	t.Run("Subagent examples use XML format", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "<example>")
		assert.Contains(t, prompt, "</example>")
		assert.Contains(t, prompt, "<reasoning>")
	})
}

func TestDisableSubagent_SystemPrompt(t *testing.T) {
	t.Run("Anthropic prompt excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderAnthropic,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("claude-sonnet-46", config, map[string]string{})

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

		prompt := SystemPrompt("claude-sonnet-46", config, map[string]string{})

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

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
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
