package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentPrompt generates a subagent prompt for the given model
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]tooltypes.ContextInfo) string {
	// Create a new prompt context with default values
	promptCtx := NewPromptContext()

	// Override context files if provided
	if len(contexts) > 0 {
		contextFiles := make(map[string]string)
		for path, info := range contexts {
			contextFiles[path] = info.Content
		}
		promptCtx.ContextFiles = contextFiles
	}

	// Create a new template renderer
	renderer := NewRenderer(TemplateFS)

	// Create a default config and update with model
	config := NewDefaultConfig().WithModel(model).WithFeatures([]string{
		"todoTools",
	})

	// Update the context with the configuration
	updateContextWithConfig(promptCtx, config)

	// Update the context with LLM configuration
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Render the subagent prompt
	prompt, err := renderer.RenderSubagentPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering subagent prompt")
	}

	return prompt
}
