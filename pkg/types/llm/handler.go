// Package llm defines types and interfaces for Large Language Model
// interactions including message handlers, threads, configuration,
// and usage tracking for different LLM providers.
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// ToolUpdateMessageHandler can be implemented by message handlers that want
// transient, accumulated tool result snapshots while a tool is still running.
// Providers may execute multiple tool calls concurrently, so implementations
// must be safe for concurrent calls involving different toolCallID values.
type ToolUpdateMessageHandler interface {
	HandleToolUpdate(toolCallID string, toolName string, result tooltypes.ToolResult)
}

// UserMessageHandler can render user-authored messages that are injected during
// an active turn, such as queued steering messages.
type UserMessageHandler interface {
	HandleUserMessage(content string, images []string)
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

// StreamingAttemptMessageHandler can reset attempt-local state before a
// provider starts or retries a streaming request.
type StreamingAttemptMessageHandler interface {
	HandleStreamingAttemptStart()
}

// UsageMessageHandler receives cumulative usage snapshots while a message is being
// processed. Implementations can use this to surface live token usage updates.
type UsageMessageHandler interface {
	HandleUsage(usage Usage)
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
	EventTypeToolUpdate = "tool_update"
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
	toolResultRole string
	bufferMu       sync.Mutex
	textBuffer     strings.Builder
	thinkingBuffer strings.Builder
	mu             sync.Mutex
}

// DeltaEntry represents a streaming delta event for headless mode output
type DeltaEntry struct {
	Kind           string                          `json:"kind"`
	Delta          string                          `json:"delta,omitempty"`
	Content        string                          `json:"content,omitempty"`
	ToolName       string                          `json:"tool_name,omitempty"`
	ToolCallID     string                          `json:"tool_call_id,omitempty"`
	Input          string                          `json:"input,omitempty"`
	Result         string                          `json:"result,omitempty"`
	ToolResult     *tooltypes.StructuredToolResult `json:"tool_result,omitempty"`
	ConversationID string                          `json:"conversation_id"`
	Role           string                          `json:"role"`
}

// NewHeadlessStreamHandler creates a new HeadlessStreamHandler with the given
// conversation ID and optional provider name.
func NewHeadlessStreamHandler(conversationID string, provider ...string) *HeadlessStreamHandler {
	toolResultRole := "assistant"
	if len(provider) > 0 && strings.EqualFold(strings.TrimSpace(provider[0]), "anthropic") {
		toolResultRole = "user"
	}
	return &HeadlessStreamHandler{
		conversationID: conversationID,
		toolResultRole: toolResultRole,
	}
}

func (h *HeadlessStreamHandler) output(entry DeltaEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, _ := json.Marshal(entry)
	fmt.Fprintf(os.Stdout, "%s\n", data)
}

// HandleStreamingAttemptStart discards incomplete content from an abandoned
// provider attempt before a retry starts.
func (h *HeadlessStreamHandler) HandleStreamingAttemptStart() {
	h.bufferMu.Lock()
	h.textBuffer.Reset()
	h.thinkingBuffer.Reset()
	h.bufferMu.Unlock()
}

// HandleTextDelta outputs text delta events
func (h *HeadlessStreamHandler) HandleTextDelta(delta string) {
	h.bufferMu.Lock()
	h.textBuffer.WriteString(delta)
	h.bufferMu.Unlock()
	h.output(DeltaEntry{
		Kind:           "text-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleThinkingStart outputs thinking block start event
func (h *HeadlessStreamHandler) HandleThinkingStart() {
	h.bufferMu.Lock()
	h.thinkingBuffer.Reset()
	h.bufferMu.Unlock()
	h.output(DeltaEntry{
		Kind:           "thinking-start",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleThinkingDelta outputs thinking delta events
func (h *HeadlessStreamHandler) HandleThinkingDelta(delta string) {
	h.bufferMu.Lock()
	h.thinkingBuffer.WriteString(delta)
	h.bufferMu.Unlock()
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
	h.bufferMu.Lock()
	thinking := h.thinkingBuffer.String()
	h.thinkingBuffer.Reset()
	h.bufferMu.Unlock()
	if thinking != "" {
		h.HandleThinking(thinking)
	}
}

// HandleContentBlockEnd outputs content block end event
func (h *HeadlessStreamHandler) HandleContentBlockEnd() {
	h.output(DeltaEntry{
		Kind:           "content-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
	h.bufferMu.Lock()
	text := h.textBuffer.String()
	h.textBuffer.Reset()
	h.bufferMu.Unlock()
	if text != "" {
		h.HandleText(text)
	}
}

// HandleText outputs a complete text block in provider order.
func (h *HeadlessStreamHandler) HandleText(text string) {
	h.output(DeltaEntry{
		Kind:           "text",
		Content:        text,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleUserMessage outputs user-authored messages before subsequent assistant
// and tool events.
func (h *HeadlessStreamHandler) HandleUserMessage(content string, images []string) {
	for _, image := range images {
		if display := headlessImageDisplay(image); display != "" {
			h.output(DeltaEntry{
				Kind:           "text",
				Content:        display,
				ConversationID: h.conversationID,
				Role:           "user",
			})
		}
	}
	if strings.TrimSpace(content) != "" {
		h.output(DeltaEntry{
			Kind:           "text",
			Content:        content,
			ConversationID: h.conversationID,
			Role:           "user",
		})
	}
}

func headlessImageDisplay(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if strings.HasPrefix(image, "data:") {
		metadata, _, _ := strings.Cut(strings.TrimPrefix(image, "data:"), ",")
		mediaType, _, _ := strings.Cut(metadata, ";")
		if mediaType = strings.TrimSpace(mediaType); mediaType != "" {
			return fmt.Sprintf("Inline image input (%s).", mediaType)
		}
		return "Inline image input."
	}
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return "Image input: " + image
	}
	mediaType := headlessImageMediaType(filepath.Ext(image))
	if mediaType != "" {
		return fmt.Sprintf("Inline image input (%s).", mediaType)
	}
	return "Inline image input."
}

func headlessImageMediaType(extension string) string {
	switch strings.ToLower(extension) {
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

// HandleToolUse outputs tool invocations immediately so subsequent transient
// updates always have a known call ID.
func (h *HeadlessStreamHandler) HandleToolUse(toolCallID, toolName, input string) {
	h.output(DeltaEntry{
		Kind:           "tool-use",
		ToolName:       toolName,
		ToolCallID:     toolCallID,
		Input:          input,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleToolUpdate outputs transient accumulated tool result snapshots.
func (h *HeadlessStreamHandler) HandleToolUpdate(toolCallID, toolName string, result tooltypes.ToolResult) {
	structuredResult := result.StructuredData()
	if structuredResult.ToolName == "" || structuredResult.ToolName == "unknown" {
		structuredResult.ToolName = toolName
	}
	h.output(DeltaEntry{
		Kind:           "tool-update",
		ToolName:       structuredResult.ToolName,
		ToolCallID:     toolCallID,
		Result:         headlessToolResultText(result, structuredResult),
		ToolResult:     &structuredResult,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleToolResult outputs the final tool result after all transient snapshots.
func (h *HeadlessStreamHandler) HandleToolResult(toolCallID, toolName string, result tooltypes.ToolResult) {
	structuredResult := result.StructuredData()
	if structuredResult.ToolName == "" || structuredResult.ToolName == "unknown" {
		structuredResult.ToolName = toolName
	}
	h.output(DeltaEntry{
		Kind:           "tool-result",
		ToolName:       structuredResult.ToolName,
		ToolCallID:     toolCallID,
		Result:         headlessFinalToolResultText(result, structuredResult),
		ToolResult:     &structuredResult,
		ConversationID: h.conversationID,
		Role:           h.toolResultRole,
	})
}

func headlessFinalToolResultText(result tooltypes.ToolResult, structuredResult tooltypes.StructuredToolResult) string {
	if data, err := structuredResult.MarshalJSON(); err == nil {
		return string(data)
	}
	return headlessToolResultText(result, structuredResult)
}

func headlessToolResultText(result tooltypes.ToolResult, structuredResult tooltypes.StructuredToolResult) string {
	if structuredResult.ToolName == "bash" {
		var metadata tooltypes.BashMetadata
		if tooltypes.ExtractMetadata(structuredResult.Metadata, &metadata) {
			return metadata.Output
		}
	}
	return result.GetResult()
}

// HandleThinking outputs a complete thinking block in provider order.
func (h *HeadlessStreamHandler) HandleThinking(thinking string) {
	h.output(DeltaEntry{
		Kind:           "thinking",
		Content:        thinking,
		ConversationID: h.conversationID,
		Role:           "assistant",
	})
}

// HandleDone is called when message processing is complete
func (h *HeadlessStreamHandler) HandleDone() {}
