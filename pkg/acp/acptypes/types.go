// Package acptypes defines types for the Agent Client Protocol (ACP).
// ACP is a protocol for communication between AI coding agents and client
// applications (IDEs, text editors, or other UIs).
package acptypes

import "encoding/json"

// ProtocolVersion is the ACP protocol version (major only)
const ProtocolVersion = 1

// Request represents a JSON-RPC 2.0 request
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification represents a JSON-RPC 2.0 notification (no ID)
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Standard JSON-RPC error codes
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// SessionID uniquely identifies an ACP session
type SessionID string

// Implementation provides info about a client or agent
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// AgentCapabilities advertised during initialization
type AgentCapabilities struct {
	LoadSession         bool                 `json:"loadSession,omitempty"`
	PromptCapabilities  *PromptCapabilities  `json:"promptCapabilities,omitempty"`
	MCPCapabilities     *MCPCapabilities     `json:"mcpCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
	Meta                map[string]any       `json:"_meta,omitempty"`
}

// PromptCapabilities describes what content types the agent supports
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// MCPCapabilities describes MCP transport support
type MCPCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// SessionCapabilities describes session features
type SessionCapabilities struct {
	SetMode bool `json:"setMode,omitempty"`
}

// ClientCapabilities received during initialization
type ClientCapabilities struct {
	FS       *FSCapabilities `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
	Meta     map[string]any  `json:"_meta,omitempty"`
}

// FSCapabilities describes file system support
type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// InitializeRequest is sent by client to initialize the agent
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// InitializeResponse is sent by agent in response to initialize
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

// AuthMethod describes an authentication method
type AuthMethod struct {
	Type string `json:"type"`
}

// MCPServer describes an MCP server provided by the client
type MCPServer struct {
	Name       string            `json:"name"`
	Type       string            `json:"type,omitempty"` // stdio, http
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	URL        string            `json:"url,omitempty"`
	AuthHeader string            `json:"authHeader,omitempty"`
}

// NewSessionRequest creates a new session
type NewSessionRequest struct {
	CWD        string         `json:"cwd"`
	MCPServers []MCPServer    `json:"mcpServers,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// NewSessionResponse returns the new session ID
type NewSessionResponse struct {
	SessionID SessionID         `json:"sessionId"`
	Modes     *SessionModeState `json:"modes,omitempty"`
	Meta      map[string]any    `json:"_meta,omitempty"`
}

// SessionModeState describes available session modes
type SessionModeState struct {
	Mode    string   `json:"mode,omitempty"`
	Options []string `json:"options,omitempty"`
}

// LoadSessionRequest loads an existing session
type LoadSessionRequest struct {
	SessionID  SessionID      `json:"sessionId"`
	CWD        string         `json:"cwd"`
	MCPServers []MCPServer    `json:"mcpServers,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// LoadSessionResponse returns the loaded session info
type LoadSessionResponse struct {
	Modes *SessionModeState `json:"modes,omitempty"`
	Meta  map[string]any    `json:"_meta,omitempty"`
}

// ContentBlock represents different content types in prompts and responses
type ContentBlock struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Data        string            `json:"data,omitempty"`
	MimeType    string            `json:"mimeType,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Name        string            `json:"name,omitempty"`
	Resource    *EmbeddedResource `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`
	Meta        map[string]any    `json:"_meta,omitempty"`
}

// ContentBlockType constants
const (
	ContentTypeText         = "text"
	ContentTypeImage        = "image"
	ContentTypeAudio        = "audio"
	ContentTypeResource     = "resource"
	ContentTypeResourceLink = "resource_link"
)

// EmbeddedResource represents an embedded resource in content
type EmbeddedResource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// Annotations provides metadata for content
type Annotations struct {
	Audience []string `json:"audience,omitempty"`
	Priority float64  `json:"priority,omitempty"`
}

// PromptRequest sends a prompt to the agent
type PromptRequest struct {
	SessionID SessionID      `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// PromptResponse indicates prompt completion
type PromptResponse struct {
	StopReason StopReason     `json:"stopReason"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// StopReason indicates why the agent stopped
type StopReason string

// StopReason values
const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonMaxTurnRequests StopReason = "max_turn_requests"
	StopReasonRefusal         StopReason = "refusal"
	StopReasonCancelled       StopReason = "cancelled"
)

// CancelRequest cancels an in-progress prompt
type CancelRequest struct {
	SessionID SessionID `json:"sessionId"`
}

// ListSessionsRequest requests a list of available sessions
type ListSessionsRequest struct {
	Meta map[string]any `json:"_meta,omitempty"`
}

// SessionSummary provides summary information about a session
type SessionSummary struct {
	SessionID    SessionID      `json:"sessionId"`
	CreatedAt    string         `json:"createdAt,omitempty"`
	Title        string         `json:"title,omitempty"`
	MessageCount int            `json:"messageCount,omitempty"`
	Meta         map[string]any `json:"_meta,omitempty"`
}

// ListSessionsResponse returns available sessions
type ListSessionsResponse struct {
	Sessions []SessionSummary `json:"sessions"`
	Meta     map[string]any   `json:"_meta,omitempty"`
}

// SessionUpdateType constants for session/update notifications
const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateThoughtChunk      = "agent_thought_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
	UpdateAvailableCommands = "available_commands"
	UpdateModeChange        = "mode_change"
)

// ToolCallStatus indicates the status of a tool call
type ToolCallStatus string

// ToolCallStatus values
const (
	ToolStatusPending    ToolCallStatus = "pending"
	ToolStatusInProgress ToolCallStatus = "in_progress"
	ToolStatusCompleted  ToolCallStatus = "completed"
	ToolStatusFailed     ToolCallStatus = "failed"
)

// ToolKind categorizes tools by their purpose
type ToolKind string

// ToolKind values
const (
	ToolKindRead    ToolKind = "read"
	ToolKindEdit    ToolKind = "edit"
	ToolKindExecute ToolKind = "execute"
	ToolKindSearch  ToolKind = "search"
	ToolKindFetch   ToolKind = "fetch"
	ToolKindThink   ToolKind = "think"
	ToolKindOther   ToolKind = "other"
)

// ToolCallUpdate represents a tool_call session update
type ToolCallUpdate struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Title         string            `json:"title,omitempty"`
	Kind          ToolKind          `json:"kind,omitempty"`
	Status        ToolCallStatus    `json:"status"`
	RawInput      json.RawMessage   `json:"rawInput,omitempty"`
	Content       []ToolCallContent `json:"content,omitempty"`
}

// ToolCallContent represents content in a tool call result
type ToolCallContent struct {
	Type    string       `json:"type"`
	Content ContentBlock `json:"content,omitempty"`
	Path    string       `json:"path,omitempty"`
	OldText string       `json:"oldText,omitempty"`
	NewText string       `json:"newText,omitempty"`
}

// AgentMessageChunk represents an agent_message_chunk session update
type AgentMessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

// ThoughtChunk represents an agent_thought_chunk session update
type ThoughtChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

// PlanEntryPriority represents the priority of a plan entry
type PlanEntryPriority string

// PlanEntryPriority values
const (
	PlanPriorityHigh   PlanEntryPriority = "high"
	PlanPriorityMedium PlanEntryPriority = "medium"
	PlanPriorityLow    PlanEntryPriority = "low"
)

// PlanEntryStatus represents the status of a plan entry
type PlanEntryStatus string

// PlanEntryStatus values
const (
	PlanStatusPending    PlanEntryStatus = "pending"
	PlanStatusInProgress PlanEntryStatus = "in_progress"
	PlanStatusCompleted  PlanEntryStatus = "completed"
)

// PlanEntry represents a single entry in an agent plan
type PlanEntry struct {
	Content  string            `json:"content"`
	Priority PlanEntryPriority `json:"priority"`
	Status   PlanEntryStatus   `json:"status"`
}

// PlanUpdate represents a plan session update
type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
}

// PermissionOption represents an option for permission requests
type PermissionOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Shortcut  string `json:"shortcut,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

// PermissionOutcome represents the outcome of a permission request
type PermissionOutcome struct {
	Outcome  string `json:"outcome"` // selected, dismissed, timeout
	OptionID string `json:"optionId,omitempty"`
}

// ToolCallForPermission represents tool call info for permission requests
type ToolCallForPermission struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Input      json.RawMessage `json:"input,omitempty"`
}

// RequestPermissionParams for session/request_permission
type RequestPermissionParams struct {
	SessionID SessionID             `json:"sessionId"`
	ToolCall  ToolCallForPermission `json:"toolCall"`
	Message   string                `json:"message,omitempty"`
	Options   []PermissionOption    `json:"options"`
}

// RequestPermissionResponse from session/request_permission
type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}
