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

// TriggerUserMessageSend invokes user_message_send hooks.
// Returns (blocked, reason). A zero-value Trigger returns (false, "").
func (t Trigger) TriggerUserMessageSend(ctx context.Context, message string) (bool, string) {
	if !t.Manager.HasHooks(HookTypeUserMessageSend) {
		return false, ""
	}

	payload := UserMessageSendPayload{
		BasePayload: BasePayload{
			Event:     HookTypeUserMessageSend,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		Message: message,
	}

	result, err := t.Manager.ExecuteUserMessageSend(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("user_message_send hook failed")
		return false, ""
	}
	return result.Blocked, result.Reason
}

// TriggerBeforeToolCall invokes before_tool_call hooks.
// Returns (blocked, reason, input) - input is the potentially modified tool input.
// A zero-value Trigger returns (false, "", toolInput).
func (t Trigger) TriggerBeforeToolCall(ctx context.Context, toolName, toolInput, toolUserID string) (bool, string, string) {
	if !t.Manager.HasHooks(HookTypeBeforeToolCall) {
		return false, "", toolInput
	}

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

	result, err := t.Manager.ExecuteBeforeToolCall(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("before_tool_call hook failed")
		return false, "", toolInput
	}

	if result.Blocked {
		return true, result.Reason, ""
	}
	if len(result.Input) > 0 {
		return false, "", string(result.Input)
	}
	return false, "", toolInput
}

// TriggerAfterToolCall invokes after_tool_call hooks.
// Returns modified output or nil to use original.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAfterToolCall(ctx context.Context, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult) *tooltypes.StructuredToolResult {
	if !t.Manager.HasHooks(HookTypeAfterToolCall) {
		return nil
	}

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

	result, err := t.Manager.ExecuteAfterToolCall(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("after_tool_call hook failed")
		return nil
	}
	return result.Output
}

// TriggerAgentStop invokes agent_stop hooks.
// Returns follow-up messages that can be appended to the conversation.
// A zero-value Trigger returns nil.
func (t Trigger) TriggerAgentStop(ctx context.Context, messages []llmtypes.Message) []string {
	if !t.Manager.HasHooks(HookTypeAgentStop) {
		return nil
	}

	payload := AgentStopPayload{
		BasePayload: BasePayload{
			Event:     HookTypeAgentStop,
			ConvID:    t.ConversationID,
			CWD:       t.getCwd(ctx),
			InvokedBy: t.invokedBy(),
		},
		Messages: messages,
	}

	result, err := t.Manager.ExecuteAgentStop(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("agent_stop hook failed")
		return nil
	}
	return result.FollowUpMessages
}

// SetConversationID updates the conversation ID for the trigger.
// This is useful when the conversation ID is set after thread creation.
func (t *Trigger) SetConversationID(id string) {
	t.ConversationID = id
}
