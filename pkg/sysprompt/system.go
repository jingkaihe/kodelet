package sysprompt

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

type buildOptions struct {
	IsSubagent bool
}

// SystemPrompt generates a system prompt for the given model
func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]string) string {
	return BuildPrompt(model, llmConfig, contexts, buildOptions{IsSubagent: llmConfig.IsSubAgent})
}

// BuildPrompt generates a system prompt for main agent and subagent variants.
func BuildPrompt(model string, llmConfig llm.Config, contexts map[string]string, options buildOptions) string {
	promptCtx := NewPromptContext(contexts)
	patterns := llm.DefaultContextPatterns()
	if llmConfig.Context != nil && len(llmConfig.Context.Patterns) > 0 {
		patterns = llmConfig.Context.Patterns
	}
	promptCtx.ActiveContextFile = ResolveActiveContextFile(promptCtx.WorkingDirectory, contexts, patterns)
	promptCtx.SubagentEnabled = !llmConfig.DisableSubagent && !options.IsSubagent
	promptCtx.TodoToolsEnabled = llmConfig.EnableTodos && !options.IsSubagent
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Add MCP configuration to the prompt context
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)

	renderer, err := RendererForConfig(llmConfig)
	if err != nil {
		logger.G(context.Background()).WithError(err).Warn("failed to load custom sysprompt template, falling back to default")
	}

	prompt, err := renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", llmConfig.Provider).WithField("model", model).Fatal("Error rendering system prompt")
	}

	return prompt
}
