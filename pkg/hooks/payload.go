package hooks

import (
	"encoding/json"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BasePayload contains fields common to all hook payloads
type BasePayload struct {
	Event      HookType  `json:"event"`
	ConvID     string    `json:"conv_id"`
	CWD        string    `json:"cwd"`
	InvokedBy  InvokedBy `json:"invoked_by"`
	RecipeName string    `json:"recipe_name,omitempty"` // Active recipe name, empty if none
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

// AgentStopPayload is sent to agent_stop hooks
type AgentStopPayload struct {
	BasePayload
	Messages []llmtypes.Message `json:"messages"`
}

// AgentStopResult is returned by agent_stop hooks
type AgentStopResult struct {
	// FollowUpMessages contains optional messages to append to the conversation.
	// This enables LLM-based hooks to provide feedback, ask clarifying questions,
	// or request additional actions based on the agent's work.
	FollowUpMessages []string `json:"follow_up_messages,omitempty"`
}

// TurnEndPayload is sent when an assistant turn completes
type TurnEndPayload struct {
	BasePayload
	Response   string `json:"response"`    // The assistant's response text
	TurnNumber int    `json:"turn_number"` // Which turn in the conversation (1-indexed)
}

// TurnEndResult is returned by turn_end hooks
type TurnEndResult struct {
	// Future: could support response modification, turn cancellation, etc.
}
