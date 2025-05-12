package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
)

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
	MessageCh chan types.MessageEvent
}

// Implementation of MessageHandler for ChannelMessageHandler
func (h *ChannelMessageHandler) HandleText(text string) {
	h.MessageCh <- types.MessageEvent{
		Type:    types.EventTypeText,
		Content: text,
	}
}

func (h *ChannelMessageHandler) HandleToolUse(toolName string, input string) {
	h.MessageCh <- types.MessageEvent{
		Type:    types.EventTypeToolUse,
		Content: fmt.Sprintf("%s: %s", toolName, input),
	}
}

func (h *ChannelMessageHandler) HandleToolResult(toolName string, result string) {
	h.MessageCh <- types.MessageEvent{
		Type:    types.EventTypeToolResult,
		Content: result,
	}
}

func (h *ChannelMessageHandler) HandleDone() {
	h.MessageCh <- types.MessageEvent{
		Type:    types.EventTypeText,
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
func NewThread(config types.Config) types.Thread {
	// Determine which provider to use based on the model name
	modelName := config.Model

	// Default to Anthropic Claude if no model specified
	if modelName == "" {
		return anthropic.NewAnthropicThread(config)
	}

	// Check model name patterns to determine provider
	switch {
	// If the model starts with "claude" or matches Anthropic's constants, use Anthropic
	case strings.HasPrefix(strings.ToLower(modelName), "claude"):
		return anthropic.NewAnthropicThread(config)

	// Add cases for other providers here in the future
	// Example:
	// case strings.HasPrefix(strings.ToLower(modelName), "gpt"):
	//     return NewOpenAIThread(config)

	// Default to Anthropic for now
	default:
		return anthropic.NewAnthropicThread(config)
	}
}

// SendMessageAndGetTextWithUsage is a convenience method for one-shot queries that returns the response as a string and usage information
func SendMessageAndGetTextWithUsage(ctx context.Context, state state.State, query string, config types.Config, silent bool, modelOverride ...string) (string, types.Usage) {
	thread := NewThread(config)
	thread.SetState(state)

	handler := &StringCollectorHandler{Silent: silent}
	err := thread.SendMessage(ctx, query, handler, modelOverride...)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), types.Usage{}
	}
	return handler.CollectedText(), thread.GetUsage()
}

// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state state.State, query string, config types.Config, silent bool, modelOverride ...string) string {
	text, _ := SendMessageAndGetTextWithUsage(ctx, state, query, config, silent, modelOverride...)
	return text
}