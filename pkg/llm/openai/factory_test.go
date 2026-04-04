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
		name    string
		config  llmtypes.Config
		envMode string
		want    bool
	}{
		{
			name:   "default returns false",
			config: llmtypes.Config{},
			want:   false,
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
			name: "nil OpenAI config",
			config: llmtypes.Config{
				OpenAI: nil,
			},
			want: false,
		},
		{
			name:    "api mode env responses means true",
			envMode: "responses",
			config:  llmtypes.Config{},
			want:    true,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("KODELET_OPENAI_API_MODE")

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

	os.Unsetenv("KODELET_OPENAI_API_MODE")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
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
			APIMode: llmtypes.OpenAIAPIModeResponses,
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
