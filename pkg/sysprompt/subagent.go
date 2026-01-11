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
		"isSubagent",
	})

	updateContextWithConfig(promptCtx, config)

	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	var prompt string
	var err error

	provider := strings.ToLower(llmConfig.Provider)
	preset := ""
	if llmConfig.OpenAI != nil {
		preset = strings.ToLower(llmConfig.OpenAI.Preset)
	}

	switch {
	case preset == PresetCodex:
		prompt, err = renderer.RenderCodexPrompt(promptCtx, model)
	case provider == ProviderOpenAI || provider == ProviderOpenAIResponses:
		prompt, err = renderer.RenderOpenAIPrompt(promptCtx)
	default:
		prompt, err = renderer.RenderSubagentPrompt(promptCtx)
	}

	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", provider).Fatal("Error rendering subagent prompt")
	}

	return prompt
}
