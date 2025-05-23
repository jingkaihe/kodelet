package openai

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestNewOpenAIThread(t *testing.T) {
	// Test with default values
	config := llm.Config{}
	thread := NewOpenAIThread(config)

	assert.Equal(t, "gpt-4.1", thread.config.Model)
	assert.Equal(t, 8192, thread.config.MaxTokens)
	assert.Equal(t, "medium", thread.reasoningEffort)

	// Test with custom values
	config = llm.Config{
		Model:           "gpt-4o",
		MaxTokens:       4096,
		ReasoningEffort: "high",
	}
	thread = NewOpenAIThread(config)

	assert.Equal(t, "gpt-4o", thread.config.Model)
	assert.Equal(t, 4096, thread.config.MaxTokens)
	assert.Equal(t, "high", thread.reasoningEffort)
}

func TestGetModelPricing(t *testing.T) {
	// Test exact matches
	pricing := getModelPricing("gpt-4.1")
	assert.Equal(t, 0.000002, pricing.Input)
	assert.Equal(t, 0.000008, pricing.Output)
	assert.Equal(t, 1047576, pricing.ContextWindow)

	// Test fuzzy matches
	pricing = getModelPricing("gpt-4.1-preview")
	assert.Equal(t, 0.000002, pricing.Input) // Should match gpt-4.1

	pricing = getModelPricing("gpt-4.1-mini-preview")
	assert.Equal(t, 0.0000004, pricing.Input) // Should match gpt-4.1-mini

	pricing = getModelPricing("gpt-4o-latest")
	assert.Equal(t, 0.0000025, pricing.Input) // Should match gpt-4o

	// Test unknown model
	pricing = getModelPricing("unknown-model")
	assert.Equal(t, 0.000002, pricing.Input) // Should default to gpt-4.1
}

func TestExtractMessages(t *testing.T) {
	// Simple test case with a few messages
	messagesJSON := `[
		{"role": "system", "content": "You are a helpful AI assistant."},
		{"role": "user", "content": "Hello!"},
		{"role": "assistant", "content": "Hi there! How can I help you today?"},
		{"role": "user", "content": "Can you help me with a project?"},
		{"role": "assistant", "content": "Of course! What kind of project are you working on?"}
	]`

	messages, err := ExtractMessages([]byte(messagesJSON))
	assert.NoError(t, err)
	assert.Len(t, messages, 4) // System message should be filtered out

	// Check first user message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello!", messages[0].Content)

	// Check first assistant message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi there! How can I help you today?", messages[1].Content)

	// Test with tool calls
	messagesWithToolsJSON := `[
		{"role": "system", "content": "You are a helpful AI assistant."},
		{"role": "user", "content": "What time is it?"},
		{"role": "assistant", "content": "", "tool_calls": [{"id": "call_123", "function": {"name": "get_time", "arguments": "{}"}}]},
		{"role": "tool", "content": "10:30 AM", "tool_call_id": "call_123"},
		{"role": "assistant", "content": "The current time is 10:30 AM."}
	]`

	messages, err = ExtractMessages([]byte(messagesWithToolsJSON))
	assert.NoError(t, err)
	assert.Len(t, messages, 4) // System message should be filtered out

	// Check that tool calls are properly serialized
	toolCallMessage := messages[1]
	assert.Equal(t, "assistant", toolCallMessage.Role)
	assert.Contains(t, toolCallMessage.Content, "get_time") // The content should contain the serialized tool call
}
