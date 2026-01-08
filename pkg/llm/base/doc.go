// Package base provides shared functionality for LLM thread implementations.
//
// This package implements a composition-based approach to reduce code duplication
// across the Anthropic, OpenAI, and Google LLM provider implementations. It
// extracts common fields, methods, and constants that were previously duplicated
// in each provider's thread implementation.
//
// # Architecture
//
// The base package uses Go's struct embedding pattern to share functionality.
// Provider-specific Thread structs embed *base.Thread to inherit common behavior
// while maintaining their own provider-specific fields and methods.
//
//	type AnthropicThread struct {
//	    *base.Thread              // Embedded shared functionality
//	    client   *anthropic.Client
//	    messages []anthropic.MessageParam
//	    // ... other Anthropic-specific fields
//	}
//
// # Shared Components
//
// The Thread struct provides:
//
//   - Configuration management (Config field and GetConfig method)
//   - State management for tool execution (State, GetState, SetState)
//   - Token usage tracking (Usage, GetUsage)
//   - Conversation persistence (Store, EnablePersistence, IsPersisted)
//   - Structured tool results (ToolResults, SetStructuredToolResult, GetStructuredToolResults)
//   - Hook trigger integration (HookTrigger)
//   - Subagent context factory (SubagentContextFactory)
//   - Thread-safe access via mutex locks (Mu, ConversationMu)
//
// # Shared Methods
//
// Methods provided by base.Thread:
//
//   - GetState/SetState: Manage tool execution state
//   - GetConfig: Access LLM configuration
//   - GetConversationID/SetConversationID: Manage conversation ID
//   - IsPersisted/EnablePersistence: Handle conversation persistence
//   - GetUsage: Thread-safe access to token usage
//   - SetStructuredToolResult/GetStructuredToolResults/SetStructuredToolResults:
//     Manage structured tool results with thread safety
//   - ShouldAutoCompact: Check if context window compaction is needed
//   - CreateMessageSpan/FinalizeMessageSpan: OpenTelemetry tracing helpers
//
// # Constants
//
// The package defines shared constants:
//
//   - MaxImageFileSize: Maximum size for image inputs (5MB)
//   - MaxImageCount: Maximum number of images per message (10)
//
// # Usage
//
// Provider implementations should:
//
//  1. Embed *base.Thread in their Thread struct
//  2. Call base.NewThread() in their constructor
//  3. Set the LoadConversation callback for provider-specific conversation loading
//  4. Use inherited methods instead of duplicating implementations
//
// Example:
//
//	func NewProviderThread(config llmtypes.Config, ...) *Thread {
//	    baseThread := base.NewThread(config, conversationID, subagentContextFactory, hookTrigger)
//	    t := &Thread{
//	        Thread: baseThread,
//	        client: newClient(),
//	    }
//	    baseThread.LoadConversation = func(ctx context.Context) {
//	        t.loadConversation(ctx)
//	    }
//	    return t
//	}
//
// # Thread Safety
//
// The following methods are thread-safe and use mutex locking:
//
//   - GetUsage
//   - SetStructuredToolResult
//   - GetStructuredToolResults
//   - SetStructuredToolResults
//   - EnablePersistence
//
// Simple getters and setters (GetState, SetState, etc.) are not thread-safe
// by design, as they are typically called from a single goroutine during
// message processing.
//
// # Tracing
//
// CreateMessageSpan and FinalizeMessageSpan provide OpenTelemetry tracing
// integration. They accept variadic extraAttributes to allow providers to
// add provider-specific attributes without modifying the base package:
//
//	// Provider-specific attribute examples:
//	// Anthropic: thinking_budget_tokens, prompt_cache, cache tokens
//	// OpenAI: reasoning_effort, use_copilot
//	// Google: backend
//
// See ADR 023 for the architectural decision record documenting the
// rationale for this unification approach.
package base
