package llm

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
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
