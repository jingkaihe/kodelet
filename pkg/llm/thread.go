package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// MessageHandler defines how message events should be processed
type MessageHandler interface {
	HandleText(text string)
	HandleToolUse(toolName string, input string)
	HandleToolResult(toolName string, result string)
	HandleDone()
}

// Thread represents a conversation thread with an LLM
type Thread struct {
	client   anthropic.Client
	config   Config
	state    state.State
	messages []anthropic.MessageParam
}

// NewThread creates a new conversation thread
func NewThread(config Config) *Thread {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = anthropic.ModelClaude3_7SonnetLatest
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	return &Thread{
		client: anthropic.NewClient(),
		config: config,
	}
}

// Accessors and setters for Thread
func (t *Thread) SetState(s state.State) {
	t.state = s
}

func (t *Thread) GetState() state.State {
	return t.state
}

func (t *Thread) SetMessages(messages []anthropic.MessageParam) {
	t.messages = messages
}

func (t *Thread) GetMessages() []anthropic.MessageParam {
	return t.messages
}

func (t *Thread) AddUserMessage(message string) {
	t.messages = append(t.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))
}

// ConsoleMessageHandler prints messages to the console
type ConsoleMessageHandler struct {
	Silent bool
}

// Implementation of MessageHandler for ConsoleMessageHandler
func (h *ConsoleMessageHandler) HandleText(text string) {
	if !h.Silent {
		fmt.Println(text)
		fmt.Println()
	}
}

func (h *ConsoleMessageHandler) HandleToolUse(toolName string, input string) {
	if !h.Silent {
		fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
	}
}

func (h *ConsoleMessageHandler) HandleToolResult(toolName string, result string) {
	if !h.Silent {
		fmt.Printf("🔄 Tool result: %s\n\n", result)
	}
}

func (h *ConsoleMessageHandler) HandleDone() {
	// No action needed for console handler
}

// ChannelMessageHandler sends messages through a channel (for TUI)
type ChannelMessageHandler struct {
	MessageCh chan MessageEvent
}

// MessageEvent represents an event from processing a message
type MessageEvent struct {
	Type    string
	Content string
	Done    bool
}

// Event types
const (
	EventTypeText       = "text"
	EventTypeToolUse    = "tool_use"
	EventTypeToolResult = "tool_result"
)

// Implementation of MessageHandler for ChannelMessageHandler
func (h *ChannelMessageHandler) HandleText(text string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: text,
	}
}

func (h *ChannelMessageHandler) HandleToolUse(toolName string, input string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeToolUse,
		Content: fmt.Sprintf("%s: %s", toolName, input),
	}
}

func (h *ChannelMessageHandler) HandleToolResult(toolName string, result string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeToolResult,
		Content: result,
	}
}

func (h *ChannelMessageHandler) HandleDone() {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: "Done",
		Done:    true,
	}
}

// StringCollectorHandler collects text responses into a string
type StringCollectorHandler struct {
	Silent bool
	text   strings.Builder
}

// Implementation of MessageHandler for StringCollectorHandler
func (h *StringCollectorHandler) HandleText(text string) {
	h.text.WriteString(text)
	h.text.WriteString("\n")

	if !h.Silent {
		fmt.Println(text)
		fmt.Println()
	}
}

func (h *StringCollectorHandler) HandleToolUse(toolName string, input string) {
	if !h.Silent {
		fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
	}
}

func (h *StringCollectorHandler) HandleToolResult(toolName string, result string) {
	if !h.Silent {
		fmt.Printf("🔄 Tool result: %s\n\n", result)
	}
}

func (h *StringCollectorHandler) HandleDone() {
	// No action needed for string collector
}

func (h *StringCollectorHandler) CollectedText() string {
	return h.text.String()
}

// SendMessage sends a user message to the thread and processes the response
func (t *Thread) SendMessage(
	ctx context.Context,
	message string,
	handler MessageHandler,
	modelOverride ...string,
) error {
	// Add the user message to history
	t.AddUserMessage(message)

	// Main interaction loop for handling tool calls
	for {
		// Determine which model to use
		model := t.config.Model
		if len(modelOverride) > 0 && modelOverride[0] != "" {
			model = modelOverride[0]
		}

		// Send request to Anthropic API
		response, err := t.client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: int64(t.config.MaxTokens),
			System: []anthropic.TextBlockParam{
				{
					Text: sysprompt.SystemPrompt(model),
					CacheControl: anthropic.CacheControlEphemeralParam{
						Type: "ephemeral",
					},
				},
			},
			Messages: t.messages,
			Model:    model,
			Tools:    tools.ToAnthropicTools(tools.Tools),
		})
		if err != nil {
			return fmt.Errorf("error sending message to Anthropic: %w", err)
		}

		// Add the assistant response to history
		t.messages = append(t.messages, response.ToParam())

		// Process the response content blocks
		toolUseCount := 0
		for _, block := range response.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				handler.HandleText(variant.Text)
			case anthropic.ToolUseBlock:
				toolUseCount++
				inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
				handler.HandleToolUse(block.Name, string(inputJSON))

				// Run the tool
				output := tools.RunTool(ctx, t.state, block.Name, string(variant.JSON.Input.Raw()))
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

	handler.HandleDone()
	return nil
}

// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state state.State, query string, config Config, silent bool, modelOverride ...string) string {
	thread := NewThread(config)
	thread.SetState(state)

	handler := &StringCollectorHandler{Silent: silent}
	err := thread.SendMessage(ctx, query, handler, modelOverride...)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return handler.CollectedText()
}
