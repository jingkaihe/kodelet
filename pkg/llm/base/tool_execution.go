package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToolExecution holds the normalized result of one tool execution cycle.
type ToolExecution struct {
	// Input is the final tool input after tool.call extension handlers.
	Input string
	// Result is the final tool result after extension mutation.
	Result tooltypes.ToolResult
	// StructuredResult is the final structured payload after tool.result extension handlers.
	StructuredResult tooltypes.StructuredToolResult
	// RenderedOutput is the CLI-rendered output form of StructuredResult.
	RenderedOutput string
}

// ExecuteTool runs one complete tool lifecycle:
// extension tool.call -> tool execution -> extension tool.result -> rendering.
func ExecuteTool(
	ctx context.Context,
	thread llmtypes.Thread,
	state tooltypes.State,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
) ToolExecution {
	effectiveInput := toolInput
	blocked := false
	reason := ""

	callContext := buildExtensionCallContext(thread, state)
	runtime := extensionRuntime(thread)
	if runtime != nil {
		decision := runtime.DispatchToolCall(ctx, callContext, toolName, toolInput, toolCallID)
		blocked = decision.Blocked
		reason = decision.Reason
		effectiveInput = decision.Input
	}

	var result tooltypes.ToolResult
	if blocked {
		result = tooltypes.NewBlockedToolResult(toolName, reason)
	} else {
		if thread != nil {
			workingDir := ""
			if state != nil {
				workingDir = state.WorkingDirectory()
			}
			toolContext := tools.ToolContextFromThreadState(thread.GetConfig(), thread.GetConversationID(), workingDir, thread)
			if toolContext.RecipeName == "" {
				if metadataRecipeName, ok := thread.GetMetadata()["recipe_name"].(string); ok {
					toolContext.RecipeName = metadataRecipeName
				}
			}
			ctx = tools.ContextWithToolContext(ctx, toolContext)
		}
		result = tools.RunTool(ctx, state, toolName, effectiveInput)
	}

	structuredResult := result.StructuredData()
	if runtime != nil {
		structuredResult = runtime.DispatchToolResult(ctx, callContext, toolName, effectiveInput, toolCallID, structuredResult)
		result = StructuredResultToolResult{Result: structuredResult, RendererRegistry: rendererRegistry}
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

// StructuredResultToolResult adapts a structured result back to ToolResult so
// post-tool extension mutations affect both rendering and provider LLM input.
type StructuredResultToolResult struct {
	Result           tooltypes.StructuredToolResult
	RendererRegistry *renderers.RendererRegistry
}

func (r StructuredResultToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.GetResult(), r.GetError())
}

func (r StructuredResultToolResult) IsError() bool {
	return !r.Result.Success
}

func (r StructuredResultToolResult) GetError() string {
	return r.Result.Error
}

func (r StructuredResultToolResult) GetResult() string {
	registry := r.RendererRegistry
	if registry == nil {
		registry = renderers.NewRendererRegistry()
	}
	return registry.Render(r.Result)
}

func (r StructuredResultToolResult) StructuredData() tooltypes.StructuredToolResult {
	return r.Result
}

func (r StructuredResultToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	return []tooltypes.ToolResultContentPart{{
		Type: tooltypes.ToolResultContentPartTypeText,
		Text: r.GetResult(),
	}}
}

func (r StructuredResultToolResult) String() string {
	return r.GetResult()
}

func buildExtensionCallContext(thread llmtypes.Thread, state tooltypes.State) extensions.ExtensionCallContext {
	if thread == nil {
		return extensions.ExtensionCallContext{InvokedBy: "main"}
	}

	config := thread.GetConfig()
	workingDir := config.WorkingDirectory
	if state != nil && state.WorkingDirectory() != "" {
		workingDir = state.WorkingDirectory()
	}
	invokedBy := "main"
	if config.IsSubAgent {
		invokedBy = "subagent"
	}

	recipeName := config.RecipeName
	if recipeName == "" {
		if metadataRecipeName, ok := thread.GetMetadata()["recipe_name"].(string); ok {
			recipeName = metadataRecipeName
		}
	}

	return extensions.ExtensionCallContext{
		ConversationID: thread.GetConversationID(),
		CWD:            workingDir,
		Provider:       config.Provider,
		Model:          config.Model,
		Profile:        config.Profile,
		RecipeName:     recipeName,
		InvokedBy:      invokedBy,
	}
}

func extensionRuntime(thread llmtypes.Thread) *extensions.Runtime {
	if thread == nil {
		return nil
	}
	runtime, _ := thread.GetConfig().Extensions.(*extensions.Runtime)
	return runtime
}
