package sysprompt

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext(contexts)
	patterns := llm.DefaultContextPatterns()
	if llmConfig.Context != nil && len(llmConfig.Context.Patterns) > 0 {
		patterns = llmConfig.Context.Patterns
	}
	promptCtx.ActiveContextFile = ResolveActiveContextFile(promptCtx.WorkingDirectory, contexts, patterns)

	renderer := NewRenderer(TemplateFS)
	config := NewDefaultConfig().WithModel(model)

	// Add isSubagent feature when running as subagent to exclude subagent tool usage examples
	if llmConfig.IsSubAgent {
		config.EnabledFeatures = append(config.EnabledFeatures, "isSubagent")
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Add MCP configuration to the prompt context
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)

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
		prompt, err = renderer.RenderSystemPrompt(promptCtx)
	}

	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", provider).Fatal("Error rendering system prompt")
	}

	return prompt
}
