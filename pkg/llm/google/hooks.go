package google

import (
	"context"
	"encoding/json"
	"os"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// invokedBy returns whether this is a main agent or subagent
func (t *Thread) invokedBy() hooks.InvokedBy {
	if t.config.IsSubAgent {
		return hooks.InvokedBySubagent
	}
	return hooks.InvokedByMain
}

// triggerUserMessageSend invokes user_message_send hooks
// Returns (blocked, reason)
func (t *Thread) triggerUserMessageSend(ctx context.Context, message string) (bool, string) {
	if !t.hookManager.HasHooks(hooks.HookTypeUserMessageSend) {
		return false, ""
	}

	cwd, _ := os.Getwd()
	payload := hooks.UserMessageSendPayload{
		BasePayload: hooks.BasePayload{
			Event:     hooks.HookTypeUserMessageSend,
			ConvID:    t.conversationID,
			CWD:       cwd,
			InvokedBy: t.invokedBy(),
		},
		Message: message,
	}

	result, err := t.hookManager.ExecuteUserMessageSend(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("user_message_send hook failed")
		return false, ""
	}
	return result.Blocked, result.Reason
}

// triggerBeforeToolCall invokes before_tool_call hooks
// Returns (blocked, reason, input) - input is the potentially modified tool input
func (t *Thread) triggerBeforeToolCall(ctx context.Context, toolName, toolInput, toolUserID string) (bool, string, string) {
	if !t.hookManager.HasHooks(hooks.HookTypeBeforeToolCall) {
		return false, "", toolInput
	}

	cwd, _ := os.Getwd()
	payload := hooks.BeforeToolCallPayload{
		BasePayload: hooks.BasePayload{
			Event:     hooks.HookTypeBeforeToolCall,
			ConvID:    t.conversationID,
			CWD:       cwd,
			InvokedBy: t.invokedBy(),
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolUserID: toolUserID,
	}

	result, err := t.hookManager.ExecuteBeforeToolCall(ctx, payload)
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

// triggerAfterToolCall invokes after_tool_call hooks
// Returns modified output or nil to use original
func (t *Thread) triggerAfterToolCall(ctx context.Context, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult) *tooltypes.StructuredToolResult {
	if !t.hookManager.HasHooks(hooks.HookTypeAfterToolCall) {
		return nil
	}

	cwd, _ := os.Getwd()
	payload := hooks.AfterToolCallPayload{
		BasePayload: hooks.BasePayload{
			Event:     hooks.HookTypeAfterToolCall,
			ConvID:    t.conversationID,
			CWD:       cwd,
			InvokedBy: t.invokedBy(),
		},
		ToolName:   toolName,
		ToolInput:  json.RawMessage(toolInput),
		ToolOutput: toolOutput,
		ToolUserID: toolUserID,
	}

	result, err := t.hookManager.ExecuteAfterToolCall(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("after_tool_call hook failed")
		return nil
	}
	return result.Output
}

// triggerAgentStop invokes agent_stop hooks
// Returns follow-up messages that can be appended to the conversation
func (t *Thread) triggerAgentStop(ctx context.Context, messages []llmtypes.Message) []string {
	if !t.hookManager.HasHooks(hooks.HookTypeAgentStop) {
		return nil
	}

	cwd, _ := os.Getwd()
	payload := hooks.AgentStopPayload{
		BasePayload: hooks.BasePayload{
			Event:     hooks.HookTypeAgentStop,
			ConvID:    t.conversationID,
			CWD:       cwd,
			InvokedBy: t.invokedBy(),
		},
		Messages: messages,
	}

	result, err := t.hookManager.ExecuteAgentStop(ctx, payload)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("agent_stop hook failed")
		return nil
	}
	return result.FollowUpMessages
}
