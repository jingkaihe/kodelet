package types

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/state"
)

// MessageOpt represents options for sending messages
type MessageOpt struct {
	// PromptCache indicates if prompt caching should be used
	PromptCache bool
	// UseWeakModel allows temporarily overriding the model for this message
	UseWeakModel bool
}

// ThreadKey is the key for the thread in the context
type ThreadKey struct{}

// Thread represents a conversation thread with an LLM
type Thread interface {
	// SetState sets the state for the thread
	SetState(s state.State)
	// GetState returns the current state of the thread
	GetState() state.State
	// AddUserMessage adds a user message to the thread
	AddUserMessage(message string)
	// SendMessage sends a message to the LLM and processes the response
	SendMessage(ctx context.Context, message string, handler MessageHandler, opt MessageOpt) (finalOutput string, err error)
	// GetUsage returns the current token usage for the thread
	GetUsage() Usage
	// GetConversationID returns the current conversation ID
	GetConversationID() string
	// SetConversationID sets the conversation ID
	SetConversationID(id string)
	// SaveConversation saves the current thread to the conversation store
	SaveConversation(ctx context.Context, summarise bool) error
	// IsPersisted returns whether this thread is being persisted
	IsPersisted() bool
	// EnablePersistence enables conversation persistence for this thread
	EnablePersistence(enabled bool)
}
