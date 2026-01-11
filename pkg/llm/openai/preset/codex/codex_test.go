package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBasePrompt(t *testing.T) {
	prompt, err := GetBasePrompt()
	require.NoError(t, err)
	assert.NotEmpty(t, prompt, "base prompt should be loaded")
}

func TestGetGPT52Prompt(t *testing.T) {
	prompt, err := GetGPT52Prompt()
	require.NoError(t, err)
	assert.NotEmpty(t, prompt, "GPT-5.2 prompt should be loaded")
}

func TestGetGPT51CodexMaxPrompt(t *testing.T) {
	prompt, err := GetGPT51CodexMaxPrompt()
	require.NoError(t, err)
	assert.NotEmpty(t, prompt, "GPT-5.1 Codex Max prompt should be loaded")
}

func TestGetSystemPromptForModel(t *testing.T) {
	tests := []struct {
		model    string
		wantFunc func() (string, error)
	}{
		{"gpt-5.2-codex", GetGPT52Prompt},
		{"gpt-5.2", GetGPT52Prompt},
		{"gpt-5.1-codex-max", GetGPT51CodexMaxPrompt},
		{"gpt-5.1-codex-mini", GetBasePrompt},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, err := GetSystemPromptForModel(tt.model)
			require.NoError(t, err)

			want, err := tt.wantFunc()
			require.NoError(t, err)

			assert.Equal(t, want, got)
		})
	}
}

func TestPromptCaching(t *testing.T) {
	// First call loads from embed
	prompt1, err := GetBasePrompt()
	require.NoError(t, err)

	// Second call should return cached value
	prompt2, err := GetBasePrompt()
	require.NoError(t, err)

	assert.Equal(t, prompt1, prompt2)
}
