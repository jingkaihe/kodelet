package sysprompt

import llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"

// RenderRuntimeSections renders system-info, loaded-contexts, and MCP-server sections.
func RenderRuntimeSections(ctx *PromptContext, renderer *Renderer) []string {
	if ctx == nil {
		return nil
	}

	resolvedRenderer := renderer
	if resolvedRenderer == nil {
		resolvedRenderer = defaultRenderer
	}

	return []string{
		ctx.formatSystemInfoWithRenderer(resolvedRenderer),
		ctx.formatContextsWithRenderer(resolvedRenderer),
		ctx.formatMCPServersWithRenderer(resolvedRenderer),
	}
}

// BuildRuntimeContext creates prompt context configured for runtime section rendering.
func BuildRuntimeContext(llmConfig llmtypes.Config, contexts map[string]string) *PromptContext {
	promptCtx := newPromptContext(contexts)
	patterns := llmtypes.DefaultContextPatterns()
	if llmConfig.Context != nil && len(llmConfig.Context.Patterns) > 0 {
		patterns = llmConfig.Context.Patterns
	}
	promptCtx.ActiveContextFile = resolveActiveContextFile(promptCtx.WorkingDirectory, contexts, patterns)
	promptCtx.WithMCPConfig(llmConfig.MCPExecutionMode, llmConfig.MCPWorkspaceDir)
	promptCtx.Args = llmConfig.SyspromptArgs

	return promptCtx
}

// ResolveRendererForConfig resolves the sysprompt renderer from config.
func ResolveRendererForConfig(llmConfig llmtypes.Config) (*Renderer, error) {
	return rendererForConfig(llmConfig)
}
