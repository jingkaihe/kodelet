package hooks

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// ContextSwapper is implemented by threads that support context replacement.
// This interface is defined in the hooks package to avoid circular dependencies,
// but is implemented by each provider's thread.
type ContextSwapper interface {
	// SwapContext replaces the current message history with a summary
	SwapContext(ctx context.Context, summary string) error
}

// SwapContextHandler replaces thread messages with the provided summary
type SwapContextHandler struct{}

// Name returns the handler identifier used in recipe metadata
func (h *SwapContextHandler) Name() string {
	return "swap_context"
}

// HandleTurnEnd is called when turn_end event fires to swap the conversation context
func (h *SwapContextHandler) HandleTurnEnd(ctx context.Context, thread llmtypes.Thread, response string) error {
	swapper, ok := thread.(ContextSwapper)
	if !ok {
		return errors.New("thread does not support context swapping")
	}

	return swapper.SwapContext(ctx, response)
}
