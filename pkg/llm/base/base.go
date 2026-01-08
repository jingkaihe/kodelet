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

// BaseThread contains shared fields that are common across all LLM provider implementations.
// Provider-specific Thread structs should embed this struct to inherit common functionality.
type BaseThread struct {
	Config                 llmtypes.Config                           // LLM configuration
	State                  tooltypes.State                           // Tool execution state
	Usage                  *llmtypes.Usage                           // Token usage tracking
	ConversationID         string                                    // Unique conversation identifier
	IsPersisted_           bool                                      // Whether conversation is being persisted
	Store                  ConversationStore                         // Conversation persistence store
	ToolResults            map[string]tooltypes.StructuredToolResult // Maps tool_call_id to structured result
	SubagentContextFactory llmtypes.SubagentContextFactory           // Factory for creating subagent contexts
	HookTrigger            hooks.Trigger                             // Hook trigger for lifecycle hooks

	Mu             sync.Mutex // Mutex for thread-safe operations on usage and tool results
	ConversationMu sync.Mutex // Mutex for conversation-related operations
}

// NewBaseThread creates a new BaseThread with initialized fields.
// This constructor should be called by provider-specific constructors.
func NewBaseThread(
	config llmtypes.Config,
	conversationID string,
	subagentContextFactory llmtypes.SubagentContextFactory,
	hookTrigger hooks.Trigger,
) *BaseThread {
	return &BaseThread{
		Config:                 config,
		ConversationID:         conversationID,
		IsPersisted_:           false,
		Usage:                  &llmtypes.Usage{},
		ToolResults:            make(map[string]tooltypes.StructuredToolResult),
		SubagentContextFactory: subagentContextFactory,
		HookTrigger:            hookTrigger,
	}
}
