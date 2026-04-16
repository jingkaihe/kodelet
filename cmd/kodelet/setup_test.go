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
	assert.Equal(t, "openai", openAIProfile["provider"])

	anthropicProfile, ok := config.Profiles["anthropic"]
	require.True(t, ok)
	assert.Equal(t, "anthropic", anthropicProfile["provider"])
	assert.Equal(t, "full", anthropicProfile["tool_mode"])
	assert.Equal(t, false, anthropicProfile["disable_fs_search_tools"])
	assert.Equal(t, 64000, anthropicProfile["max_tokens"])
	assert.Equal(t, 32000, anthropicProfile["thinking_budget_tokens"])
}
