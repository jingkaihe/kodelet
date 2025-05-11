package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/state"
)

// MessageHandler defines how message events should be processed
type MessageHandler interface {
	HandleText(text string)
	HandleToolUse(toolName string, input string)
	HandleToolResult(toolName string, result string)
	HandleDone()
}

// Thread represents a conversation thread with an LLM
type Thread interface {
	// SetState sets the state for the thread
	SetState(s state.State)
	// GetState returns the current state of the thread
	GetState() state.State
	// AddUserMessage adds a user message to the thread
	AddUserMessage(message string)
	// SendMessage sends a message to the LLM and processes the response
	SendMessage(ctx context.Context, message string, handler MessageHandler, modelOverride ...string) error
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

// NewThread creates a new thread based on the model specified in the config
func NewThread(config Config) Thread {
	// Determine which provider to use based on the model name
	modelName := config.Model

	// Default to Anthropic Claude if no model specified
	if modelName == "" {
		return NewAnthropicThread(config)
	}

	// Check model name patterns to determine provider
	switch {
	// If the model starts with "claude" or matches Anthropic's constants, use Anthropic
	case strings.HasPrefix(strings.ToLower(modelName), "claude"):
		return NewAnthropicThread(config)

	// Add cases for other providers here in the future
	// Example:
	// case strings.HasPrefix(strings.ToLower(modelName), "gpt"):
	//     return NewOpenAIThread(config)

	// Default to Anthropic for now
	default:
		return NewAnthropicThread(config)
	}
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
