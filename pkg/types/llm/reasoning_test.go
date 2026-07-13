package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeReasoningConfig(t *testing.T) {
	config := Config{
		ReasoningEffort:         " HIGH ",
		AllowedReasoningEfforts: []string{" low ", "HIGH", "low", ""},
	}

	require.NoError(t, NormalizeReasoningConfig(&config))
	assert.Equal(t, "high", config.ReasoningEffort)
	assert.Equal(t, []string{"low", "high"}, config.AllowedReasoningEfforts)
}

func TestNormalizeReasoningConfigDefaultsAndValidatesPolicy(t *testing.T) {
	config := Config{}
	require.NoError(t, NormalizeReasoningConfig(&config))
	assert.Equal(t, DefaultReasoningEffort, config.ReasoningEffort)
	assert.Equal(t, []string{DefaultReasoningEffort}, ReasoningEffortOptions(config))

	config = Config{ReasoningEffort: "max", AllowedReasoningEfforts: []string{"low", "high"}}
	err := NormalizeReasoningConfig(&config)
	require.ErrorContains(t, err, "not included")

	config = Config{ReasoningEffort: "banana"}
	err = NormalizeReasoningConfig(&config)
	require.ErrorContains(t, err, "invalid reasoning_effort")
}

func TestReasoningEffortOptionsUsesProviderDefaultsWithoutPolicy(t *testing.T) {
	openAI := Config{Provider: "openai", ReasoningEffort: "medium"}
	assert.Equal(t, []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, ReasoningEffortOptions(openAI))

	anthropic := Config{Provider: "anthropic", ReasoningEffort: "medium"}
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, ReasoningEffortOptions(anthropic))

	anthropic.ReasoningEffort = "minimal"
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, ReasoningEffortOptions(anthropic))

	unknown := Config{Provider: "custom", ReasoningEffort: "high"}
	assert.Equal(t, []string{"high"}, ReasoningEffortOptions(unknown))

	restricted := Config{Provider: "openai", ReasoningEffort: "high", AllowedReasoningEfforts: []string{"low", "high"}}
	assert.Equal(t, []string{"low", "high"}, ReasoningEffortOptions(restricted))
}

func TestConversationConfigSnapshotApplyPreservesLivePolicy(t *testing.T) {
	config := Config{
		Profile:                 "work",
		Provider:                "openai",
		Model:                   "gpt-snapshot",
		WeakModel:               "gpt-weak",
		MaxTokens:               32000,
		WeakModelMaxTokens:      4000,
		ThinkingBudgetTokens:    1234,
		ReasoningEffort:         "high",
		AllowedReasoningEfforts: []string{"medium", "high"},
		CompactRatio:            0.7,
		OpenAI: &OpenAIConfig{
			Platform:      "codex",
			BaseURL:       "https://do-not-snapshot.example",
			APIKeyEnvVar:  "SECRET_ENV",
			APIMode:       OpenAIAPIModeResponses,
			TextVerbosity: " HIGH ",
			ServiceTier:   OpenAIServiceTierFast,
			ManualCache:   true,
		},
		AllowedTools: []string{"bash"},
	}

	snapshot, err := NewConversationConfigSnapshot(config)
	require.NoError(t, err)
	assert.Equal(t, OpenAITextVerbosity(" HIGH "), config.OpenAI.TextVerbosity)
	assert.Equal(t, ConversationConfigSnapshotVersion, snapshot.Version)
	assert.Equal(t, "high", snapshot.ReasoningEffort)
	require.NotNil(t, snapshot.OpenAI)
	assert.Equal(t, OpenAIAPIModeResponses, snapshot.OpenAI.APIMode)
	assert.Equal(t, OpenAITextVerbosityHigh, snapshot.OpenAI.TextVerbosity)
	rawSnapshot, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.Contains(t, string(rawSnapshot), `"text_verbosity":"high"`)
	assert.NotContains(t, string(rawSnapshot), "do-not-snapshot.example")
	assert.NotContains(t, string(rawSnapshot), "SECRET_ENV")

	live := Config{
		Provider:                "anthropic",
		Model:                   "changed",
		ReasoningEffort:         "low",
		AllowedReasoningEfforts: []string{"low"},
		AllowedTools:            []string{"file_read"},
		Aliases: map[string]string{
			"gpt-snapshot": "remapped-main",
			"gpt-weak":     "remapped-weak",
		},
		OpenAI: &OpenAIConfig{
			BaseURL:      "https://live.example",
			APIKeyEnvVar: "LIVE_ENV",
		},
		Anthropic: &AnthropicConfig{Platform: "unused"},
	}
	applied, err := snapshot.Apply(live)
	require.NoError(t, err)
	assert.Equal(t, "openai", applied.Provider)
	assert.Equal(t, "gpt-snapshot", applied.Model)
	assert.Equal(t, "high", applied.ReasoningEffort)
	assert.Empty(t, applied.AllowedReasoningEfforts)
	assert.Empty(t, applied.Aliases)
	assert.True(t, applied.ModelAliasesResolved)
	assert.Equal(t, []string{"file_read"}, applied.AllowedTools)
	require.NotNil(t, applied.OpenAI)
	assert.Nil(t, applied.Anthropic)
	assert.Equal(t, "https://live.example", applied.OpenAI.BaseURL)
	assert.Equal(t, "LIVE_ENV", applied.OpenAI.APIKeyEnvVar)
	assert.Equal(t, OpenAITextVerbosityHigh, applied.OpenAI.TextVerbosity)
}

func TestConversationConfigSnapshotCapturesProviderDefaults(t *testing.T) {
	snapshot, err := NewConversationConfigSnapshot(Config{
		Provider:        "anthropic",
		Model:           "claude-test",
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	require.NotNil(t, snapshot.Anthropic)

	applied, err := snapshot.Apply(Config{
		Anthropic: &AnthropicConfig{
			Platform:         "copilot",
			BaseURL:          "https://live.example",
			AdaptiveThinking: true,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, applied.Anthropic)
	assert.Empty(t, applied.Anthropic.Platform)
	assert.False(t, applied.Anthropic.AdaptiveThinking)
	assert.Equal(t, "https://live.example", applied.Anthropic.BaseURL)
}

func TestConversationConfigSnapshotPreservesUnsetOpenAITextVerbosity(t *testing.T) {
	snapshot, err := NewConversationConfigSnapshot(Config{
		Provider:        "openai",
		Model:           "gpt-test",
		ReasoningEffort: "medium",
	})
	require.NoError(t, err)
	require.NotNil(t, snapshot.OpenAI)
	assert.Empty(t, snapshot.OpenAI.TextVerbosity)

	rawSnapshot, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(rawSnapshot), "text_verbosity")
}
