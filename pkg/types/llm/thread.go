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
	// MaxTurns limits the number of turns within a single SendMessage call
	// A value of 0 means no limit, and negative values are treated as 0
	MaxTurns int
	// CompactRatio is the ratio of context window at which to trigger auto-compact (0.0-1.0)
	CompactRatio float64
	// DisableAutoCompact disables auto-compact functionality
	DisableAutoCompact bool
	// DisableUsageLog disables LLM usage logging for this message
	DisableUsageLog bool
}

// subAgentConfigKey is a dedicated context key type to avoid collisions
type subAgentConfigKey struct{}

// SubAgentConfigKey is the context key for SubAgentConfig
var SubAgentConfigKey = subAgentConfigKey{}

// SubAgentConfig holds the configuration for a subagent in the context
type SubAgentConfig struct {
	Thread             Thread         // Thread used by the sub-agent
	ParentThread       Thread         // Parent thread for usage aggregation
	MessageHandler     MessageHandler // Message handler for the sub-agent
	CompactRatio       float64        // CompactRatio from parent agent
	DisableAutoCompact bool           // DisableAutoCompact from parent agent
}

// SubagentContextFactory is a function type for creating subagent contexts
type SubagentContextFactory func(ctx context.Context, parentThread Thread, handler MessageHandler, compactRatio float64, disableAutoCompact bool) context.Context

// Thread represents a conversation thread with an LLM
type Thread interface {
	// SetState sets the state for the thread
	SetState(s tooltypes.State)
	// GetState returns the current state of the thread
	GetState() tooltypes.State
	// AddUserMessage adds a user message with optional images to the thread
	AddUserMessage(ctx context.Context, message string, imagePaths ...string)
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
	EnablePersistence(ctx context.Context, enabled bool)
	// Provider returns the provider of the thread
	Provider() string
	// GetMessages returns the messages from the thread
	GetMessages() ([]Message, error)
	// GetConfig returns the configuration of the thread
	GetConfig() Config
	// NewSubAgent creates a new subagent thread with the given configuration
	NewSubAgent(ctx context.Context, config Config) Thread
	// AggregateSubagentUsage aggregates usage from a subagent into this thread's usage
	// This aggregates token counts and costs but NOT context window (which should remain isolated)
	AggregateSubagentUsage(usage Usage)
}
