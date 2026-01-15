// Package builtin provides built-in hooks that are automatically registered
// with the hook manager. These hooks handle system-level functionality like
// context compaction coordination.
package builtin

import (
	"github.com/jingkaihe/kodelet/pkg/hooks"
)

// AfterTurnCompactHook checks context threshold after each turn and triggers compact.
// This enables proactive compaction during long multi-turn sessions.
//
// When the threshold is reached, it returns a callback result to trigger the
// "compact" recipe. The recipe runs, produces a summary, and executeRecipe
// returns the summary as messages which are then applied to the main thread.
type AfterTurnCompactHook struct{}

// NewAfterTurnCompactHook creates a new AfterTurnCompactHook
func NewAfterTurnCompactHook() *AfterTurnCompactHook {
	return &AfterTurnCompactHook{}
}

// Name returns the hook name
func (h *AfterTurnCompactHook) Name() string {
	return "builtin:compact-trigger"
}

// Type returns the hook type this handles
func (h *AfterTurnCompactHook) Type() hooks.HookType {
	return hooks.HookTypeAfterTurn
}

// Execute checks context threshold and triggers compact callback if needed
func (h *AfterTurnCompactHook) Execute(payload *hooks.AfterTurnPayload) (*hooks.AfterTurnResult, error) {
	// Only trigger if auto-compact is enabled and we have usage info
	if !payload.AutoCompactEnabled || payload.Usage.MaxContextWindow == 0 {
		return &hooks.AfterTurnResult{}, nil
	}

	ratio := float64(payload.Usage.CurrentContextWindow) / float64(payload.Usage.MaxContextWindow)
	if ratio >= payload.AutoCompactThreshold {
		return &hooks.AfterTurnResult{
			Result:   hooks.HookResultCallback,
			Callback: "compact",
		}, nil
	}

	return &hooks.AfterTurnResult{}, nil
}

// AfterTurnHook is an interface for after_turn hooks that can be registered programmatically
type AfterTurnHook interface {
	Name() string
	Type() hooks.HookType
	Execute(payload *hooks.AfterTurnPayload) (*hooks.AfterTurnResult, error)
}

// GetAfterTurnBuiltinHooks returns all built-in after_turn hooks
func GetAfterTurnBuiltinHooks() []AfterTurnHook {
	return []AfterTurnHook{
		NewAfterTurnCompactHook(),
	}
}

// RegisterBuiltinHooks registers all built-in hooks with the given hook manager
func RegisterBuiltinHooks(manager *hooks.HookManager) {
	for _, hook := range GetAfterTurnBuiltinHooks() {
		manager.RegisterAfterTurnBuiltinHook(hook)
	}
}
