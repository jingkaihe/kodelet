package hooks

import (
	"encoding/json"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BasePayload contains fields common to all hook payloads
type BasePayload struct {
	Event     HookType  `json:"event"`
	ConvID    string    `json:"conv_id"`
	CWD       string    `json:"cwd"`
	InvokedBy InvokedBy `json:"invoked_by"`
}

// BeforeToolCallPayload is sent to before_tool_call hooks
type BeforeToolCallPayload struct {
	BasePayload
	ToolName   string          `json:"tool_name"`
	ToolInput  json.RawMessage `json:"tool_input"`
	ToolUserID string          `json:"tool_user_id"`
}

// BeforeToolCallResult is returned by before_tool_call hooks
type BeforeToolCallResult struct {
	Blocked bool            `json:"blocked"`
	Reason  string          `json:"reason,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
}

// AfterToolCallPayload is sent to after_tool_call hooks
type AfterToolCallPayload struct {
	BasePayload
	ToolName   string                         `json:"tool_name"`
	ToolInput  json.RawMessage                `json:"tool_input"`
	ToolOutput tooltypes.StructuredToolResult `json:"tool_output"`
	ToolUserID string                         `json:"tool_user_id"`
}

// AfterToolCallResult is returned by after_tool_call hooks
type AfterToolCallResult struct {
	Output *tooltypes.StructuredToolResult `json:"output,omitempty"`
}

// UserMessageSendPayload is sent to user_message_send hooks
type UserMessageSendPayload struct {
	BasePayload
	Message string `json:"message"`
}

// UserMessageSendResult is returned by user_message_send hooks
type UserMessageSendResult struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
}

// UsageInfo provides token usage statistics for hook payloads
type UsageInfo struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CurrentContextWindow int `json:"current_context_window"`
	MaxContextWindow     int `json:"max_context_window"`
}

// AgentStopPayload is sent to agent_stop hooks
type AgentStopPayload struct {
	BasePayload
	Messages []llmtypes.Message `json:"messages"`
	Usage    UsageInfo          `json:"usage"`

	// InvokedRecipe is the recipe that triggered this agent session (if any)
	// Empty string if no recipe was used (e.g., direct query)
	InvokedRecipe string `json:"invoked_recipe,omitempty"`

	// AutoCompactEnabled indicates if auto-compact is enabled for this session
	AutoCompactEnabled bool `json:"auto_compact_enabled"`

	// AutoCompactThreshold is the threshold ratio (e.g., 0.80)
	AutoCompactThreshold float64 `json:"auto_compact_threshold,omitempty"`

	// CallbackArgs contains arguments passed when this session was triggered by a callback
	CallbackArgs map[string]string `json:"callback_args,omitempty"`
}

// HookResult represents the outcome of an agent_stop hook
type HookResult string

// HookResult constants define the possible outcomes of an agent_stop hook
const (
	HookResultNone     HookResult = ""         // No action, agent stops normally
	HookResultContinue HookResult = "continue" // Continue with follow-up messages
	HookResultMutate   HookResult = "mutate"   // Replace conversation messages
	HookResultCallback HookResult = "callback" // Invoke a recipe via callback
)

// AgentStopResult is returned by agent_stop hooks
type AgentStopResult struct {
	// Result specifies the outcome of the hook
	Result HookResult `json:"result,omitempty"`

	// FollowUpMessages contains optional messages to append to the conversation.
	// This enables LLM-based hooks to provide feedback, ask clarifying questions,
	// or request additional actions based on the agent's work.
	// Used when Result is empty or "continue"
	FollowUpMessages []string `json:"follow_up_messages,omitempty"`

	// Messages has different meanings based on Result:
	// - Result="continue": Follow-up messages to append (agent continues processing)
	// - Result="mutate": Replacement messages for the entire conversation history
	Messages []llmtypes.Message `json:"messages,omitempty"`

	// Callback specifies which recipe to invoke via callback
	// Only used when Result="callback"
	Callback string `json:"callback,omitempty"`

	// CallbackArgs provides arguments to pass to the recipe
	CallbackArgs map[string]string `json:"callback_args,omitempty"`

	// TargetConversationID specifies which conversation to apply mutations to
	// Defaults to current conversation if empty
	TargetConversationID string `json:"target_conversation_id,omitempty"`
}
