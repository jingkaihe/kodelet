package openai

import (
	"os"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldUseResponsesAPI(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		envValue string
		want     bool
	}{
		{
			name:   "default returns false",
			config: llmtypes.Config{},
			want:   false,
		},
		{
			name: "config with UseResponsesAPI true",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					UseResponsesAPI: true,
				},
			},
			want: true,
		},
		{
			name: "config with UseResponsesAPI false",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					UseResponsesAPI: false,
				},
			},
			want: false,
		},
		{
			name: "nil OpenAI config",
			config: llmtypes.Config{
				OpenAI: nil,
			},
			want: false,
		},
		{
			name:     "env var true overrides config false",
			envValue: "true",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					UseResponsesAPI: false,
				},
			},
			want: true,
		},
		{
			name:     "env var false overrides config true",
			envValue: "false",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					UseResponsesAPI: true,
				},
			},
			want: false,
		},
		{
			name:     "env var 1 means true",
			envValue: "1",
			config:   llmtypes.Config{},
			want:     true,
		},
		{
			name:     "env var TRUE (uppercase) means true",
			envValue: "TRUE",
			config:   llmtypes.Config{},
			want:     true,
		},
		{
			name:     "env var 0 means false",
			envValue: "0",
			config:   llmtypes.Config{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env var before each test
			os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")

			if tt.envValue != "" {
				os.Setenv("KODELET_OPENAI_USE_RESPONSES_API", tt.envValue)
				defer os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
			}

			got := shouldUseResponsesAPI(tt.config)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewThreadDispatchesToChatCompletions(t *testing.T) {
	// Set up test API key
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Ensure Responses API is disabled
	os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			UseResponsesAPI: false,
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)

	// Should return Chat Completions thread (provider = "openai")
	assert.Equal(t, "openai", thread.Provider())
}

func TestNewThreadDispatchesToResponsesAPI(t *testing.T) {
	// Set up test API key
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Enable Responses API via config
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			UseResponsesAPI: true,
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)

	// Should return Responses API thread (provider = "openai-responses")
	assert.Equal(t, "openai-responses", thread.Provider())
}

func TestNewThreadDispatchesToResponsesAPIViaEnvVar(t *testing.T) {
	// Set up test API key
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Enable Responses API via env var
	os.Setenv("KODELET_OPENAI_USE_RESPONSES_API", "true")
	defer os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		// Config says false, but env var should override
		OpenAI: &llmtypes.OpenAIConfig{
			UseResponsesAPI: false,
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)

	// Should return Responses API thread due to env var override
	assert.Equal(t, "openai-responses", thread.Provider())
}
