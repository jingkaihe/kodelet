// Package builtin provides built-in hooks that are automatically registered
// with the hook manager. These hooks handle system-level functionality like
// context compaction coordination.
package builtin

import (
	"github.com/jingkaihe/kodelet/pkg/hooks"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// CompactHook coordinates the compact recipe flow.
// It handles two scenarios:
// 1. When compact recipe finishes: extract summary and apply as mutation
// 2. When context threshold reached: trigger compact recipe (optional, via config)
type CompactHook struct {
	// EnableAutoCompactCallback enables the hook to trigger compact recipe
	// when context threshold is reached. This is typically disabled since
	// auto-compact is handled in the providers before LLM calls.
	EnableAutoCompactCallback bool
}

// NewCompactHook creates a new CompactHook
func NewCompactHook() *CompactHook {
	return &CompactHook{
		EnableAutoCompactCallback: false, // Auto-compact handled in providers
	}
}

// Name returns the hook name
func (h *CompactHook) Name() string {
	return "builtin:compact"
}

// Type returns the hook type this handles
func (h *CompactHook) Type() hooks.HookType {
	return hooks.HookTypeAgentStop
}

// Execute processes the agent_stop payload and returns a result
func (h *CompactHook) Execute(payload *hooks.AgentStopPayload) (*hooks.AgentStopResult, error) {
	// Case 1: Compact recipe just finished - apply its output as mutation
	if payload.InvokedRecipe == "compact" {
		// Extract the assistant's summary from the last message
		var summary string
		for i := len(payload.Messages) - 1; i >= 0; i-- {
			if payload.Messages[i].Role == "assistant" {
				summary = payload.Messages[i].Content
				break
			}
		}

		if summary != "" {
			// Get target conversation from callback args if available
			targetConvID := ""
			if payload.CallbackArgs != nil {
				targetConvID = payload.CallbackArgs["target_conversation_id"]
			}

			return &hooks.AgentStopResult{
				Result: hooks.HookResultMutate,
				Messages: []llmtypes.Message{
					{Role: "user", Content: summary},
				},
				TargetConversationID: targetConvID,
			}, nil
		}
		return &hooks.AgentStopResult{}, nil
	}

	// Case 2: Regular session - optionally check if auto-compact should trigger via callback
	// This is typically disabled since auto-compact is handled in providers
	if h.EnableAutoCompactCallback && payload.AutoCompactEnabled && payload.Usage.MaxContextWindow > 0 {
		ratio := float64(payload.Usage.CurrentContextWindow) / float64(payload.Usage.MaxContextWindow)
		if ratio >= payload.AutoCompactThreshold {
			return &hooks.AgentStopResult{
				Result:   hooks.HookResultCallback,
				Callback: "compact",
				CallbackArgs: map[string]string{
					"target_conversation_id": payload.ConvID,
				},
			}, nil
		}
	}

	// No action needed
	return &hooks.AgentStopResult{}, nil
}

// Hook is an interface for hooks that can be registered programmatically
type Hook interface {
	Name() string
	Type() hooks.HookType
	Execute(payload *hooks.AgentStopPayload) (*hooks.AgentStopResult, error)
}

// GetBuiltinHooks returns all built-in hooks
func GetBuiltinHooks() []Hook {
	return []Hook{
		NewCompactHook(),
	}
}

// RegisterBuiltinHooks registers all built-in hooks with the given hook manager
func RegisterBuiltinHooks(manager *hooks.HookManager) {
	for _, hook := range GetBuiltinHooks() {
		manager.RegisterBuiltinHook(hook)
	}
}
