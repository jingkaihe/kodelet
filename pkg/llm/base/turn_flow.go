package base

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ProcessUserMessage dispatches user.message and returns the effective message.
func ProcessUserMessage(
	ctx context.Context,
	thread llmtypes.Thread,
	message string,
) (string, error) {
	if runtime := extensionRuntime(thread); runtime != nil {
		decision := runtime.DispatchUserMessage(ctx, buildExtensionCallContext(thread, threadState(thread)), message)
		if decision.Blocked {
			return "", fmt.Errorf("message blocked by extension: %s", decision.Reason)
		}
		return decision.Message, nil
	}

	return message, nil
}

// DispatchAgentStart notifies extension handlers when an agent loop starts.
func DispatchAgentStart(ctx context.Context, thread llmtypes.Thread) {
	if runtime := extensionRuntime(thread); runtime != nil {
		runtime.DispatchAgentStart(ctx, buildExtensionCallContext(thread, threadState(thread)))
	}
}

// DispatchTurnStart notifies extension handlers before a model turn starts.
func DispatchTurnStart(ctx context.Context, thread llmtypes.Thread, turnNumber int) {
	if runtime := extensionRuntime(thread); runtime != nil {
		runtime.DispatchTurnStart(ctx, buildExtensionCallContext(thread, threadState(thread)), turnNumber)
	}
}

// ProcessSystemPrompt dispatches agent.init and returns the effective prompt.
func ProcessSystemPrompt(ctx context.Context, thread llmtypes.Thread, systemPrompt string) string {
	return ProcessAgentInit(ctx, thread, systemPrompt).SystemPrompt
}

// AgentInitDecision is the host-side result of processing agent.init handlers.
type AgentInitDecision struct {
	SystemPrompt  string
	AllowedTools  []string
	ToolsModified bool
}

// ProcessAgentInit dispatches agent.init and applies supported prompt/tool-list mutations.
func ProcessAgentInit(ctx context.Context, thread llmtypes.Thread, systemPrompt string) AgentInitDecision {
	decision := AgentInitDecision{SystemPrompt: systemPrompt}
	clearAllowedToolsMetadata(thread)
	if runtime := extensionRuntime(thread); runtime != nil {
		config := thread.GetConfig()
		state := threadState(thread)
		extensionDecision := runtime.DispatchAgentInitDecision(ctx, buildExtensionCallContext(thread, state), systemPrompt, agentInitAllowedTools(config, state))
		decision.SystemPrompt = extensionDecision.SystemPrompt
		decision.AllowedTools = extensionDecision.AllowedTools
		decision.ToolsModified = extensionDecision.ToolsModified
		if extensionDecision.ToolsModified {
			thread.SetMetadataValue(extensionAllowedToolsMetadataKey, extensionDecision.AllowedTools)
		}
	}
	return decision
}

type metadataReplacer interface {
	SetMetadata(map[string]any)
}

func clearAllowedToolsMetadata(thread llmtypes.Thread) {
	if thread == nil {
		return
	}
	if replacer, ok := thread.(metadataReplacer); ok {
		metadata := thread.GetMetadata()
		delete(metadata, extensionAllowedToolsMetadataKey)
		replacer.SetMetadata(metadata)
		return
	}
	thread.SetMetadataValue(extensionAllowedToolsMetadataKey, nil)
}

func agentInitAllowedTools(config llmtypes.Config, state tooltypes.State) []string {
	if len(config.AllowedTools) > 0 {
		return append([]string(nil), config.AllowedTools...)
	}
	if state == nil {
		return nil
	}
	stateTools := state.Tools()
	virtualTools := tools.VirtualToolNames()
	allowedTools := make([]string, 0, len(stateTools)+len(virtualTools))
	for _, tool := range stateTools {
		if tool == nil {
			continue
		}
		allowedTools = append(allowedTools, tool.Name())
	}
	allowedTools = append(allowedTools, virtualTools...)
	return allowedTools
}

// TriggerTurnEnd notifies extension handlers when assistant output is finalized for a turn.
func TriggerTurnEnd(
	ctx context.Context,
	thread llmtypes.Thread,
	finalOutput string,
	turnCount int,
) {
	if finalOutput == "" {
		return
	}
	if runtime := extensionRuntime(thread); runtime != nil {
		runtime.DispatchTurnEnd(ctx, buildExtensionCallContext(thread, threadState(thread)), finalOutput, turnCount)
	}
}

// HasPendingSteer reports whether steering arrived while the current model turn was in flight.
// Providers use this before stopping so the next exchange can inject the queued messages.
func HasPendingSteer(ctx context.Context, conversationID string) bool {
	steerStore, err := steer.NewSteerStore()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to check for pending steer before agent stop")
		return false
	}

	pendingSteer, err := steerStore.ReadPendingSteer(conversationID)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to read pending steer before agent stop")
		return false
	}
	if len(pendingSteer) == 0 {
		return false
	}

	logger.G(ctx).
		WithField("conversation_id", conversationID).
		WithField("steer_count", len(pendingSteer)).
		Info("pending steer detected before agent stop, continuing conversation")
	return true
}

// HandleAgentStopFollowUps checks agent.end extension handlers and appends any follow-up user messages.
// Returns true when follow-ups were added and the caller should continue the loop.
func HandleAgentStopFollowUps(
	ctx context.Context,
	thread llmtypes.Thread,
	handler llmtypes.MessageHandler,
) bool {
	logger.G(ctx).Debug("no tools used, checking agent end follow-ups")

	messages, err := thread.GetMessages()
	if err != nil {
		return false
	}

	if runtime := extensionRuntime(thread); runtime != nil {
		followUps := runtime.DispatchAgentEnd(ctx, buildExtensionCallContext(thread, threadState(thread)), messages)
		if len(followUps) == 0 {
			return false
		}

		logger.G(ctx).WithField("count", len(followUps)).Info("agent end follow-up messages returned, continuing conversation")
		for _, msg := range followUps {
			thread.AddUserMessage(ctx, msg)
			handler.HandleText(fmt.Sprintf("\n📨 Extension follow-up: %s\n", msg))
		}

		return true
	}
	return false
}

func threadState(thread llmtypes.Thread) tooltypes.State {
	if thread == nil {
		return nil
	}
	return thread.GetState()
}
