package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunExtensionListAndInspect(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	withTempHomeAndCWD(t, home, cwd)
	restoreExtensionCommandViper(t, filepath.Join(home, ".kodelet", "extensions"))

	extensionPath := filepath.Join(cwd, ".kodelet", "extensions", "weather", "kodelet-extension-weather")
	require.NoError(t, os.MkdirAll(filepath.Dir(extensionPath), 0o755))
	require.NoError(t, os.WriteFile(extensionPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	listOutput := captureAllStdout(t, func() {
		require.NoError(t, runExtensionList(context.Background(), ExtensionListConfig{}))
	})
	assert.Contains(t, listOutput, "weather")
	assert.Contains(t, listOutput, extensionPath)

	inspectOutput := captureAllStdout(t, func() {
		require.NoError(t, runExtensionInspect(context.Background(), "weather", ExtensionInspectConfig{JSONOutput: true}))
	})
	var inspected ExtensionOutput
	require.NoError(t, json.Unmarshal([]byte(inspectOutput), &inspected))
	assert.Equal(t, "weather", inspected.ID)
	assert.Equal(t, extensionPath, inspected.Path)
}

func restoreExtensionCommandViper(t *testing.T, globalDir string) {
	t.Helper()
	originalSettings := viper.AllSettings()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
	viper.Reset()
	viper.Set("extensions.enabled", true)
	viper.Set("extensions.local_dir", "./.kodelet/extensions")
	viper.Set("extensions.global_dir", globalDir)
	viper.Set("extensions.timeout", "5s")
	viper.Set("extensions.tool_timeout", "5s")
	viper.Set("extensions.max_output_size", 102400)
}
