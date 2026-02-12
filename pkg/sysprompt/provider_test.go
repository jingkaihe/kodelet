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
	t.Run("OpenAI prompt loading from embedded template", func(t *testing.T) {
		renderer := NewRenderer(TemplateFS)
		ctx := NewPromptContext(map[string]string{})
		content, err := renderer.RenderOpenAIPrompt(ctx)
		assert.NoError(t, err)
		assert.NotEmpty(t, content)
		assert.Contains(t, content, "coding agent")
	})

	t.Run("OpenAI provider uses embedded OpenAI prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "coding agent")
	})

	t.Run("OpenAI subagent prompt also uses embedded template", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "coding agent")
	})

	t.Run("OpenAI Responses API provider uses embedded OpenAI prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAIResponses,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "coding agent")
	})

	t.Run("OpenAI Responses API subagent prompt also uses embedded template", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAIResponses,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "coding agent")
	})
}

func TestOpenAIConditionalSections(t *testing.T) {
	t.Run("Main agent OpenAI prompt includes Subagent Tool Usage section", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		// Should contain the main agent subagent usage section
		assert.Contains(t, prompt, "## Subagent Tool Usage")
		assert.Contains(t, prompt, "ALWAYS prioritize `subagent` for open-ended code search")
		assert.Contains(t, prompt, "### When to Use Subagent")
		assert.Contains(t, prompt, "### When NOT to Use Subagent")

		// Should not contain subagent response guidelines
		assert.NotContains(t, prompt, "## Subagent Response Guidelines")
		assert.NotContains(t, prompt, "As a subagent, you help with open-ended code search")
	})

	t.Run("Subagent OpenAI prompt includes Subagent Response Guidelines", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		// Should contain the subagent response guidelines
		assert.Contains(t, prompt, "## Subagent Response Guidelines")
		assert.Contains(t, prompt, "As a subagent, you help with open-ended code search")
		assert.Contains(t, prompt, "Focus on comprehensive analysis")
		assert.Contains(t, prompt, "Use absolute file paths")
		assert.Contains(t, prompt, "### Response Structure Examples")

		// Should not contain main agent subagent usage section
		assert.NotContains(t, prompt, "## Subagent Tool Usage")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent` for open-ended code search")
	})

	t.Run("Subagent Tool Usage section appears in correct location for main agent", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		// Find positions of key sections
		sharingProgressPos := strings.Index(prompt, "## Sharing progress updates")
		subagentToolUsagePos := strings.Index(prompt, "## Subagent Tool Usage")
		presentingWorkPos := strings.Index(prompt, "## Presenting your work and final message")

		// Verify order: Sharing progress updates -> Subagent Tool Usage -> Presenting work
		assert.Greater(t, subagentToolUsagePos, sharingProgressPos, "Subagent Tool Usage should come after Sharing progress updates")
		assert.Greater(t, presentingWorkPos, subagentToolUsagePos, "Presenting work should come after Subagent Tool Usage")
	})

	t.Run("Template variables are properly substituted in OpenAI prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		// Should contain actual tool names, not template variables
		assert.Contains(t, prompt, "`subagent`")
		assert.Contains(t, prompt, "`grep_tool`")
		assert.Contains(t, prompt, "`glob_tool`")
		assert.Contains(t, prompt, "`todo_write`")

		// Should not contain unresolved template variables
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

		// Test main agent (should have planning section)
		mainPrompt := SystemPrompt("gpt-4", config, contexts)
		assert.Contains(t, mainPrompt, "## Planning")
		assert.Contains(t, mainPrompt, "You have access to an `todo_write` tool")

		// Test subagent (todoTools is disabled for subagents)
		subagentPrompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.Contains(t, subagentPrompt, "## Planning")
		assert.NotContains(t, subagentPrompt, "You have access to an `todo_write` tool")
	})

	t.Run("Subagent response examples use proper markdown format", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)

		// Should contain markdown-formatted examples, not XML
		assert.Contains(t, prompt, "**Core Authentication**")
		assert.Contains(t, prompt, "**Critical Issues**")
		assert.Contains(t, prompt, "**Recommendations**")
		assert.Contains(t, prompt, "- `/home/user/project/")

		// Should not contain XML-style examples
		assert.NotContains(t, prompt, "<example>")
		assert.NotContains(t, prompt, "</example>")
		assert.NotContains(t, prompt, "<reasoning>")
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

		assert.NotContains(t, prompt, "## Subagent Tool Usage")
		assert.NotContains(t, prompt, "ALWAYS prioritize `subagent`")
		assert.NotContains(t, prompt, "### When to Use Subagent")
		assert.Contains(t, prompt, "coding agent", "Basic prompt content should still exist")
	})

	t.Run("OpenAI prompt includes subagent content when DisableSubagent is false", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAI,
			DisableSubagent: false,
		}

		prompt := SystemPrompt("gpt-4", config, map[string]string{})

		assert.Contains(t, prompt, "## Subagent Tool Usage")
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

	t.Run("DisableSubagent does not affect subagent response guidelines for actual subagents", func(t *testing.T) {
		config := llm.Config{
			Provider:        ProviderOpenAI,
			IsSubAgent:      true,
			DisableSubagent: false,
		}

		prompt := SubAgentPrompt("gpt-4", config, map[string]string{})

		assert.Contains(t, prompt, "## Subagent Response Guidelines")
		assert.NotContains(t, prompt, "## Subagent Tool Usage")
	})
}
