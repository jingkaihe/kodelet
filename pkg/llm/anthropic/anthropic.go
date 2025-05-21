package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package
type ConversationStore = conversations.ConversationStore

// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
	Input              float64
	Output             float64
	PromptCachingWrite float64
	PromptCachingRead  float64
	ContextWindow      int
}

// ModelPricingMap maps model names to their pricing information
var ModelPricingMap = map[string]ModelPricing{
	// Latest models
	anthropic.ModelClaude3_7SonnetLatest: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaude3_5HaikuLatest: {
		Input:              0.0000008,  // $0.80 per million tokens
		Output:             0.000004,   // $4.00 per million tokens
		PromptCachingWrite: 0.000001,   // $1.00 per million tokens
		PromptCachingRead:  0.00000008, // $0.08 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaude3OpusLatest: {
		Input:              0.000015,   // $15.00 per million tokens
		Output:             0.000075,   // $75.00 per million tokens
		PromptCachingWrite: 0.00001875, // $18.75 per million tokens
		PromptCachingRead:  0.0000015,  // $1.50 per million tokens
		ContextWindow:      200_000,
	},
	// Legacy models
	anthropic.ModelClaude3_5SonnetLatest: {
		Input:              0.000003,   // $3.00 per million tokens
		Output:             0.000015,   // $15.00 per million tokens
		PromptCachingWrite: 0.00000375, // $3.75 per million tokens
		PromptCachingRead:  0.0000003,  // $0.30 per million tokens
		ContextWindow:      200_000,
	},
	anthropic.ModelClaude_3_Haiku_20240307: {
		Input:              0.00000025, // $0.25 per million tokens
		Output:             0.00000125, // $1.25 per million tokens
		PromptCachingWrite: 0.0000003,  // $0.30 per million tokens
		PromptCachingRead:  0.00000003, // $0.03 per million tokens
		ContextWindow:      200_000,
	},
}

// getModelPricing returns the pricing information for a given model
func getModelPricing(model string) ModelPricing {
	// First try exact match
	if pricing, ok := ModelPricingMap[model]; ok {
		return pricing
	}
	// Try to find a match based on model family
	lowerModel := strings.ToLower(model)
	if strings.Contains(lowerModel, "claude-3-7-sonnet") {
		return ModelPricingMap[anthropic.ModelClaude3_7SonnetLatest]
	} else if strings.Contains(lowerModel, "claude-3-5-haiku") {
		return ModelPricingMap[anthropic.ModelClaude3_5HaikuLatest]
	} else if strings.Contains(lowerModel, "claude-3-opus") {
		return ModelPricingMap[anthropic.ModelClaude3OpusLatest]
	} else if strings.Contains(lowerModel, "claude-3-5-sonnet") {
		return ModelPricingMap["claude-3-5-sonnet-20240620"]
	} else if strings.Contains(lowerModel, "claude-3-haiku") {
		return ModelPricingMap["claude-3-haiku-20240307"]
	}

	// Default to Claude 3.7 Sonnet pricing if no match
	return ModelPricingMap[anthropic.ModelClaude3_7SonnetLatest]
}

// AnthropicThread implements the Thread interface using Anthropic's Claude API
type AnthropicThread struct {
	client         anthropic.Client
	config         llmtypes.Config
	state          tooltypes.State
	messages       []anthropic.MessageParam
	usage          *llmtypes.Usage
	conversationID string
	summary        string
	isPersisted    bool
	store          ConversationStore
	mu             sync.Mutex
	conversationMu sync.Mutex
}

func (t *AnthropicThread) Provider() string {
	return "anthropic"
}

// NewAnthropicThread creates a new thread with Anthropic's Claude API
func NewAnthropicThread(config llmtypes.Config) *AnthropicThread {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = anthropic.ModelClaude3_7SonnetLatest
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	return &AnthropicThread{
		client:         anthropic.NewClient(),
		config:         config,
		conversationID: conversations.GenerateID(),
		isPersisted:    false,
		usage:          &llmtypes.Usage{}, // must be initialised to avoid nil pointer dereference
	}
}

// SetState sets the state for the thread
func (t *AnthropicThread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *AnthropicThread) GetState() tooltypes.State {
	return t.state
}

// AddUserMessage adds a user message to the thread
func (t *AnthropicThread) AddUserMessage(message string) {
	t.messages = append(t.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))
}

func (t *AnthropicThread) cacheMessages() {
	// remove cache control from the messages
	for msgIdx, msg := range t.messages {
		for blkIdx, block := range msg.Content {
			if block.OfRequestTextBlock != nil {
				block.OfRequestTextBlock.CacheControl = anthropic.CacheControlEphemeralParam{}
				t.messages[msgIdx].Content[blkIdx] = block
			}
		}
	}
	if len(t.messages) > 0 {
		lastMsg := t.messages[len(t.messages)-1]
		if len(lastMsg.Content) > 0 {
			lastBlock := lastMsg.Content[len(lastMsg.Content)-1]
			if lastBlock.OfRequestTextBlock != nil {
				lastBlock.OfRequestTextBlock.CacheControl = anthropic.CacheControlEphemeralParam{}
				t.messages[len(t.messages)-1].Content[len(lastMsg.Content)-1] = lastBlock
			}
		}
	}
}

// SendMessage sends a message to the LLM and processes the response
func (t *AnthropicThread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	// Check if tracing is enabled and wrap the handler
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.createMessageSpan(ctx, tracer, message, opt)
	defer t.finalizeMessageSpan(span, err)

	if opt.PromptCache {
		t.cacheMessages()
	}

	var originalMessages []anthropic.MessageParam
	if opt.PromptCache {
		originalMessages = make([]anthropic.MessageParam, len(t.messages))
		copy(originalMessages, t.messages)
	}

	t.AddUserMessage(message)

	// Determine which model to use
	model, maxTokens := t.getModelAndTokens(opt)
	var systemPrompt string
	if t.config.IsSubAgent {
		systemPrompt = sysprompt.SubAgentPrompt(model)
	} else {
		systemPrompt = sysprompt.SystemPrompt(model)
	}

	// Main interaction loop for handling tool calls
OUTER:
	for {
		select {
		case <-ctx.Done():
			logrus.Info("stopping kodelet.llm.anthropic")
			break OUTER
		default:
			var exchangeOutput string
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, model, maxTokens, systemPrompt, opt)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logrus.Info("Request to anthropic cancelled, stopping kodelet.llm.anthropic")
					// remove the last tool use from the messages
					if len(t.messages) > 0 {
						lastMsg := t.messages[len(t.messages)-1]
						if isMessageToolUse(lastMsg) {
							t.messages = t.messages[:len(t.messages)-1]
						}
					}
					break OUTER
				}
				return "", err
			}

			// Update finalOutput with the most recent output
			finalOutput = exchangeOutput

			// If no tools were used, we're done
			if !toolsUsed {
				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.messages = originalMessages
	}

	// Save conversation state after completing the interaction
	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		t.SaveConversation(saveCtx, false)
	}

	if !t.config.IsSubAgent {
		// only main agaent can signal done
		handler.HandleDone()
	}
	return finalOutput, nil
}

func isMessageToolUse(msg anthropic.MessageParam) bool {
	if len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if block.OfRequestToolUseBlock != nil {
			return true
		}
	}
	return false
}

// processMessageExchange handles a single message exchange with the LLM, including
// preparing message parameters, making the API call, and processing the response
func (t *AnthropicThread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model string,
	maxTokens int,
	systemPrompt string,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	var finalOutput string

	// Prepare message parameters
	messageParams := anthropic.MessageNewParams{
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{
				Text: systemPrompt,
				CacheControl: anthropic.CacheControlEphemeralParam{
					Type: "ephemeral",
				},
			},
		},
		Messages: t.messages,
		Model:    model,
		Tools:    tools.ToAnthropicTools(t.tools(opt)),
	}
	if t.shouldUtiliseThinking(model) {
		messageParams.Thinking = anthropic.ThinkingConfigParamUnion{
			OfThinkingConfigEnabled: &anthropic.ThinkingConfigEnabledParam{
				Type:         "enabled",
				BudgetTokens: int64(t.config.ThinkingBudgetTokens),
			},
		}
	}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", model),
		attribute.Int("max_tokens", maxTokens),
	)

	response, err := t.NewMessage(ctx, messageParams)
	if err != nil {
		return "", false, fmt.Errorf("error sending message to Anthropic: %w", err)
	}

	// Record API call completion
	telemetry.AddEvent(ctx, "api_call_complete",
		attribute.Int("input_tokens", int(response.Usage.InputTokens)),
		attribute.Int("output_tokens", int(response.Usage.OutputTokens)),
	)

	// Add the assistant response to history
	t.messages = append(t.messages, response.ToParam())

	t.updateUsage(response, model)

	// Process the response content blocks
	toolUseCount := 0
	for _, block := range response.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			handler.HandleText(variant.Text)
			finalOutput = variant.Text
		case anthropic.ThinkingBlock:
			handler.HandleThinking(variant.Thinking)
		case anthropic.ToolUseBlock:
			toolUseCount++
			inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
			handler.HandleToolUse(block.Name, string(inputJSON))

			// For tracing, add tool execution event
			telemetry.AddEvent(ctx, "tool_execution_start",
				attribute.String("tool_name", block.Name),
			)

			runToolCtx := t.WithSubAgent(ctx, handler)
			output := tools.RunTool(runToolCtx, t.state, block.Name, string(variant.JSON.Input.Raw()))
			handler.HandleToolResult(block.Name, output.String())

			// For tracing, add tool execution completion event
			telemetry.AddEvent(ctx, "tool_execution_complete",
				attribute.String("tool_name", block.Name),
				attribute.Int("result_length", len(output.String())),
			)

			// Add tool result to messages for next API call
			t.messages = append(t.messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(block.ID, output.String(), false),
			))
		}
	}

	// Return whether tools were used in this exchange
	return finalOutput, toolUseCount > 0, nil
}

// getModelAndTokens determines which model and max tokens to use based on configuration and options
func (t *AnthropicThread) getModelAndTokens(opt llmtypes.MessageOpt) (string, int) {
	model := t.config.Model
	maxTokens := t.config.MaxTokens

	if opt.UseWeakModel && t.config.WeakModel != "" {
		model = t.config.WeakModel
		if t.config.WeakModelMaxTokens > 0 {
			maxTokens = t.config.WeakModelMaxTokens
		}
	}

	return model, maxTokens
}

func (t *AnthropicThread) shouldUtiliseThinking(model string) bool {
	if t.config.ThinkingBudgetTokens == 0 {
		return false
	}
	if model != anthropic.ModelClaude3_7SonnetLatest {
		return false
	}
	return true
}

// NewMessage sends a message to Anthropic with OTEL tracing
func (t *AnthropicThread) NewMessage(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	tracer := telemetry.Tracer("kodelet.llm.anthropic")

	// Create attributes for the span
	spanAttrs := []attribute.KeyValue{
		attribute.String("model", params.Model),
		attribute.Int64("max_tokens", params.MaxTokens),
	}

	if t.shouldUtiliseThinking(params.Model) {
		spanAttrs = append(spanAttrs,
			attribute.Bool("thinking", params.Thinking.OfThinkingConfigEnabled.BudgetTokens > 0),
			attribute.Int64("budget_tokens", params.Thinking.OfThinkingConfigEnabled.BudgetTokens),
		)
	}
	for i, sys := range params.System {
		spanAttrs = append(spanAttrs, attribute.String(fmt.Sprintf("system.%d", i), sys.Text))
	}

	// Add the last 10 messages (or fewer if there aren't 10) to the span attributes
	spanAttrs = append(spanAttrs, t.getLastMessagesAttributes(params.Messages, 10)...)

	// Create a new span for the API call
	ctx, span := tracer.Start(ctx, "llm.anthropic.new_message", trace.WithAttributes(spanAttrs...))
	defer span.End()

	// Call the Anthropic API
	response, err := t.client.Messages.New(ctx, params)

	// Handle errors and update span
	if err != nil {
		telemetry.RecordError(ctx, err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("error sending message to Anthropic: %w", err)
	}

	// Add response data to the span
	span.SetAttributes(
		attribute.Int("input_tokens", int(response.Usage.InputTokens)),
		attribute.Int("output_tokens", int(response.Usage.OutputTokens)),
		attribute.Int("cache_creation_tokens", int(response.Usage.CacheCreationInputTokens)),
		attribute.Int("cache_read_tokens", int(response.Usage.CacheReadInputTokens)),
	)
	span.SetStatus(codes.Ok, "")

	return response, nil
}

// getLastMessagesAttributes extracts information from the last n messages for telemetry purposes
func (t *AnthropicThread) getLastMessagesAttributes(messages []anthropic.MessageParam, lastN int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}

	// Determine how many messages to process (last n or all if fewer than n)
	startIdx := 0
	if len(messages) > lastN {
		startIdx = len(messages) - lastN
	}

	// Process the last n messages
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		idx := i - startIdx // relative index for attribute naming

		// Add message role
		attrs = append(attrs, attribute.String(fmt.Sprintf("message.%d.role", idx), string(msg.Role)))

		contentJson, err := json.Marshal(msg.Content)
		if err != nil {
			attrs = append(attrs, attribute.String(
				fmt.Sprintf("message.%d.content", idx),
				fmt.Sprintf("error marshalling content: %s", err),
			))
		} else {
			attrs = append(attrs, attribute.String(
				fmt.Sprintf("message.%d.content", idx),
				string(contentJson),
			))
		}
	}

	return attrs
}

func (t *AnthropicThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

func (t *AnthropicThread) updateUsage(response *anthropic.Message, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Track usage statistics
	t.usage.InputTokens += int(response.Usage.InputTokens)
	t.usage.OutputTokens += int(response.Usage.OutputTokens)
	t.usage.CacheCreationInputTokens += int(response.Usage.CacheCreationInputTokens)
	t.usage.CacheReadInputTokens += int(response.Usage.CacheReadInputTokens)

	// Calculate costs based on model pricing
	pricing := getModelPricing(model)

	// Calculate individual costs
	t.usage.InputCost += float64(response.Usage.InputTokens) * pricing.Input
	t.usage.OutputCost += float64(response.Usage.OutputTokens) * pricing.Output
	t.usage.CacheCreationCost += float64(response.Usage.CacheCreationInputTokens) * pricing.PromptCachingWrite
	t.usage.CacheReadCost += float64(response.Usage.CacheReadInputTokens) * pricing.PromptCachingRead

	t.usage.CurrentContextWindow = int(response.Usage.InputTokens) + int(response.Usage.OutputTokens) + int(response.Usage.CacheCreationInputTokens) + int(response.Usage.CacheReadInputTokens)
	t.usage.MaxContextWindow = pricing.ContextWindow
}
func (t *AnthropicThread) NewSubAgent(ctx context.Context) llmtypes.Thread {
	config := t.config
	config.IsSubAgent = true
	thread := NewAnthropicThread(config)
	thread.isPersisted = false // subagent is not persisted
	thread.SetState(tools.NewBasicState(ctx, tools.WithSubAgentTools(), tools.WithExtraMCPTools(t.state.MCPTools())))
	thread.usage = t.usage

	return thread
}

func (t *AnthropicThread) WithSubAgent(ctx context.Context, handler llmtypes.MessageHandler) context.Context {
	subAgent := t.NewSubAgent(ctx)
	ctx = context.WithValue(ctx, llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:         subAgent,
		MessageHandler: handler,
	})
	return ctx
}

func (t *AnthropicThread) ShortSummary(ctx context.Context) string {
	prompt := `Summarise the conversation in one sentence, less or equal than 12 words. Keep it short and concise.
Treat the USER role as the first person (I), and the ASSISTANT role as the person you are talking to.
`
	handler := &llmtypes.StringCollectorHandler{
		Silent: true,
	}
	t.isPersisted = false
	defer func() {
		t.isPersisted = true
	}()

	// Use a faster model for summarization as it's a simpler task
	_, err := t.SendMessage(ctx, prompt, handler, llmtypes.MessageOpt{
		UseWeakModel:       true,
		PromptCache:        false, // maybe we should make it configurable, but there is likely no cache for weak model
		NoToolUse:          true,
		NoSaveConversation: true,
	})
	if err != nil {
		return err.Error()
	}

	return handler.CollectedText()
}

// GetUsage returns the current token usage for the thread
func (t *AnthropicThread) GetUsage() llmtypes.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.usage
}

// GetConversationID returns the current conversation ID
func (t *AnthropicThread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *AnthropicThread) SetConversationID(id string) {
	t.conversationID = id
}

// IsPersisted returns whether this thread is being persisted
func (t *AnthropicThread) IsPersisted() bool {
	return t.isPersisted
}

// GetMessages returns the current messages in the thread
func (t *AnthropicThread) GetMessages() []anthropic.MessageParam {
	return t.messages
}

// EnablePersistence enables conversation persistence for this thread
func (t *AnthropicThread) EnablePersistence(enabled bool) {
	t.isPersisted = enabled

	// Initialize the store if enabling persistence and it's not already initialized
	if enabled && t.store == nil {
		store, err := conversations.GetConversationStore()
		if err != nil {
			// Log the error but continue without persistence
			fmt.Printf("Error initializing conversation store: %v\n", err)
			t.isPersisted = false
			return
		}
		t.store = store
	}

	// If enabling persistence and there's an existing conversation ID,
	// try to load it from the store
	if enabled && t.conversationID != "" && t.store != nil {
		t.loadConversation()
	}
}

// createMessageSpan creates and configures a tracing span for message handling
func (t *AnthropicThread) createMessageSpan(
	ctx context.Context,
	tracer trace.Tracer,
	message string,
	opt llmtypes.MessageOpt,
) (context.Context, trace.Span) {
	attributes := []attribute.KeyValue{
		attribute.String("model", t.config.Model),
		attribute.Int("max_tokens", t.config.MaxTokens),
		attribute.Int("weak_model_max_tokens", t.config.WeakModelMaxTokens),
		attribute.Int("thinking_budget_tokens", t.config.ThinkingBudgetTokens),
		attribute.Bool("prompt_cache", opt.PromptCache),
		attribute.Bool("use_weak_model", opt.UseWeakModel),
		attribute.Bool("is_sub_agent", t.config.IsSubAgent),
		attribute.String("conversation_id", t.conversationID),
		attribute.Bool("is_persisted", t.isPersisted),
		attribute.Int("message_length", len(message)),
	}

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// finalizeMessageSpan records final metrics and status to the span before ending it
func (t *AnthropicThread) finalizeMessageSpan(span trace.Span, err error) {
	// Record usage metrics after completion
	usage := t.GetUsage()
	span.SetAttributes(
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
		attribute.Int("tokens.cache_creation", usage.CacheCreationInputTokens),
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
