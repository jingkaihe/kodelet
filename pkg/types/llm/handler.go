// Package llm defines types and interfaces for Large Language Model
// interactions including message handlers, threads, configuration,
// and usage tracking for different LLM providers.
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// consoleMu protects console output from concurrent writes during parallel tool execution
var consoleMu sync.Mutex

// formatJSONInput formats a JSON string with indentation for better readability.
// If the input is not valid JSON, it returns the original string.
func formatJSONInput(input string) string {
	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return input
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("  ", "  ")
	if err := encoder.Encode(data); err != nil {
		return input
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// MessageHandler defines how message events should be processed
type MessageHandler interface {
	HandleText(text string)
	HandleToolUse(toolCallID string, toolName string, input string)
	HandleToolResult(toolCallID string, toolName string, result tooltypes.ToolResult)
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
	HandleThinkingBlockEnd()          // Called when a thinking block ends (for visual separation)
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

	// Streaming event types
	EventTypeTextDelta        = "text_delta"
	EventTypeThinkingStart    = "thinking_start"
	EventTypeThinkingDelta    = "thinking_delta"
	EventTypeThinkingBlockEnd = "thinking_block_end"
	EventTypeContentBlockEnd  = "content_block_end"
)

// ConsoleMessageHandler prints messages to the console
type ConsoleMessageHandler struct {
	Silent bool
}

// HandleText prints the text to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleText(text string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Println(text)
		fmt.Println()
		consoleMu.Unlock()
	}
}

// HandleToolUse prints tool invocation details to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleToolUse(_ string, toolName string, input string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Printf("🔧 Using tool: %s\n  %s\n\n", toolName, formatJSONInput(input))
		consoleMu.Unlock()
	}
}

// HandleToolResult prints tool execution results to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleToolResult(_, _ string, result tooltypes.ToolResult) {
	if !h.Silent {
		registry := renderers.NewRendererRegistry()
		rendered := registry.Render(result.StructuredData())
		consoleMu.Lock()
		fmt.Printf("🔄 Tool result:\n%s\n\n", rendered)
		consoleMu.Unlock()
	}
}

// HandleThinking prints thinking content to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinking(thinking string) {
	if !h.Silent {
		thinking = strings.Trim(thinking, "\n")
		consoleMu.Lock()
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
		consoleMu.Unlock()
	}
}

// HandleDone is called when message processing is complete
func (h *ConsoleMessageHandler) HandleDone() {
	// No action needed for console handler
}

// HandleTextDelta prints streamed text chunks to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleTextDelta(delta string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Print(delta)
		consoleMu.Unlock()
	}
}

// HandleThinkingStart prints the thinking prefix to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinkingStart() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Print("💭 Thinking: ")
		consoleMu.Unlock()
	}
}

// HandleThinkingDelta prints streamed thinking chunks to the console unless Silent is true
func (h *ConsoleMessageHandler) HandleThinkingDelta(delta string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Print(delta)
		consoleMu.Unlock()
	}
}

// HandleThinkingBlockEnd prints a separator when a thinking block ends unless Silent is true
func (h *ConsoleMessageHandler) HandleThinkingBlockEnd() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Println("\n----")
		consoleMu.Unlock()
	}
}

// HandleContentBlockEnd prints a newline when a content block ends unless Silent is true
func (h *ConsoleMessageHandler) HandleContentBlockEnd() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Println()
		consoleMu.Unlock()
	}
}

// StringCollectorHandler collects text responses into a string
type StringCollectorHandler struct {
	Silent bool
	text   strings.Builder
	mu     sync.Mutex
}

// HandleText collects the text in a string builder and optionally prints to console
func (h *StringCollectorHandler) HandleText(text string) {
	h.mu.Lock()
	h.text.WriteString(text)
	h.text.WriteString("\n")
	h.mu.Unlock()

	if !h.Silent {
		consoleMu.Lock()
		fmt.Println(text)
		fmt.Println()
		consoleMu.Unlock()
	}
}

// HandleToolUse optionally prints tool invocation details to the console (does not affect collection)
func (h *StringCollectorHandler) HandleToolUse(_ string, toolName string, input string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Printf("🔧 Using tool: %s\n  %s\n\n", toolName, formatJSONInput(input))
		consoleMu.Unlock()
	}
}

// HandleToolResult optionally prints tool execution results to the console (does not affect collection)
func (h *StringCollectorHandler) HandleToolResult(_, _ string, result tooltypes.ToolResult) {
	if !h.Silent {
		registry := renderers.NewRendererRegistry()
		rendered := registry.Render(result.StructuredData())
		consoleMu.Lock()
		fmt.Printf("🔄 Tool result: %s\n\n", rendered)
		consoleMu.Unlock()
	}
}

// HandleThinking optionally prints thinking content to the console (does not affect collection)
func (h *StringCollectorHandler) HandleThinking(thinking string) {
	thinking = strings.Trim(thinking, "\n")
	if !h.Silent {
		consoleMu.Lock()
		fmt.Printf("💭 Thinking: %s\n\n", thinking)
		consoleMu.Unlock()
	}
}

// HandleDone is called when message processing is complete
func (h *StringCollectorHandler) HandleDone() {
	// No action needed for string collector
}

// CollectedText returns the accumulated text responses as a single string
func (h *StringCollectorHandler) CollectedText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

// HandleTextDelta collects streamed text chunks and optionally prints to console
func (h *StringCollectorHandler) HandleTextDelta(delta string) {
	h.mu.Lock()
	h.text.WriteString(delta)
	h.mu.Unlock()

	if !h.Silent {
		consoleMu.Lock()
		fmt.Print(delta)
		consoleMu.Unlock()
	}
}

// HandleThinkingStart optionally prints the thinking prefix to the console
func (h *StringCollectorHandler) HandleThinkingStart() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Print("💭 Thinking: ")
		consoleMu.Unlock()
	}
}

// HandleThinkingDelta optionally prints streamed thinking chunks to the console
func (h *StringCollectorHandler) HandleThinkingDelta(delta string) {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Print(delta)
		consoleMu.Unlock()
	}
}

// HandleThinkingBlockEnd optionally prints a separator when a thinking block ends
func (h *StringCollectorHandler) HandleThinkingBlockEnd() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Println("\n----")
		consoleMu.Unlock()
	}
}

// HandleContentBlockEnd optionally prints a newline when a content block ends
func (h *StringCollectorHandler) HandleContentBlockEnd() {
	if !h.Silent {
		consoleMu.Lock()
		fmt.Println()
		consoleMu.Unlock()
	}
}

// HeadlessStreamHandler outputs streaming events as JSON to stdout
// for headless mode with --stream-deltas enabled.
type HeadlessStreamHandler struct {
	conversationID string
	mu             sync.Mutex
}

// DeltaEntry represents a streaming delta event for headless mode output
type DeltaEntry struct {
	Kind           string `json:"kind"`
	Delta          string `json:"delta,omitempty"`
	Content        string `json:"content,omitempty"`
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
}

// NewHeadlessStreamHandler creates a new HeadlessStreamHandler with the given conversation ID
func NewHeadlessStreamHandler(conversationID string) *HeadlessStreamHandler {
	return &HeadlessStreamHandler{
		conversationID: conversationID,
	}
}

func (h *HeadlessStreamHandler) output(entry DeltaEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, _ := json.Marshal(entry)
	fmt.Fprintf(os.Stdout, "%s\n", data)
}

// HandleTextDelta outputs text delta events
func (h *HeadlessStreamHandler) HandleTextDelta(delta string) {
	h.output(DeltaEntry{
		Kind:           "text-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleThinkingStart outputs thinking block start event
func (h *HeadlessStreamHandler) HandleThinkingStart() {
	h.output(DeltaEntry{
		Kind:           "thinking-start",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleThinkingDelta outputs thinking delta events
func (h *HeadlessStreamHandler) HandleThinkingDelta(delta string) {
	h.output(DeltaEntry{
		Kind:           "thinking-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleThinkingBlockEnd outputs thinking block end event
func (h *HeadlessStreamHandler) HandleThinkingBlockEnd() {
	h.output(DeltaEntry{
		Kind:           "thinking-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleContentBlockEnd outputs content block end event
func (h *HeadlessStreamHandler) HandleContentBlockEnd() {
	h.output(DeltaEntry{
		Kind:           "content-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleText is a no-op as complete text is handled by ConversationStreamer
func (h *HeadlessStreamHandler) HandleText(_ string) {}

// HandleToolUse is a no-op as tool calls are handled by ConversationStreamer
func (h *HeadlessStreamHandler) HandleToolUse(_, _, _ string) {}

// HandleToolResult is a no-op as tool results are handled by ConversationStreamer
func (h *HeadlessStreamHandler) HandleToolResult(_, _ string, _ tooltypes.ToolResult) {}

// HandleThinking is a no-op as complete thinking is handled by ConversationStreamer
func (h *HeadlessStreamHandler) HandleThinking(_ string) {}

// HandleDone is called when message processing is complete
func (h *HeadlessStreamHandler) HandleDone() {}
