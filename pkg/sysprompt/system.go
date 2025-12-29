package sysprompt

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(ctx context.Context, model string, llmConfig llm.Config, contexts map[string]string) (string, error) {
	promptCtx := NewPromptContext(contexts)

	renderer := NewRenderer(TemplateFS)
	config := NewDefaultConfig().WithModel(model)

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Add MCP configuration to the prompt context
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)

	var basePrompt string
	var err error

	// If custom prompt is configured, use it
	if llmConfig.CustomPrompt.IsConfigured() {
		customRenderer := NewCustomPromptRenderer(GetFragmentDirs())
		basePrompt, err = customRenderer.RenderCustomPrompt(
			ctx,
			llmConfig.CustomPrompt.TemplatePath,
			llmConfig.CustomPrompt.RecipeName,
			llmConfig.CustomPrompt.Arguments,
			promptCtx,
		)
		if err != nil {
			return "", errors.Wrap(err, "failed to render custom system prompt")
		}
		// For custom prompts, append system info, contexts, and MCP servers
		basePrompt += promptCtx.FormatSystemInfo()
		basePrompt += promptCtx.FormatContexts()
		basePrompt += promptCtx.FormatMCPServers()
	} else {
		// Default behavior - render from embedded templates
		provider := strings.ToLower(llmConfig.Provider)
		switch provider {
		case ProviderOpenAI:
			basePrompt, err = renderer.RenderOpenAIPrompt(promptCtx)
		default:
			basePrompt, err = renderer.RenderSystemPrompt(promptCtx)
		}
		if err != nil {
			return "", errors.Wrap(err, "failed to render system prompt")
		}
	}

	return basePrompt, nil
}
