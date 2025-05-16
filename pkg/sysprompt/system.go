package sysprompt

import (
	"github.com/sirupsen/logrus"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string) string {
	// Create a new prompt context with default values
	ctx := NewPromptContext()

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model)

	// Update the context with the configuration
	UpdateContextWithConfig(ctx, config)

	// Render the system prompt
	prompt, err := renderer.RenderSystemPrompt(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("Error rendering system prompt")
	}

	return prompt
}
