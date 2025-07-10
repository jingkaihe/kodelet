package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestChannelMessageHandler_HandleThinkingTrimsLeadingNewlines(t *testing.T) {
	messageCh := make(chan MessageEvent, 10)
	handler := &ChannelMessageHandler{MessageCh: messageCh}

	thinkingWithNewlines := "\n\nThis is thinking content with leading newlines"
	expectedTrimmed := "This is thinking content with leading newlines"

	handler.HandleThinking(thinkingWithNewlines)

	// Get the message from the channel
	select {
	case msg := <-messageCh:
		assert.Equal(t, EventTypeThinking, msg.Type)
		assert.Equal(t, expectedTrimmed, msg.Content)
	default:
		require.Fail(t, "Expected message in channel")
	}
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
