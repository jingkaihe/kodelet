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
	Config           llmtypes.Config                           // LLM configuration
	State            tooltypes.State                           // Tool execution state
	Usage            *llmtypes.Usage                           // Token usage tracking
	ConversationID   string                                    // Unique conversation identifier
	Persisted        bool                                      // Whether conversation is being persisted
	Store            ConversationStore                         // Conversation persistence store
	ToolResults      map[string]tooltypes.StructuredToolResult // Maps tool_call_id to structured result
	HookTrigger      hooks.Trigger                             // Hook trigger for lifecycle hooks
	LoadConversation LoadConversationFunc                      // Provider-specific callback for loading conversations
	RecipeHooks      map[string]llmtypes.HookConfig            // Recipe hook configurations

	Mu             sync.Mutex // Mutex for thread-safe operations on usage and tool results
	ConversationMu sync.Mutex // Mutex for conversation-related operations
}

// NewThread creates a new Thread with initialized fields.
// This constructor should be called by provider-specific constructors.
func NewThread(
	config llmtypes.Config,
	conversationID string,
	hookTrigger hooks.Trigger,
) *Thread {
	return &Thread{
		Config:         config,
		ConversationID: conversationID,
		Persisted:      false,
		Usage:          &llmtypes.Usage{},
		ToolResults:    make(map[string]tooltypes.StructuredToolResult),
		HookTrigger:    hookTrigger,
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

// PrepareUtilityMode configures a thread for internal utility calls such as summary generation.
// Utility mode disables persistence and lifecycle hooks to avoid side effects.
func (t *Thread) PrepareUtilityMode(ctx context.Context) {
	t.EnablePersistence(ctx, false)
	t.HookTrigger = hooks.Trigger{}
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

// SetRecipeHooks sets the recipe hook configurations for the thread.
// These hooks are triggered at specific lifecycle events (e.g., turn_end).
func (t *Thread) SetRecipeHooks(h map[string]llmtypes.HookConfig) {
	t.RecipeHooks = h
}

// GetRecipeHooks returns the recipe hook configurations for the thread.
func (t *Thread) GetRecipeHooks() map[string]llmtypes.HookConfig {
	return t.RecipeHooks
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

// TryAutoCompact triggers context compaction when auto-compact conditions are met.
// compactFn should perform provider-specific compaction logic.
func (t *Thread) TryAutoCompact(
	ctx context.Context,
	disableAutoCompact bool,
	compactRatio float64,
	compactFn func(context.Context) error,
) {
	if disableAutoCompact || compactFn == nil {
		return
	}

	if !t.ShouldAutoCompact(compactRatio) {
		return
	}

	usage := t.GetUsage()
	utilization := 0.0
	if usage.MaxContextWindow > 0 {
		utilization = float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
	}
	logger.G(ctx).WithField("context_utilization", utilization).Info("triggering auto-compact")

	if err := compactFn(ctx); err != nil {
		logger.G(ctx).WithError(err).Error("failed to auto-compact context")
	} else {
		logger.G(ctx).Info("auto-compact completed successfully")
	}
}

// EstimateContextWindowFromMessage estimates the context window size based on message content.
// This is useful after compaction to provide an approximate context size before the next API call.
// Uses a rough estimate of ~4 characters per token.
// This method is thread-safe and uses mutex locking.
func (t *Thread) EstimateContextWindowFromMessage(msg string) {
	if t.Usage == nil {
		return
	}
	// Estimate tokens from message content (rough: ~4 chars per token)
	estimatedTokens := max(len(msg)/4, 100)

	t.Usage.CurrentContextWindow = estimatedTokens
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
