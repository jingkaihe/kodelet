package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type testTool struct {
	name        string
	description string
	validateErr error
	traceErr    error
	result      tooltypes.ToolResult
	executed    bool
	rawSchema   map[string]any
}

func (t *testTool) GenerateSchema() *jsonschema.Schema              { return GenerateSchema[map[string]any]() }
func (t *testTool) RawInputSchema() map[string]any                  { return t.rawSchema }
func (t *testTool) Name() string                                    { return t.name }
func (t *testTool) Description() string                             { return t.description }
func (t *testTool) ValidateInput(_ tooltypes.State, _ string) error { return t.validateErr }
func (t *testTool) Execute(_ context.Context, _ tooltypes.State, _ string) tooltypes.ToolResult {
	t.executed = true
	if t.result != nil {
		return t.result
	}
	return tooltypes.BaseToolResult{Result: "ok"}
}
func (t *testTool) TracingKVs(_ string) ([]attribute.KeyValue, error) { return nil, t.traceErr }

type streamingTestTool struct {
	*testTool
	streamingExecuted bool
}

func (t *streamingTestTool) ExecuteStreaming(
	_ context.Context,
	_ tooltypes.State,
	_ string,
	onUpdate tooltypes.ToolUpdateCallback,
) tooltypes.ToolResult {
	t.streamingExecuted = true
	onUpdate(tooltypes.BaseToolResult{Result: "partial"})
	return tooltypes.BaseToolResult{Result: "complete"}
}

func TestGetAvailableToolNames(t *testing.T) {
	tools := getAvailableToolNames()

	// Should include all tools from toolRegistry
	assert.Contains(t, tools, "bash")
	assert.Contains(t, tools, "file_read")
	assert.Contains(t, tools, "openai_web_search")

	// Should have the expected number of tools (registry tools plus virtual tools)
	assert.Equal(t, len(toolRegistry)+len(virtualToolNames), len(tools))
}

func TestValidateTools_ValidTools(t *testing.T) {
	validTools := []string{"bash", "file_read", "file_write"}
	err := ValidateTools(validTools)
	assert.NoError(t, err)
}

func TestValidateTools_AllowsVirtualOpenAIWebSearch(t *testing.T) {
	err := ValidateTools([]string{"bash", "openai_web_search"})
	assert.NoError(t, err)
}

func TestValidateTools_EmptyList(t *testing.T) {
	err := ValidateTools([]string{})
	assert.NoError(t, err)
}

func TestValidateTools_SingleUnknownTool(t *testing.T) {
	invalidTools := []string{"unknown_tool"}
	err := ValidateTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: unknown_tool")
	assert.Contains(t, err.Error(), "Available tools:")
	assert.Contains(t, err.Error(), "bash")
	assert.Contains(t, err.Error(), "file_read")
}

func TestValidateTools_MultipleUnknownTools(t *testing.T) {
	invalidTools := []string{"unknown_tool1", "unknown_tool2"}
	err := ValidateTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tools: unknown_tool1, unknown_tool2")
	assert.Contains(t, err.Error(), "Available tools:")
	assert.Contains(t, err.Error(), "bash")
	assert.Contains(t, err.Error(), "file_read")
}

func TestValidateTools_MixedValidAndInvalidTools(t *testing.T) {
	mixedTools := []string{"bash", "unknown_tool", "file_read"}
	err := ValidateTools(mixedTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: unknown_tool")
	assert.Contains(t, err.Error(), "Available tools:")
	// Should not mention the valid tools in the error
	assert.NotContains(t, err.Error(), "bash, unknown_tool")
}

func TestErrorMessageFormat(t *testing.T) {
	// Test that error messages are properly formatted and include all necessary information
	t.Run("single unknown tool error format", func(t *testing.T) {
		err := ValidateTools([]string{"nonexistent"})
		require.Error(t, err)

		errMsg := err.Error()
		lines := strings.Split(errMsg, "\n")
		assert.Len(t, lines, 2, "Error message should have exactly 2 lines")
		assert.True(t, strings.HasPrefix(lines[0], "unknown tool: nonexistent"))
		assert.True(t, strings.HasPrefix(lines[1], "Available tools: "))

		// Check that available tools line contains some expected tools
		availableToolsLine := lines[1]
		assert.Contains(t, availableToolsLine, "bash")
		assert.Contains(t, availableToolsLine, "file_read")
	})

	t.Run("multiple unknown tools error format", func(t *testing.T) {
		err := ValidateTools([]string{"nonexistent1", "nonexistent2"})
		require.Error(t, err)

		errMsg := err.Error()
		lines := strings.Split(errMsg, "\n")
		assert.Len(t, lines, 2, "Error message should have exactly 2 lines")
		assert.True(t, strings.HasPrefix(lines[0], "unknown tools: nonexistent1, nonexistent2"))
		assert.True(t, strings.HasPrefix(lines[1], "Available tools: "))
	})
}

func TestGetMainTools_FallsBackOnValidationErrors(t *testing.T) {
	// Test with invalid tools
	invalidTools := []string{"unknown_tool", "bash"}
	tools := GetMainTools(context.Background(), invalidTools)

	// Should fallback to default tools
	defaultTools := GetMainTools(context.Background(), []string{})
	assert.Equal(t, len(defaultTools), len(tools), "Should fallback to default tools")

	// Verify we got the default tools, not the invalid ones
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}
	assert.NotContains(t, toolNames, "unknown_tool", "Should not contain unknown tool")
}

func TestGetMainTools_UsesValidTools(t *testing.T) {
	// Test with valid tools
	validTools := []string{"bash", "file_read", "file_write"}
	tools := GetMainTools(context.Background(), validTools)

	// Should use the requested tools (plus meta tools)
	assert.GreaterOrEqual(t, len(tools), len(validTools), "Should include at least the requested tools")

	// Verify we got the requested tools
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	for _, requestedTool := range validTools {
		assert.Contains(t, toolNames, requestedTool, "Should contain requested tool: %s", requestedTool)
	}
}

func TestGetToolsFromNamesAndOpenAIConversion(t *testing.T) {
	tools := GetToolsFromNames([]string{"bash"})
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "bash")
	assert.NotContains(t, toolNames, "file_read")
	assert.NotContains(t, toolNames, "openai_web_search")

	openAITools := ToOpenAITools(tools[:1])
	require.Len(t, openAITools, 1)
	assert.Equal(t, "function", string(openAITools[0].Type))
	require.NotNil(t, openAITools[0].Function)
	assert.Equal(t, tools[0].Name(), openAITools[0].Function.Name)
	assert.NotNil(t, openAITools[0].Function.Parameters)
}

func TestOpenAIConversionPreservesRawJSONSchema(t *testing.T) {
	rawSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": []any{"string", "null"}},
		},
		"additionalProperties": false,
		"x-mcp-extension":      true,
	}
	converted := ToOpenAITools([]tooltypes.Tool{&testTool{name: "raw", description: "raw", rawSchema: rawSchema}})

	require.Len(t, converted, 1)
	require.NotNil(t, converted[0].Function)
	assert.Equal(t, rawSchema, converted[0].Function.Parameters)
}

func TestRunToolFindsValidatesAndExecutesTool(t *testing.T) {
	tool := &testTool{name: "test_tool", description: "test", result: tooltypes.BaseToolResult{Result: "ran"}}
	state := NewBasicState(context.Background(), WithExtensionTools([]tooltypes.Tool{tool}))

	result := RunTool(context.Background(), state, "test_tool", `{"ok":true}`)

	require.False(t, result.IsError())
	assert.Equal(t, "ran", result.GetResult())
	assert.True(t, tool.executed)
}

func TestRunToolWithUpdatesUsesStreamingTool(t *testing.T) {
	tool := &streamingTestTool{testTool: &testTool{name: "streaming_tool", description: "test"}}
	state := NewBasicState(context.Background(), WithExtensionTools([]tooltypes.Tool{tool}))
	updates := []string{}

	result := RunToolWithUpdates(context.Background(), state, "streaming_tool", `{}`, func(update tooltypes.ToolResult) {
		updates = append(updates, update.GetResult())
	})

	assert.True(t, tool.streamingExecuted)
	assert.False(t, tool.executed)
	assert.Equal(t, []string{"partial"}, updates)
	assert.Equal(t, "complete", result.GetResult())
}

func TestRunToolWithoutUpdateCallbackUsesRegularExecution(t *testing.T) {
	tool := &streamingTestTool{testTool: &testTool{name: "streaming_tool", description: "test"}}
	state := NewBasicState(context.Background(), WithExtensionTools([]tooltypes.Tool{tool}))

	result := RunTool(context.Background(), state, "streaming_tool", `{}`)

	assert.False(t, tool.streamingExecuted)
	assert.True(t, tool.executed)
	assert.Equal(t, "ok", result.GetResult())
}

func TestRunToolReturnsFindAndValidationErrors(t *testing.T) {
	state := NewBasicState(context.Background(), WithLLMConfig(llmtypes.Config{AllowedTools: []string{NoToolsMarker}}))
	missing := RunTool(context.Background(), state, "missing", `{}`)
	require.True(t, missing.IsError())
	assert.Contains(t, missing.GetError(), "failed to find tool")

	tool := &testTool{name: "bad_tool", validateErr: assert.AnError}
	state = NewBasicState(context.Background(), WithExtensionTools([]tooltypes.Tool{tool}))
	invalid := RunTool(context.Background(), state, "bad_tool", `{}`)
	require.True(t, invalid.IsError())
	assert.Contains(t, invalid.GetError(), assert.AnError.Error())
	assert.False(t, tool.executed)
}

func TestGetMainTools_ExplicitAllowlistIncludesGoalMetaTools(t *testing.T) {
	tools := GetMainTools(context.Background(), []string{"bash"})

	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "bash")
	assert.Contains(t, toolNames, "get_goal")
	assert.Contains(t, toolNames, "update_goal")
}

func TestGetMainToolsWithOptions_ExplicitAllowlistIncludesGoalMetaTools(t *testing.T) {
	tools := GetMainToolsWithOptions(context.Background(), []string{"bash"}, true)

	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "bash")
	assert.Contains(t, toolNames, "get_goal")
	assert.Contains(t, toolNames, "update_goal")
}

func TestGetMainTools_NoToolsMarker(t *testing.T) {
	// Test that NoToolsMarker returns nil (no tools)
	tools := GetMainTools(context.Background(), []string{NoToolsMarker})

	assert.Nil(t, tools, "NoToolsMarker should return nil tools")
	assert.Len(t, tools, 0, "NoToolsMarker should return zero tools")
}

func TestNoToolsMarker_Constant(t *testing.T) {
	// Ensure the constant value is correct
	assert.Equal(t, "none", NoToolsMarker, "NoToolsMarker should be 'none'")
}

func TestFileReadExcludedFromDefaults(t *testing.T) {
	t.Run("main tools exclude file_read by default", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{})

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "file_write")
		assert.Contains(t, toolNames, "file_edit")
	})

	t.Run("file_read can be enabled via explicit allowlist", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{"bash", "file_read"})

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "file_read")
	})
}

func TestGetMainToolsWithOptions_FSSearchToolsDisabled(t *testing.T) {
	t.Run("removes grep and glob from default main tools and meta tools", func(t *testing.T) {
		tools := GetMainToolsWithOptions(context.Background(), nil, false)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "bash")
		assert.NotContains(t, toolNames, "grep_tool")
		assert.NotContains(t, toolNames, "glob_tool")
	})

	t.Run("removes grep and glob even when explicitly requested", func(t *testing.T) {
		tools := GetMainToolsWithOptions(context.Background(), []string{"bash", "grep_tool", "glob_tool"}, false)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "bash")
		assert.NotContains(t, toolNames, "grep_tool")
		assert.NotContains(t, toolNames, "glob_tool")
	})

	t.Run("search-only allowlists do not fall back to defaults", func(t *testing.T) {
		tools := GetMainToolsWithOptions(context.Background(), []string{"grep_tool", "glob_tool"}, false)

		assert.Empty(t, tools)
	})

	t.Run("fallback after validation still keeps grep and glob disabled", func(t *testing.T) {
		tools := GetMainToolsWithOptions(context.Background(), []string{"unknown_tool"}, false)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "bash")
		assert.NotContains(t, toolNames, "grep_tool")
		assert.NotContains(t, toolNames, "glob_tool")
	})
}
