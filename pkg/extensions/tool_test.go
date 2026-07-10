package extensions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestNewToolValidationAndSchemaDefaults(t *testing.T) {
	t.Run("default schema", func(t *testing.T) {
		tool, err := newTool("weather", nil, ToolRegistration{Name: "weather", Description: "Weather"}, 0, 100)
		require.NoError(t, err)
		require.NotNil(t, tool.GenerateSchema())
		assert.Equal(t, "object", tool.GenerateSchema().Type)
	})

	t.Run("invalid schema", func(t *testing.T) {
		_, err := newTool("weather", nil, ToolRegistration{
			Name:        "weather",
			Description: "Weather",
			InputSchema: map[string]any{"type": make(chan int)},
		}, 0, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal extension tool schema")
	})

	t.Run("preserves JSON Schema constraints", func(t *testing.T) {
		inputSchema := map[string]any{
			"type":        "object",
			"description": "Constrained request",
			"properties": map[string]any{
				"mode": map[string]any{
					"description": "Execution mode",
					"enum":        []any{"fast", "safe"},
				},
				"limit": map[string]any{
					"type":    "integer",
					"minimum": float64(1),
					"maximum": float64(10),
				},
				"target": map[string]any{
					"anyOf": []any{
						map[string]any{"const": "workspace"},
						map[string]any{"type": "string", "pattern": "^file:"},
					},
				},
			},
			"required":             []any{"mode"},
			"additionalProperties": false,
		}
		tool, err := newTool("search", nil, ToolRegistration{
			Name:        "search",
			Description: "Search",
			InputSchema: inputSchema,
		}, 0, 100)
		require.NoError(t, err)

		schemaJSON, err := json.Marshal(tool.GenerateSchema())
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(schemaJSON, &schema))
		assert.Equal(t, inputSchema, schema)
	})

	t.Run("missing name and description", func(t *testing.T) {
		_, err := newTool("weather", nil, ToolRegistration{Description: "Weather"}, 0, 100)
		require.ErrorContains(t, err, "extension tool name is required")
		_, err = newTool("weather", nil, ToolRegistration{Name: "weather"}, 0, 100)
		require.ErrorContains(t, err, "extension tool description is required")
	})
}

func TestToolValidateInputAndTracing(t *testing.T) {
	tool, err := newTool("weather", nil, ToolRegistration{Name: "get_weather", Description: "Weather"}, 0, 100)
	require.NoError(t, err)

	assert.NoError(t, tool.ValidateInput(nil, `{"location":"London"}`))
	require.Error(t, tool.ValidateInput(nil, `not-json`))
	kvs, err := tool.TracingKVs(`{}`)
	require.NoError(t, err)
	assert.Contains(t, kvs, attribute.String("tool.type", "extension"))
	assert.Contains(t, kvs, attribute.String("tool.name", "get_weather"))
	assert.Contains(t, kvs, attribute.String("extension.id", "weather"))
}

func TestToolExecuteHandlesTruncation(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	extDir := filepath.Join(rootDir, "weather")
	writeExecutable(t, filepath.Join(extDir, "kodelet-extension-weather"), helperExtensionScript(t))

	runtime, err := NewRuntime(
		context.Background(),
		WithConfig(DefaultConfig()),
		WithWorkingDir(rootDir),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, runtime.Close()) })

	registered := runtime.Tools()
	require.Len(t, registered, 1)
	tool := registered[0]

	truncated := tool.Execute(context.Background(), nil, `{"location":"VeryLong"}`)
	assert.False(t, truncated.IsError())
	assert.Contains(t, truncated.GetResult(), "Weather for VeryLong")

	extensionTool, ok := tool.(*Tool)
	require.True(t, ok)
	extensionTool.maxOutput = 5
	truncated = extensionTool.Execute(context.Background(), nil, `{"location":"London"}`)
	assert.False(t, truncated.IsError())
	assert.Contains(t, truncated.GetResult(), "[TRUNCATED")
}

func TestToolResultAssistantFacingStringAndStructuredData(t *testing.T) {
	success := &ToolResult{toolName: "get_weather", extensionID: "weather", result: "sunny", data: map[string]any{"temp": 18}}
	assert.Contains(t, success.AssistantFacing(), "sunny")
	assert.Equal(t, "sunny", success.String())
	structured := success.StructuredData()
	require.True(t, structured.Success)
	var metadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &metadata))
	assert.Equal(t, "weather", metadata.ExtensionID)
	assert.Equal(t, float64(18), mustJSONNumber(t, metadata.Data["temp"]))

	failure := &ToolResult{toolName: "get_weather", extensionID: "weather", err: "boom"}
	assert.Contains(t, failure.AssistantFacing(), "<error>")
	assert.Equal(t, "boom", failure.GetError())
	assert.Contains(t, failure.String(), "extension tool get_weather failed")
	assert.False(t, failure.StructuredData().Success)
}

func TestShouldRestartAfterCallError(t *testing.T) {
	assert.False(t, shouldRestartAfterCallError(nil))
	assert.True(t, shouldRestartAfterCallError(context.DeadlineExceeded))
	assert.True(t, shouldRestartAfterCallError(context.Canceled))
	assert.False(t, shouldRestartAfterCallError(errors.New("extension rpc error -32000: bad input")))
	assert.True(t, shouldRestartAfterCallError(errors.New("failed to read rpc header")))
}

func mustJSONNumber(t *testing.T, value any) float64 {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	var number float64
	require.NoError(t, json.Unmarshal(payload, &number))
	return number
}
