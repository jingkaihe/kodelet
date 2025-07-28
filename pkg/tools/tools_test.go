package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAvailableToolNames(t *testing.T) {
	tools := getAvailableToolNames()

	// Should include all tools from toolRegistry
	assert.Contains(t, tools, "bash")
	assert.Contains(t, tools, "file_read")
	assert.Contains(t, tools, "subagent")

	// Should have the expected number of tools (check against toolRegistry)
	assert.Equal(t, len(toolRegistry), len(tools))
}

func TestGetAvailableSubAgentToolNames(t *testing.T) {
	tools := getAvailableSubAgentToolNames()

	// Should include most tools from toolRegistry except subagent
	assert.Contains(t, tools, "bash")
	assert.Contains(t, tools, "file_read")

	// Should NOT include subagent tool
	assert.NotContains(t, tools, "subagent")

	// Should have one less tool than toolRegistry (excluding subagent)
	assert.Equal(t, len(toolRegistry)-1, len(tools))
}

func TestValidateTools_ValidTools(t *testing.T) {
	validTools := []string{"bash", "file_read", "thinking"}
	err := ValidateTools(validTools)
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

func TestValidateSubAgentTools_ValidTools(t *testing.T) {
	validTools := []string{"bash", "file_read", "thinking"}
	err := ValidateSubAgentTools(validTools)
	assert.NoError(t, err)
}

func TestValidateSubAgentTools_EmptyList(t *testing.T) {
	err := ValidateSubAgentTools([]string{})
	assert.NoError(t, err)
}

func TestValidateSubAgentTools_SubagentToolOnly(t *testing.T) {
	invalidTools := []string{"subagent"}
	err := ValidateSubAgentTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "subagent tool cannot be used by subagent to prevent infinite recursion")
	assert.Contains(t, err.Error(), "Available tools:")
	assert.Contains(t, err.Error(), "bash")
	assert.Contains(t, err.Error(), "file_read")
	// Should not include subagent in available tools
	assert.NotContains(t, err.Error(), "subagent,")
	assert.NotContains(t, err.Error(), ", subagent")
}

func TestValidateSubAgentTools_SubagentToolWithOthers(t *testing.T) {
	invalidTools := []string{"bash", "subagent", "unknown_tool"}
	err := ValidateSubAgentTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tools: subagent, unknown_tool")
	assert.Contains(t, err.Error(), "subagent tool cannot be used by subagent to prevent infinite recursion")
	assert.Contains(t, err.Error(), "Available tools:")
}

func TestValidateSubAgentTools_UnknownToolOnly(t *testing.T) {
	invalidTools := []string{"unknown_tool"}
	err := ValidateSubAgentTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: unknown_tool")
	assert.Contains(t, err.Error(), "Available tools:")
	assert.Contains(t, err.Error(), "bash")
	assert.Contains(t, err.Error(), "file_read")
	// Should not include subagent in available tools
	assert.NotContains(t, err.Error(), "subagent,")
	assert.NotContains(t, err.Error(), ", subagent")
}

func TestValidateSubAgentTools_MultipleUnknownTools(t *testing.T) {
	invalidTools := []string{"unknown_tool1", "unknown_tool2"}
	err := ValidateSubAgentTools(invalidTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tools: unknown_tool1, unknown_tool2")
	assert.Contains(t, err.Error(), "Available tools:")
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

	t.Run("subagent tool error format", func(t *testing.T) {
		err := ValidateSubAgentTools([]string{"subagent"})
		require.Error(t, err)

		errMsg := err.Error()
		lines := strings.Split(errMsg, "\n")
		assert.Len(t, lines, 2, "Error message should have exactly 2 lines")
		assert.Contains(t, lines[0], "subagent tool cannot be used by subagent to prevent infinite recursion")
		assert.True(t, strings.HasPrefix(lines[1], "Available tools: "))

		// Verify subagent is not in the available tools list
		availableToolsLine := lines[1]
		assert.NotContains(t, availableToolsLine, "subagent")
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

func TestGetSubAgentTools_FallsBackOnValidationErrors(t *testing.T) {
	// Test with subagent tool (invalid for subagents)
	invalidTools := []string{"bash", "subagent", "file_read"}
	tools := GetSubAgentTools(context.Background(), invalidTools)

	// Should fallback to default tools
	defaultTools := GetSubAgentTools(context.Background(), []string{})
	assert.Equal(t, len(defaultTools), len(tools), "Should fallback to default tools")

	// Verify we got the default tools, not the invalid ones
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}
	assert.NotContains(t, toolNames, "subagent", "Should not contain subagent tool")
}

func TestGetMainTools_UsesValidTools(t *testing.T) {
	// Test with valid tools
	validTools := []string{"bash", "file_read", "thinking"}
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
