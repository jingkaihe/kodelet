package hooks

import (
	"context"
	"encoding/json"
	"os"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Trigger provides methods to invoke lifecycle hooks.
// It encapsulates the common hook triggering logic shared across LLM providers.
// A zero-value Trigger is safe to use and acts as a no-op.
type Trigger struct {
	Manager        HookManager
	ConversationID string
	IsSubAgent     bool
}

// NewTrigger creates a new hook trigger with the given parameters.
func NewTrigger(manager HookManager, conversationID string, isSubAgent bool) Trigger {
	return Trigger{
		Manager:        manager,
		ConversationID: conversationID,
		IsSubAgent:     isSubAgent,
	}
}

// invokedBy returns whether this is a main agent or subagent
func (t Trigger) invokedBy() InvokedBy {
	if t.IsSubAgent {
		return InvokedBySubagent
	}
	return InvokedByMain
}

// getCwd returns the current working directory, logging a warning on error
func (t Trigger) getCwd(ctx context.Context) string {
	cwd, err := os.Getwd()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to get working directory for hook")
		return ""
	}
	return cwd
}

// TriggerUserMessageSend invokes user_message_send hooks including built-in handlers.
// The thread parameter is passed to built-in handlers that need to modify thread state.
// The recipeHooks parameter contains hook configurations from recipe metadata.
// Returns (blocked, reason). A zero-value Trigger returns (false, "").
func (t Trigger) TriggerUserMessageSend(ctx context.Context, thread llmtypes.Thread, message string, recipeHooks map[string]llmtypes.HookConfig) (bool, string) {
	payload := UserMessageSendPayload{
		BasePayload: BasePayload{
			Event:     HookTypeUserMessageSend,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		Message: message,
	}

	// First, execute external hooks (if any)
	if t.Manager.HasHooks(HookTypeUserMessageSend) {
		result, err := t.Manager.ExecuteUserMessageSend(ctx, payload)
		if err == nil && result.Blocked {
			return result.Blocked, result.Reason
		}
	}

	// Then, execute built-in handler if specified in recipe
	if hookConfig, ok := recipeHooks["user_message_send"]; ok {
		registry := DefaultBuiltinRegistry()
		if handler, exists := registry.Get(hookConfig.Handler); exists {
			result, err := handler.HandleUserMessageSend(ctx, thread, payload)
			if err != nil {
				logger.G(ctx).WithError(err).WithField("handler", hookConfig.Handler).Error("built-in handler failed")
			} else if result != nil && result.Blocked {
				return result.Blocked, result.Reason
			}
		}
	}

	return false, ""
}

// TriggerBeforeToolCall invokes before_tool_call hooks including built-in handlers.
// The thread parameter is passed to built-in handlers that need to modify thread state.
// The recipeHooks parameter contains hook configurations from recipe metadata.
// Returns (blocked, reason, input) - input is the potentially modified tool input.
// A zero-value Trigger returns (false, "", toolInput).
func (t Trigger) TriggerBeforeToolCall(ctx context.Context, thread llmtypes.Thread, toolName, toolInput, toolUserID string, recipeHooks map[string]llmtypes.HookConfig) (bool, string, string) {
	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{
			Event:     HookTypeBeforeToolCall,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolUserID: toolUserID,
	}

	currentInput := toolInput

	// First, execute external hooks (if any)
	if t.Manager.HasHooks(HookTypeBeforeToolCall) {
		result, err := t.Manager.ExecuteBeforeToolCall(ctx, payload)
		if err == nil {
			if result.Blocked {
				return true, result.Reason, ""
			}
			if len(result.Input) > 0 {
				currentInput = string(result.Input)
			}
		}
	}

	// Then, execute built-in handler if specified in recipe
	if hookConfig, ok := recipeHooks["before_tool_call"]; ok {
		registry := DefaultBuiltinRegistry()
		if handler, exists := registry.Get(hookConfig.Handler); exists {
			result, err := handler.HandleBeforeToolCall(ctx, thread, payload)
			if err != nil {
				logger.G(ctx).WithError(err).WithField("handler", hookConfig.Handler).Error("built-in handler failed")
			} else if result != nil {
				if result.Blocked {
					return true, result.Reason, ""
				}
				if len(result.Input) > 0 {
					currentInput = string(result.Input)
				}
			}
		}
	}

	return false, "", currentInput
}

// TriggerAfterToolCall invokes after_tool_call hooks including built-in handlers.
// The thread parameter is passed to built-in handlers that need to modify thread state.
// The recipeHooks parameter contains hook configurations from recipe metadata.
// Returns modified output or nil to use original.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAfterToolCall(ctx context.Context, thread llmtypes.Thread, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult, recipeHooks map[string]llmtypes.HookConfig) *tooltypes.StructuredToolResult {
	payload := AfterToolCallPayload{
		BasePayload: BasePayload{
			Event:     HookTypeAfterToolCall,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolOutput: toolOutput,
		ToolUserID: toolUserID,
	}

	var currentOutput *tooltypes.StructuredToolResult

	// First, execute external hooks (if any)
	if t.Manager.HasHooks(HookTypeAfterToolCall) {
		result, err := t.Manager.ExecuteAfterToolCall(ctx, payload)
		if err == nil && result.Output != nil {
			currentOutput = result.Output
		}
	}

	// Then, execute built-in handler if specified in recipe
	if hookConfig, ok := recipeHooks["after_tool_call"]; ok {
		registry := DefaultBuiltinRegistry()
		if handler, exists := registry.Get(hookConfig.Handler); exists {
			result, err := handler.HandleAfterToolCall(ctx, thread, payload)
			if err != nil {
				logger.G(ctx).WithError(err).WithField("handler", hookConfig.Handler).Error("built-in handler failed")
			} else if result != nil && result.Output != nil {
				currentOutput = result.Output
			}
		}
	}

	return currentOutput
}

// TriggerAgentStop invokes agent_stop hooks including built-in handlers.
// The thread parameter is passed to built-in handlers that need to modify thread state.
// The recipeHooks parameter contains hook configurations from recipe metadata.
// Returns follow-up messages that can be appended to the conversation.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAgentStop(ctx context.Context, thread llmtypes.Thread, messages []llmtypes.Message, recipeHooks map[string]llmtypes.HookConfig) []string {
	payload := AgentStopPayload{
		BasePayload: BasePayload{
			Event:     HookTypeAgentStop,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		Messages: messages,
	}

	var followUpMessages []string

	// First, execute external hooks (if any)
	if t.Manager.HasHooks(HookTypeAgentStop) {
		result, err := t.Manager.ExecuteAgentStop(ctx, payload)
		if err == nil && len(result.FollowUpMessages) > 0 {
			followUpMessages = append(followUpMessages, result.FollowUpMessages...)
		}
	}

	// Then, execute built-in handler if specified in recipe
	if hookConfig, ok := recipeHooks["agent_stop"]; ok {
		registry := DefaultBuiltinRegistry()
		if handler, exists := registry.Get(hookConfig.Handler); exists {
			result, err := handler.HandleAgentStop(ctx, thread, payload)
			if err != nil {
				logger.G(ctx).WithError(err).WithField("handler", hookConfig.Handler).Error("built-in handler failed")
			} else if result != nil && len(result.FollowUpMessages) > 0 {
				followUpMessages = append(followUpMessages, result.FollowUpMessages...)
			}
		}
	}

	return followUpMessages
}

// TriggerTurnEnd invokes turn_end hooks including built-in handlers.
// The recipeHooks parameter contains hook configurations from recipe metadata.
// The thread parameter is passed to built-in handlers that need to modify thread state.
// A zero-value Trigger is a no-op.
func (t Trigger) TriggerTurnEnd(ctx context.Context, thread llmtypes.Thread, response string, turnNumber int, recipeHooks map[string]llmtypes.HookConfig) {
	payload := TurnEndPayload{
		BasePayload: BasePayload{
			Event:     HookTypeTurnEnd,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		Response:   response,
		TurnNumber: turnNumber,
	}

	// First, execute external hooks (if any)
	if t.Manager.HasHooks(HookTypeTurnEnd) {
		t.Manager.ExecuteTurnEnd(ctx, payload)
	}

	// Then, execute built-in handler if specified in recipe
	if hookConfig, ok := recipeHooks["turn_end"]; ok {
		// Skip if once=true and not the first turn
		if hookConfig.Once && turnNumber > 1 {
			return
		}

		registry := DefaultBuiltinRegistry()
		if handler, exists := registry.Get(hookConfig.Handler); exists {
			if _, err := handler.HandleTurnEnd(ctx, thread, payload); err != nil {
				logger.G(ctx).WithError(err).WithField("handler", hookConfig.Handler).Error("built-in handler failed")
			}
		}
	}
}

// SetConversationID updates the conversation ID for the trigger.
// This is useful when the conversation ID is set after thread creation.
func (t *Trigger) SetConversationID(id string) {
	t.ConversationID = id
}
