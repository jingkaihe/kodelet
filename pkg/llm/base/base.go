// Package base provides shared functionality for LLM thread implementations.
// It contains common fields, methods, and constants used across all LLM providers
// (Anthropic, OpenAI, and Google) to reduce code duplication.
package base

import (
	"context"
	"maps"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Constants for image processing (shared across all providers)
const (
	MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
	MaxImageCount    = 10              // Maximum 10 images per message
)

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package in provider implementations.
type ConversationStore = conversations.ConversationStore

// LoadConversationFunc is a callback function type for provider-specific conversation loading.
// This is called by EnablePersistence when persistence is enabled and a store is available.
type LoadConversationFunc func(ctx context.Context)

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
	LoadConversation       LoadConversationFunc                      // Provider-specific callback for loading conversations
	CallbackRegistry       *hooks.CallbackRegistry                   // Registry for recipe callbacks

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

// EnablePersistence enables or disables conversation persistence.
// When enabling persistence:
//   - Initializes the conversation store if not already initialized
//   - Calls the LoadConversation callback to load any existing conversation
//
// If store initialization fails, persistence is disabled and the error is logged.
// The LoadConversation callback must be set by the provider before calling this method
// if provider-specific conversation loading is needed.
// This method is thread-safe and uses mutex locking.
func (t *Thread) EnablePersistence(ctx context.Context, enabled bool) {
	t.ConversationMu.Lock()
	defer t.ConversationMu.Unlock()

	t.Persisted = enabled

	// Initialize the store if enabling persistence and it's not already initialized
	if enabled && t.Store == nil {
		store, err := conversations.GetConversationStore(ctx)
		if err != nil {
			// Log the error but continue without persistence
			logger.G(ctx).WithError(err).Error("Error initializing conversation store")
			t.Persisted = false
			return
		}
		t.Store = store
	}

	// If enabling persistence and there's an existing conversation ID,
	// try to load it from the store using the provider-specific callback
	if enabled && t.Store != nil && t.LoadConversation != nil {
		t.LoadConversation(ctx)
	}
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

// EstimateContextWindowFromMessages estimates the context window size based on message content.
// This is useful after compaction to provide an approximate context size before the next API call.
// Uses a rough estimate of ~4 characters per token.
// This method is thread-safe and uses mutex locking.
func (t *Thread) EstimateContextWindowFromMessages(messages []llmtypes.Message) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.Usage == nil {
		return
	}

	// Estimate tokens from message content (rough: ~4 chars per token)
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Content)
	}
	// Minimum 100 tokens as a reasonable baseline estimate
	estimatedTokens := max(totalChars/4, 100)

	t.Usage.CurrentContextWindow = estimatedTokens
	// MaxContextWindow remains unchanged
}

// AggregateSubagentUsage aggregates usage from a subagent into this thread's usage.
// This aggregates token counts and costs but NOT context window metrics
// (CurrentContextWindow and MaxContextWindow remain unchanged to avoid premature auto-compact).
// This method is thread-safe and uses mutex locking.
func (t *Thread) AggregateSubagentUsage(usage llmtypes.Usage) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.Usage == nil {
		t.Usage = &llmtypes.Usage{}
	}
	t.Usage.InputTokens += usage.InputTokens
	t.Usage.OutputTokens += usage.OutputTokens
	t.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
	t.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
	t.Usage.InputCost += usage.InputCost
	t.Usage.OutputCost += usage.OutputCost
	t.Usage.CacheCreationCost += usage.CacheCreationCost
	t.Usage.CacheReadCost += usage.CacheReadCost
	// Note: CurrentContextWindow and MaxContextWindow are intentionally NOT aggregated
	// to keep context window tracking isolated per thread for accurate auto-compact decisions
}

// SetStructuredToolResult stores the structured result for a tool call.
// This method is thread-safe and uses mutex locking.
func (t *Thread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.ToolResults == nil {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.ToolResults[toolCallID] = result
}

// GetStructuredToolResults returns a copy of all structured tool results.
// This method is thread-safe and uses mutex locking.
// A copy is returned to avoid race conditions.
func (t *Thread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.ToolResults == nil {
		return make(map[string]tooltypes.StructuredToolResult)
	}
	result := make(map[string]tooltypes.StructuredToolResult)
	maps.Copy(result, t.ToolResults)
	return result
}

// SetStructuredToolResults replaces all structured tool results with the provided map.
// This method is thread-safe and uses mutex locking.
// A copy of the input map is made to avoid external modifications.
func (t *Thread) SetStructuredToolResults(results map[string]tooltypes.StructuredToolResult) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if results == nil {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
	} else {
		t.ToolResults = make(map[string]tooltypes.StructuredToolResult)
		maps.Copy(t.ToolResults, results)
	}
}

// ShouldAutoCompact checks if auto-compact should be triggered based on context window utilization.
// Returns true if the current context window utilization ratio >= compactRatio.
// Returns false if compactRatio is invalid (<= 0 or > 1) or MaxContextWindow is 0.
func (t *Thread) ShouldAutoCompact(compactRatio float64) bool {
	if compactRatio <= 0.0 || compactRatio > 1.0 {
		return false
	}

	usage := t.GetUsage()
	if usage.MaxContextWindow == 0 {
		return false
	}

	utilizationRatio := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
	return utilizationRatio >= compactRatio
}

// ProcessAgentStopHookResult handles the result from an agent_stop hook.
// Returns (shouldContinue, followUpMessages, error).
// - shouldContinue: true if the agent should continue processing (e.g., for continue/callback results)
// - followUpMessages: messages to add to the conversation if continuing
// - error: any error that occurred during processing
//
// The getMessages callback retrieves the current thread's messages for callback execution.
// The replaceMessages callback is called for mutate results to allow provider-specific message replacement.
// The saveConversation callback is called after mutation to persist changes.
func (t *Thread) ProcessAgentStopHookResult(
	ctx context.Context,
	result *hooks.AgentStopResult,
	getMessages func() ([]llmtypes.Message, error),
	replaceMessages func(ctx context.Context, messages []llmtypes.Message),
	saveConversation func(ctx context.Context),
) (bool, []string, error) {
	if result == nil {
		return false, nil, nil
	}

	switch result.Result {
	case hooks.HookResultCallback:
		if t.CallbackRegistry == nil {
			logger.G(ctx).Warn("hook requested callback but no callback registry configured")
			return false, nil, nil
		}

		logger.G(ctx).WithField("callback", result.Callback).Info("hook requested recipe callback")

		// Get current messages to pass to the callback
		var currentMessages []llmtypes.Message
		if getMessages != nil {
			msgs, err := getMessages()
			if err != nil {
				logger.G(ctx).WithError(err).Warn("failed to get messages for callback")
			} else {
				currentMessages = msgs
			}
		}

		callbackResult, err := t.CallbackRegistry.Execute(ctx, result.Callback, result.CallbackArgs, currentMessages)
		if err != nil {
			return false, nil, err
		}

		// Apply callback's messages to current thread (consistent with after_turn handling)
		if len(callbackResult.Messages) > 0 && replaceMessages != nil {
			replaceMessages(ctx, callbackResult.Messages)
			// Update context window estimate after compaction
			t.EstimateContextWindowFromMessages(callbackResult.Messages)
			if saveConversation != nil {
				saveConversation(ctx)
			}
			logger.G(ctx).Info("agent_stop callback applied message mutation")
		}

		return callbackResult.Continue, nil, nil

	case hooks.HookResultMutate:
		logger.G(ctx).Info("hook requested message mutation")
		if len(result.Messages) > 0 && replaceMessages != nil {
			replaceMessages(ctx, result.Messages)
			// Update context window estimate after compaction
			t.EstimateContextWindowFromMessages(result.Messages)
			if saveConversation != nil {
				saveConversation(ctx)
			}
		}
		return false, nil, nil

	case hooks.HookResultContinue:
		// Return follow-up messages for the caller to process
		return true, result.FollowUpMessages, nil

	default:
		// Legacy behavior: return follow-up messages
		if len(result.FollowUpMessages) > 0 {
			return true, result.FollowUpMessages, nil
		}
		return false, nil, nil
	}
}

// SetInvokedRecipe sets the recipe name that invoked this session for hook coordination.
// This updates the HookTrigger to include the recipe context in agent_stop payloads.
func (t *Thread) SetInvokedRecipe(recipe string) {
	t.HookTrigger.InvokedRecipe = recipe
}

// SetCallbackRegistry sets the callback registry for recipe execution during hooks.
// The registry parameter should be *hooks.CallbackRegistry.
func (t *Thread) SetCallbackRegistry(registry interface{}) {
	if r, ok := registry.(*hooks.CallbackRegistry); ok {
		t.CallbackRegistry = r
	}
}

// ProcessAfterTurnResult handles the result from an after_turn hook.
// Returns error if processing failed.
//
// The getMessages callback retrieves the current thread's messages for callback execution.
// The replaceMessages callback is called for mutate results or after callback execution
// to allow provider-specific message replacement.
// The saveConversation callback is called after mutation to persist changes.
func (t *Thread) ProcessAfterTurnResult(
	ctx context.Context,
	result *hooks.AfterTurnResult,
	getMessages func() ([]llmtypes.Message, error),
	replaceMessages func(ctx context.Context, messages []llmtypes.Message),
	saveConversation func(ctx context.Context),
) error {
	if result == nil || result.Result == hooks.HookResultNone {
		return nil
	}

	switch result.Result {
	case hooks.HookResultMutate:
		// Direct mutation - apply messages to current thread immediately
		logger.G(ctx).Info("after_turn hook requested message mutation")
		if len(result.Messages) > 0 && replaceMessages != nil {
			replaceMessages(ctx, result.Messages)
			// Update context window estimate after compaction
			t.EstimateContextWindowFromMessages(result.Messages)
			if saveConversation != nil {
				saveConversation(ctx)
			}
		}
		return nil

	case hooks.HookResultCallback:
		// Execute callback (e.g., compact) and apply its result to current thread
		if t.CallbackRegistry == nil {
			logger.G(ctx).Warn("after_turn hook requested callback but no callback registry configured")
			return nil
		}

		logger.G(ctx).WithField("callback", result.Callback).Info("after_turn hook requested recipe callback")

		// Get current messages to pass to the callback (e.g., for compact to summarize)
		var currentMessages []llmtypes.Message
		if getMessages != nil {
			msgs, err := getMessages()
			if err != nil {
				logger.G(ctx).WithError(err).Warn("failed to get messages for callback")
			} else {
				currentMessages = msgs
			}
		}

		callbackResult, err := t.CallbackRegistry.Execute(ctx, result.Callback, result.CallbackArgs, currentMessages)
		if err != nil {
			return err
		}

		// Apply callback's mutation to current thread and save
		if len(callbackResult.Messages) > 0 && replaceMessages != nil {
			replaceMessages(ctx, callbackResult.Messages)
			// Update context window estimate after compaction
			t.EstimateContextWindowFromMessages(callbackResult.Messages)
			if saveConversation != nil {
				saveConversation(ctx)
			}
			logger.G(ctx).Info("after_turn callback applied message mutation to current thread")
		}
		return nil

	default:
		return nil
	}
}

// CreateMessageSpan creates a new tracing span for LLM message processing.
// It includes common attributes shared across all providers and allows for additional
// provider-specific attributes to be passed in.
//
// Common attributes included:
//   - model, max_tokens, weak_model_max_tokens, is_sub_agent
//   - conversation_id, is_persisted, message_length, use_weak_model
//
// Provider-specific attributes (passed via extraAttributes):
//   - Anthropic: thinking_budget_tokens, prompt_cache
//   - OpenAI: reasoning_effort, use_copilot
//   - Google: backend
func (t *Thread) CreateMessageSpan(
	ctx context.Context,
	tracer trace.Tracer,
	message string,
	opt llmtypes.MessageOpt,
	extraAttributes ...attribute.KeyValue,
) (context.Context, trace.Span) {
	attributes := []attribute.KeyValue{
		attribute.String("model", t.Config.Model),
		attribute.Int("max_tokens", t.Config.MaxTokens),
		attribute.Int("weak_model_max_tokens", t.Config.WeakModelMaxTokens),
		attribute.Bool("use_weak_model", opt.UseWeakModel),
		attribute.Bool("is_sub_agent", t.Config.IsSubAgent),
		attribute.String("conversation_id", t.ConversationID),
		attribute.Bool("is_persisted", t.Persisted),
		attribute.Int("message_length", len(message)),
	}

	attributes = append(attributes, extraAttributes...)

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// FinalizeMessageSpan records final metrics and status to the span before ending it.
// It includes common usage attributes and allows for additional provider-specific attributes.
//
// Common attributes included:
//   - tokens.input, tokens.output, cost.total
//   - context_window.current, context_window.max
//
// Provider-specific attributes (passed via extraAttributes):
//   - Anthropic: tokens.cache_creation, tokens.cache_read
//   - Google: tokens.cache_read
func (t *Thread) FinalizeMessageSpan(span trace.Span, err error, extraAttributes ...attribute.KeyValue) {
	usage := t.GetUsage()
	attributes := []attribute.KeyValue{
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
		attribute.Float64("cost.total", usage.TotalCost()),
		attribute.Int("context_window.current", usage.CurrentContextWindow),
		attribute.Int("context_window.max", usage.MaxContextWindow),
	}

	attributes = append(attributes, extraAttributes...)
	span.SetAttributes(attributes...)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetStatus(codes.Ok, "")
		span.AddEvent("message_processing_completed")
	}
	span.End()
}
