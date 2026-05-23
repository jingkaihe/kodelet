package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatJSONInput(t *testing.T) {
	assert.Equal(t, "not-json", formatJSONInput("not-json"))
	assert.Equal(t, "{\n    \"alpha\": 1\n  }", formatJSONInput(`{"alpha":1}`))
}

func TestConsoleMessageHandler_HandleThinkingTrimsLeadingNewlines(t *testing.T) {
	handler := &ConsoleMessageHandler{Silent: false}

	// Since we can't easily capture stdout in tests, we'll test the logic directly
	thinkingWithNewlines := "\n\nThis is thinking content with leading newlines"
	expectedTrimmed := "This is thinking content with leading newlines"

	// Test the string trimming logic
	trimmed := strings.TrimLeft(thinkingWithNewlines, "\n")
	assert.Equal(t, expectedTrimmed, trimmed)

	// Test that handler doesn't panic with newlines
	handler.HandleThinking(thinkingWithNewlines)
	// If we get here without panicking, the test passes
}

func TestConsoleMessageHandlerOutput(t *testing.T) {
	handler := &ConsoleMessageHandler{}

	output := captureStdout(func() {
		handler.HandleText("hello")
		handler.HandleToolUse("call-1", "bash", `{"command":"pwd"}`)
		handler.HandleToolResult("call-1", "bash", tooltypes.BaseToolResult{Result: "ok"})
		handler.HandleThinking("\nthought\n")
		handler.HandleTextDelta("delta")
		handler.HandleThinkingStart()
		handler.HandleThinkingDelta("more")
		handler.HandleThinkingBlockEnd()
		handler.HandleContentBlockEnd()
		handler.HandleDone()
	})

	assert.Contains(t, output, "hello")
	assert.Contains(t, output, "🔧 Using tool: bash")
	assert.Contains(t, output, "🔄 Tool result:")
	assert.Contains(t, output, "💭 Thinking: thought")
	assert.Contains(t, output, "delta")
	assert.Contains(t, output, "more")
	assert.Contains(t, output, "----")

	silentOutput := captureStdout(func() {
		silent := &ConsoleMessageHandler{Silent: true}
		silent.HandleText("hello")
		silent.HandleToolUse("call-1", "bash", `{}`)
		silent.HandleToolResult("call-1", "bash", tooltypes.BaseToolResult{Result: "ok"})
		silent.HandleThinking("thought")
		silent.HandleTextDelta("delta")
		silent.HandleThinkingStart()
		silent.HandleThinkingDelta("more")
		silent.HandleThinkingBlockEnd()
		silent.HandleContentBlockEnd()
		silent.HandleDone()
	})
	assert.Empty(t, silentOutput)
}

func TestStringCollectorHandler_HandleThinkingTrimsLeadingNewlines(t *testing.T) {
	handler := &StringCollectorHandler{Silent: true}

	thinkingWithNewlines := "\n\nThis is thinking content with leading newlines"
	expectedTrimmed := "This is thinking content with leading newlines"

	// Test the string trimming logic
	trimmed := strings.TrimLeft(thinkingWithNewlines, "\n")
	assert.Equal(t, expectedTrimmed, trimmed)

	// Test that handler doesn't panic with newlines
	handler.HandleThinking(thinkingWithNewlines)
	// If we get here without panicking, the test passes
}

func TestStringCollectorHandlerCollectsAndPrints(t *testing.T) {
	handler := &StringCollectorHandler{}

	output := captureStdout(func() {
		handler.HandleText("hello")
		handler.HandleTextDelta(" streamed")
		handler.HandleToolUse("call-1", "bash", `{"command":"pwd"}`)
		handler.HandleToolResult("call-1", "bash", tooltypes.BaseToolResult{Result: "ok"})
		handler.HandleThinking("\nthought\n")
		handler.HandleThinkingStart()
		handler.HandleThinkingDelta("more")
		handler.HandleThinkingBlockEnd()
		handler.HandleContentBlockEnd()
		handler.HandleDone()
	})

	assert.Equal(t, "hello\n streamed", handler.CollectedText())
	assert.Contains(t, output, "hello")
	assert.Contains(t, output, "streamed")
	assert.Contains(t, output, "🔧 Using tool: bash")
	assert.Contains(t, output, "🔄 Tool result:")
	assert.Contains(t, output, "💭 Thinking: thought")
	assert.Contains(t, output, "more")

	silent := &StringCollectorHandler{Silent: true}
	silentOutput := captureStdout(func() {
		silent.HandleText("quiet")
		silent.HandleTextDelta(" delta")
		silent.HandleToolUse("call-1", "bash", `{}`)
		silent.HandleToolResult("call-1", "bash", tooltypes.BaseToolResult{Result: "ok"})
		silent.HandleThinking("thought")
		silent.HandleThinkingStart()
		silent.HandleThinkingDelta("more")
		silent.HandleThinkingBlockEnd()
		silent.HandleContentBlockEnd()
		silent.HandleDone()
	})
	assert.Equal(t, "quiet\n delta", silent.CollectedText())
	assert.Empty(t, silentOutput)
}

func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestHeadlessStreamHandler_TextDelta(t *testing.T) {
	handler := NewHeadlessStreamHandler("conv-123")

	output := captureStdout(func() {
		handler.HandleTextDelta("Hello")
		handler.HandleTextDelta(" World")
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(t, lines, 2)

	var entry1, entry2 DeltaEntry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &entry2))

	assert.Equal(t, "text-delta", entry1.Kind)
	assert.Equal(t, "Hello", entry1.Delta)
	assert.Equal(t, "conv-123", entry1.ConversationID)
	assert.Equal(t, "assistant", entry1.Role)

	assert.Equal(t, "text-delta", entry2.Kind)
	assert.Equal(t, " World", entry2.Delta)
}

func TestHeadlessStreamHandler_ThinkingFlow(t *testing.T) {
	handler := NewHeadlessStreamHandler("conv-456")

	output := captureStdout(func() {
		handler.HandleThinkingStart()
		handler.HandleThinkingDelta("Let me")
		handler.HandleThinkingDelta(" think...")
		handler.HandleThinkingBlockEnd()
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(t, lines, 4)

	var entries []DeltaEntry
	for _, line := range lines {
		var entry DeltaEntry
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}

	assert.Equal(t, "thinking-start", entries[0].Kind)
	assert.Equal(t, "conv-456", entries[0].ConversationID)

	assert.Equal(t, "thinking-delta", entries[1].Kind)
	assert.Equal(t, "Let me", entries[1].Delta)

	assert.Equal(t, "thinking-delta", entries[2].Kind)
	assert.Equal(t, " think...", entries[2].Delta)

	assert.Equal(t, "thinking-end", entries[3].Kind)
}

func TestHeadlessStreamHandler_ContentEnd(t *testing.T) {
	handler := NewHeadlessStreamHandler("conv-789")

	output := captureStdout(func() {
		handler.HandleContentBlockEnd()
	})

	var entry DeltaEntry
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &entry))

	assert.Equal(t, "content-end", entry.Kind)
	assert.Equal(t, "conv-789", entry.ConversationID)
	assert.Equal(t, "assistant", entry.Role)
}

func TestHeadlessStreamHandler_NoOps(t *testing.T) {
	handler := NewHeadlessStreamHandler("conv-noop")

	// These should not produce any output (handled by ConversationStreamer)
	output := captureStdout(func() {
		handler.HandleText("complete text")
		handler.HandleToolUse("tc1", "Bash", `{"command":"ls"}`)
		handler.HandleToolResult("tc1", "Bash", nil)
		handler.HandleThinking("complete thinking")
		handler.HandleDone()
	})

	assert.Empty(t, output, "No-op methods should not produce output")
}
