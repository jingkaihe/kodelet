// Package google provides a client implementation for interacting with Google's GenAI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing
// supporting both Vertex AI and Gemini API backends.
package google

import (
	"context"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
	"github.com/pkg/errors"
	"github.com/avast/retry-go/v4"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package
type ConversationStore = conversations.ConversationStore

// Constants for image processing
const (
	MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
	MaxImageCount    = 10              // Maximum 10 images per message
)

// GoogleThread implements the Thread interface using Google's GenAI API
type GoogleThread struct {
	client                 *genai.Client
	config                 llmtypes.Config
	backend                string                         // "gemini" or "vertexai"
	state                  tooltypes.State
	messages               []*genai.Content               // Google's message format
	usage                  *llmtypes.Usage
	conversationID         string
	isPersisted            bool
	store                  ConversationStore
	thinkingBudget         int32                          // Token budget for thinking
	toolResults            map[string]tooltypes.StructuredToolResult // For structured tool storage
	subagentContextFactory llmtypes.SubagentContextFactory // Cross-provider subagent support
	mu                     sync.Mutex
}

// GoogleResponse represents a response from Google GenAI
type GoogleResponse struct {
	Text         string
	ThinkingText string
	ToolCalls    []*GoogleToolCall
	Usage        *genai.UsageMetadata
}

// GoogleToolCall represents a tool call in Google's format
type GoogleToolCall struct {
	ID   string
	Name string
	Args map[string]interface{}
}

// Provider returns the provider name for this thread
func (t *GoogleThread) Provider() string {
	return "google"
}

// NewGoogleThread creates a new thread with Google's GenAI API
func NewGoogleThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*GoogleThread, error) {
	// Create a copy of the config to avoid modifying the original
	configCopy := config
	
	// Apply defaults if not provided
	if configCopy.Model == "" {
		configCopy.Model = "gemini-2.5-pro"
	}
	if configCopy.WeakModel == "" {
		configCopy.WeakModel = "gemini-2.5-flash"
	}
	if configCopy.MaxTokens == 0 {
		configCopy.MaxTokens = 8192
	}

	// Auto-detect backend based on config and environment
	backend := detectBackend(configCopy)

	clientConfig := &genai.ClientConfig{}

	switch backend {
	case "vertexai":
		clientConfig.Backend = genai.BackendVertexAI
		if configCopy.Google != nil {
			clientConfig.Project = configCopy.Google.Project
			clientConfig.Location = configCopy.Google.Location
		}
		// Use ADC, service account, or API key

	case "gemini":
		clientConfig.Backend = genai.BackendGeminiAPI
		if configCopy.Google != nil {
			clientConfig.APIKey = configCopy.Google.APIKey
		}
	}

	client, err := genai.NewClient(context.Background(), clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Google GenAI client")
	}

	thinkingBudget := int32(8000) // Default thinking budget
	if configCopy.Google != nil && configCopy.Google.ThinkingBudget > 0 {
		thinkingBudget = configCopy.Google.ThinkingBudget
	}

	return &GoogleThread{
		client:                 client,
		config:                 configCopy,
		backend:                backend,
		usage:                  &llmtypes.Usage{},
		conversationID:         convtypes.GenerateID(),
		isPersisted:            false,
		toolResults:            make(map[string]tooltypes.StructuredToolResult),
		subagentContextFactory: subagentContextFactory,
		thinkingBudget:         thinkingBudget,
	}, nil
}

// SetState sets the state for the thread
func (t *GoogleThread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *GoogleThread) GetState() tooltypes.State {
	return t.state
}

// GetConfig returns the configuration of the thread
func (t *GoogleThread) GetConfig() llmtypes.Config {
	return t.config
}

// GetUsage returns the current token usage for the thread
func (t *GoogleThread) GetUsage() llmtypes.Usage {
	return *t.usage
}

// GetConversationID returns the current conversation ID
func (t *GoogleThread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *GoogleThread) SetConversationID(id string) {
	t.conversationID = id
}

// IsPersisted returns whether this thread is being persisted
func (t *GoogleThread) IsPersisted() bool {
	return t.isPersisted
}

// EnablePersistence enables conversation persistence for this thread
func (t *GoogleThread) EnablePersistence(ctx context.Context, enabled bool) {
	t.isPersisted = enabled

	// Initialize the store if enabling persistence and it's not already initialized
	if enabled && t.store == nil {
		store, err := conversations.GetConversationStore(ctx)
		if err != nil {
			// Log the error but continue without persistence
			logger.G(ctx).WithError(err).Error("Error initializing conversation store")
			t.isPersisted = false
			return
		}
		t.store = store
	}
}

// AddUserMessage adds a user message with optional images to the thread
func (t *GoogleThread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	var parts []*genai.Part

	// Validate image count
	if len(imagePaths) > MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), MaxImageCount, MaxImageCount)
		imagePaths = imagePaths[:MaxImageCount]
	}

	// Process images and add them as parts
	for _, imagePath := range imagePaths {
		imagePart, err := t.processImage(imagePath)
		if err != nil {
			logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
			continue
		}
		parts = append(parts, imagePart)
	}

	// Add text part
	parts = append(parts, genai.NewPartFromText(message))

	// Create content and add to messages
	content := genai.NewContentFromParts(parts, genai.RoleUser)
	t.messages = append(t.messages, content)
}

// SendMessage sends a message to the LLM and processes the response
func (t *GoogleThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check for pending feedback (following existing pattern)
	if !t.config.IsSubAgent && t.conversationID != "" {
		if err := t.processPendingFeedback(ctx); err != nil {
			return "", errors.Wrap(err, "failed to process pending feedback")
		}
	}

	// Add user message to history
	t.AddUserMessage(ctx, message, opt.Images...)

	// Auto-compaction check (following existing pattern)
	if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
		if err := t.CompactContext(ctx); err != nil {
			return "", errors.Wrap(err, "failed to compact context")
		}
	}

	maxTurns := opt.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10 // Default max turns
	}

	var finalOutput strings.Builder

	// Message exchange loop (similar to existing providers)
	for turn := 0; turn < maxTurns; turn++ {
		response, err := t.processMessageExchange(ctx, handler, opt)
		if err != nil {
			return "", err
		}

		finalOutput.WriteString(response.Text)

		// Update usage tracking
		t.updateUsage(response.Usage)

		// Check if we need another turn (tool calls present)
		if !t.hasToolCalls(response) {
			break
		}

		// Execute tools and add results
		t.executeToolCalls(ctx, response, handler, opt)
	}

	handler.HandleDone()

	// Save conversation if enabled
	if !opt.NoSaveConversation && t.isPersisted {
		if err := t.SaveConversation(ctx, false); err != nil {
			return "", errors.Wrap(err, "failed to save conversation")
		}
	}

	return finalOutput.String(), nil
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Don't retry context cancellations
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Retry on network errors and specific Google API errors
	errStr := err.Error()
	
	// Common retryable patterns
	retryablePatterns := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"temporary failure",
		"service unavailable",
		"internal error",
		"quota exceeded",
		"rate limit",
		"too many requests",
	}
	
	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}
	
	return false
}

// executeWithRetry executes a function with retry logic
func (t *GoogleThread) executeWithRetry(ctx context.Context, operation func() error) error {
	retryConfig := t.config.Retry
	if retryConfig.Attempts == 0 {
		// If no retry config, just execute once
		return operation()
	}

	initialDelay := time.Duration(retryConfig.InitialDelay) * time.Millisecond
	maxDelay := time.Duration(retryConfig.MaxDelay) * time.Millisecond

	var delayType retry.DelayTypeFunc
	switch retryConfig.BackoffType {
	case "fixed":
		delayType = retry.FixedDelay
	case "exponential":
		fallthrough
	default:
		delayType = retry.BackOffDelay
	}

	var originalErrors []error
	err := retry.Do(
		func() error {
			err := operation()
			if err != nil {
				originalErrors = append(originalErrors, err)
			}
			return err
		},
		retry.RetryIf(isRetryableError),
		retry.Attempts(uint(retryConfig.Attempts)),
		retry.Delay(initialDelay),
		retry.DelayType(delayType),
		retry.MaxDelay(maxDelay),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			logger.G(ctx).WithError(err).WithField("attempt", n+1).WithField("max_attempts", retryConfig.Attempts).Warn("retrying Google GenAI API call")
		}),
	)

	if err != nil {
		return errors.Wrapf(err, "all %d retry attempts failed, original errors: %v", len(originalErrors), originalErrors)
	}

	return nil
}

// GetMessages returns the messages from the thread
func (t *GoogleThread) GetMessages() ([]llmtypes.Message, error) {
	// Convert Google Content format back to llmtypes.Message
	return t.convertToStandardMessages(), nil
}

// NewSubAgent creates a new subagent thread with the given configuration
func (t *GoogleThread) NewSubAgent(ctx context.Context, config llmtypes.Config) llmtypes.Thread {
	subagentThread, err := NewGoogleThread(config, t.subagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create Google subagent")
		return nil
	}
	subagentThread.SetState(t.state)
	return subagentThread
}

// SaveConversation is implemented in persistence.go

// SetStructuredToolResult stores structured tool results for persistence
func (t *GoogleThread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.toolResults[toolCallID] = result
}

// GetStructuredToolResults returns all structured tool results
func (t *GoogleThread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	return t.toolResults
}

// Helper functions implemented in other files:
// - processImage: streaming.go
// - processPendingFeedback: persistence.go  
// - processMessageExchange: streaming.go
// - executeToolCalls: tools.go
// - convertToStandardMessages: streaming.go
// - CompactContext: persistence.go

// shouldAutoCompact checks if auto-compaction should be triggered
func (t *GoogleThread) shouldAutoCompact(compactRatio float64) bool {
	if t.usage.MaxContextWindow == 0 {
		return false
	}
	utilizationRatio := float64(t.usage.CurrentContextWindow) / float64(t.usage.MaxContextWindow)
	return utilizationRatio >= compactRatio
}

// updateUsage is implemented in models.go



// ExtractMessages is implemented in persistence.go

