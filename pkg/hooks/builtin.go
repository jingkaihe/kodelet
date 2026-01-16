package hooks

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// BuiltinHandler defines the interface for built-in hook handlers.
// These are internal handlers that can be referenced by recipes.
type BuiltinHandler interface {
	// Name returns the handler identifier used in recipe metadata
	Name() string

	// HandleTurnEnd is called when turn_end event fires
	// - ctx: context for cancellation
	// - thread: the LLM thread to operate on
	// - response: the assistant's response for this turn
	HandleTurnEnd(ctx context.Context, thread llmtypes.Thread, response string) error
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
