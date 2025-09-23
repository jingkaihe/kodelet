package sysprompt

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext(contexts)

	renderer := NewRenderer(TemplateFS)
	config := NewDefaultConfig().WithModel(model)

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Choose prompt rendering method based on provider
	var prompt string
	var err error

	provider := strings.ToLower(llmConfig.Provider)
	switch provider {
	case ProviderOpenAI:
		// Use OpenAI-optimized prompt from embedded template
		prompt, err = renderer.RenderOpenAIPrompt(promptCtx)
	default:
		// Use template-based prompt for Anthropic and unknown providers
		prompt, err = renderer.RenderSystemPrompt(promptCtx)
	}

	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", provider).Fatal("Error rendering system prompt")
	}

	return prompt
}
