package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToolExecution holds the normalized result of one tool execution cycle.
type ToolExecution struct {
	// Input is the final tool input after before_tool_call hooks.
	Input string
	// Result is the raw tool result from execution.
	Result tooltypes.ToolResult
	// StructuredResult is the final structured payload after after_tool_call hooks.
	StructuredResult tooltypes.StructuredToolResult
	// RenderedOutput is the CLI-rendered output form of StructuredResult.
	RenderedOutput string
}

// ExecuteTool runs one complete tool lifecycle:
// before_tool_call hooks -> tool execution -> after_tool_call hooks -> rendering.
func ExecuteTool(
	ctx context.Context,
	trigger hooks.Trigger,
	thread llmtypes.Thread,
	state tooltypes.State,
	recipeHooks map[string]llmtypes.HookConfig,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
) ToolExecution {
	blocked, reason, effectiveInput := trigger.TriggerBeforeToolCall(
		ctx, thread, toolName, toolInput, toolCallID, recipeHooks,
	)

	var result tooltypes.ToolResult
	if blocked {
		result = tooltypes.NewBlockedToolResult(toolName, reason)
	} else {
		result = tools.RunTool(ctx, state, toolName, effectiveInput)
	}

	structuredResult := result.StructuredData()
	if modified := trigger.TriggerAfterToolCall(
		ctx, thread, toolName, effectiveInput, toolCallID, structuredResult, recipeHooks,
	); modified != nil {
		structuredResult = *modified
	}

	if rendererRegistry == nil {
		panic("rendererRegistry must not be nil")
	}

	renderedOutput := rendererRegistry.Render(structuredResult)

	return ToolExecution{
		Input:            effectiveInput,
		Result:           result,
		StructuredResult: structuredResult,
		RenderedOutput:   renderedOutput,
	}
}
