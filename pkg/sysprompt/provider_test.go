package sysprompt

import (
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

		// This should succeed and use template-based rendering
		prompt := SystemPrompt("claude-sonnet-4", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains template-based content
		assert.Contains(t, prompt, "interactive CLI tool")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		// This should succeed and default to template-based rendering
		prompt := SystemPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains template-based content
		assert.Contains(t, prompt, "interactive CLI tool")
	})
}

func TestSubAgentPrompt_ProviderSelection(t *testing.T) {
	t.Run("Anthropic provider uses templates", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderAnthropic,
		}
		contexts := map[string]string{}

		// This should succeed and use template-based rendering
		prompt := SubAgentPrompt("claude-sonnet-4", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains subagent template-based content
		assert.Contains(t, prompt, "AI SWE Agent")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		// This should succeed and default to template-based rendering
		prompt := SubAgentPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains subagent template-based content
		assert.Contains(t, prompt, "AI SWE Agent")
	})
}

func TestOpenAIPromptLoading(t *testing.T) {
	t.Run("OpenAI prompt loading from embedded template", func(t *testing.T) {
		// Test the renderer's OpenAI prompt loading capability
		renderer := NewRenderer(TemplateFS)
		
		// This should always succeed since the template is embedded
		content, err := renderer.loadOpenAIPrompt()
		assert.NoError(t, err)
		assert.NotEmpty(t, content)
		assert.Contains(t, content, "coding agent")
	})

	t.Run("OpenAI provider uses embedded OpenAI prompt", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		// This should always work since the template is embedded
		prompt := SystemPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains OpenAI-specific content
		assert.Contains(t, prompt, "coding agent")
	})

	t.Run("OpenAI subagent prompt also uses embedded template", func(t *testing.T) {
		config := llm.Config{
			Provider: ProviderOpenAI,
		}
		contexts := map[string]string{}

		// Subagent should also use the same OpenAI template
		prompt := SubAgentPrompt("gpt-4", config, contexts)
		assert.NotEmpty(t, prompt)
		// Verify it contains OpenAI-specific content
		assert.Contains(t, prompt, "coding agent")
	})
}