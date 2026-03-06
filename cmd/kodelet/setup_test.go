package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRecommendedSetupConfigYAML_OpenAIProfilesUsePatchMode(t *testing.T) {
	var config struct {
		Profiles map[string]map[string]any `yaml:"profiles"`
	}

	err := yaml.Unmarshal([]byte(recommendedSetupConfigYAML()), &config)
	require.NoError(t, err)

	openAIProfile, ok := config.Profiles["openai"]
	require.True(t, ok)
	assert.Equal(t, "patch", openAIProfile["tool_mode"])
	assert.Equal(t, true, openAIProfile["disable_fs_search_tools"])

	openAISubagentProfile, ok := config.Profiles["openai-subagent"]
	require.True(t, ok)
	assert.Equal(t, "patch", openAISubagentProfile["tool_mode"])
	assert.Equal(t, true, openAISubagentProfile["disable_fs_search_tools"])
	_, hasAllowedTools := openAISubagentProfile["allowed_tools"]
	assert.False(t, hasAllowedTools)

	premiumProfile, ok := config.Profiles["premium"]
	require.True(t, ok)
	assert.Equal(t, 64000, premiumProfile["max_tokens"])
	assert.Equal(t, 32000, premiumProfile["thinking_budget_tokens"])
}
