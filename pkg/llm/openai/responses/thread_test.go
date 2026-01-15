package responses

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewThread(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config, nil)
	require.NoError(t, err)
	require.NotNil(t, thread)
	assert.Equal(t, "openai-responses", thread.Provider())
}

func TestNewThreadWithCustomAPIKey(t *testing.T) {
	os.Setenv("MY_CUSTOM_API_KEY", "test-key")
	defer os.Unsetenv("MY_CUSTOM_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			APIKeyEnvVar: "MY_CUSTOM_API_KEY",
		},
	}

	thread, err := NewThread(config, nil)
	require.NoError(t, err)
	require.NotNil(t, thread)
}

func TestNewThreadWithoutAPIKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	_, err := NewThread(config, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENAI_API_KEY")
}

func TestIsReasoningModelDynamic(t *testing.T) {
	// Create a thread with the default OpenAI preset loaded
	thread := &Thread{
		customModels: map[string]string{
			"o1":      "reasoning",
			"o1-mini": "reasoning",
			"o3":      "reasoning",
			"o3-mini": "reasoning",
			"o4-mini": "reasoning",
			"gpt-5":   "reasoning",
			"gpt-4.1": "non-reasoning",
			"gpt-4o":  "non-reasoning",
		},
	}

	tests := []struct {
		model    string
		expected bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"gpt-5", true},
		{"gpt-4.1", false},
		{"gpt-4o", false},
		{"claude-3", false}, // Not in preset, returns false
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, thread.isReasoningModelDynamic(tt.model))
		})
	}
}

func TestExtractMessages(t *testing.T) {
	// Create sample input items in JSON format
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello, world!"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "Hi there!"
		}
	]`

	messages, err := ExtractMessages([]byte(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, messages, 2)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello, world!", messages[0].Content)
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi there!", messages[1].Content)
}

func TestExtractMessagesWithToolResults(t *testing.T) {
	// Create sample input items with function call and result
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "What files are in the directory?"
		},
		{
			"type": "function_call",
			"call_id": "call_123",
			"name": "list_files",
			"arguments": "{\"path\": \"/tmp\"}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_123",
			"output": "file1.txt\nfile2.txt"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "The directory contains file1.txt and file2.txt."
		}
	]`

	// Add tool results map
	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_123": {
			ToolName: "list_files",
			Success:  true,
		},
	}

	messages, err := ExtractMessages([]byte(inputItems), toolResults)
	require.NoError(t, err)
	require.Len(t, messages, 4)

	assert.Equal(t, "user", messages[0].Role)
	assert.Contains(t, messages[1].Content, "list_files")
	assert.Contains(t, messages[2].Content, "Tool result")
	assert.Equal(t, "assistant", messages[3].Role)
}

func TestStreamMessages(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello"
		},
		{
			"type": "function_call",
			"call_id": "call_123",
			"name": "test_tool",
			"arguments": "{}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_123",
			"output": "result"
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 3)

	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "user", streamable[0].Role)

	assert.Equal(t, "tool-use", streamable[1].Kind)
	assert.Equal(t, "test_tool", streamable[1].ToolName)

	assert.Equal(t, "tool-result", streamable[2].Kind)
}

func TestExtractMessagesWithReasoning(t *testing.T) {
	// Create sample input items with reasoning as a separate item
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "What is 2+2?"
		},
		{
			"type": "reasoning",
			"role": "assistant",
			"content": "I need to add 2 and 2 together. 2+2=4."
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "The answer is 4."
		}
	]`

	messages, err := ExtractMessages([]byte(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, messages, 3) // user + thinking + assistant

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "What is 2+2?", messages[0].Content)

	// Thinking message should come before the assistant message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Contains(t, messages[1].Content, "Thinking")
	assert.Contains(t, messages[1].Content, "I need to add 2 and 2 together")

	assert.Equal(t, "assistant", messages[2].Role)
	assert.Equal(t, "The answer is 4.", messages[2].Content)
}

func TestStreamMessagesWithReasoning(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello"
		},
		{
			"type": "reasoning",
			"role": "assistant",
			"content": "The user greeted me, I should respond politely."
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "Hi there!"
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 3) // user + thinking + text

	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "user", streamable[0].Role)

	assert.Equal(t, "thinking", streamable[1].Kind)
	assert.Equal(t, "assistant", streamable[1].Role)
	assert.Equal(t, "The user greeted me, I should respond politely.", streamable[1].Content)

	assert.Equal(t, "text", streamable[2].Kind)
	assert.Equal(t, "assistant", streamable[2].Role)
	assert.Equal(t, "Hi there!", streamable[2].Content)
}

func TestStorageRoundTripWithReasoning(t *testing.T) {
	// Create stored items directly (simulating what happens during streaming)
	// Items are stored in order: user message -> reasoning -> assistant message
	storedItems := []StoredInputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: "What is 2+2?",
		},
		{
			Type:    "reasoning",
			Role:    "assistant",
			Content: "I need to add 2 and 2 together. 2+2=4.",
		},
		{
			Type:    "message",
			Role:    "assistant",
			Content: "The answer is 4.",
		},
	}

	// Verify stored format has reasoning as a separate item
	require.Len(t, storedItems, 3)
	assert.Equal(t, "message", storedItems[0].Type)
	assert.Equal(t, "reasoning", storedItems[1].Type)
	assert.Equal(t, "I need to add 2 and 2 together. 2+2=4.", storedItems[1].Content)
	assert.Equal(t, "message", storedItems[2].Type)

	// Convert to SDK format - reasoning is skipped (only for display)
	restoredItems := fromStoredItems(storedItems)

	// Verify restored items (2 SDK items, reasoning is skipped for API calls)
	require.Len(t, restoredItems, 2)
	assert.NotNil(t, restoredItems[0].OfMessage)
	assert.Equal(t, openairesponses.EasyInputMessageRoleUser, restoredItems[0].OfMessage.Role)
	assert.NotNil(t, restoredItems[1].OfMessage)
	assert.Equal(t, openairesponses.EasyInputMessageRoleAssistant, restoredItems[1].OfMessage.Role)

	// Verify JSON round-trip preserves reasoning for display
	jsonData, err := json.Marshal(storedItems)
	require.NoError(t, err)

	var parsedItems []StoredInputItem
	err = json.Unmarshal(jsonData, &parsedItems)
	require.NoError(t, err)

	require.Len(t, parsedItems, 3)
	assert.Equal(t, "reasoning", parsedItems[1].Type)
	assert.Equal(t, "I need to add 2 and 2 together. 2+2=4.", parsedItems[1].Content)
}

func TestLoadCustomConfiguration(t *testing.T) {
	config := llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{
			Models: &llmtypes.CustomModels{
				Reasoning:    []string{"custom-o1", "custom-o3"},
				NonReasoning: []string{"custom-gpt"},
			},
			Pricing: map[string]llmtypes.ModelPricing{
				"custom-model": {
					Input:         0.001,
					Output:        0.002,
					ContextWindow: 128000,
				},
			},
		},
	}

	customModels, customPricing := loadCustomConfiguration(config)

	assert.Equal(t, "reasoning", customModels["custom-o1"])
	assert.Equal(t, "reasoning", customModels["custom-o3"])
	assert.Equal(t, "non-reasoning", customModels["custom-gpt"])

	pricing, ok := customPricing["custom-model"]
	require.True(t, ok)
	assert.Equal(t, 0.001, pricing.Input)
	assert.Equal(t, 0.002, pricing.Output)
	assert.Equal(t, 128000, pricing.ContextWindow)
}

func TestLoadCustomConfigurationDefaultPreset(t *testing.T) {
	// When no config is provided, the default "openai" preset should be loaded
	config := llmtypes.Config{}

	customModels, customPricing := loadCustomConfiguration(config)

	// Should load the default OpenAI preset
	assert.NotEmpty(t, customModels)
	assert.NotEmpty(t, customPricing)

	// Verify some known OpenAI models are present
	assert.Equal(t, "reasoning", customModels["o1"])
	assert.Equal(t, "reasoning", customModels["o3"])
	assert.Equal(t, "non-reasoning", customModels["gpt-4o"])

	// Verify pricing is loaded
	_, hasGPT4o := customPricing["gpt-4o"]
	assert.True(t, hasGPT4o, "gpt-4o pricing should be present")
}

func TestIsInvalidPreviousResponseIDError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "generic error",
			err:      assert.AnError,
			expected: false,
		},
		{
			name:     "previous_response_id error",
			err:      errors.New("invalid previous_response_id: response not found"),
			expected: true,
		},
		{
			name:     "response not found error",
			err:      errors.New("response not found for the given ID"),
			expected: true,
		},
		{
			name:     "invalid response id error",
			err:      errors.New("invalid response id provided"),
			expected: true,
		},
		{
			name:     "no response found error",
			err:      errors.New("no response found"),
			expected: true,
		},
		{
			name:     "case insensitive match",
			err:      errors.New("PREVIOUS_RESPONSE_ID is invalid"),
			expected: true,
		},
		{
			name:     "unrelated 404 error",
			err:      errors.New("resource not found"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isInvalidPreviousResponseIDError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAddUserMessageUpdatesPendingItems(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config, nil)
	require.NoError(t, err)

	// Initially, both slices should be empty
	assert.Empty(t, thread.inputItems)
	assert.Empty(t, thread.pendingItems)

	// Add a user message
	ctx := context.Background()
	thread.AddUserMessage(ctx, "Hello, world!")

	// Both slices should now have one item
	assert.Len(t, thread.inputItems, 1)
	assert.Len(t, thread.pendingItems, 1)

	// Add another message
	thread.AddUserMessage(ctx, "How are you?")

	// Both slices should now have two items
	assert.Len(t, thread.inputItems, 2)
	assert.Len(t, thread.pendingItems, 2)
}

func TestThreadPendingItemsInitialization(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config, nil)
	require.NoError(t, err)

	// Verify pendingItems is initialized (not nil)
	assert.NotNil(t, thread.pendingItems)
	assert.NotNil(t, thread.inputItems)

	// Verify lastResponseID is initially empty
	assert.Empty(t, thread.lastResponseID)
}

// Integration tests that use the real OpenAI API
// These tests are skipped if OPENAI_API_KEY is not set to a valid key

func skipIfNoAPIKey(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "test-key" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set or is test-key")
	}
}

func TestIntegration_ShortSummary(t *testing.T) {
	skipIfNoAPIKey(t)

	ctx := context.Background()

	// Use a cheap model for testing
	config := llmtypes.Config{
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		WeakModel: "gpt-4.1-mini",
		MaxTokens: 1024,
	}

	thread, err := NewThread(config, nil)
	require.NoError(t, err)

	// Add some conversation history
	thread.AddUserMessage(ctx, "I want to refactor the authentication module in my Go application.")
	thread.AddUserMessage(ctx, "The current implementation uses JWT tokens but I want to switch to OAuth2.")
	thread.AddUserMessage(ctx, "Can you help me plan this migration?")

	// Generate a short summary
	summary := thread.ShortSummary(ctx)

	t.Logf("Generated summary: %s", summary)

	// Verify the summary is not empty and is reasonably short
	assert.NotEmpty(t, summary)
	assert.NotEqual(t, "Could not generate summary.", summary)

	// Summary should be concise (the prompt asks for <= 12 words)
	words := len(splitWords(summary))
	assert.LessOrEqual(t, words, 20, "Summary should be concise, got %d words: %s", words, summary)
}

// splitWords is a simple helper to count words in a string
func splitWords(s string) []string {
	var words []string
	var current []rune
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if len(current) > 0 {
				words = append(words, string(current))
				current = nil
			}
		} else {
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}
