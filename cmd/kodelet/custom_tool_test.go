package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
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

func TestCustomToolInvokeInputJSONSatisfiesRequiredFlags(t *testing.T) {
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
		err := runCustomToolCommand(t, []string{"invoke", "hello", "--input-json", `{"name":"Ada"}`})
		require.NoError(t, err)
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &payload))
	assert.Equal(t, "Ada", payload["name"])
}

func TestCustomToolInvokeInputJSONSupportsUnsupportedProperties(t *testing.T) {
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
		err := runCustomToolCommand(t, []string{"invoke", "hello", "--name", "Ada", "--input-json", `{"config":{"verbose":true}}`})
		require.NoError(t, err)
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &payload))
	assert.Equal(t, "Ada", payload["name"])
	assert.Equal(t, map[string]any{"verbose": true}, payload["config"])
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

func TestCustomToolInvokeSupportsToolNamesMatchingInvokeAliases(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "invoke"), `{"name":"invoke","description":"Invoke-named tool","input_schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}`)
	createTestCustomTool(t, filepath.Join(localToolsDir, "cti"), `{"name":"cti","description":"CTI-named tool","input_schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	invokeOutput := captureStdout(t, func() {
		err := runCustomToolCommand(t, []string{"invoke", "invoke", "--name", "Ada"})
		require.NoError(t, err)
	})
	ctiOutput := captureStdout(t, func() {
		err := runCustomToolAliasCommand(t, []string{"cti", "--name", "Grace"})
		require.NoError(t, err)
	})

	assert.Contains(t, invokeOutput, `"name":"Ada"`)
	assert.Contains(t, ctiOutput, `"name":"Grace"`)
}

func TestCustomToolJSONListAndCompletion(t *testing.T) {
	homeDir := t.TempDir()
	globalToolsDir := t.TempDir()
	localToolsDir := filepath.Join(t.TempDir(), ".kodelet", "tools")
	setupCustomToolTestConfig(t, homeDir, globalToolsDir, localToolsDir)
	require.NoError(t, os.MkdirAll(localToolsDir, 0o755))

	createTestCustomTool(t, filepath.Join(localToolsDir, "alpha"), `{"name":"alpha","description":"Alpha tool","input_schema":{"type":"object","properties":{}}}`)
	createTestCustomTool(t, filepath.Join(localToolsDir, "beta"), `{"name":"beta","description":"Beta tool","input_schema":{"type":"object","properties":{}}}`)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(localToolsDir)))
	defer func() { _ = os.Chdir(oldWD) }()

	jsonOutput := captureStdout(t, func() {
		err := runCustomToolListCommand(t, []string{"list", "--json"})
		require.NoError(t, err)
	})
	var payload struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOutput), &payload))
	require.Len(t, payload.Tools, 2)
	assert.Equal(t, "alpha", payload.Tools[0].Name)
	assert.Equal(t, "Alpha tool", payload.Tools[0].Description)
	assert.NotEmpty(t, payload.Tools[0].Path)

	names, directive := completeCustomToolNames(customToolCmd, nil, "b")
	assert.Equal(t, []string{"beta"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCustomToolSchemaDefaultsAndEnum(t *testing.T) {
	assert.Equal(t, "red, 2, true", formatSchemaEnum([]any{"red", 2, true}))

	assert.Equal(t, "", mustSchemaDefaultString(t, nil))
	assert.Equal(t, "hello", mustSchemaDefaultString(t, "hello"))
	_, err := schemaDefaultString(12)
	assert.Error(t, err)

	assert.Equal(t, 0, mustSchemaDefaultInt(t, nil))
	assert.Equal(t, 3, mustSchemaDefaultInt(t, int32(3)))
	assert.Equal(t, 4, mustSchemaDefaultInt(t, int64(4)))
	assert.Equal(t, 5, mustSchemaDefaultInt(t, float64(5)))
	_, err = schemaDefaultInt("5")
	assert.Error(t, err)

	assert.Equal(t, 0.0, mustSchemaDefaultFloat(t, nil))
	assert.Equal(t, 1.5, mustSchemaDefaultFloat(t, 1.5))
	assert.Equal(t, 2.5, mustSchemaDefaultFloat(t, float32(2.5)))
	assert.Equal(t, 6.0, mustSchemaDefaultFloat(t, int64(6)))
	_, err = schemaDefaultFloat("1.5")
	assert.Error(t, err)

	assert.False(t, mustSchemaDefaultBool(t, nil))
	assert.True(t, mustSchemaDefaultBool(t, true))
	_, err = schemaDefaultBool("true")
	assert.Error(t, err)

	assert.Nil(t, mustSchemaDefaultStringSlice(t, nil))
	assert.Equal(t, []string{"a", "b"}, mustSchemaDefaultStringSlice(t, []any{"a", "b"}))
	_, err = schemaDefaultStringSlice("a")
	assert.Error(t, err)
	_, err = schemaDefaultStringSlice([]any{"a", 1})
	assert.Error(t, err)

	assert.Nil(t, mustSchemaDefaultIntSlice(t, nil))
	assert.Equal(t, []int{1, 2, 3}, mustSchemaDefaultIntSlice(t, []any{float64(1), int64(2), 3}))
	_, err = schemaDefaultIntSlice("1")
	assert.Error(t, err)
	_, err = schemaDefaultIntSlice([]any{"1"})
	assert.Error(t, err)
}

func TestCustomToolSchemaFlagErrors(t *testing.T) {
	cmd := &cobra.Command{Use: "tool"}
	cmd.Flags().SortFlags = false

	supported, err := addSchemaFlag(cmd.Flags(), "broken", &jsonschema.Schema{Type: "array"}, "")
	assert.False(t, supported)
	assert.Error(t, err)

	supported, err = addSchemaFlag(cmd.Flags(), "object", &jsonschema.Schema{Type: "object"}, "")
	require.NoError(t, err)
	assert.False(t, supported)
}

func mustSchemaDefaultString(t *testing.T, value any) string {
	t.Helper()
	result, err := schemaDefaultString(value)
	require.NoError(t, err)
	return result
}

func mustSchemaDefaultInt(t *testing.T, value any) int {
	t.Helper()
	result, err := schemaDefaultInt(value)
	require.NoError(t, err)
	return result
}

func mustSchemaDefaultFloat(t *testing.T, value any) float64 {
	t.Helper()
	result, err := schemaDefaultFloat(value)
	require.NoError(t, err)
	return result
}

func mustSchemaDefaultBool(t *testing.T, value any) bool {
	t.Helper()
	result, err := schemaDefaultBool(value)
	require.NoError(t, err)
	return result
}

func mustSchemaDefaultStringSlice(t *testing.T, value any) []string {
	t.Helper()
	result, err := schemaDefaultStringSlice(value)
	require.NoError(t, err)
	return result
}

func mustSchemaDefaultIntSlice(t *testing.T, value any) []int {
	t.Helper()
	result, err := schemaDefaultIntSlice(value)
	require.NoError(t, err)
	return result
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
