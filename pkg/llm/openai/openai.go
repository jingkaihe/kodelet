package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sashabaranov/go-openai"
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
	Input         float64
	Output        float64
	ContextWindow int
}

// ModelPricingMap maps model names to their pricing information
var ModelPricingMap = map[string]ModelPricing{
	"gpt-4.1": {
		Input:         0.00001,  // $0.01 per 1K tokens
		Output:        0.00003,  // $0.03 per 1K tokens
		ContextWindow: 128_000,
	},
	"gpt-4.1-mini": {
		Input:         0.000003, // $0.003 per 1K tokens
		Output:        0.000009, // $0.009 per 1K tokens
		ContextWindow: 128_000,
	},
	"gpt-4o": {
		Input:         0.000005, // $0.005 per 1K tokens
		Output:        0.000015, // $0.015 per 1K tokens
		ContextWindow: 128_000,
	},
	"gpt-3.5-turbo": {
		Input:         0.0000005, // $0.0005 per 1K tokens
		Output:        0.0000015, // $0.0015 per 1K tokens
		ContextWindow: 16_000,
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
	if strings.Contains(lowerModel, "gpt-4.1") && !strings.Contains(lowerModel, "mini") {
		return ModelPricingMap["gpt-4.1"]
	} else if strings.Contains(lowerModel, "gpt-4.1-mini") {
		return ModelPricingMap["gpt-4.1-mini"]
	} else if strings.Contains(lowerModel, "gpt-4o") {
		return ModelPricingMap["gpt-4o"]
	} else if strings.Contains(lowerModel, "gpt-3.5") {
		return ModelPricingMap["gpt-3.5-turbo"]
	}

	// Default to GPT-4.1 pricing if no match
	return ModelPricingMap["gpt-4.1"]
}

// OpenAIThread implements the Thread interface using OpenAI's API
type OpenAIThread struct {
	client         *openai.Client
	config         llmtypes.Config
	reasoningEffort string     // low, medium, high to determine token allocation
	state          tooltypes.State
	messages       []openai.ChatCompletionMessage
	usage          *llmtypes.Usage
	conversationID string
	summary        string
	isPersisted    bool
	store          ConversationStore
	mu             sync.Mutex
	conversationMu sync.Mutex
}

func (t *OpenAIThread) Provider() string {
	return "openai"
}

// NewOpenAIThread creates a new thread with OpenAI's API
func NewOpenAIThread(config llmtypes.Config) *OpenAIThread {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = "gpt-4.1" // Default to GPT-4.1
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}
	
	reasoningEffort := config.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "medium" // Default reasoning effort
	}

	return &OpenAIThread{
		client:          openai.NewClient(""),  // API key will be set via env var
		config:          config,
		reasoningEffort: reasoningEffort,
		conversationID:  conversations.GenerateID(),
		isPersisted:     false,
		usage:           &llmtypes.Usage{}, // must be initialized to avoid nil pointer dereference
	}
}

// SetState sets the state for the thread
func (t *OpenAIThread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *OpenAIThread) GetState() tooltypes.State {
	return t.state
}

// AddUserMessage adds a user message to the thread
func (t *OpenAIThread) AddUserMessage(message string) {
	t.messages = append(t.messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})
}

// SendMessage sends a message to the LLM and processes the response
func (t *OpenAIThread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	// Check if tracing is enabled and wrap the handler
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.createMessageSpan(ctx, tracer, message, opt)
	defer t.finalizeMessageSpan(span, err)

	var originalMessages []openai.ChatCompletionMessage
	if opt.NoSaveConversation {
		originalMessages = make([]openai.ChatCompletionMessage, len(t.messages))
		copy(originalMessages, t.messages)
	}

	t.AddUserMessage(message)

	// Determine which model to use
	model, maxTokens := t.getModelAndTokens(opt)
	
	// Add system message if it doesn't exist
	if len(t.messages) == 0 || t.messages[0].Role != openai.ChatMessageRoleSystem {
		var systemPrompt string
		if t.config.IsSubAgent {
			systemPrompt = sysprompt.SubAgentPrompt(model)
		} else {
			systemPrompt = sysprompt.SystemPrompt(model)
		}
		
		systemMessage := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		}
		
		// Insert system message at the beginning
		t.messages = append([]openai.ChatCompletionMessage{systemMessage}, t.messages...)
	}

	// Main interaction loop for handling tool calls
OUTER:
	for {
		select {
		case <-ctx.Done():
			logrus.Info("stopping kodelet.llm.openai")
			break OUTER
		default:
			var exchangeOutput string
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, model, maxTokens, opt)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logrus.Info("Request to OpenAI cancelled, stopping kodelet.llm.openai")
					// Remove the last tool message from the messages if it exists
					if len(t.messages) > 0 && isToolResultMessage(t.messages[len(t.messages)-1]) {
						t.messages = t.messages[:len(t.messages)-1]
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
		// only main agent can signal done
		handler.HandleDone()
	}
	return finalOutput, nil
}

// isToolResultMessage checks if a message is a tool result message
func isToolResultMessage(msg openai.ChatCompletionMessage) bool {
	return msg.Role == openai.ChatMessageRoleTool
}

// processMessageExchange handles a single message exchange with the LLM, including
// preparing message parameters, making the API call, and processing the response
func (t *OpenAIThread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model string,
	maxTokens int,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	var finalOutput string

	// Prepare completion parameters
	requestParams := openai.ChatCompletionRequest{
		Model:     model,
		Messages:  t.messages,
		MaxTokens: maxTokens,
	}

	// Add tool definitions if tool use is enabled
	if !opt.NoToolUse {
		availableTools := t.tools(opt)
		if len(availableTools) > 0 {
			requestParams.Tools = tools.ToOpenAITools(availableTools)
			requestParams.ToolChoice = "auto"
		}
	}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", model),
		attribute.Int("max_tokens", maxTokens),
	)

	// Make the API request
	response, err := t.client.CreateChatCompletion(ctx, requestParams)
	if err != nil {
		return "", false, fmt.Errorf("error sending message to OpenAI: %w", err)
	}

	// Record API call completion
	telemetry.AddEvent(ctx, "api_call_complete",
		attribute.Int("prompt_tokens", response.Usage.PromptTokens),
		attribute.Int("completion_tokens", response.Usage.CompletionTokens),
	)

	// Update usage tracking
	t.updateUsage(response.Usage, model)

	// Process the response
	if len(response.Choices) == 0 {
		return "", false, fmt.Errorf("no response choices returned from OpenAI")
	}

	// Add the assistant response to history
	assistantMessage := response.Choices[0].Message
	t.messages = append(t.messages, assistantMessage)

	// Extract text content
	content := assistantMessage.Content
	if content != "" {
		handler.HandleText(content)
		finalOutput = content
	}

	// Check for tool calls
	toolCalls := assistantMessage.ToolCalls
	if len(toolCalls) == 0 {
		return finalOutput, false, nil
	}

	// Process tool calls
	for _, toolCall := range toolCalls {
		handler.HandleToolUse(toolCall.Function.Name, toolCall.Function.Arguments)

		// For tracing, add tool execution event
		telemetry.AddEvent(ctx, "tool_execution_start",
			attribute.String("tool_name", toolCall.Function.Name),
		)

		// Execute the tool
		runToolCtx := t.WithSubAgent(ctx, handler)
		output := tools.RunTool(runToolCtx, t.state, toolCall.Function.Name, toolCall.Function.Arguments)
		handler.HandleToolResult(toolCall.Function.Name, output.String())

		// For tracing, add tool execution completion event
		telemetry.AddEvent(ctx, "tool_execution_complete",
			attribute.String("tool_name", toolCall.Function.Name),
			attribute.Int("result_length", len(output.String())),
		)

		// Add tool result to messages for next API call
		t.messages = append(t.messages, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    output.String(),
			ToolCallID: toolCall.ID,
		})
	}

	return finalOutput, true, nil
}

// getModelAndTokens determines which model and max tokens to use based on configuration and options
func (t *OpenAIThread) getModelAndTokens(opt llmtypes.MessageOpt) (string, int) {
	model := t.config.Model
	maxTokens := t.config.MaxTokens
	
	// Update reasoning effort based on current model
	if opt.UseWeakModel && t.config.WeakModel != "" {
		model = t.config.WeakModel
		if t.config.WeakModelMaxTokens > 0 {
			maxTokens = t.config.WeakModelMaxTokens
		}
		
		// Use weak reasoning effort if specified
		if t.config.WeakReasoningEffort != "" {
			t.reasoningEffort = t.config.WeakReasoningEffort
		} else {
			t.reasoningEffort = "low" // Default to low for weak models
		}
	} else {
		// Restore the original reasoning effort
		if t.config.ReasoningEffort != "" {
			t.reasoningEffort = t.config.ReasoningEffort
		} else {
			t.reasoningEffort = "medium"
		}
	}
	
	// Adjust maxTokens based on reasoning effort
	switch t.reasoningEffort {
	case "low":
		maxTokens = int(float64(maxTokens) * 0.5) // Use fewer tokens for low effort
	case "high":
		maxTokens = int(float64(maxTokens) * 1.5) // Use more tokens for high effort
	}
	
	// Ensure we don't exceed reasonable limits
	if maxTokens < 256 {
		maxTokens = 256
	} else if maxTokens > 16384 {
		maxTokens = 16384
	}

	return model, maxTokens
}



func (t *OpenAIThread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

func (t *OpenAIThread) updateUsage(usage openai.Usage, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Track usage statistics
	t.usage.InputTokens += usage.PromptTokens
	t.usage.OutputTokens += usage.CompletionTokens
	
	// Calculate costs based on model pricing
	pricing := getModelPricing(model)
	
	// Calculate individual costs
	t.usage.InputCost += float64(usage.PromptTokens) * pricing.Input
	t.usage.OutputCost += float64(usage.CompletionTokens) * pricing.Output
	
	t.usage.CurrentContextWindow = usage.PromptTokens + usage.CompletionTokens
	t.usage.MaxContextWindow = pricing.ContextWindow
}

func (t *OpenAIThread) NewSubAgent(ctx context.Context) llmtypes.Thread {
	config := t.config
	config.IsSubAgent = true
	thread := NewOpenAIThread(config)
	thread.isPersisted = false // subagent is not persisted
	thread.SetState(tools.NewBasicState(ctx, tools.WithSubAgentTools(), tools.WithExtraMCPTools(t.state.MCPTools())))
	thread.usage = t.usage

	return thread
}

func (t *OpenAIThread) WithSubAgent(ctx context.Context, handler llmtypes.MessageHandler) context.Context {
	subAgent := t.NewSubAgent(ctx)
	ctx = context.WithValue(ctx, llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:         subAgent,
		MessageHandler: handler,
	})
	return ctx
}

func (t *OpenAIThread) ShortSummary(ctx context.Context) string {
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
		NoToolUse:          true,
		NoSaveConversation: true,
	})
	if err != nil {
		return err.Error()
	}

	return handler.CollectedText()
}

// GetUsage returns the current token usage for the thread
func (t *OpenAIThread) GetUsage() llmtypes.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.usage
}

// GetConversationID returns the current conversation ID
func (t *OpenAIThread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *OpenAIThread) SetConversationID(id string) {
	t.conversationID = id
}

// IsPersisted returns whether this thread is being persisted
func (t *OpenAIThread) IsPersisted() bool {
	return t.isPersisted
}

// GetMessages returns the current messages in the thread
func (t *OpenAIThread) GetMessages() ([]llmtypes.Message, error) {
	result := make([]llmtypes.Message, 0, len(t.messages))
	
	for _, msg := range t.messages {
		// Skip system messages
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}
		
		role := string(msg.Role)
		content := msg.Content
		
		// Handle tool calls
		if msg.ToolCalls != nil && len(msg.ToolCalls) > 0 {
			toolCallContent, _ := json.Marshal(msg.ToolCalls)
			content = string(toolCallContent)
		}
		
		result = append(result, llmtypes.Message{
			Role:    role,
			Content: content,
		})
	}
	
	return result, nil
}

// EnablePersistence enables conversation persistence for this thread
func (t *OpenAIThread) EnablePersistence(enabled bool) {
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
func (t *OpenAIThread) createMessageSpan(
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
		attribute.String("reasoning_effort", t.reasoningEffort),
	}

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// finalizeMessageSpan records final metrics and status to the span before ending it
func (t *OpenAIThread) finalizeMessageSpan(span trace.Span, err error) {
	// Record usage metrics after completion
	usage := t.GetUsage()
	span.SetAttributes(
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
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