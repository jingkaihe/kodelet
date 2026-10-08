package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
						map[string]any{"type": []any{"string", "null"}, "pattern": "^file:"},
					},
				},
			},
			"required":             []any{"mode"},
			"additionalProperties": false,
			"x-mcp-extension":      map[string]any{"enabled": true},
		}
		tool, err := newTool("search", nil, ToolRegistration{
			Name:        "search",
			Description: "Search",
			InputSchema: inputSchema,
		}, 0, 100)
		require.NoError(t, err)

		assert.Equal(t, inputSchema, tooltypes.JSONSchemaForTool(tool))
		assert.Equal(t, "object", tool.GenerateSchema().Type)
	})

	t.Run("missing name and description", func(t *testing.T) {
		_, err := newTool("weather", nil, ToolRegistration{Description: "Weather"}, 0, 100)
		require.ErrorContains(t, err, "extension tool name is required")
		_, err = newTool("weather", nil, ToolRegistration{Name: "weather"}, 0, 100)
		require.ErrorContains(t, err, "extension tool description is required")
	})
}

func TestToolOutputSchemaAndProvenance(t *testing.T) {
	var registration ToolRegistration
	require.NoError(t, json.Unmarshal([]byte(`{
		"name":"mcp__ambiguous_server_tool_name",
		"description":"Structured result",
		"inputSchema":{"type":"object"},
		"outputSchema":{"type":"object","properties":{"value":{"type":["string","null"]}},"x-extra":true},
		"group":"mcp/ambiguous_server"
	}`), &registration))
	tool, err := newTool("mcp-extension", nil, registration, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, "mcp/ambiguous_server", tool.ToolGroup())
	assert.Equal(t, registration.OutputSchema, tooltypes.OutputSchemaForTool(tool))
	registration.OutputSchema["properties"].(map[string]any)["value"] = "changed"
	first := tooltypes.OutputSchemaForTool(tool)
	assert.IsType(t, map[string]any{}, first["properties"].(map[string]any)["value"])
	first["properties"].(map[string]any)["value"] = "changed again"
	assert.IsType(t, map[string]any{}, tool.RawOutputSchema()["properties"].(map[string]any)["value"])

	tool, err = newTool("custom-extension", nil, ToolRegistration{Name: "tool", Description: "Plain output"}, 0, 100)
	require.NoError(t, err)
	assert.Nil(t, tool.RawOutputSchema())
	assert.Equal(t, "extension/custom-extension", tool.ToolGroup())
	assert.Nil(t, tooltypes.OutputSchemaForTool(nil))

	_, err = newTool("custom-extension", nil, ToolRegistration{
		Name: "tool", Description: "Invalid schema", OutputSchema: map[string]any{"invalid": make(chan int)},
	}, 0, 100)
	require.ErrorContains(t, err, "failed to marshal extension tool output schema")
}

func TestToolResultCanonicalDataIsSeparateFromPresentation(t *testing.T) {
	for _, payload := range []string{`{"items":[{"id":7}]}`, `[1,false]`, `false`, `0`, `""`, `null`} {
		t.Run(payload, func(t *testing.T) {
			var execution ToolExecutionResult
			require.NoError(t, json.Unmarshal([]byte(`{"content":"human text","data":{"presentation":{"summary":"Summary"}},"structuredContent":`+payload+`}`), &execution))
			tool := &Tool{name: "structured_tool", extensionID: "custom", maxOutput: 100}
			result := tool.resultFromExecution(execution, 0)
			structured := result.StructuredData()
			assert.Equal(t, execution.StructuredContent, structured.Data)
			assert.Equal(t, "human text", result.GetResult())
			var metadata tooltypes.ExtensionToolMetadata
			require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &metadata))
			assert.Equal(t, execution.Data, metadata.Data)
		})
	}
}

func TestToolTransportFailureHasUnknownOutcome(t *testing.T) {
	for _, cancelBeforeReply := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelBeforeReply), func(t *testing.T) {
			process, sdk, reader := newEventTraceProcess(t)
			tool, err := newTool("test", process, ToolRegistration{Name: "test", Description: "test"}, 0, 100)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan tooltypes.ToolResult, 1)
			go func() { done <- tool.Execute(ctx, nil, `{}`) }()
			request, err := readIncomingMessage(reader)
			require.NoError(t, err)
			if cancelBeforeReply {
				cancel()
				_, err := readIncomingMessage(reader) // Drain the cancellation notification on the unbuffered pipe.
				require.NoError(t, err)
			} else {
				sendEventTraceMessage(t, sdk, map[string]any{
					"jsonrpc": "2.0", "id": request.ID, "error": &rpcError{Code: -32000, Message: "request failed"},
				})
			}
			result := <-done
			assert.True(t, result.IsError())
			kind, outcome := result.(tooltypes.ToolFailureProvider).ToolFailure()
			assert.Equal(t, "unknown", outcome)
			if cancelBeforeReply {
				assert.Equal(t, "cancelled", kind)
			} else {
				assert.Equal(t, "transport", kind)
			}
		})
	}
	result := (&Tool{name: "test"}).resultFromExecution(ToolExecutionResult{Error: "completed tool error"}, 0)
	kind, outcome := result.ToolFailure()
	assert.Empty(t, kind)
	assert.Empty(t, outcome)
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
	var metadata tooltypes.ExtensionToolMetadata
	require.True(t, tooltypes.ExtractMetadata(truncated.StructuredData().Metadata, &metadata))
	assert.True(t, metadata.Truncated)
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
	failureStructured := failure.StructuredData()
	assert.False(t, failureStructured.Success)
	require.True(t, tooltypes.ExtractMetadata(failureStructured.Metadata, &metadata))
	assert.Equal(t, "weather", metadata.ExtensionID)
}

func TestToolResultPreservesImageAttachments(t *testing.T) {
	var execution ToolExecutionResult
	require.NoError(t, json.Unmarshal(
		[]byte(`{"content":"Generated image","attachments":[{"type":"image","path":"/runner/generated.png","alt":"A drawing"}]}`),
		&execution,
	))
	tool := &Tool{
		name:        "generate_image",
		extensionID: "generator",
		maxOutput:   100,
	}
	result := tool.resultFromExecution(execution, 0)
	structured := result.StructuredData()
	require.Len(t, structured.Attachments, 1)
	assert.Equal(t, execution.Attachments, structured.Attachments)
	assert.Contains(t, result.AssistantFacing(), "Generated image")
	assert.NotContains(t, result.AssistantFacing(), "base64")
	execution.Attachments[0].Path = "changed"
	structured.Attachments[0].Path = "also changed"
	assert.Equal(t, "/runner/generated.png", result.StructuredData().Attachments[0].Path)
}

func TestToolResultNormalizesAndBoundsPresentationData(t *testing.T) {
	tool := &Tool{maxOutput: 96}
	result := tool.resultFromExecution(ToolExecutionResult{
		Content: "queued",
		Data: map[string]any{
			"request_id": "req-123",
			"presentation": map[string]any{
				"summary": "  Follow up\nparser-reviewer  ",
				"body":    strings.Repeat("界", 100),
				"format":  "MARKDOWN",
				"future":  true,
			},
		},
	}, 0)

	structured := result.StructuredData()
	presentation, metadata, ok := tooltypes.ExtractExtensionToolPresentation(&structured)
	require.True(t, ok)
	assert.Equal(t, "Follow up parser-reviewer", presentation.Summary)
	assert.Equal(t, "markdown", presentation.Format)
	assert.LessOrEqual(t, len(presentation.Body), 96)
	assert.True(t, utf8.ValidString(presentation.Body))
	assert.Contains(t, presentation.Body, "[TRUNCATED")
	assert.Equal(t, "req-123", metadata.Data["request_id"])
	assert.Equal(t, true, metadata.Data["presentation"].(map[string]any)["future"])

	invalid := tool.resultFromExecution(ToolExecutionResult{Data: map[string]any{
		"preserved":    true,
		"presentation": map[string]any{"summary": "Unsafe\x1b[31m label"},
	}}, 0).StructuredData()
	require.True(t, tooltypes.ExtractMetadata(invalid.Metadata, &metadata))
	assert.Equal(t, true, metadata.Data["preserved"])
	assert.NotContains(t, metadata.Data, "presentation")
}

func TestShouldRestartAfterCallError(t *testing.T) {
	assert.False(t, shouldRestartAfterCallError(nil))
	assert.False(t, shouldRestartAfterCallError(context.DeadlineExceeded))
	assert.False(t, shouldRestartAfterCallError(context.Canceled))
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
