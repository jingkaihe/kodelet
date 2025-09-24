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

type ConversationStore = conversations.ConversationStore

const (
	MaxImageFileSize = 5 * 1024 * 1024
	MaxImageCount    = 10
)

type GoogleThread struct {
	client                 *genai.Client
	config                 llmtypes.Config
	backend                string
	state                  tooltypes.State
	messages               []*genai.Content
	usage                  *llmtypes.Usage
	conversationID         string
	isPersisted            bool
	store                  ConversationStore
	thinkingBudget         int32
	toolResults            map[string]tooltypes.StructuredToolResult
	subagentContextFactory llmtypes.SubagentContextFactory
	mu                     sync.Mutex
}

type GoogleResponse struct {
	Text         string
	ThinkingText string
	ToolCalls    []*GoogleToolCall
	Usage        *genai.UsageMetadata
}

type GoogleToolCall struct {
	ID   string
	Name string
	Args map[string]interface{}
}

func (t *GoogleThread) Provider() string {
	return "google"
}
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

func (t *GoogleThread) SetState(s tooltypes.State) {
	t.state = s
}

func (t *GoogleThread) GetState() tooltypes.State {
	return t.state
}

func (t *GoogleThread) GetConfig() llmtypes.Config {
	return t.config
}

func (t *GoogleThread) GetUsage() llmtypes.Usage {
	return *t.usage
}

func (t *GoogleThread) GetConversationID() string {
	return t.conversationID
}

func (t *GoogleThread) SetConversationID(id string) {
	t.conversationID = id
}

func (t *GoogleThread) IsPersisted() bool {
	return t.isPersisted
}

func (t *GoogleThread) EnablePersistence(ctx context.Context, enabled bool) {
	t.isPersisted = enabled

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

func (t *GoogleThread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	var parts []*genai.Part

	if len(imagePaths) > MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), MaxImageCount, MaxImageCount)
		imagePaths = imagePaths[:MaxImageCount]
	}

	for _, imagePath := range imagePaths {
		imagePart, err := t.processImage(imagePath)
		if err != nil {
			logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
			continue
		}
		parts = append(parts, imagePart)
	}

	parts = append(parts, genai.NewPartFromText(message))

	content := genai.NewContentFromParts(parts, genai.RoleUser)
	t.messages = append(t.messages, content)
}

// SendMessage sends a message to the LLM and processes the response
func (t *GoogleThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.config.IsSubAgent && t.conversationID != "" {
		if err := t.processPendingFeedback(ctx); err != nil {
			return "", errors.Wrap(err, "failed to process pending feedback")
		}
	}

	t.AddUserMessage(ctx, message, opt.Images...)

	if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
		if err := t.CompactContext(ctx); err != nil {
			return "", errors.Wrap(err, "failed to compact context")
		}
	}

	maxTurns := opt.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10
	}

	var finalOutput strings.Builder

	for turn := 0; turn < maxTurns; turn++ {
		response, err := t.processMessageExchange(ctx, handler, opt)
		if err != nil {
			return "", err
		}

		finalOutput.WriteString(response.Text)

		t.updateUsage(response.Usage)

		if !t.hasToolCalls(response) {
			break
		}

		t.executeToolCalls(ctx, response, handler, opt)
	}

	handler.HandleDone()

	if !opt.NoSaveConversation && t.isPersisted {
		if err := t.SaveConversation(ctx, false); err != nil {
			return "", errors.Wrap(err, "failed to save conversation")
		}
	}

	return finalOutput.String(), nil
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	errStr := err.Error()
	
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

func (t *GoogleThread) executeWithRetry(ctx context.Context, operation func() error) error {
	retryConfig := t.config.Retry
	if retryConfig.Attempts == 0 {
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

func (t *GoogleThread) GetMessages() ([]llmtypes.Message, error) {
	return t.convertToStandardMessages(), nil
}

func (t *GoogleThread) NewSubAgent(ctx context.Context, config llmtypes.Config) llmtypes.Thread {
	subagentThread, err := NewGoogleThread(config, t.subagentContextFactory)
	if err != nil {
		logger.G(ctx).WithError(err).Error("Failed to create Google subagent")
		return nil
	}
	subagentThread.SetState(t.state)
	return subagentThread
}

func (t *GoogleThread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.toolResults[toolCallID] = result
}

func (t *GoogleThread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	return t.toolResults
}

func (t *GoogleThread) shouldAutoCompact(compactRatio float64) bool {
	if t.usage.MaxContextWindow == 0 {
		return false
	}
	utilizationRatio := float64(t.usage.CurrentContextWindow) / float64(t.usage.MaxContextWindow)
	return utilizationRatio >= compactRatio
}

