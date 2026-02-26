package openai

import (
	"encoding/json"
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
		envMode  string
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
			name: "config with APIMode responses",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					APIMode: llmtypes.OpenAIAPIModeResponses,
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
			name:    "api mode env responses means true",
			envMode: "responses",
			config:  llmtypes.Config{},
			want:    true,
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
		{
			name: "platform codex forces responses",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "codex",
				},
			},
			want: true,
		},
		{
			name: "preset codex forces responses",
			config: llmtypes.Config{
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "codex",
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
			os.Unsetenv("KODELET_OPENAI_API_MODE")

			if tt.envValue != "" {
				os.Setenv("KODELET_OPENAI_USE_RESPONSES_API", tt.envValue)
				defer os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
			}
			if tt.envMode != "" {
				os.Setenv("KODELET_OPENAI_API_MODE", tt.envMode)
				defer os.Unsetenv("KODELET_OPENAI_API_MODE")
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

	os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
	os.Unsetenv("KODELET_OPENAI_API_MODE")

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

	assert.Equal(t, "openai", thread.Provider())
}

func TestNewThreadDispatchesToResponsesAPIViaEnvVar(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	os.Setenv("KODELET_OPENAI_USE_RESPONSES_API", "true")
	defer os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")
	os.Unsetenv("KODELET_OPENAI_API_MODE")

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

	assert.Equal(t, "openai", thread.Provider())
}

func TestNewThreadDispatchesToResponsesAPIViaApiModeEnv(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	os.Setenv("KODELET_OPENAI_API_MODE", "responses")
	defer os.Unsetenv("KODELET_OPENAI_API_MODE")
	os.Unsetenv("KODELET_OPENAI_USE_RESPONSES_API")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)

	assert.Equal(t, "openai", thread.Provider())
}

func TestRecordUsesResponsesMode(t *testing.T) {
	responsesRaw, err := json.Marshal([]map[string]any{{"type": "message", "role": "user", "content": "hi"}})
	require.NoError(t, err)
	chatRaw, err := json.Marshal([]map[string]any{{"role": "user", "content": "hi"}})
	require.NoError(t, err)

	assert.True(t, RecordUsesResponsesMode(map[string]any{"api_mode": "responses"}, chatRaw))
	assert.False(t, RecordUsesResponsesMode(map[string]any{"api_mode": "chat_completions"}, responsesRaw))
	assert.True(t, RecordUsesResponsesMode(nil, responsesRaw))
	assert.False(t, RecordUsesResponsesMode(nil, chatRaw))
}
