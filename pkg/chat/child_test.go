package chat

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildConfigurationCeiling(t *testing.T) {
	parent := llmtypes.Config{
		Provider: "openai", Model: "gpt-4o", WeakModel: "gpt-4o-mini", MaxTokens: 4096, WeakModelMaxTokens: 2048, ReasoningEffort: "medium",
		ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool", "search"}), AllowedCommands: new([]string{}), MaxTurns: new(8), NoSkills: new(true), EnableFSSearchTools: new(true)},
	}
	preset := &llmtypes.ExecutionOptions{Model: new("gpt-4o-mini"), AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), NoExtensions: new(true), NoSkills: new(true), MaxTurns: new(3)}
	config, err := childConfiguration(parent, preset, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o-mini", config.Model)
	assert.Equal(t, []string{"file_read", "grep_tool", "glob_tool"}, *config.ExecutionOptions.AllowedTools)
	assert.True(t, *config.ExecutionOptions.NoExtensions)
	assert.Equal(t, 8, *parent.ExecutionOptions.MaxTurns)
	for _, options := range []*llmtypes.ExecutionOptions{
		{Provider: new("anthropic")},
		{Model: new("unconfigured-private-model")},
		{MaxTokens: new(4097)},
		{MaxTurns: new(9)},
		{MaxTurns: new(0)},
		{NoExtensions: new(false)},
		{NoSkills: new(false)},
		{AllowedTools: new([]string{"bash"})},
		{AllowedCommands: new([]string{"*"})},
	} {
		_, err := childConfiguration(parent, preset, options)
		assert.Error(t, err, "%+v", options)
	}
	config, err = childConfiguration(parent, preset, &llmtypes.ExecutionOptions{AllowedTools: new([]string{}), MaxTurns: new(1)})
	require.NoError(t, err)
	assert.True(t, config.ExecutionOptions.ToolsDisabled())
}

func TestChildPresetResumeKeepsCeiling(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4o-mini", MaxTokens: 256, ReasoningEffort: "medium"}
	preset := childPresetSnapshot{Name: "code_search", SystemPrompt: "pinned prompt", Options: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), NoExtensions: new(true), NoSkills: new(true), MaxTurns: new(3)}}
	for _, requested := range []*llmtypes.ExecutionOptions{nil, {AllowedTools: new([]string{"file_read"})}, {NoTools: new(true)}} {
		resumed, err := applyChildPresetSnapshot(config, requested, preset)
		require.NoError(t, err)
		assert.True(t, *resumed.ExecutionOptions.NoExtensions)
		assert.True(t, *resumed.ExecutionOptions.NoSkills)
		assert.Equal(t, 3, *resumed.ExecutionOptions.MaxTurns)
	}
	for _, requested := range []*llmtypes.ExecutionOptions{{NoExtensions: new(false)}, {NoSkills: new(false)}, {AllowedTools: new([]string{"bash"})}, {MaxTurns: new(0)}, {MaxTurns: new(4)}} {
		_, err := applyChildPresetSnapshot(config, requested, preset)
		require.Error(t, err)
	}
	assert.Equal(t, []string{"file_read", "grep_tool", "glob_tool"}, *preset.Options.AllowedTools)
}
