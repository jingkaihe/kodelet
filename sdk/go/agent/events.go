package agent

import "encoding/json"

// Session event types.
const (
	EventAgentStart            = "agent.start"
	EventAgentEnd              = "agent.end"
	EventAgentError            = "agent.error"
	EventUserMessage           = "user.message"
	EventAssistantMessageDelta = "assistant.message_delta"
	EventAssistantMessage      = "assistant.message"
	EventThinkingStart         = "assistant.thinking_start"
	EventThinkingDelta         = "assistant.thinking_delta"
	EventThinkingEnd           = "assistant.thinking_end"
	EventContentEnd            = "assistant.content_end"
	EventContextCompacted      = "context.compacted"
	EventToolCall              = "tool.call"
	EventToolUpdate            = "tool.update"
	EventToolResult            = "tool.result"
	// EventOther carries ACP updates this SDK does not model; Data is the
	// decoded update object.
	EventOther = "event"
)

// Event is a session event. Data holds the typed payload for Type:
// AgentStart, AgentError, UserMessage, AssistantMessageDelta,
// AssistantMessage, ThinkingDelta, ContextCompacted, ToolCall, ToolResult
// (for tool.update and tool.result), Response (for agent.end), map[string]any
// (for event), or nil for marker events.
type Event struct {
	Type           string          `json:"type"`
	Data           any             `json:"data"`
	ConversationID string          `json:"conversationId,omitempty"`
	Raw            json.RawMessage `json:"raw,omitempty"`
}

// AgentStart is emitted when a run starts.
type AgentStart struct {
	Message string `json:"message"`
}

// AgentError is emitted when a run fails.
type AgentError struct {
	Message string `json:"message"`
}

// UserMessage is the submitted user message.
type UserMessage struct {
	Content string `json:"content"`
}

// AssistantMessageDelta is streamed assistant text.
type AssistantMessageDelta struct {
	DeltaContent string `json:"deltaContent"`
}

// AssistantMessage is the complete assistant text of a run.
type AssistantMessage struct {
	Content string `json:"content"`
}

// ThinkingDelta is streamed reasoning text.
type ThinkingDelta struct {
	DeltaContent string `json:"deltaContent"`
}

// ToolCall describes a tool invocation.
type ToolCall struct {
	ToolName   string `json:"toolName"`
	Input      any    `json:"input"`
	RawInput   string `json:"rawInput,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

// ToolResult is a tool's accumulated output, for tool.update snapshots and
// final tool.result events.
type ToolResult struct {
	ToolName   string `json:"toolName"`
	Result     string `json:"result"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Status     string `json:"status,omitempty"`
}

// CompactionMarker identifies a context compaction.
type CompactionMarker struct {
	ID string `json:"id"`
	// Method is "api" or "summary".
	Method    string `json:"method"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// ContextCompacted reports that the conversation context was compacted.
type ContextCompacted struct {
	Compaction CompactionMarker `json:"compaction"`
	// BeforeCurrentUser places the marker before the current user message
	// during pre-turn compaction.
	BeforeCurrentUser bool `json:"beforeCurrentUser"`
}

// Response is the result of a run.
type Response struct {
	Content        string `json:"content"`
	ConversationID string `json:"conversationId"`
	// Events is the run event log; tool.update snapshots are coalesced by
	// tool call ID.
	Events     []Event `json:"events"`
	StopReason string  `json:"stopReason,omitempty"`
}

// SteeringOutcome reports how a steering message was handled.
type SteeringOutcome string

// Steering outcomes.
const (
	SteeringInjected       SteeringOutcome = "injected"
	SteeringStartedNewTurn SteeringOutcome = "startedNewTurn"
	SteeringPromptRequired SteeringOutcome = "promptRequired"
	SteeringFailed         SteeringOutcome = "failed"
)

// SteerResult is returned by Session.Steer.
type SteerResult struct {
	Outcome SteeringOutcome
	Reason  string
}
