package base

import (
	"context"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToolExecution holds the normalized result of one tool execution cycle.
type ToolExecution struct {
	// Err is a transport or environment failure that makes the tool outcome unusable.
	Err error
	// Input is the final tool input after tool.call extension handlers.
	Input string
	// Result is the final tool result after extension mutation.
	Result tooltypes.ToolResult
	// StructuredResult is the final structured payload after tool.result extension handlers.
	StructuredResult tooltypes.StructuredToolResult
	// RenderedOutput is the CLI-rendered output form of StructuredResult.
	RenderedOutput string
}

// ExecuteEnvironmentTool runs one complete tool lifecycle through the thread's pinned agent environment.
func ExecuteEnvironmentTool(
	ctx context.Context,
	thread llmtypes.Thread,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
) ToolExecution {
	return executeEnvironmentTool(ctx, thread, rendererRegistry, toolName, toolInput, toolCallID, nil)
}

// ExecuteEnvironmentToolWithHandler runs a tool through the pinned environment and forwards transient updates.
func ExecuteEnvironmentToolWithHandler(
	ctx context.Context,
	thread llmtypes.Thread,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
	handler llmtypes.MessageHandler,
) ToolExecution {
	return executeEnvironmentTool(ctx, thread, rendererRegistry, toolName, toolInput, toolCallID, handler)
}

func executeEnvironmentTool(
	ctx context.Context,
	thread llmtypes.Thread,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
	handler llmtypes.MessageHandler,
) ToolExecution {
	environment := EnvironmentForThread(thread)
	if environment == nil || !environment.IsOpen() {
		return executeTool(ctx, thread, threadState(thread), rendererRegistry, toolName, toolInput, toolCallID, handler)
	}

	if rendererRegistry == nil {
		panic("rendererRegistry must not be nil")
	}
	if definition, ok := environment.Manifest().ToolDefinition(toolName); ok && definition.Placement == agentenv.ToolPlacementControlPlane {
		return executeControlPlaneTool(ctx, thread, environment, rendererRegistry, toolName, toolInput, toolCallID, handler)
	}
	if tools.IsControlPlaneTool(toolName) {
		return executeControlPlaneTool(ctx, thread, environment, rendererRegistry, toolName, toolInput, toolCallID, handler)
	}

	if thread != nil {
		workingDir := environment.Manifest().WorkingDirectory
		toolContext := tools.ToolContextFromThreadState(thread.GetConfig(), thread.GetConversationID(), workingDir, thread)
		if toolContext.RecipeName == "" {
			if metadataRecipeName, ok := thread.GetMetadata()["recipe_name"].(string); ok {
				toolContext.RecipeName = metadataRecipeName
			}
		}
		ctx = tools.ContextWithToolContext(ctx, toolContext)
	}

	var updateMu sync.Mutex
	acceptUpdates := true
	var updateSink agentenv.ToolUpdateSink
	if updateHandler, ok := handler.(llmtypes.ToolUpdateMessageHandler); ok {
		updateSink = func(update agentenv.ToolUpdate) {
			if update.Result == nil {
				return
			}
			updateMu.Lock()
			defer updateMu.Unlock()
			if !acceptUpdates {
				return
			}
			result := update.Result
			if update.Modified {
				result = StructuredResultToolResult{Result: update.StructuredResult, RendererRegistry: rendererRegistry}
			}
			updateHandler.HandleToolUpdate(toolCallID, toolName, result)
		}
	}

	execution, err := environment.ExecuteTool(ctx, agentenv.ToolRequest{
		Name:       toolName,
		Input:      toolInput,
		ToolCallID: toolCallID,
	}, updateSink)
	if updateSink != nil {
		updateMu.Lock()
		acceptUpdates = false
		updateMu.Unlock()
	}
	if err != nil {
		return ToolExecution{Err: err, Input: toolInput}
	}

	result := execution.Result
	if result == nil {
		result = tooltypes.BaseToolResult{Error: "agent environment returned no tool result"}
	}
	if execution.Modified {
		result = StructuredResultToolResult{Result: execution.StructuredResult, RendererRegistry: rendererRegistry}
	}

	return ToolExecution{
		Input:            execution.Input,
		Result:           result,
		StructuredResult: execution.StructuredResult,
		RenderedOutput:   rendererRegistry.Render(execution.StructuredResult),
	}
}

func executeControlPlaneTool(
	ctx context.Context,
	thread llmtypes.Thread,
	environment agentenv.Environment,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
	handler llmtypes.MessageHandler,
) ToolExecution {
	tool, ok := tools.ControlPlaneTool(toolName)
	if !ok {
		result := tooltypes.BaseToolResult{Error: "control-plane tool is not registered: " + toolName}
		structured := normalizeStructuredToolResult(toolName, result.StructuredData())
		return ToolExecution{
			Input:            toolInput,
			Result:           result,
			StructuredResult: structured,
			RenderedOutput:   rendererRegistry.Render(structured),
		}
	}

	request := agentenv.ToolRequest{Name: toolName, Input: toolInput, ToolCallID: toolCallID}
	callDecision, err := environment.DispatchToolCall(ctx, request)
	if err != nil {
		return ToolExecution{Err: err, Input: toolInput}
	}
	effectiveInput := callDecision.Input
	if effectiveInput == "" {
		effectiveInput = toolInput
	}

	state := controlPlaneToolState(thread, environment)
	ctx = contextWithThreadToolContext(ctx, thread, environment.Manifest().WorkingDirectory)

	var result tooltypes.ToolResult
	if callDecision.Blocked {
		result = tooltypes.NewBlockedToolResult(toolName, callDecision.Reason)
	} else {
		var updateMu sync.Mutex
		acceptUpdates := true
		var onUpdate tooltypes.ToolUpdateCallback
		if updateHandler, ok := handler.(llmtypes.ToolUpdateMessageHandler); ok && environment.CanStreamToolUpdates() {
			onUpdate = func(partialResult tooltypes.ToolResult) {
				if partialResult == nil {
					return
				}
				updateMu.Lock()
				defer updateMu.Unlock()
				if !acceptUpdates {
					return
				}

				structured := normalizeStructuredToolResult(toolName, partialResult.StructuredData())
				decision, err := environment.DispatchToolUpdate(ctx, agentenv.ToolOutputRequest{
					Name:             toolName,
					Input:            effectiveInput,
					ToolCallID:       toolCallID,
					StructuredResult: structured,
				})
				if err != nil {
					return
				}
				if !decision.Accepted {
					return
				}
				if decision.Modified {
					partialResult = StructuredResultToolResult{Result: decision.StructuredResult, RendererRegistry: rendererRegistry}
				}
				updateHandler.HandleToolUpdate(toolCallID, toolName, partialResult)
			}
		}

		result = tools.RunToolImplementationWithUpdates(ctx, state, tool, effectiveInput, onUpdate)
		if onUpdate != nil {
			updateMu.Lock()
			acceptUpdates = false
			updateMu.Unlock()
		}
	}

	structured := normalizeStructuredToolResult(toolName, result.StructuredData())
	outputDecision, err := environment.DispatchToolResult(ctx, agentenv.ToolOutputRequest{
		Name:             toolName,
		Input:            effectiveInput,
		ToolCallID:       toolCallID,
		StructuredResult: structured,
	})
	if err != nil {
		return ToolExecution{Err: err, Input: effectiveInput}
	}
	structured = outputDecision.StructuredResult
	if outputDecision.Modified {
		result = StructuredResultToolResult{Result: structured, RendererRegistry: rendererRegistry}
	}

	return ToolExecution{
		Input:            effectiveInput,
		Result:           result,
		StructuredResult: structured,
		RenderedOutput:   rendererRegistry.Render(structured),
	}
}

func contextWithThreadToolContext(ctx context.Context, thread llmtypes.Thread, workingDirectory string) context.Context {
	if thread == nil {
		return ctx
	}
	toolContext := tools.ToolContextFromThreadState(thread.GetConfig(), thread.GetConversationID(), workingDirectory, thread)
	if toolContext.RecipeName == "" {
		if metadataRecipeName, ok := thread.GetMetadata()["recipe_name"].(string); ok {
			toolContext.RecipeName = metadataRecipeName
		}
	}
	return tools.ContextWithToolContext(ctx, toolContext)
}

func normalizeStructuredToolResult(toolName string, result tooltypes.StructuredToolResult) tooltypes.StructuredToolResult {
	if result.ToolName == "" || result.ToolName == "unknown" {
		result.ToolName = toolName
	}
	return result
}

func controlPlaneToolState(thread llmtypes.Thread, environment agentenv.Environment) tooltypes.State {
	if provider, ok := environment.(agentenv.StateProvider); ok {
		if state := provider.State(); state != nil {
			return state
		}
	}
	config := llmtypes.Config{}
	if thread != nil {
		config = thread.GetConfig()
	}
	return &controlPlaneState{
		tools:      tools.ControlPlaneTools(),
		config:     config,
		workingDir: environment.Manifest().WorkingDirectory,
	}
}

type controlPlaneState struct {
	tools      []tooltypes.Tool
	config     llmtypes.Config
	workingDir string
}

func (s *controlPlaneState) BasicTools() []tooltypes.Tool {
	return append([]tooltypes.Tool(nil), s.tools...)
}

func (s *controlPlaneState) Tools() []tooltypes.Tool {
	return append([]tooltypes.Tool(nil), s.tools...)
}
func (s *controlPlaneState) DiscoverContexts() map[string]string { return nil }
func (s *controlPlaneState) GetLLMConfig() any                   { return s.config }
func (s *controlPlaneState) WorkingDirectory() string            { return s.workingDir }
func (s *controlPlaneState) LockFile(string)                     {}
func (s *controlPlaneState) UnlockFile(string)                   {}

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
	return executeTool(ctx, thread, state, rendererRegistry, toolName, toolInput, toolCallID, nil)
}

// ExecuteToolWithHandler runs one complete tool lifecycle and forwards
// transient tool snapshots to handlers that implement ToolUpdateMessageHandler.
func ExecuteToolWithHandler(
	ctx context.Context,
	thread llmtypes.Thread,
	state tooltypes.State,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
	handler llmtypes.MessageHandler,
) ToolExecution {
	return executeTool(ctx, thread, state, rendererRegistry, toolName, toolInput, toolCallID, handler)
}

func executeTool(
	ctx context.Context,
	thread llmtypes.Thread,
	state tooltypes.State,
	rendererRegistry *renderers.RendererRegistry,
	toolName string,
	toolInput string,
	toolCallID string,
	handler llmtypes.MessageHandler,
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

		var updateMu sync.Mutex
		acceptUpdates := true
		var onUpdate tooltypes.ToolUpdateCallback
		if updateHandler, ok := handler.(llmtypes.ToolUpdateMessageHandler); ok && (runtime == nil || runtime.CanStreamToolUpdates()) {
			onUpdate = func(partialResult tooltypes.ToolResult) {
				if partialResult == nil {
					return
				}

				updateMu.Lock()
				defer updateMu.Unlock()
				if !acceptUpdates {
					return
				}

				if runtime != nil {
					structuredUpdate := partialResult.StructuredData()
					if structuredUpdate.ToolName == "" || structuredUpdate.ToolName == "unknown" {
						structuredUpdate.ToolName = toolName
					}
					var modified, accepted bool
					structuredUpdate, modified, accepted = runtime.DispatchToolUpdate(ctx, callContext, toolName, effectiveInput, toolCallID, structuredUpdate)
					if !accepted {
						return
					}
					if modified {
						partialResult = StructuredResultToolResult{Result: structuredUpdate, RendererRegistry: rendererRegistry}
					}
				}
				updateHandler.HandleToolUpdate(toolCallID, toolName, partialResult)
			}
		}

		result = tools.RunToolWithUpdates(ctx, state, toolName, effectiveInput, onUpdate)
		if onUpdate != nil {
			updateMu.Lock()
			acceptUpdates = false
			updateMu.Unlock()
		}
	}

	structuredResult := result.StructuredData()
	if runtime != nil {
		var modified bool
		structuredResult, modified = runtime.DispatchToolResult(ctx, callContext, toolName, effectiveInput, toolCallID, structuredResult)
		if modified {
			result = StructuredResultToolResult{Result: structuredResult, RendererRegistry: rendererRegistry}
		}
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
		InvokedBy:      "main",
	}
}

func extensionRuntime(thread llmtypes.Thread) *extensions.Runtime {
	if thread == nil {
		return nil
	}
	runtime, _ := thread.GetConfig().Extensions.(*extensions.Runtime)
	return runtime
}
