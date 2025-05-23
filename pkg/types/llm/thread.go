package llm

import (
	"context"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MessageOpt represents options for sending messages
type MessageOpt struct {
	// PromptCache indicates if prompt caching should be used
	PromptCache bool
	// UseWeakModel allows temporarily overriding the model for this message
	UseWeakModel bool
	// NoToolUse indicates that no tool use should be performed
	NoToolUse bool
	// NoSaveConversation indicates that the following conversation should not be saved
	NoSaveConversation bool
	// Images contains image paths or URLs to include with the message
	Images []string
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
	// AddUserMessageWithImages adds a user message with optional images to the thread
	AddUserMessageWithImages(message string, imagePaths ...string)
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
	// Provider returns the provider of the thread
	Provider() string
	// GetMessages returns the messages from the thread
	GetMessages() ([]Message, error)
}
