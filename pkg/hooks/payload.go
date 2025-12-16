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

// AgentStopPayload is sent to agent_stop hooks
type AgentStopPayload struct {
	BasePayload
	Messages []llmtypes.Message `json:"messages"`
}

// AgentStopResult is returned by agent_stop hooks (empty for now)
type AgentStopResult struct{}
