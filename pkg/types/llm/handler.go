// Package llm defines types and interfaces for Large Language Model
// interactions including message handlers, threads, configuration,
// and usage tracking for different LLM providers.
package llm

import (
	"fmt"
	"strings"
)

// MessageHandler defines how message events should be processed
type MessageHandler interface {
	HandleText(text string)
	HandleToolUse(toolName string, input string)
	HandleToolResult(toolName string, result string)
	HandleThinking(thinking string)
	HandleDone()
}

// StreamingMessageHandler extends MessageHandler with delta streaming support.
// Handlers implementing this interface will receive content as it streams from the LLM.
type StreamingMessageHandler interface {
	MessageHandler
	HandleTextDelta(delta string)     // Called for each text chunk as it streams
	HandleThinkingStart()             // Called when a thinking block starts
	HandleThinkingDelta(delta string) // Called for each thinking chunk as it streams
	HandleContentBlockEnd()           // Called when any content block ends
}

// MessageEvent represents an event from processing a message
type MessageEvent struct {
	Type    string
	Content string
	Done    bool
}

// Event types
const (
	EventTypeThinking   = "thinking"
	EventTypeText       = "text"
	EventTypeToolUse    = "tool_use"
	EventTypeToolResult = "tool_result"
)

// ConsoleMessageHandler prints messages to the console
type ConsoleMessageHandler struct {
	Silent bool
}

// HandleText prints the text to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleText(text string) {
	if !h.Silent {
		fmt.Println(text)
		fmt.Println()
	}
}

// HandleToolUse prints tool invocation details to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleToolUse(toolName string, input string) {
	if !h.Silent {
		fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
	}
}

// HandleToolResult prints tool execution results to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleToolResult(_ string, result string) {
	if !h.Silent {
		fmt.Printf("🔄 Tool result:\n%s\n\n", result)
	}
}

// HandleThinking prints thinking content to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinking(thinking string) {
	if !h.Silent {
		thinking = strings.Trim(thinking, "\n")
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
	}
}

// HandleDone is called when message processing is complete
func (h *ConsoleMessageHandler) HandleDone() {
	// No action needed for console handler
}

// HandleTextDelta prints streamed text chunks to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleTextDelta(delta string) {
	if !h.Silent {
		fmt.Print(delta)
	}
}

// HandleThinkingStart prints the thinking prefix to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinkingStart() {
	if !h.Silent {
		fmt.Print("💭 Thinking: ")
	}
}

// HandleThinkingDelta prints streamed thinking chunks to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinkingDelta(delta string) {
	if !h.Silent {
		fmt.Print(delta)
	}
}

// HandleContentBlockEnd prints a newline when a content block ends unless Silent is true
func (h *ConsoleMessageHandler) HandleContentBlockEnd() {
	if !h.Silent {
		fmt.Println()
	}
}

// ChannelMessageHandler sends messages through a channel (for TUI)
type ChannelMessageHandler struct {
	MessageCh chan MessageEvent
}

// HandleText sends the text through the message channel as a text event
func (h *ChannelMessageHandler) HandleText(text string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: text,
	}
}

// HandleToolUse sends tool invocation details through the message channel as a tool use event
func (h *ChannelMessageHandler) HandleToolUse(toolName string, input string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeToolUse,
		Content: fmt.Sprintf("%s: %s", toolName, input),
	}
}

// HandleToolResult sends tool execution results through the message channel as a tool result event
func (h *ChannelMessageHandler) HandleToolResult(_ string, result string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeToolResult,
		Content: result,
	}
}

// HandleDone sends a completion event through the message channel
func (h *ChannelMessageHandler) HandleDone() {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: "Done",
		Done:    true,
	}
}

// HandleThinking sends thinking content through the message channel as a thinking event
func (h *ChannelMessageHandler) HandleThinking(thinking string) {
	thinking = strings.Trim(thinking, "\n")
	h.MessageCh <- MessageEvent{
		Type:    EventTypeThinking,
		Content: strings.TrimLeft(thinking, "\n"),
	}
}

// HandleTextDelta sends streamed text chunks through the message channel
func (h *ChannelMessageHandler) HandleTextDelta(delta string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeText,
		Content: delta,
	}
}

// HandleThinkingStart sends a thinking start event through the message channel
func (h *ChannelMessageHandler) HandleThinkingStart() {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeThinking,
		Content: "",
	}
}

// HandleThinkingDelta sends streamed thinking chunks through the message channel
func (h *ChannelMessageHandler) HandleThinkingDelta(delta string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeThinking,
		Content: delta,
	}
}

// HandleContentBlockEnd is a no-op for channel handler as events are self-contained
func (h *ChannelMessageHandler) HandleContentBlockEnd() {
	// No action needed - channel events are self-contained
}

// StringCollectorHandler collects text responses into a string
type StringCollectorHandler struct {
	Silent bool
	text   strings.Builder
}

// HandleText collects the text in a string builder and optionally prints to console
func (h *StringCollectorHandler) HandleText(text string) {
	h.text.WriteString(text)
	h.text.WriteString("\n")

	if !h.Silent {
		fmt.Println(text)
		fmt.Println()
	}
}

// HandleToolUse optionally prints tool invocation details to the console (does not affect collection)
func (h *StringCollectorHandler) HandleToolUse(toolName string, input string) {
	if !h.Silent {
		fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
	}
}

// HandleToolResult optionally prints tool execution results to the console (does not affect collection)
func (h *StringCollectorHandler) HandleToolResult(_ string, result string) {
	if !h.Silent {
		fmt.Printf("🔄 Tool result: %s\n\n", result)
	}
}

// HandleThinking optionally prints thinking content to the console (does not affect collection)
func (h *StringCollectorHandler) HandleThinking(thinking string) {
	thinking = strings.Trim(thinking, "\n")
	if !h.Silent {
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
	}
}

// HandleDone is called when message processing is complete
func (h *StringCollectorHandler) HandleDone() {
	// No action needed for string collector
}

// CollectedText returns the accumulated text responses as a single string
func (h *StringCollectorHandler) CollectedText() string {
	return h.text.String()
}

// HandleTextDelta collects streamed text chunks and optionally prints to console
func (h *StringCollectorHandler) HandleTextDelta(delta string) {
	h.text.WriteString(delta)
	if !h.Silent {
		fmt.Print(delta)
	}
}

// HandleThinkingStart optionally prints the thinking prefix to the console
func (h *StringCollectorHandler) HandleThinkingStart() {
	if !h.Silent {
		fmt.Print("💭 Thinking: ")
	}
}

// HandleThinkingDelta optionally prints streamed thinking chunks to the console
func (h *StringCollectorHandler) HandleThinkingDelta(delta string) {
	if !h.Silent {
		fmt.Print(delta)
	}
}

// HandleContentBlockEnd optionally prints a newline when a content block ends
func (h *StringCollectorHandler) HandleContentBlockEnd() {
	if !h.Silent {
		fmt.Println()
	}
}
