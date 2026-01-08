// Package base provides shared functionality for LLM thread implementations.
// It contains common fields, methods, and constants used across all LLM providers
// (Anthropic, OpenAI, and Google) to reduce code duplication.
package base

import (
	"sync"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Constants for image processing (shared across all providers)
const (
	MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
	MaxImageCount    = 10              // Maximum 10 images per message
)

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package in provider implementations.
type ConversationStore = conversations.ConversationStore

// Thread contains shared fields that are common across all LLM provider implementations.
// Provider-specific Thread structs should embed this struct to inherit common functionality.
type Thread struct {
	Config                 llmtypes.Config                           // LLM configuration
	State                  tooltypes.State                           // Tool execution state
	Usage                  *llmtypes.Usage                           // Token usage tracking
	ConversationID         string                                    // Unique conversation identifier
	Persisted              bool                                      // Whether conversation is being persisted
	Store                  ConversationStore                         // Conversation persistence store
	ToolResults            map[string]tooltypes.StructuredToolResult // Maps tool_call_id to structured result
	SubagentContextFactory llmtypes.SubagentContextFactory           // Factory for creating subagent contexts
	HookTrigger            hooks.Trigger                             // Hook trigger for lifecycle hooks

	Mu             sync.Mutex // Mutex for thread-safe operations on usage and tool results
	ConversationMu sync.Mutex // Mutex for conversation-related operations
}

// NewThread creates a new Thread with initialized fields.
// This constructor should be called by provider-specific constructors.
func NewThread(
	config llmtypes.Config,
	conversationID string,
	subagentContextFactory llmtypes.SubagentContextFactory,
	hookTrigger hooks.Trigger,
) *Thread {
	return &Thread{
		Config:                 config,
		ConversationID:         conversationID,
		Persisted:              false,
		Usage:                  &llmtypes.Usage{},
		ToolResults:            make(map[string]tooltypes.StructuredToolResult),
		SubagentContextFactory: subagentContextFactory,
		HookTrigger:            hookTrigger,
	}
}

// SetState sets the state for the thread
func (t *Thread) SetState(s tooltypes.State) {
	t.State = s
}

// GetState returns the current state of the thread
func (t *Thread) GetState() tooltypes.State {
	return t.State
}

// GetConfig returns the configuration of the thread
func (t *Thread) GetConfig() llmtypes.Config {
	return t.Config
}

// GetConversationID returns the current conversation ID
func (t *Thread) GetConversationID() string {
	return t.ConversationID
}

// SetConversationID sets the conversation ID and updates the hook trigger
func (t *Thread) SetConversationID(id string) {
	t.ConversationID = id
	t.HookTrigger.SetConversationID(id)
}

// IsPersisted returns whether this thread is being persisted
func (t *Thread) IsPersisted() bool {
	return t.Persisted
}

// GetUsage returns the current token usage for the thread.
// This method is thread-safe and uses mutex locking.
func (t *Thread) GetUsage() llmtypes.Usage {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.Usage == nil {
		return llmtypes.Usage{}
	}
	return *t.Usage
}
