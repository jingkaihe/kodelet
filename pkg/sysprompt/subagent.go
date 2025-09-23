package sysprompt

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SubAgentPrompt generates a subagent prompt for the given model
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext(contexts)

	renderer := NewRenderer(TemplateFS)

	config := NewDefaultConfig().WithModel(model).WithFeatures([]string{
		"todoTools",
	})

	updateContextWithConfig(promptCtx, config)

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Choose prompt rendering method based on provider
	var prompt string
	var err error

	provider := strings.ToLower(llmConfig.Provider)
	switch provider {
	case ProviderOpenAI:
		// Use OpenAI-optimized prompt from embedded template for subagent as well
		prompt, err = renderer.RenderOpenAIPrompt(promptCtx)
	default:
		// Use template-based subagent prompt for Anthropic and unknown providers
		prompt, err = renderer.RenderSubagentPrompt(promptCtx)
	}

	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", provider).Fatal("Error rendering subagent prompt")
	}

	return prompt
}
