package hooks

import (
	"context"
	"encoding/json"

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
	WorkingDir     string
	RecipeName     string // Active recipe name, empty if none
}

// NewTrigger creates a new hook trigger with the given parameters.

func NewTrigger(manager HookManager, conversationID string, isSubAgent bool, args ...string) Trigger {
	workingDir := ""
	recipeName := ""
	if len(args) == 1 {
		recipeName = args[0]
	} else if len(args) >= 2 {
		workingDir = args[0]
		recipeName = args[1]
	}

	return Trigger{
		Manager:        manager,
		ConversationID: conversationID,
		IsSubAgent:     isSubAgent,
		WorkingDir:     workingDir,
		RecipeName:     recipeName,
	}
}

// invokedBy returns whether this is a main agent or subagent
func (t Trigger) invokedBy() InvokedBy {
	if t.IsSubAgent {
		return InvokedBySubagent
	}
	return InvokedByMain
}

// getCwd returns the configured working directory.
func (t Trigger) getCwd(ctx context.Context) string {
	if t.WorkingDir == "" {
		logger.G(ctx).Debug("hook trigger missing working directory")
	}
	return t.WorkingDir
}

// TriggerUserMessageSend invokes user_message_send hooks.
// Returns (blocked, reason). A zero-value Trigger returns (false, "").
func (t Trigger) TriggerUserMessageSend(ctx context.Context, message string) (bool, string) {
	payload := UserMessageSendPayload{
		BasePayload: BasePayload{
			Event:      HookTypeUserMessageSend,
			ConvID:     t.ConversationID,
			CWD:        t.getCwd(ctx),
			InvokedBy:  t.invokedBy(),
			RecipeName: t.RecipeName,
		},
		Message: message,
	}

	if t.Manager.HasHooks(HookTypeUserMessageSend) {
		result, err := t.Manager.ExecuteUserMessageSend(ctx, payload)
		if err == nil && result.Blocked {
			return result.Blocked, result.Reason
		}
	}

	return false, ""
}

// TriggerBeforeToolCall invokes before_tool_call hooks.
// Returns (blocked, reason, input) - input is the potentially modified tool input.
// A zero-value Trigger returns (false, "", toolInput).
func (t Trigger) TriggerBeforeToolCall(ctx context.Context, toolName, toolInput, toolUserID string) (bool, string, string) {
	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{
			Event:      HookTypeBeforeToolCall,
			ConvID:     t.ConversationID,
			CWD:        t.getCwd(ctx),
			InvokedBy:  t.invokedBy(),
			RecipeName: t.RecipeName,
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolUserID: toolUserID,
	}

	currentInput := toolInput

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

	return false, "", currentInput
}

// TriggerAfterToolCall invokes after_tool_call hooks.
// Returns modified output or nil to use original.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAfterToolCall(ctx context.Context, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult) *tooltypes.StructuredToolResult {
	payload := AfterToolCallPayload{
		BasePayload: BasePayload{
			Event:      HookTypeAfterToolCall,
			ConvID:     t.ConversationID,
			CWD:        t.getCwd(ctx),
			InvokedBy:  t.invokedBy(),
			RecipeName: t.RecipeName,
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolOutput: toolOutput,
		ToolUserID: toolUserID,
	}

	var currentOutput *tooltypes.StructuredToolResult

	if t.Manager.HasHooks(HookTypeAfterToolCall) {
		result, err := t.Manager.ExecuteAfterToolCall(ctx, payload)
		if err == nil && result.Output != nil {
			currentOutput = result.Output
		}
	}

	return currentOutput
}

// TriggerAgentStop invokes agent_stop hooks.
// Returns follow-up messages that can be appended to the conversation.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAgentStop(ctx context.Context, messages []llmtypes.Message) []string {
	payload := AgentStopPayload{
		BasePayload: BasePayload{
			Event:      HookTypeAgentStop,
			ConvID:     t.ConversationID,
			CWD:        t.getCwd(ctx),
			InvokedBy:  t.invokedBy(),
			RecipeName: t.RecipeName,
		},
		Messages: messages,
	}

	var followUpMessages []string

	if t.Manager.HasHooks(HookTypeAgentStop) {
		result, err := t.Manager.ExecuteAgentStop(ctx, payload)
		if err == nil && len(result.FollowUpMessages) > 0 {
			followUpMessages = append(followUpMessages, result.FollowUpMessages...)
		}
	}

	return followUpMessages
}

// TriggerTurnEnd invokes turn_end hooks.
// A zero-value Trigger is a no-op.
func (t Trigger) TriggerTurnEnd(ctx context.Context, response string, turnNumber int) {
	payload := TurnEndPayload{
		BasePayload: BasePayload{
			Event:      HookTypeTurnEnd,
			ConvID:     t.ConversationID,
			CWD:        t.getCwd(ctx),
			InvokedBy:  t.invokedBy(),
			RecipeName: t.RecipeName,
		},
		Response:   response,
		TurnNumber: turnNumber,
	}

	if t.Manager.HasHooks(HookTypeTurnEnd) {
		t.Manager.ExecuteTurnEnd(ctx, payload)
	}
}

// SetConversationID updates the conversation ID for the trigger.
// This is useful when the conversation ID is set after thread creation.
func (t *Trigger) SetConversationID(id string) {
	t.ConversationID = id
}
