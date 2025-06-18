package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string) string {
	// Create a new prompt context with default values
	promptCtx := NewPromptContext()

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model)

	// Update the context with the configuration
	updateContextWithConfig(promptCtx, config)

	// Render the system prompt
	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering system prompt")
	}

	return prompt
}
