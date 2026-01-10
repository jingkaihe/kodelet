package responses

import (
	"encoding/json"
	"os"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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
