package tui

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestFormatMessage(t *testing.T) {
	formatter := NewMessageFormatter(100)

	tests := []struct {
		name            string
		message         llmtypes.Message
		expectedRole    string
		expectedContent string
	}{
		{
			name: "user message",
			message: llmtypes.Message{
				Role:    "user",
				Content: "Hello, world!",
			},
			expectedRole:    "You",
			expectedContent: "Hello, world!",
		},
		{
			name: "assistant message",
			message: llmtypes.Message{
				Role:    "assistant",
				Content: "Hello back!",
			},
			expectedRole:    "Assistant",
			expectedContent: "Hello back!",
		},
		{
			name: "system message",
			message: llmtypes.Message{
				Role:    "",
				Content: "System notification",
			},
			expectedRole:    "",
			expectedContent: "System notification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatter.FormatMessage(tt.message)
			// Check that content is present
			assert.Contains(t, result, tt.expectedContent)
			// Check that role is present (if not system message)
			if tt.expectedRole != "" {
				assert.Contains(t, result, tt.expectedRole)
			}
		})
	}
}

func TestFormatAssistantEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    llmtypes.MessageEvent
		expected string
	}{
		{
			name: "text event",
			event: llmtypes.MessageEvent{
				Type:    llmtypes.EventTypeText,
				Content: "Some text",
				Done:    false,
			},
			expected: "Some text",
		},
		{
			name: "tool use event",
			event: llmtypes.MessageEvent{
				Type:    llmtypes.EventTypeToolUse,
				Content: "bash",
				Done:    false,
			},
			expected: "🔧 Using tool: bash",
		},
		{
			name: "tool result event",
			event: llmtypes.MessageEvent{
				Type:    llmtypes.EventTypeToolResult,
				Content: "result data",
				Done:    false,
			},
			expected: "🔄 Tool result: result data",
		},
		{
			name: "thinking event",
			event: llmtypes.MessageEvent{
				Type:    llmtypes.EventTypeThinking,
				Content: "Let me think...",
				Done:    false,
			},
			expected: "💭 Thinking: Let me think...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatAssistantEvent(tt.event)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatMessages(t *testing.T) {
	formatter := NewMessageFormatter(100)

	messages := []llmtypes.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "", Content: "System message"},
	}

	result := formatter.FormatMessages(messages)

	// Check that all messages are present in the output
	assert.Contains(t, result, "You")
	assert.Contains(t, result, "Hello")
	assert.Contains(t, result, "Assistant")
	assert.Contains(t, result, "Hi there")
	assert.Contains(t, result, "System message")
}
