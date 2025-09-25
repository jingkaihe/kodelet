// Package google provides a client implementation for interacting with Google's GenAI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing
// supporting both Vertex AI and Gemini API backends.
package google

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"google.golang.org/genai"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/jingkaihe/kodelet/pkg/version"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	backend                string
	state                  tooltypes.State
	messages               []*genai.Content
	usage                  *llmtypes.Usage
	conversationID         string
	summary                string
	isPersisted            bool
	store                  ConversationStore
	thinkingBudget         int32
	toolResults            map[string]tooltypes.StructuredToolResult
	subagentContextFactory llmtypes.SubagentContextFactory
	mu                     sync.Mutex
	conversationMu         sync.Mutex
}

// GoogleResponse represents a response from Google's GenAI API
type GoogleResponse struct {
	Text         string
	ThinkingText string
	ToolCalls    []*GoogleToolCall
	Usage        *genai.UsageMetadata
}

// GoogleToolCall represents a tool call in Google's response format
type GoogleToolCall struct {
	ID   string
	Name string
	Args map[string]interface{}
}

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

// detectBackend determines which backend to use based on configuration and environment
func detectBackend(config llmtypes.Config) string {
	// 1. Explicit configuration takes precedence
	if config.Google != nil && config.Google.Backend != "" {
		return strings.ToLower(config.Google.Backend)
	}

	// 2. Check environment variable for explicit backend preference
	if envBackend := os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"); envBackend != "" {
		if strings.ToLower(envBackend) == "true" || envBackend == "1" {
			return "vertexai"
		}
		return "gemini"
	}

	// 3. Auto-detect based on available configuration and environment

	// Check for Vertex AI indicators
	hasVertexAIConfig := false
	if config.Google != nil {
		hasVertexAIConfig = config.Google.Project != "" || config.Google.Location != ""
	}

	// Check for Vertex AI environment variables
	hasVertexAIEnv := os.Getenv("GOOGLE_CLOUD_PROJECT") != "" ||
		os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" ||
		os.Getenv("GCLOUD_PROJECT") != ""

	// Check for Gemini API key
	hasGeminiAPIKey := config.Google != nil && config.Google.APIKey != ""

	// Check for Gemini API key in environment
	hasGeminiAPIKeyEnv := os.Getenv("GOOGLE_API_KEY") != "" ||
		os.Getenv("GEMINI_API_KEY") != ""

	// Decision logic:
	// - If explicit API key is provided, prefer Gemini API (user choice)
	// - If Vertex AI config is provided, use Vertex AI
	// - If only environment variables exist, prefer Vertex AI (enterprise grade)
	// - If neither is explicitly configured, default to Gemini API
	if hasGeminiAPIKey {
		return "gemini"
	}

	if hasVertexAIConfig {
		return "vertexai"
	}

	if hasVertexAIEnv {
		return "vertexai"
	}

	if hasGeminiAPIKeyEnv {
		return "gemini"
	}

	// Default to Gemini API if no clear indicators
	return "gemini"
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
	t.mu.Lock()
	defer t.mu.Unlock()
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

	// If enabling persistence and there's an existing conversation ID,
	// try to load it from the store
	if enabled && t.conversationID != "" && t.store != nil {
		t.loadConversation(ctx)
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
		imagePart, err := t.processImage(ctx, imagePath)
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
func (t *GoogleThread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	// Check if tracing is enabled and wrap the handler
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.createMessageSpan(ctx, tracer, message, opt)
	defer t.finalizeMessageSpan(span, err)

	// Process pending feedback messages if this is not a subagent
	if !t.config.IsSubAgent && t.conversationID != "" {
		if err := t.processPendingFeedback(ctx, handler); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to process pending feedback, continuing")
		}
	}

	// Add user message with images if provided
	t.AddUserMessage(ctx, message, opt.Images...)

	// Check if auto-compact should be triggered before each exchange
	if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
		logger.G(ctx).WithField("context_utilization", float64(t.GetUsage().CurrentContextWindow)/float64(t.GetUsage().MaxContextWindow)).Info("triggering auto-compact")
		err := t.CompactContext(ctx)
		if err != nil {
			logger.G(ctx).WithError(err).Error("failed to auto-compact context")
		} else {
			logger.G(ctx).Info("auto-compact completed successfully")
		}
	}

	// Main interaction loop for handling tool calls
	turnCount := 0
	maxTurns := opt.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10 // Default maximum turns
	}

OUTER:
	for {
		select {
		case <-ctx.Done():
			logger.G(ctx).Info("stopping kodelet.llm.google")
			break OUTER
		default:
			// Check turn limit
			logger.G(ctx).WithField("turn_count", turnCount).WithField("max_turns", maxTurns).Debug("checking turn limit")

			if turnCount >= maxTurns {
				logger.G(ctx).
					WithField("turn_count", turnCount).
					WithField("max_turns", maxTurns).
					Warn("reached maximum turn limit, stopping interaction")
				break OUTER
			}

			var exchangeOutput string
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, opt)
			if err != nil {
				logger.G(ctx).WithError(err).Error("error processing message exchange")
				if errors.Is(err, context.Canceled) {
					logger.G(ctx).Info("Request to Google GenAI cancelled, stopping kodelet.llm.google")
					break OUTER
				}
				return "", err
			}

			// Increment turn count after each exchange
			turnCount++

			// Update finalOutput with the most recent output
			finalOutput = exchangeOutput

			// If no tools were used, we're done
			if !toolsUsed {
				logger.G(ctx).Debug("no tools used, ending interaction")
				break OUTER
			}
		}
	}

	// Save conversation state after completing the interaction
	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		t.SaveConversation(saveCtx, true)
	}

	if !t.config.IsSubAgent {
		// only main agent can signal done
		handler.HandleDone()
	}
	return finalOutput, nil
}

// processMessageExchange handles a single message exchange with the LLM
func (t *GoogleThread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	// Get relevant contexts from state and regenerate system prompt
	var contexts map[string]string
	if t.state != nil {
		contexts = t.state.DiscoverContexts()
	}
	var systemPrompt string
	if t.config.IsSubAgent {
		systemPrompt = sysprompt.SubAgentPrompt(t.config.Model, t.config, contexts)
	} else {
		systemPrompt = sysprompt.SystemPrompt(t.config.Model, t.config, contexts)
	}

	config := &genai.GenerateContentConfig{
		Temperature:     genai.Ptr(float32(1.0)),
		MaxOutputTokens: int32(t.config.MaxTokens),
		Tools:           toGoogleTools(t.tools(opt)),
	}

	modelName := t.config.Model
	if opt.UseWeakModel && t.config.WeakModel != "" {
		modelName = t.config.WeakModel
		if t.config.WeakModelMaxTokens > 0 {
			config.MaxOutputTokens = int32(t.config.WeakModelMaxTokens)
		}
	}

	if t.supportsThinking(modelName) && !opt.UseWeakModel {
		config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  &t.thinkingBudget,
		}
	}

	prompt := t.buildPrompt(systemPrompt)

	response := &GoogleResponse{}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", modelName),
		attribute.Int("max_tokens", int(config.MaxOutputTokens)),
	)

	// Record start time for usage logging
	apiStartTime := time.Now()

	err := t.executeWithRetry(ctx, func() error {
		response = &GoogleResponse{}
		for chunk, err := range t.client.Models.GenerateContentStream(ctx, modelName, prompt, config) {
			if err != nil {
				return errors.Wrap(err, "streaming failed")
			}

			if len(chunk.Candidates) == 0 {
				continue
			}

			candidate := chunk.Candidates[0]
			if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
				continue
			}

			// Process each part in the response
			for _, part := range candidate.Content.Parts {
				if err := t.processPart(part, response, handler); err != nil {
					logger.G(ctx).WithError(err).Warn("Failed to process response part")
				}
			}

			if chunk.UsageMetadata != nil {
				response.Usage = &genai.UsageMetadata{
					PromptTokenCount:        chunk.UsageMetadata.PromptTokenCount,
					ResponseTokenCount:      chunk.UsageMetadata.CandidatesTokenCount, // Different field name
					CachedContentTokenCount: chunk.UsageMetadata.CachedContentTokenCount,
					TotalTokenCount:         chunk.UsageMetadata.TotalTokenCount,
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}

	// Record API call completion
	if response.Usage != nil {
		telemetry.AddEvent(ctx, "api_call_complete",
			attribute.Int("input_tokens", int(response.Usage.PromptTokenCount)),
			attribute.Int("output_tokens", int(response.Usage.ResponseTokenCount)),
		)
	}

	// Add the assistant response to history
	t.addAssistantMessage(response)

	// Update usage statistics
	t.updateUsage(response.Usage)

	// Execute tool calls if any
	toolsUsed := t.hasToolCalls(response)
	if toolsUsed {
		t.executeToolCalls(ctx, response, handler, opt)
	}

	// Log structured LLM usage after all content processing is complete (main agent only)
	if !t.config.IsSubAgent && !opt.DisableUsageLog {
		outputTokens := 0
		if response.Usage != nil {
			outputTokens = int(response.Usage.ResponseTokenCount)
		}
		usage.LogLLMUsage(ctx, t.GetUsage(), modelName, apiStartTime, outputTokens)
	}

	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		t.SaveConversation(ctx, false)
	}

	// Return whether tools were used in this exchange
	return response.Text, toolsUsed, nil
}

// processPart processes a single part of the Google GenAI response
func (t *GoogleThread) processPart(part *genai.Part, response *GoogleResponse, handler llmtypes.MessageHandler) error {
	switch {
	case part.Text != "":
		if part.Thought {
			handler.HandleThinking(part.Text)
			response.ThinkingText += part.Text
		} else {
			handler.HandleText(part.Text)
			response.Text += part.Text
		}

	case part.FunctionCall != nil:
		toolCall := &GoogleToolCall{
			ID:   generateToolCallID(),
			Name: part.FunctionCall.Name,
			Args: part.FunctionCall.Args,
		}
		response.ToolCalls = append(response.ToolCalls, toolCall)

		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			return errors.Wrap(err, "failed to marshal tool arguments")
		}
		handler.HandleToolUse(toolCall.Name, string(argsJSON))

	case part.CodeExecutionResult != nil:
		result := fmt.Sprintf("Code execution result:\n%s", part.CodeExecutionResult.Output)
		if part.CodeExecutionResult.Outcome == genai.OutcomeUnspecified {
			result += "\nOutcome: Unspecified"
		}
		handler.HandleToolResult("code_execution", result)
		response.Text += result

	default:
		logger.G(context.Background()).Debug("Unhandled part type in Google response")
	}

	return nil
}

// buildPrompt builds the prompt for the Google GenAI API
func (t *GoogleThread) buildPrompt(systemPrompt string) []*genai.Content {
	prompt := []*genai.Content{}

	// Add system message if provided
	if systemPrompt != "" {
		systemContent := genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText(systemPrompt),
		}, genai.RoleUser)
		prompt = append(prompt, systemContent)
	}

	// Add conversation messages
	prompt = append(prompt, t.messages...)
	return prompt
}

// addAssistantMessage adds the assistant's response to the message history
func (t *GoogleThread) addAssistantMessage(response *GoogleResponse) {
	var parts []*genai.Part

	if response.ThinkingText != "" {
		parts = append(parts, &genai.Part{
			Text:    response.ThinkingText,
			Thought: true,
		})
	}

	if response.Text != "" {
		parts = append(parts, genai.NewPartFromText(response.Text))
	}

	for _, toolCall := range response.ToolCalls {
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: toolCall.Name,
				Args: toolCall.Args,
			},
		})
	}

	if len(parts) > 0 {
		content := genai.NewContentFromParts(parts, genai.RoleModel)
		t.messages = append(t.messages, content)
	}
}

// supportsThinking checks if the model supports thinking capabilities
func (t *GoogleThread) supportsThinking(modelName string) bool {
	pricing, exists := ModelPricingMap[modelName]
	if !exists {
		for key, value := range ModelPricingMap {
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(key)) {
				return value.HasThinking
			}
		}
		return false
	}
	return pricing.HasThinking
}

// processImage converts an image path/URL to a Google GenAI part
func (t *GoogleThread) processImage(ctx context.Context, imagePath string) (*genai.Part, error) {
	if strings.HasPrefix(imagePath, "https://") {
		return t.processImageURL(ctx, imagePath)
	}

	if strings.HasPrefix(imagePath, "http://") {
		return nil, errors.New("HTTP URLs are not supported for security reasons, use HTTPS only")
	}

	return t.processImageFile(ctx, imagePath)
}

// processImageURL fetches image from HTTPS URL and creates a Google GenAI part
func (t *GoogleThread) processImageURL(ctx context.Context, imageURL string) (*genai.Part, error) {
	parsedURL, err := url.Parse(imageURL)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid URL: %s", imageURL)
	}

	if parsedURL.Scheme != "https" {
		return nil, errors.Errorf("only HTTPS URLs are supported for security: %s", imageURL)
	}

	originalDomain := parsedURL.Hostname()

	// Create HTTP client with redirect policy similar to web_fetch
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Hostname() != originalDomain {
				return errors.Errorf("redirect to different domain not allowed: %s -> %s",
					originalDomain, req.URL.Hostname())
			}

			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}

			return nil
		},
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request for URL: %s", imageURL)
	}

	req.Header.Set("User-Agent", fmt.Sprintf("Kodelet/%s", version.Get().Version))

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch image from URL: %s", imageURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.Errorf("HTTP error %d when fetching image from %s: %s", resp.StatusCode, imageURL, resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, errors.Errorf("URL does not return an image, got content-type: %s", contentType)
	}

	// Read the image data with size limit
	imageData, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageFileSize+1))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read image data from URL: %s", imageURL)
	}

	if len(imageData) > MaxImageFileSize {
		return nil, errors.Errorf("image from URL %s is too large (%d bytes), maximum is %d bytes",
			imageURL, len(imageData), MaxImageFileSize)
	}

	// Determine MIME type from extension in URL or use content-type
	mimeType := contentType
	if urlPath := parsedURL.Path; urlPath != "" {
		if extMimeType := getMimeTypeFromExtension(filepath.Ext(urlPath)); extMimeType != "" {
			mimeType = extMimeType
		}
	}

	supportedFormats := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	supported := false
	for _, format := range supportedFormats {
		if strings.Contains(mimeType, format) {
			supported = true
			mimeType = format // Normalize to exact format
			break
		}
	}

	if !supported {
		return nil, errors.Errorf("unsupported image format from URL %s: %s (supported: jpeg, png, gif, webp)", imageURL, mimeType)
	}

	return genai.NewPartFromBytes(imageData, mimeType), nil
}

// processImageFile creates a Google GenAI part from a local image file
func (t *GoogleThread) processImageFile(ctx context.Context, imagePath string) (*genai.Part, error) {
	// ctx parameter included for consistency with processImageURL but not used for local file operations
	_ = ctx

	imagePath = strings.TrimPrefix(imagePath, "file://")

	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return nil, errors.Errorf("image file not found: %s", imagePath)
	}

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read image file: %s", imagePath)
	}

	if len(imageData) > MaxImageFileSize {
		return nil, errors.Errorf("image file %s is too large (%d bytes), maximum is %d bytes",
			imagePath, len(imageData), MaxImageFileSize)
	}

	mimeType := getMimeTypeFromExtension(filepath.Ext(imagePath))
	if mimeType == "" {
		return nil, errors.Errorf("unsupported image format for file: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", imagePath)
	}

	return genai.NewPartFromBytes(imageData, mimeType), nil
}

// getMimeTypeFromExtension returns the MIME type for supported image formats
func getMimeTypeFromExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// isRetryableError determines if an error should be retried based on structured error types.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Don't retry context cancellation or deadline exceeded
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for Google GenAI APIError with HTTP status code
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) {
		statusCode := apiErr.Code
		return statusCode >= 400 && statusCode < 600
	}

	return false
}

// executeWithRetry executes an operation with retry logic
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

// Tool-related functions

// toGoogleTools converts kodelet tools to Google GenAI tools
func toGoogleTools(tools []tooltypes.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	// Google GenAI expects all function declarations grouped under a single Tool
	var functionDeclarations []*genai.FunctionDeclaration
	for _, tool := range tools {
		schema := convertToGoogleSchema(tool.GenerateSchema())
		functionDeclarations = append(functionDeclarations, &genai.FunctionDeclaration{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schema,
		})
	}

	return []*genai.Tool{{
		FunctionDeclarations: functionDeclarations,
	}}
}

// convertToGoogleSchema converts JSON schema to Google GenAI schema format
func convertToGoogleSchema(schema *jsonschema.Schema) *genai.Schema {
	googleSchema := &genai.Schema{
		Type: convertSchemaType(schema.Type),
	}

	if schema.Description != "" {
		googleSchema.Description = schema.Description
	}

	if schema.Properties != nil {
		googleSchema.Properties = make(map[string]*genai.Schema)
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			propName := pair.Key
			propSchema := pair.Value
			googleSchema.Properties[propName] = convertToGoogleSchema(propSchema)
		}
	}

	if len(schema.Required) > 0 {
		googleSchema.Required = schema.Required
	}

	if schema.Items != nil {
		googleSchema.Items = convertToGoogleSchema(schema.Items)
	}

	return googleSchema
}

// convertSchemaType converts JSON schema type to Google GenAI type
func convertSchemaType(schemaType string) genai.Type {
	switch strings.ToLower(schemaType) {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeString
	}
}

// generateToolCallID generates a unique ID for tool calls
func generateToolCallID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("call_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("call_%s", hex.EncodeToString(bytes))
}

// executeToolCalls executes tool calls and adds results to the conversation
func (t *GoogleThread) executeToolCalls(ctx context.Context, response *GoogleResponse, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) {
	var toolResultParts []*genai.Part

	for _, toolCall := range response.ToolCalls {
		logger.G(ctx).WithField("tool", toolCall.Name).Debug("Executing tool call")

		// For tracing, add tool execution event
		telemetry.AddEvent(ctx, "tool_execution_start",
			attribute.String("tool_name", toolCall.Name),
		)

		runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)

		argsJSON, err := json.Marshal(toolCall.Args)
		if err != nil {
			logger.G(ctx).WithError(err).Error("Failed to marshal tool arguments")
			continue
		}

		output := tools.RunTool(runToolCtx, t.state, toolCall.Name, string(argsJSON))

		// Use CLI rendering for consistent output formatting
		structuredResult := output.StructuredData()
		registry := renderers.NewRendererRegistry()
		renderedOutput := registry.Render(structuredResult)

		handler.HandleToolResult(toolCall.Name, renderedOutput)

		// Store structured results
		t.SetStructuredToolResult(toolCall.ID, structuredResult)

		// For tracing, add tool execution completion event
		telemetry.AddEvent(ctx, "tool_execution_complete",
			attribute.String("tool_name", toolCall.Name),
			attribute.String("result", output.AssistantFacing()),
		)

		resultPart := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				Name: toolCall.Name,
				Response: map[string]interface{}{
					"call_id": toolCall.ID,
					"result":  output.AssistantFacing(),
					"error":   output.IsError(),
				},
			},
		}
		toolResultParts = append(toolResultParts, resultPart)
	}

	// Google GenAI requires all function responses for a turn in one message
	if len(toolResultParts) > 0 {
		content := genai.NewContentFromParts(toolResultParts, genai.RoleUser)
		t.messages = append(t.messages, content)
	}
}

// hasToolCalls checks if the response contains tool calls
func (t *GoogleThread) hasToolCalls(response *GoogleResponse) bool {
	return len(response.ToolCalls) > 0
}

// tools returns the available tools, filtered by options
func (t *GoogleThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	if t.state == nil {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

// GetMessages returns the current messages in the thread
func (t *GoogleThread) GetMessages() ([]llmtypes.Message, error) {
	return t.convertToStandardMessages(), nil
}

// convertToStandardMessages converts Google GenAI messages to standard format
func (t *GoogleThread) convertToStandardMessages() []llmtypes.Message {
	var messages []llmtypes.Message

	for _, content := range t.messages {
		for _, part := range content.Parts {
			switch {
			case part.Text != "":
				role := "assistant"
				if content.Role == genai.RoleUser {
					role = "user"
				}

				if part.Thought {
					continue
				}

				messages = append(messages, llmtypes.Message{
					Role:    role,
					Content: part.Text,
				})

			case part.FunctionCall != nil:
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				messages = append(messages, llmtypes.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("🔧 Using tool: %s with input: %s", part.FunctionCall.Name, string(argsJSON)),
				})

			case part.FunctionResponse != nil:
				resultJSON, _ := json.Marshal(part.FunctionResponse.Response)
				messages = append(messages, llmtypes.Message{
					Role:    "user",
					Content: fmt.Sprintf("🔄 Tool result:\n%s", string(resultJSON)),
				})
			}
		}
	}

	return messages
}

// NewSubAgent creates a subagent thread reusing the parent's client
func (t *GoogleThread) NewSubAgent(ctx context.Context, config llmtypes.Config) llmtypes.Thread {
	// Create subagent thread reusing the parent's client instead of creating a new one
	subagentThread := &GoogleThread{
		client:                 t.client, // Reuse parent's client
		config:                 config,
		backend:                t.backend, // Reuse parent's backend
		usage:                  t.usage,   // Share usage tracking with parent
		conversationID:         convtypes.GenerateID(),
		isPersisted:            false, // subagent is not persisted
		toolResults:            make(map[string]tooltypes.StructuredToolResult),
		subagentContextFactory: t.subagentContextFactory, // Propagate the injected function
		thinkingBudget:         t.thinkingBudget,         // Use same thinking budget
	}

	subagentThread.SetState(t.state)
	return subagentThread
}

// SetStructuredToolResult stores the structured result for a tool call
func (t *GoogleThread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.toolResults[toolCallID] = result
}

// GetStructuredToolResults returns all structured tool results
func (t *GoogleThread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		return make(map[string]tooltypes.StructuredToolResult)
	}
	// Return a copy to avoid race conditions
	result := make(map[string]tooltypes.StructuredToolResult)
	for k, v := range t.toolResults {
		result[k] = v
	}
	return result
}

// SetStructuredToolResults sets all structured tool results (for loading from conversation)
func (t *GoogleThread) SetStructuredToolResults(results map[string]tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if results == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	} else {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
		for k, v := range results {
			t.toolResults[k] = v
		}
	}
}

// shouldAutoCompact checks if auto-compact should be triggered based on context window utilization
func (t *GoogleThread) shouldAutoCompact(compactRatio float64) bool {
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

// CompactContext performs comprehensive context compacting by creating a detailed summary
func (t *GoogleThread) CompactContext(ctx context.Context) error {
	// Temporarily disable persistence during compacting
	wasPersistedOriginal := t.isPersisted
	t.isPersisted = false
	defer func() {
		t.isPersisted = wasPersistedOriginal
	}()

	// Use the strong model for comprehensive compacting
	_, err := t.SendMessage(ctx, prompts.CompactPrompt, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
		UseWeakModel:       false, // Use strong model for comprehensive compacting
		NoToolUse:          true,
		DisableAutoCompact: true, // Prevent recursion
		DisableUsageLog:    true, // Don't log usage for internal compact operations
	})
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	// Get the compact summary from the last assistant message
	compactSummary, err := t.getLastAssistantMessageText()
	if err != nil {
		return errors.Wrap(err, "failed to get compact summary from assistant message")
	}

	// Replace the conversation history with the compact summary
	t.mu.Lock()
	defer t.mu.Unlock()

	t.messages = []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText(compactSummary),
		}, genai.RoleUser),
	}

	// Clear stale tool results - they reference tool calls that no longer exist
	t.toolResults = make(map[string]tooltypes.StructuredToolResult)

	// Get state reference while under mutex protection
	state := t.state

	// Clear file access tracking to start fresh with context retrieval
	if state != nil {
		state.SetFileLastAccess(make(map[string]time.Time))
	}

	return nil
}

// getLastAssistantMessageText extracts text content from the most recent assistant message
func (t *GoogleThread) getLastAssistantMessageText() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.messages) == 0 {
		return "", errors.New("no messages found")
	}

	// Find the last assistant message
	var messageText string
	for i := len(t.messages) - 1; i >= 0; i-- {
		content := t.messages[i]
		if content.Role == genai.RoleModel {
			// Extract text from the assistant message parts
			for _, part := range content.Parts {
				if part.Text != "" && !part.Thought {
					messageText += part.Text
				}
			}
			break
		}
	}

	if messageText == "" {
		return "", errors.New("no text content found in assistant message")
	}

	return messageText, nil
}

// ShortSummary generates a brief summary of the conversation
func (t *GoogleThread) ShortSummary(ctx context.Context) string {
	// Temporarily disable persistence during summarization
	t.isPersisted = false
	defer func() {
		t.isPersisted = true
	}()

	// Use a faster model for summarization as it's a simpler task
	_, err := t.SendMessage(ctx, prompts.ShortSummaryPrompt, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
		UseWeakModel:       true,
		NoToolUse:          true,
		DisableAutoCompact: true, // Prevent auto-compact during summarization
		DisableUsageLog:    true, // Don't log usage for internal summary operations
	})
	if err != nil {
		return err.Error()
	}

	// Get the summary from the last assistant message
	summary, err := t.getLastAssistantMessageText()
	if err != nil {
		return err.Error()
	}

	return summary
}

// processPendingFeedback processes any pending feedback messages
func (t *GoogleThread) processPendingFeedback(ctx context.Context, handler llmtypes.MessageHandler) error {
	// Use a separate function to ensure feedback processing doesn't break the main flow
	defer func() {
		if r := recover(); r != nil {
			logger.G(ctx).WithField("panic", r).Error("panic occurred while processing feedback")
		}
	}()

	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to create feedback store, continuing without feedback")
		return nil
	}

	pendingFeedback, err := feedbackStore.ReadPendingFeedback(t.conversationID)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("failed to read pending feedback, continuing without feedback")
		return nil
	}

	if len(pendingFeedback) > 0 {
		logger.G(ctx).WithField("feedback_count", len(pendingFeedback)).Info("processing pending feedback messages")

		// Convert feedback messages to Google GenAI messages
		for i, fbMsg := range pendingFeedback {
			// Add some basic validation
			if fbMsg.Content == "" {
				logger.G(ctx).WithField("message_index", i).Warn("skipping empty feedback message")
				continue
			}

			userContent := genai.NewContentFromParts([]*genai.Part{
				genai.NewPartFromText(fbMsg.Content),
			}, genai.RoleUser)
			t.messages = append(t.messages, userContent)
			handler.HandleText(fmt.Sprintf("🗣️ User feedback: %s", fbMsg.Content))
		}

		// Clear the feedback now that we've processed it
		if err := feedbackStore.ClearPendingFeedback(t.conversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear pending feedback, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared pending feedback")
		}
	}

	return nil
}

// loadConversation loads a conversation from the store
func (t *GoogleThread) loadConversation(ctx context.Context) {
	if t.store == nil {
		return
	}

	record, err := t.store.Load(ctx, t.conversationID)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("failed to load conversation")
		return
	}

	// Load messages
	if len(record.RawMessages) > 0 {
		var messages []*genai.Content
		if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
			logger.G(ctx).WithError(err).Error("failed to unmarshal conversation messages")
			return
		}
		t.messages = messages
	}

	// Load usage statistics
	t.usage = &record.Usage
	t.summary = record.Summary

	// Set tool results
	t.SetStructuredToolResults(record.ToolResults)

	// Load file access times if state is available
	if t.state != nil && record.FileLastAccess != nil {
		t.state.SetFileLastAccess(record.FileLastAccess)
	}

	// Load background processes if state is available
	if t.state != nil && len(record.BackgroundProcesses) > 0 {
		t.restoreBackgroundProcesses(record.BackgroundProcesses)
	}

	logger.G(ctx).WithField("conversation_id", t.conversationID).Debug("loaded conversation from store")
}

// createMessageSpan creates and configures a tracing span for message handling
func (t *GoogleThread) createMessageSpan(
	ctx context.Context,
	tracer trace.Tracer,
	message string,
	opt llmtypes.MessageOpt,
) (context.Context, trace.Span) {
	attributes := []attribute.KeyValue{
		attribute.String("model", t.config.Model),
		attribute.Int("max_tokens", t.config.MaxTokens),
		attribute.Int("weak_model_max_tokens", t.config.WeakModelMaxTokens),
		attribute.Bool("use_weak_model", opt.UseWeakModel),
		attribute.Bool("is_sub_agent", t.config.IsSubAgent),
		attribute.String("conversation_id", t.conversationID),
		attribute.Bool("is_persisted", t.isPersisted),
		attribute.Int("message_length", len(message)),
		attribute.String("backend", t.backend),
	}

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// finalizeMessageSpan records final metrics and status to the span before ending it
func (t *GoogleThread) finalizeMessageSpan(span trace.Span, err error) {
	// Record usage metrics after completion
	usage := t.GetUsage()
	span.SetAttributes(
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
		attribute.Int("tokens.cache_read", usage.CacheReadInputTokens),
		attribute.Float64("cost.total", usage.TotalCost()),
		attribute.Int("context_window.current", usage.CurrentContextWindow),
		attribute.Int("context_window.max", usage.MaxContextWindow),
	)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetStatus(codes.Ok, "")
		span.AddEvent("message_processing_completed")
	}
	span.End()
}

// updateUsage updates the thread's usage statistics
func (t *GoogleThread) updateUsage(metadata *genai.UsageMetadata) {
	if metadata == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	inputTokens := int(metadata.PromptTokenCount)
	outputTokens := int(metadata.ResponseTokenCount)
	cacheReadTokens := int(metadata.CachedContentTokenCount)

	hasAudio := false // TODO: Detect if audio was used in the request
	inputCost, outputCost := calculateCost(t.config.Model, inputTokens, outputTokens, hasAudio)

	t.usage.InputTokens += inputTokens
	t.usage.OutputTokens += outputTokens
	t.usage.CacheReadInputTokens += cacheReadTokens
	t.usage.InputCost += inputCost
	t.usage.OutputCost += outputCost

	if t.usage.MaxContextWindow == 0 {
		t.usage.MaxContextWindow = getContextWindow(t.config.Model)
	}

	t.usage.CurrentContextWindow = t.usage.InputTokens + t.usage.OutputTokens + t.usage.CacheReadInputTokens
}

// restoreBackgroundProcesses restores background processes from the conversation record
func (t *GoogleThread) restoreBackgroundProcesses(processes []tooltypes.BackgroundProcess) {
	for _, process := range processes {
		// Check if process is still alive
		if utils.IsProcessAlive(process.PID) {
			// Reattach to the process
			if restoredProcess, err := utils.ReattachProcess(process); err == nil {
				t.state.AddBackgroundProcess(restoredProcess)
			}
		}
	}
}
