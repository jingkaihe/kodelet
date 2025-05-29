package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// SubAgentPrompt generates a subagent prompt for the given model
func SubAgentPrompt(model string) string {
	// Create a new prompt context with default values
	promptCtx := NewPromptContext()

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model)

	// Update the context with the configuration
	UpdateContextWithConfig(promptCtx, config)

	// Render the subagent prompt
	prompt, err := renderer.RenderSubagentPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering subagent prompt")
	}

	return prompt
}
