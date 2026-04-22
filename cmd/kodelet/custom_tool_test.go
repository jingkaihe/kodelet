package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomToolListSorted(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "zeta"), `{"name":"zeta","description":"Zeta tool","input_schema":{"type":"object","properties":{}}}`)
	createTestCustomTool(t, filepath.Join(localToolsDir, "alpha"), `{"name":"alpha","description":"Alpha tool","input_schema":{"type":"object","properties":{}}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	output := captureStdout(t, func() {
		err := runCustomToolListCommand(t, []string{"list"})
		require.NoError(t, err)
	})

	alphaIndex := strings.Index(output, "alpha")
	zetaIndex := strings.Index(output, "zeta")
	assert.NotEqual(t, -1, alphaIndex)
	assert.NotEqual(t, -1, zetaIndex)
	assert.Less(t, alphaIndex, zetaIndex)
}

func TestCustomToolDescribeOutputsSchema(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "hello"), `{"name":"hello","description":"Hello tool","input_schema":{"type":"object","properties":{"name":{"type":"string","description":"Your name"}},"required":["name"]}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	output := captureStdout(t, func() {
		err := runCustomToolCommand(t, []string{"describe", "hello"})
		require.NoError(t, err)
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	assert.Equal(t, "hello", payload["name"])
	assert.Equal(t, "Hello tool", payload["description"])

	inputSchema, ok := payload["input_schema"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", inputSchema["type"])
	properties, ok := inputSchema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, properties, "name")
}

func TestCustomToolInvokeDynamicFlags(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "hello"), `{"name":"hello","description":"Hello tool","input_schema":{"type":"object","properties":{"name":{"type":"string","description":"Your name"},"age":{"type":"integer"},"admin":{"type":"boolean"},"tags":{"type":"array","items":{"type":"string"}}},"required":["name"]}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	output := captureStdout(t, func() {
		err := runCustomToolCommand(t, []string{"invoke", "hello", "--name", "Ada", "--age", "36", "--admin", "--tags", "go,cli"})
		require.NoError(t, err)
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &payload))
	assert.Equal(t, "Ada", payload["name"])
	assert.Equal(t, float64(36), payload["age"])
	assert.Equal(t, true, payload["admin"])
	assert.Equal(t, []any{"go", "cli"}, payload["tags"])
}

func TestCustomToolInvokeHelpShowsDynamicFlags(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "hello"), `{"name":"hello","description":"Hello tool","input_schema":{"type":"object","properties":{"name":{"type":"string","description":"Your name"},"config":{"type":"object","description":"Advanced config"}},"required":["name"]}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	output := captureStdout(t, func() {
		err := runCustomToolCommand(t, []string{"invoke", "hello", "--help"})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "--name string")
	assert.Contains(t, output, "--input-json string")
	assert.Contains(t, output, "Properties only available through --input-json: config")
	assert.Contains(t, output, "required")
}

func TestCustomToolInvokeAliasWorks(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "hello"), `{"name":"hello","description":"Hello tool","input_schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	output := captureStdout(t, func() {
		err := runCustomToolAliasCommand(t, []string{"hello", "--name", "Grace"})
		require.NoError(t, err)
	})

	assert.Contains(t, output, `"name":"Grace"`)
}

func runCustomToolListCommand(t *testing.T, args []string) error {
	t.Helper()
	cmd := customToolCmd
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func runCustomToolCommand(t *testing.T, args []string) error {
	t.Helper()
	cmd := customToolCmd
	cmd.SetContext(context.Background())
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func runCustomToolAliasCommand(t *testing.T, args []string) error {
	t.Helper()
	cmd := customToolInvokeAliasCmd
	cmd.SetContext(context.Background())
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func setupCustomToolTestConfig(t *testing.T, homeDir, globalToolsDir, localToolsDir string) {
	t.Helper()
	originalSettings := viper.AllSettings()
	t.Setenv("HOME", homeDir)
	viper.Reset()
	viper.Set("custom_tools.enabled", true)
	viper.Set("custom_tools.global_dir", globalToolsDir)
	viper.Set("custom_tools.local_dir", localToolsDir)
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
}

func createTestCustomTool(t *testing.T, path string, descriptionJSON string) {
	t.Helper()
	script := "#!/bin/bash\n" +
		"if [ \"$1\" = \"description\" ]; then\n" +
		"  cat <<'EOF'\n" + descriptionJSON + "\nEOF\n" +
		"elif [ \"$1\" = \"run\" ]; then\n" +
		"  cat\n" +
		"else\n" +
		"  exit 1\n" +
		"fi\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}
