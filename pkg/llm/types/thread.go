package types

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/state"
)

// Thread represents a conversation thread with an LLM
type Thread interface {
	// SetState sets the state for the thread
	SetState(s state.State)
	// GetState returns the current state of the thread
	GetState() state.State
	// AddUserMessage adds a user message to the thread
	AddUserMessage(message string)
	// SendMessage sends a message to the LLM and processes the response
	SendMessage(ctx context.Context, message string, handler MessageHandler, modelOverride ...string) error
	// GetUsage returns the current token usage for the thread
	GetUsage() Usage
}
