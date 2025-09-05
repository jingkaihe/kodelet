package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext()

	if len(contexts) > 0 {
		promptCtx.ContextFiles = contexts
	}

	renderer := NewRenderer(TemplateFS)
	config := NewDefaultConfig().WithModel(model)

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering system prompt")
	}

	return prompt
}