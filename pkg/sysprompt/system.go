package sysprompt

import (
	"context"

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

	if llmConfig.EnableTodos {
		config.EnabledFeatures = append(config.EnabledFeatures, "todoTools")
	}

	// Add isSubagent feature and remove todoTools when running as subagent
	if llmConfig.IsSubAgent {
		config.EnabledFeatures = append(config.EnabledFeatures, "isSubagent")
		filtered := make([]string, 0, len(config.EnabledFeatures))
		for _, f := range config.EnabledFeatures {
			if f != "todoTools" {
				filtered = append(filtered, f)
			}
		}
		config.EnabledFeatures = filtered
	}

	// Remove subagent feature when DisableSubagent is set
	if llmConfig.DisableSubagent {
		filtered := make([]string, 0, len(config.EnabledFeatures))
		for _, f := range config.EnabledFeatures {
			if f != "subagent" {
				filtered = append(filtered, f)
			}
		}
		config.EnabledFeatures = filtered
	}

	updateContextWithConfig(promptCtx, config)
	promptCtx.BashAllowedCommands = llmConfig.AllowedCommands

	// Add MCP configuration to the prompt context
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)

	var prompt string
	var err error

	provider := llmConfig.Provider
	prompt, err = renderer.RenderSystemPrompt(promptCtx)
	if err != nil {
		ctx := context.Background()
		log := logger.G(ctx)
		log.WithError(err).WithField("provider", provider).Fatal("Error rendering system prompt")
	}

	return prompt
}
