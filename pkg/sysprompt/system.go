package sysprompt

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

type buildOptions struct {
	IsSubagent bool
}

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llmtypes.Config, contexts map[string]string) string {
	return buildPrompt(model, llmConfig, contexts, buildOptions{IsSubagent: llmConfig.IsSubAgent})
}

func buildPrompt(model string, llmConfig llmtypes.Config, contexts map[string]string, options buildOptions) string {
	promptCtx := BuildRuntimeContext(llmConfig, contexts)
	promptCtx.SubagentEnabled = !llmConfig.DisableSubagent && !options.IsSubagent
	promptCtx.TodoToolsEnabled = llmConfig.EnableTodos && !options.IsSubagent
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	renderer, err := rendererForConfig(llmConfig)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to load custom sysprompt template, falling back to default")
	}

	templatePath := promptTemplatePath(model, llmConfig)
	prompt, err := renderer.RenderTemplate(templatePath, promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).
			WithField("provider", llmConfig.Provider).
			WithField("model", model).
			WithField("template", templatePath).
			Fatal("Error rendering system prompt")
	}

	return prompt
}

func promptTemplatePath(model string, llmConfig llmtypes.Config) string {
	if strings.TrimSpace(llmConfig.Sysprompt) != "" {
		return SystemTemplate
	}

	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(normalizedModel, "codex") {
		return CodexTemplate
	}

	return SystemTemplate
}
