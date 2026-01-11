package sysprompt

import (
	"context"
	"strings"

	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	promptCtx := NewPromptContext(contexts)

	renderer := NewRenderer(TemplateFS)
	config := NewDefaultConfig().WithModel(model)

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Add MCP configuration to the prompt context
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)

	var prompt string
	var err error

	provider := strings.ToLower(llmConfig.Provider)
	switch provider {
	case ProviderCodex:
		// Use Codex-specific system prompt from embedded files
		prompt, err = codexpreset.GetSystemPromptForModel(model)
	case ProviderOpenAI, ProviderOpenAIResponses:
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
