package main

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACPRespectsProfileDisableFSSearchTools(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Set("profile", "openai")
	viper.Set("profiles", map[string]any{
		"openai": map[string]any{
			"disable_fs_search_tools": true,
		},
	})

	config, err := buildACPServerConfig(acpCmd)
	require.NoError(t, err)
	assert.True(t, config.DisableFSSearchTools)
}

func TestACPRespectsProfileDisableSubagent(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Set("profile", "openai")
	viper.Set("profiles", map[string]any{
		"openai": map[string]any{
			"disable_subagent": true,
		},
	})

	config, err := buildACPServerConfig(acpCmd)
	require.NoError(t, err)
	assert.True(t, config.DisableSubagent)
}
