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
		fmt.Printf("🔄 Tool result:\n%s\n\n", result)
	}
}

func (h *ConsoleMessageHandler) HandleThinking(thinking string) {
	if !h.Silent {
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
	}
}

func (h *ConsoleMessageHandler) HandleDone() {
	// No action needed for console handler
}

// ChannelMessageHandler sends messages through a channel (for TUI)
type ChannelMessageHandler struct {
	MessageCh chan MessageEvent
}

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

func (h *ChannelMessageHandler) HandleThinking(thinking string) {
	h.MessageCh <- MessageEvent{
		Type:    EventTypeThinking,
		Content: thinking,
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

func (h *StringCollectorHandler) HandleThinking(thinking string) {
	if !h.Silent {
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
	}
}

func (h *StringCollectorHandler) HandleDone() {
	// No action needed for string collector
}

func (h *StringCollectorHandler) CollectedText() string {
	return h.text.String()
}

// ToolExecutionStore interface for storing tool executions
type ToolExecutionStore interface {
	AddToolExecution(conversationID, toolName, input, userFacing string, messageIndex int) error
}

// ConversationStoringHandler wraps another handler and stores tool executions to a conversation
type ConversationStoringHandler struct {
	wrapped           MessageHandler
	conversationStore ToolExecutionStore
	conversationID    string
	messageIndex      int
	pendingToolUse    []PendingToolExecution // Queue of pending tool uses
}

// PendingToolExecution represents a tool execution waiting for results
type PendingToolExecution struct {
	ToolName string
	Input    string
	CallID   string // Unique identifier for this specific call
}

// NewConversationStoringHandler creates a new handler that stores tool executions
func NewConversationStoringHandler(wrapped MessageHandler, store ToolExecutionStore, conversationID string, messageIndex int) *ConversationStoringHandler {
	return &ConversationStoringHandler{
		wrapped:           wrapped,
		conversationStore: store,
		conversationID:    conversationID,
		messageIndex:      messageIndex,
		pendingToolUse:    make([]PendingToolExecution, 0),
	}
}

// Implementation of MessageHandler for ConversationStoringHandler
func (h *ConversationStoringHandler) HandleText(text string) {
	h.wrapped.HandleText(text)
}

func (h *ConversationStoringHandler) HandleToolUse(toolName string, input string) {
	// Add tool use to the queue (FIFO order)
	h.pendingToolUse = append(h.pendingToolUse, PendingToolExecution{
		ToolName: toolName,
		Input:    input,
	})

	h.wrapped.HandleToolUse(toolName, input)
}

func (h *ConversationStoringHandler) HandleToolResult(toolName string, result string) {
	// Store the tool execution in the conversation
	if h.conversationStore != nil && len(h.pendingToolUse) > 0 {
		// Find the first pending tool use with matching name (FIFO)
		for i, pending := range h.pendingToolUse {
			if pending.ToolName == toolName {
				// Store the execution
				h.conversationStore.AddToolExecution(h.conversationID, toolName, pending.Input, result, h.messageIndex)

				// Remove this item from the queue
				h.pendingToolUse = append(h.pendingToolUse[:i], h.pendingToolUse[i+1:]...)
				break
			}
		}
	}

	// Forward to the wrapped handler
	h.wrapped.HandleToolResult(toolName, result)
}

func (h *ConversationStoringHandler) HandleThinking(thinking string) {
	h.wrapped.HandleThinking(thinking)
}

func (h *ConversationStoringHandler) HandleDone() {
	h.wrapped.HandleDone()
}

// UpdateMessageIndex allows updating the current message index
func (h *ConversationStoringHandler) UpdateMessageIndex(index int) {
	h.messageIndex = index
}
