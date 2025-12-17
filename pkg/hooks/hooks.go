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

// HookManager manages hook discovery and execution
type HookManager struct {
	hooks   map[HookType][]*Hook
	timeout time.Duration
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

	return HookManager{
		hooks:   hooks,
		timeout: DefaultTimeout,
	}, nil
}

// SetTimeout sets the execution timeout for hooks
func (m *HookManager) SetTimeout(timeout time.Duration) {
	m.timeout = timeout
}

// HasHooks returns true if there are any hooks registered for the given type
func (m HookManager) HasHooks(hookType HookType) bool {
	return len(m.hooks[hookType]) > 0
}

// GetHooks returns all hooks registered for the given type
func (m HookManager) GetHooks(hookType HookType) []*Hook {
	return m.hooks[hookType]
}
