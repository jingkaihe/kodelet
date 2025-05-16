package sysprompt

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

// SubAgentPrompt generates a subagent prompt for the given model
func SubAgentPrompt(model string) string {
	// Create a new prompt context with default values
	ctx := NewPromptContext()

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model)

	// Update the context with the configuration
	UpdateContextWithConfig(ctx, config)

	// Render the subagent prompt
	prompt, err := renderer.RenderSubagentPrompt(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("Error rendering subagent prompt")
	}

	fmt.Println(prompt)

	return prompt
}
