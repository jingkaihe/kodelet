package openai

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm/openai/responses"
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
			name: "GPT-6 Astra forces responses",
			config: llmtypes.Config{
				Model: "gpt-6-astra",
			},
			want: true,
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

func TestNewThreadRejectsGPT6AstraFlexTier(t *testing.T) {
	for _, platform := range []string{"openai", "codex"} {
		for _, model := range []string{"gpt-6-astra", ""} {
			t.Run(platform+"/model="+model, func(t *testing.T) {
				thread, err := NewThread(llmtypes.Config{
					Provider: "openai",
					Model:    model,
					OpenAI: &llmtypes.OpenAIConfig{
						Platform:    platform,
						ServiceTier: llmtypes.OpenAIServiceTierFlex,
					},
				})
				require.ErrorContains(t, err, "flex is not supported for gpt-6-astra")
				assert.Nil(t, thread)
			})
		}
	}
}

func TestNewThreadModelDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("KODELET_OPENAI_API_MODE", "chat_completions")
	for _, platform := range []string{"", "openai", "codex"} {
		t.Run("platform="+platform, func(t *testing.T) {
			config := llmtypes.Config{}
			if platform != "" {
				config.OpenAI = &llmtypes.OpenAIConfig{Platform: platform}
			}
			thread, err := NewThread(config)
			require.NoError(t, err)
			require.IsType(t, &responses.Thread{}, thread)
			t.Cleanup(func() { require.NoError(t, thread.(*responses.Thread).Close()) })
			assert.Equal(t, "gpt-6-astra", thread.GetConfig().Model)
			assert.Empty(t, config.Model, "defaulting must not mutate the caller's config")
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
	assert.False(t, RecordUsesResponsesMode(map[string]any{"api_mode": 123}, []byte(`not-json`)))
	assert.False(t, RecordUsesResponsesMode(nil, []byte(`[]`)))
}

func TestResponsesMessageWrappers(t *testing.T) {
	raw := []byte(`[
		{"type":"message","role":"user","content":"hi"},
		{"type":"reasoning","content":"thinking"},
		{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call-1","output":"done"}
	]`)

	messages, err := ExtractResponsesMessages(raw, nil)
	require.NoError(t, err)
	require.NotEmpty(t, messages)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "hi", messages[0].Content)

	streamable, err := StreamResponsesMessages(raw, nil)
	require.NoError(t, err)
	require.Len(t, streamable, 4)
	assert.Equal(t, "tool-use", streamable[2].Kind)
	assert.Equal(t, "lookup", streamable[2].ToolName)
	assert.Equal(t, "tool-result", streamable[3].Kind)
}
