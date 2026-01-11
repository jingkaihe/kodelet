package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCodexPrompt(t *testing.T) {
	prompt, err := GetCodexPrompt()
	require.NoError(t, err)
	assert.NotEmpty(t, prompt, "codex prompt should be loaded")
}

func TestGetSystemPromptForModel(t *testing.T) {
	models := []string{
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
	}

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			got, err := GetSystemPromptForModel(model)
			require.NoError(t, err)

			want, err := GetCodexPrompt()
			require.NoError(t, err)

			assert.Equal(t, want, got)
		})
	}
}

func TestPromptCaching(t *testing.T) {
	// First call loads from embed
	prompt1, err := GetCodexPrompt()
	require.NoError(t, err)

	// Second call should return cached value
	prompt2, err := GetCodexPrompt()
	require.NoError(t, err)

	assert.Equal(t, prompt1, prompt2)
}
