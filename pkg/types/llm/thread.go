package llm

import (
	"context"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// MessageOpt represents options for sending messages
type MessageOpt struct {
	// PromptCache indicates if prompt caching should be used
	PromptCache bool
	// UseWeakModel allows temporarily overriding the model for this message
	UseWeakModel bool
}

// SubAgentConfig is the key for the thread in the context
type SubAgentConfig struct {
	Thread         Thread         // Thread used by the sub-agent
	MessageHandler MessageHandler // Message handler for the sub-agent
}

// Thread represents a conversation thread with an LLM
type Thread interface {
	// SetState sets the state for the thread
	SetState(s tooltypes.State)
	// GetState returns the current state of the thread
	GetState() tooltypes.State
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
