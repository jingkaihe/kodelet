package llm

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConversationStreamer(t *testing.T) {
	ctx := context.Background()

	// Create a new conversation streamer
	streamer, closeFunc, err := NewConversationStreamer(ctx)
	require.NoError(t, err)
	require.NotNil(t, streamer)
	require.NotNil(t, closeFunc)

	// Verify it has the expected parsers registered
	// (We can't directly test the internal registry, but we can verify the streamer was created)

	// Clean up
	err = closeFunc()
	assert.NoError(t, err)
}

func TestConvertAnthropicStreamableMessages(t *testing.T) {
	// Test empty slice
	result := convertAnthropicStreamableMessages(nil)
	assert.Equal(t, 0, len(result))

	// Test conversion with sample data
	// This verifies the conversion logic works correctly
	input := []anthropic.StreamableMessage{
		{
			Kind:       "text",
			Role:       "user",
			Content:    "Hello",
			ToolName:   "",
			ToolCallID: "",
			Input:      "",
		},
		{
			Kind:       "tool-use",
			Role:       "assistant",
			Content:    "",
			ToolName:   "bash",
			ToolCallID: "call-123",
			Input:      `{"command": "ls"}`,
		},
	}

	result = convertAnthropicStreamableMessages(input)
	require.Equal(t, 2, len(result))

	// Check first message
	assert.Equal(t, "text", result[0].Kind)
	assert.Equal(t, "user", result[0].Role)
	assert.Equal(t, "Hello", result[0].Content)

	// Check second message
	assert.Equal(t, "tool-use", result[1].Kind)
	assert.Equal(t, "assistant", result[1].Role)
	assert.Equal(t, "bash", result[1].ToolName)
	assert.Equal(t, "call-123", result[1].ToolCallID)
	assert.Equal(t, `{"command": "ls"}`, result[1].Input)
}

func TestConvertOpenAIStreamableMessages(t *testing.T) {
	// Test empty slice
	result := convertOpenAIStreamableMessages(nil)
	assert.Equal(t, 0, len(result))

	// Test conversion with sample data
	input := []openai.StreamableMessage{
		{
			Kind:       "text",
			Role:       "user",
			Content:    "Hello",
			ToolName:   "",
			ToolCallID: "",
			Input:      "",
		},
		{
			Kind:       "tool-result",
			Role:       "assistant",
			Content:    "Output here",
			ToolName:   "bash",
			ToolCallID: "call-456",
			Input:      "",
		},
	}

	result = convertOpenAIStreamableMessages(input)
	require.Equal(t, 2, len(result))

	// Check first message
	assert.Equal(t, "text", result[0].Kind)
	assert.Equal(t, "user", result[0].Role)
	assert.Equal(t, "Hello", result[0].Content)

	// Check second message
	assert.Equal(t, "tool-result", result[1].Kind)
	assert.Equal(t, "assistant", result[1].Role)
	assert.Equal(t, "bash", result[1].ToolName)
	assert.Equal(t, "call-456", result[1].ToolCallID)
	assert.Equal(t, "Output here", result[1].Content)
}