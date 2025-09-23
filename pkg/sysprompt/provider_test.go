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

		prompt := SystemPrompt("claude-sonnet-4", config, contexts)
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

		prompt := SubAgentPrompt("claude-sonnet-4", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "AI SWE Agent")
	})

	t.Run("Unknown provider defaults to Anthropic templates", func(t *testing.T) {
		config := llm.Config{
			Provider: "unknown-provider",
		}
		contexts := map[string]string{}

		prompt := SubAgentPrompt("some-model", config, contexts)
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "AI SWE Agent")
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
}
