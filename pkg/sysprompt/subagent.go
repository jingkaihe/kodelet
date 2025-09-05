package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SubAgentPrompt generates a subagent prompt for the given model
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext()

	if len(contexts) > 0 {
		promptCtx.ContextFiles = contexts
	}

	renderer := NewRenderer(TemplateFS)

	config := NewDefaultConfig().WithModel(model).WithFeatures([]string{
		"todoTools",
	})

	updateContextWithConfig(promptCtx, config)

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	prompt, err := renderer.RenderSubagentPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).Fatal("Error rendering subagent prompt")
	}

	return prompt
}