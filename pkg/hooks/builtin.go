package hooks

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BuiltinHandler defines the interface for built-in hook handlers.
// These are internal handlers that can be referenced by recipes.
// Handlers return nil for hook types they don't implement.
type BuiltinHandler interface {
	// Name returns the handler identifier used in recipe metadata
	Name() string

	// HandleBeforeToolCall is called when before_tool_call event fires.
	// Returns nil if this handler doesn't implement before_tool_call.
	HandleBeforeToolCall(ctx context.Context, thread llmtypes.Thread, payload BeforeToolCallPayload) (*BeforeToolCallResult, error)

	// HandleAfterToolCall is called when after_tool_call event fires.
	// Returns nil if this handler doesn't implement after_tool_call.
	HandleAfterToolCall(ctx context.Context, thread llmtypes.Thread, payload AfterToolCallPayload) (*AfterToolCallResult, error)

	// HandleUserMessageSend is called when user_message_send event fires.
	// Returns nil if this handler doesn't implement user_message_send.
	HandleUserMessageSend(ctx context.Context, thread llmtypes.Thread, payload UserMessageSendPayload) (*UserMessageSendResult, error)

	// HandleAgentStop is called when agent_stop event fires.
	// Returns nil if this handler doesn't implement agent_stop.
	HandleAgentStop(ctx context.Context, thread llmtypes.Thread, payload AgentStopPayload) (*AgentStopResult, error)

	// HandleTurnEnd is called when turn_end event fires.
	// Returns nil if this handler doesn't implement turn_end.
	HandleTurnEnd(ctx context.Context, thread llmtypes.Thread, payload TurnEndPayload) (*TurnEndResult, error)
}

// BaseBuiltinHandler provides default nil implementations for all hook methods.
// Embed this in your handler to only override the hooks you care about.
// Returning (nil, nil) indicates the handler does not implement that hook type.
type BaseBuiltinHandler struct{}

// HandleBeforeToolCall returns nil to indicate this handler doesn't implement before_tool_call.
func (h *BaseBuiltinHandler) HandleBeforeToolCall(_ context.Context, _ llmtypes.Thread, _ BeforeToolCallPayload) (*BeforeToolCallResult, error) {
	return nil, nil //nolint:nilnil // nil indicates handler doesn't implement this hook
}

// HandleAfterToolCall returns nil to indicate this handler doesn't implement after_tool_call.
func (h *BaseBuiltinHandler) HandleAfterToolCall(_ context.Context, _ llmtypes.Thread, _ AfterToolCallPayload) (*AfterToolCallResult, error) {
	return nil, nil //nolint:nilnil // nil indicates handler doesn't implement this hook
}

// HandleUserMessageSend returns nil to indicate this handler doesn't implement user_message_send.
func (h *BaseBuiltinHandler) HandleUserMessageSend(_ context.Context, _ llmtypes.Thread, _ UserMessageSendPayload) (*UserMessageSendResult, error) {
	return nil, nil //nolint:nilnil // nil indicates handler doesn't implement this hook
}

// HandleAgentStop returns nil to indicate this handler doesn't implement agent_stop.
func (h *BaseBuiltinHandler) HandleAgentStop(_ context.Context, _ llmtypes.Thread, _ AgentStopPayload) (*AgentStopResult, error) {
	return nil, nil //nolint:nilnil // nil indicates handler doesn't implement this hook
}

// HandleTurnEnd returns nil to indicate this handler doesn't implement turn_end.
func (h *BaseBuiltinHandler) HandleTurnEnd(_ context.Context, _ llmtypes.Thread, _ TurnEndPayload) (*TurnEndResult, error) {
	return nil, nil //nolint:nilnil // nil indicates handler doesn't implement this hook
}

// Ensure BaseBuiltinHandler implements all hook methods (compile-time check)
var _ interface {
	HandleBeforeToolCall(context.Context, llmtypes.Thread, BeforeToolCallPayload) (*BeforeToolCallResult, error)
	HandleAfterToolCall(context.Context, llmtypes.Thread, AfterToolCallPayload) (*AfterToolCallResult, error)
	HandleUserMessageSend(context.Context, llmtypes.Thread, UserMessageSendPayload) (*UserMessageSendResult, error)
	HandleAgentStop(context.Context, llmtypes.Thread, AgentStopPayload) (*AgentStopResult, error)
	HandleTurnEnd(context.Context, llmtypes.Thread, TurnEndPayload) (*TurnEndResult, error)
} = (*BaseBuiltinHandler)(nil)

// ToolCallContext provides context for tool call hooks
type ToolCallContext struct {
	ToolName   string
	ToolInput  string
	ToolUserID string
	ToolOutput *tooltypes.StructuredToolResult // Only for after_tool_call
}

// BuiltinRegistry holds registered built-in handlers
type BuiltinRegistry struct {
	handlers map[string]BuiltinHandler
}

// DefaultBuiltinRegistry returns registry with default handlers
func DefaultBuiltinRegistry() *BuiltinRegistry {
	r := &BuiltinRegistry{
		handlers: make(map[string]BuiltinHandler),
	}
	r.Register(&SwapContextHandler{})
	return r
}

// Register adds a handler to the registry
func (r *BuiltinRegistry) Register(h BuiltinHandler) {
	r.handlers[h.Name()] = h
}

// Get retrieves a handler by name
func (r *BuiltinRegistry) Get(name string) (BuiltinHandler, bool) {
	h, ok := r.handlers[name]
	return h, ok
}
