package sysprompt

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

const (
	providerAnthropic       = "anthropic"
	providerOpenAI          = "openai"
	providerOpenAIResponses = "openai-responses"
)

func TestSystemPrompt_ProviderSelection(t *testing.T) {
	t.Run("Anthropic provider uses templates", func(t *testing.T) {
		config := llm.Config{
			Provider: providerAnthropic,
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
			Provider: providerAnthropic,
		}
		contexts := map[string]string{}

		prompt := subagentPrompt("claude-sonnet-46", config, contexts)
		assert.NotEmpty(t, prompt)
		// SubAgentPrompt now delegates to SystemPrompt with IsSubAgent=true
		assert.Contains(t, prompt, "interactive CLI tool")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		prompt := subagentPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		// SubAgentPrompt now delegates to SystemPrompt with IsSubAgent=true
		assert.Contains(t, prompt, "interactive CLI tool")
	})
}

func TestUnifiedSystemPrompt_ForOpenAIProvidersAndPlatforms(t *testing.T) {
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
				Provider: providerOpenAI,
			},
		},
		{
			name:  "OpenAI provider subagent",
			model: "gpt-4",
			config: llm.Config{
				Provider: providerOpenAI,
			},
			useSubagent: true,
		},
		{
			name:  "OpenAI Responses provider",
			model: "gpt-4",
			config: llm.Config{
				Provider: providerOpenAIResponses,
			},
		},
		{
			name:  "OpenAI Responses provider subagent",
			model: "gpt-4",
			config: llm.Config{
				Provider: providerOpenAIResponses,
			},
			useSubagent: true,
		},
		{
			name:  "OpenAI platform",
			model: "gpt-4.1",
			config: llm.Config{
				Provider: providerOpenAI,
				OpenAI: &llm.OpenAIConfig{
					Platform: "openai",
				},
			},
		},
		{
			name:  "Codex platform",
			model: "gpt-5.3-codex",
			config: llm.Config{
				Provider: providerOpenAI,
				OpenAI: &llm.OpenAIConfig{
					Platform: "codex",
				},
			},
			expectsCodex: true,
		},
		{
			name:  "Model suffix codex uses codex template",
			model: "gpt-5.1-codex",
			config: llm.Config{
				Provider: providerOpenAI,
			},
			expectsCodex: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := ""
			if tc.useSubagent {
				prompt = subagentPrompt(tc.model, tc.config, map[string]string{})
			} else {
				prompt = SystemPrompt(tc.model, tc.config, map[string]string{})
			}

			assert.NotEmpty(t, prompt)
			assert.Contains(t, prompt, "interactive CLI tool")
			if tc.expectsCodex {
				assert.Contains(t, prompt, "Your capabilities:")
				assert.NotContains(t, prompt, "# Tool Usage")
			} else {
				assert.NotContains(t, prompt, "coding agent")
			}
		})
	}
}

func TestPromptTemplateConditionalSections(t *testing.T) {
	baseConfig := llm.Config{Provider: providerOpenAI}

	t.Run("Main agent prompt includes subagent usage examples", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "better handled by `subagent`")
	})

	t.Run("Subagent prompt excludes subagent usage examples", func(t *testing.T) {
		prompt := subagentPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)
		assert.NotContains(t, prompt, "## Subagent tool usage examples")
	})

	t.Run("Subagent examples section appears after tools section", func(t *testing.T) {
		prompt := SystemPrompt("gpt-4", baseConfig, map[string]string{})
		assert.NotEmpty(t, prompt)

		toolsPos := strings.Index(prompt, "# Tool Usage")
		subagentExamplesPos := strings.Index(prompt, "## Subagent tool usage examples")
		contextPos := strings.Index(prompt, "# Context")

		assert.Greater(t, subagentExamplesPos, toolsPos)
		assert.Greater(t, contextPos, subagentExamplesPos)
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
		assert.NotContains(t, mainPrompt, "# Task Management")
		assert.NotContains(t, mainPrompt, "You have access to the `todo_write` and `todo_read` tools")

		mainPromptWithTodos := SystemPrompt("gpt-4", llm.Config{Provider: providerOpenAI, EnableTodos: true}, map[string]string{})
		assert.Contains(t, mainPromptWithTodos, "# Task Management")
		assert.Contains(t, mainPromptWithTodos, "You have access to the `todo_write` and `todo_read` tools")

		subagentPrompt := subagentPrompt("gpt-4", llm.Config{Provider: providerOpenAI, EnableTodos: true}, map[string]string{})
		assert.NotContains(t, subagentPrompt, "# Task Management")
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
			Provider:        providerAnthropic,
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
			Provider:        providerAnthropic,
			DisableSubagent: false,
		}

		prompt := SystemPrompt("claude-sonnet-46", config, map[string]string{})

		assert.Contains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.Contains(t, prompt, "Subagent tool usage examples")
	})

	t.Run("OpenAI prompt excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        providerOpenAI,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.Contains(t, prompt, "interactive CLI tool", "Basic prompt content should still exist")
	})

	t.Run("OpenAI prompt includes subagent content when DisableSubagent is false", func(t *testing.T) {
		config := llm.Config{
			Provider:        providerOpenAI,
			DisableSubagent: false,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.Contains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "ALWAYS prioritize `subagent`")
	})

	t.Run("OpenAI Responses API excludes subagent content when DisableSubagent is true", func(t *testing.T) {
		config := llm.Config{
			Provider:        providerOpenAIResponses,
			DisableSubagent: true,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
	})

	t.Run("DisableSubagent does not affect subagent prompt shape for actual subagents", func(t *testing.T) {
		config := llm.Config{
			Provider:        providerOpenAI,
			IsSubAgent:      true,
			DisableSubagent: false,
		}

		prompt := subagentPrompt("gpt-4", config, map[string]string{})

		assert.NotContains(t, prompt, "## Subagent tool usage examples")
		assert.Contains(t, prompt, "# Tool Usage")
		assert.NotContains(t, prompt, "# Task Management")
	})
}
