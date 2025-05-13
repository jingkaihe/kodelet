package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
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
	config         types.Config
	state          state.State
	messages       []anthropic.MessageParam
	usage          *types.Usage
	conversationID string
	summary        string
	isPersisted    bool
	store          ConversationStore
	mu             sync.Mutex
}

// NewAnthropicThread creates a new thread with Anthropic's Claude API
func NewAnthropicThread(config types.Config) *AnthropicThread {
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
		usage:          &types.Usage{}, // must be initialised to avoid nil pointer dereference
	}
}

// SetState sets the state for the thread
func (t *AnthropicThread) SetState(s state.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *AnthropicThread) GetState() state.State {
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
			lastMsg.Content[len(lastMsg.Content)-1].OfRequestTextBlock.CacheControl = anthropic.CacheControlEphemeralParam{}
		}
	}
}

// SendMessage sends a message to the LLM and processes the response
func (t *AnthropicThread) SendMessage(
	ctx context.Context,
	message string,
	handler types.MessageHandler,
	opt types.MessageOpt,
) (finalOutput string, err error) {
	if opt.PromptCache {
		t.cacheMessages()
	}
	t.AddUserMessage(message)

	// Determine which model to use
	model := t.config.Model

	if opt.UseWeakModel && t.config.WeakModel != "" {
		model = t.config.WeakModel
	}
	var systemPrompt string
	if t.config.IsSubAgent {
		systemPrompt = sysprompt.SubAgentPrompt(model)
	} else {
		systemPrompt = sysprompt.SystemPrompt(model)
	}

	// Main interaction loop for handling tool calls
	for {
		// Prepare message parameters
		messageParams := anthropic.MessageNewParams{
			MaxTokens: int64(t.config.MaxTokens),
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
			Tools:    t.tools(),
		}

		response, err := t.client.Messages.New(ctx, messageParams)
		if err != nil {
			return "", fmt.Errorf("error sending message to Anthropic: %w", err)
		}

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
			case anthropic.ToolUseBlock:
				toolUseCount++
				inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
				handler.HandleToolUse(block.Name, string(inputJSON))

				runToolCtx := t.WithSubAgent(ctx)
				output := tools.RunTool(runToolCtx, t.state, block.Name, string(variant.JSON.Input.Raw()))
				handler.HandleToolResult(block.Name, output.String())

				// Add tool result to messages for next API call
				t.messages = append(t.messages, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(block.ID, output.String(), false),
				))
			}
		}

		// If no tools were used, we're done
		if toolUseCount == 0 {
			break
		}
	}

	// Save conversation state after completing the interaction
	if t.isPersisted && t.store != nil {
		t.SaveConversation(ctx, false)
	}

	handler.HandleDone()
	return finalOutput, nil
}

func (t *AnthropicThread) tools() []anthropic.ToolUnionParam {
	if !t.config.IsSubAgent {
		return tools.ToAnthropicTools(tools.Tools)
	}
	// remove the agent tool from the list
	selectedTools := []tools.Tool{}
	for _, tool := range tools.Tools {
		if tool.Name() != "subagent" {
			selectedTools = append(selectedTools, tool)
		}
	}
	return tools.ToAnthropicTools(selectedTools)
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
func (t *AnthropicThread) NewSubAgent(ctx context.Context) types.Thread {
	config := t.config
	config.IsSubAgent = true
	thread := NewAnthropicThread(config)
	thread.isPersisted = false // subagent is not persisted
	thread.SetState(state.NewBasicState())
	thread.usage = t.usage

	return thread
}

func (t *AnthropicThread) WithSubAgent(ctx context.Context) context.Context {
	subAgent := t.NewSubAgent(ctx)
	ctx = context.WithValue(ctx, types.ThreadKey{}, subAgent)
	return ctx
}

func (t *AnthropicThread) ShortSummary(ctx context.Context) string {
	prompt := `Summarise the conversation in one sentence, less or equal than 12 words. Keep it short and concise.
Treat the USER role as the first person (I), and the ASSISTANT role as the person you are talking to.
`
	handler := &types.StringCollectorHandler{
		Silent: true,
	}
	t.isPersisted = false
	defer func() {
		t.isPersisted = true
	}()
	// Use a faster model for summarization as it's a simpler task
	_, err := t.SendMessage(ctx, prompt, handler, types.MessageOpt{
		UseWeakModel: true,
		PromptCache:  false, // maybe we should make it configurable, but there is likely no cache for weak model
	})
	if err != nil {
		return ""
	}

	if len(t.messages) >= 2 {
		t.messages = t.messages[:len(t.messages)-2]
	}

	return handler.CollectedText()
}

// GetUsage returns the current token usage for the thread
func (t *AnthropicThread) GetUsage() types.Usage {
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
