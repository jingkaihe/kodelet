package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	// Create a new prompt context with default values
	promptCtx := NewPromptContext()

	// Override context files if provided
	if len(contexts) > 0 {
		promptCtx.ContextFiles = contexts
	}

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model)

	// Update the context with the configuration
	updateContextWithConfig(promptCtx, config)

	// Update the context with LLM configuration
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Render the system prompt
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering system prompt")
	}

	return prompt
}
