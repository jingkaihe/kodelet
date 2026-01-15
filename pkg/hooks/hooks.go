// Package hooks provides an extensibility mechanism for agent lifecycle events.
// It allows external executables to observe and intercept tool calls, user messages,
// and agent lifecycle events for audit logging, security controls, and monitoring.
package hooks

import (
	"time"
)

// HookType represents the type of lifecycle hook
type HookType string

// Hook type constants define the lifecycle events that can be hooked
const (
	HookTypeBeforeToolCall  HookType = "before_tool_call"
	HookTypeAfterToolCall   HookType = "after_tool_call"
	HookTypeUserMessageSend HookType = "user_message_send"
	HookTypeAfterTurn       HookType = "after_turn"
	HookTypeAgentStop       HookType = "agent_stop"
)

// InvokedBy indicates whether the hook was triggered by main agent or subagent
type InvokedBy string

// InvokedBy constants indicate the source of hook invocation
const (
	InvokedByMain     InvokedBy = "main"
	InvokedBySubagent InvokedBy = "subagent"
)

// Hook represents a discovered hook executable
type Hook struct {
	Name     string   // Filename of the executable
	Path     string   // Full path to the executable
	HookType HookType // Type returned by "hook" command
}

// BuiltinHookExecutor is an interface for agent_stop hooks that can be executed programmatically
type BuiltinHookExecutor interface {
	Name() string
	Type() HookType
	Execute(payload *AgentStopPayload) (*AgentStopResult, error)
}

// AfterTurnHookExecutor is an interface for after_turn hooks that can be executed programmatically
type AfterTurnHookExecutor interface {
	Name() string
	Type() HookType
	Execute(payload *AfterTurnPayload) (*AfterTurnResult, error)
}

// HookManager manages hook discovery and execution
type HookManager struct {
	hooks             map[HookType][]*Hook
	builtinHooks      map[HookType][]BuiltinHookExecutor
	afterTurnBuiltins []AfterTurnHookExecutor
	timeout           time.Duration
}

// DefaultTimeout is the default execution timeout for hooks
const DefaultTimeout = 30 * time.Second

// NewHookManager creates a new HookManager with discovered hooks
// Returns an empty manager (no-op) if discovery fails
func NewHookManager(opts ...DiscoveryOption) (HookManager, error) {
	discovery, err := NewDiscovery(opts...)
	if err != nil {
		return HookManager{}, err
	}

	hooks, err := discovery.DiscoverHooks()
	if err != nil {
		return HookManager{}, err
	}

	manager := HookManager{
		hooks:        hooks,
		builtinHooks: make(map[HookType][]BuiltinHookExecutor),
		timeout:      DefaultTimeout,
	}

	// Register default built-in hooks
	manager.registerDefaultBuiltinHooks()

	return manager, nil
}

// registerDefaultBuiltinHooks registers the default built-in hooks
// This is called automatically by NewHookManager
func (m *HookManager) registerDefaultBuiltinHooks() {
	// Built-in hooks are registered here
	// The compact hook is registered to handle compact recipe coordination
	// Additional built-in hooks can be added here in the future
}

// RegisterBuiltinHook registers a built-in hook executor for agent_stop
func (m *HookManager) RegisterBuiltinHook(hook BuiltinHookExecutor) {
	if m.builtinHooks == nil {
		m.builtinHooks = make(map[HookType][]BuiltinHookExecutor)
	}
	m.builtinHooks[hook.Type()] = append(m.builtinHooks[hook.Type()], hook)
}

// RegisterAfterTurnBuiltinHook registers a built-in hook executor for after_turn
func (m *HookManager) RegisterAfterTurnBuiltinHook(hook AfterTurnHookExecutor) {
	m.afterTurnBuiltins = append(m.afterTurnBuiltins, hook)
}

// SetTimeout sets the execution timeout for hooks
func (m *HookManager) SetTimeout(timeout time.Duration) {
	m.timeout = timeout
}

// HasHooks returns true if there are any hooks registered for the given type
func (m HookManager) HasHooks(hookType HookType) bool {
	if hookType == HookTypeAfterTurn {
		return len(m.hooks[hookType]) > 0 || len(m.afterTurnBuiltins) > 0
	}
	return len(m.hooks[hookType]) > 0 || len(m.builtinHooks[hookType]) > 0
}

// GetHooks returns all hooks registered for the given type
func (m HookManager) GetHooks(hookType HookType) []*Hook {
	return m.hooks[hookType]
}
