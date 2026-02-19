package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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
	validTools := []string{"bash", "file_read", "file_write"}
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
	validTools := []string{"bash", "file_read", "file_write"}
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
	tools := GetMainTools(context.Background(), invalidTools, false)

	// Should fallback to default tools
	defaultTools := GetMainTools(context.Background(), []string{}, false)
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
	validTools := []string{"bash", "file_read", "file_write"}
	tools := GetMainTools(context.Background(), validTools, false)

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

func TestGetMainTools_NoToolsMarker(t *testing.T) {
	// Test that NoToolsMarker returns nil (no tools)
	tools := GetMainTools(context.Background(), []string{NoToolsMarker}, false)

	assert.Nil(t, tools, "NoToolsMarker should return nil tools")
	assert.Len(t, tools, 0, "NoToolsMarker should return zero tools")
}

func TestGetSubAgentTools_NoToolsMarker(t *testing.T) {
	// Test that NoToolsMarker returns nil (no tools)
	tools := GetSubAgentTools(context.Background(), []string{NoToolsMarker})

	assert.Nil(t, tools, "NoToolsMarker should return nil tools")
	assert.Len(t, tools, 0, "NoToolsMarker should return zero tools")
}

func TestNoToolsMarker_Constant(t *testing.T) {
	// Ensure the constant value is correct
	assert.Equal(t, "none", NoToolsMarker, "NoToolsMarker should be 'none'")
}

func TestGetSubAgentTools_ExcludesSubagentTool(t *testing.T) {
	// Verify that default subagent tools don't include the subagent tool
	tools := GetSubAgentTools(context.Background(), []string{})

	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.NotContains(t, toolNames, "subagent", "Subagent tools should not include the subagent tool to prevent recursion")

	// Verify some expected tools ARE included
	assert.Contains(t, toolNames, "bash", "Should include bash tool")
	assert.Contains(t, toolNames, "file_read", "Should include file_read tool")
	assert.Contains(t, toolNames, "grep_tool", "Should include grep_tool")
}

func TestGetMainTools_IncludesSubagentTool(t *testing.T) {
	// Verify that main tools DO include the subagent tool
	tools := GetMainTools(context.Background(), []string{}, false)

	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "subagent", "Main tools should include the subagent tool")
}

func TestGetMainTools_TodoToolsToggle(t *testing.T) {
	t.Run("todo tools disabled by default", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{}, false)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "todo_read")
		assert.NotContains(t, toolNames, "todo_write")
	})

	t.Run("todo tools enabled when flag is true", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{}, true)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "todo_read")
		assert.Contains(t, toolNames, "todo_write")
	})

	t.Run("explicit todo tools are filtered out when todos disabled", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{"bash", "todo_read", "todo_write"}, false)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Name()
		}

		assert.Contains(t, toolNames, "bash")
		assert.NotContains(t, toolNames, "todo_read")
		assert.NotContains(t, toolNames, "todo_write")
	})
}

func TestFilterOutSubagent(t *testing.T) {
	t.Run("removes subagent from tool list", func(t *testing.T) {
		tools := GetMainTools(context.Background(), []string{}, false)
		filtered := filterOutSubagent(tools)

		toolNames := make([]string, len(filtered))
		for i, tool := range filtered {
			toolNames[i] = tool.Name()
		}

		assert.NotContains(t, toolNames, "subagent")
		assert.Contains(t, toolNames, "bash")
		assert.Contains(t, toolNames, "file_read")
		assert.Contains(t, toolNames, "web_fetch")
		assert.Contains(t, toolNames, "image_recognition")
		assert.Equal(t, len(tools)-1, len(filtered))
	})

	t.Run("preserves all tools when subagent not present", func(t *testing.T) {
		tools := GetSubAgentTools(context.Background(), []string{})
		filtered := filterOutSubagent(tools)

		assert.Equal(t, len(tools), len(filtered))
	})

	t.Run("handles empty tool list", func(t *testing.T) {
		var tools []tooltypes.Tool
		filtered := filterOutSubagent(tools)

		assert.Empty(t, filtered)
	})

	t.Run("handles nil tool list", func(t *testing.T) {
		filtered := filterOutSubagent(nil)

		assert.Empty(t, filtered)
	})
}
