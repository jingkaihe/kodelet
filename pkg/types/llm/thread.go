package llm

import (
	"context"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentConfig removed per ADR 027 - subagents now use shell-out via exec.Command

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

// HookConfig is a forward declaration of hooks.HookConfig to avoid circular imports.
// The actual type is defined in pkg/hooks/builtin.go.
type HookConfig struct {
	Handler string // Built-in handler name (e.g., "swap_context")
	Once    bool   // If true, only execute on the first turn
}

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
	// AggregateSubagentUsage aggregates usage from a subagent into this thread's usage
	// This aggregates token counts and costs but NOT context window (which should remain isolated)
	AggregateSubagentUsage(usage Usage)
	// SetRecipeHooks sets the recipe hook configurations for the thread
	SetRecipeHooks(hooks map[string]HookConfig)
	// GetRecipeHooks returns the recipe hook configurations for the thread
	GetRecipeHooks() map[string]HookConfig
}
