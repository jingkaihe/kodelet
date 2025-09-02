package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomToolManager_NewCustomToolManager(t *testing.T) {
	manager, err := NewCustomToolManager()
	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.NotEmpty(t, manager.globalDir)
	assert.NotEmpty(t, manager.localDir)
}

func TestCustomToolManager_DiscoverTools_NoDirectory(t *testing.T) {
	manager := &CustomToolManager{
		tools:     make(map[string]*CustomTool),
		globalDir: "/nonexistent/global",
		localDir:  "/nonexistent/local",
		config:    CustomToolConfig{Enabled: true},
	}

	ctx := context.Background()
	err := manager.DiscoverTools(ctx)
	assert.NoError(t, err) // Should not error when directories don't exist
	assert.Empty(t, manager.tools)
}

func TestCustomToolManager_DiscoverTools_Disabled(t *testing.T) {
	manager := &CustomToolManager{
		tools:  make(map[string]*CustomTool),
		config: CustomToolConfig{Enabled: false},
	}

	ctx := context.Background()
	err := manager.DiscoverTools(ctx)
	assert.NoError(t, err)
	assert.Empty(t, manager.tools)
}

func TestCustomToolManager_ValidateTool_Success(t *testing.T) {
	// Create a temporary executable tool
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "test_tool")

	// Create a simple shell script
	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "test_tool", "description": "A test tool", "input_schema": {"type": "object", "properties": {"message": {"type": "string"}}}}'
elif [ "$1" = "run" ]; then
    echo "Tool executed successfully"
else
    echo "Usage: test_tool [description|run]"
    exit 1
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	manager := &CustomToolManager{
		config: CustomToolConfig{
			Timeout:       30 * time.Second,
			MaxOutputSize: 1024 * 100, // 100KB
		},
	}

	ctx := context.Background()
	tool, err := manager.validateTool(ctx, toolPath)
	require.NoError(t, err)
	assert.Equal(t, "test_tool", tool.name)
	assert.Equal(t, "A test tool", tool.description)
	assert.NotNil(t, tool.schema)
}

func TestCustomToolManager_ValidateTool_InvalidJSON(t *testing.T) {
	// Create a temporary executable tool with invalid JSON
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "bad_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo 'invalid json'
else
    echo "Tool executed"
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	manager := &CustomToolManager{
		config: CustomToolConfig{
			Timeout: 5 * time.Second,
		},
	}

	ctx := context.Background()
	_, err = manager.validateTool(ctx, toolPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse tool description")
}

func TestCustomToolManager_ValidateTool_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "no_name_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"description": "A tool without name", "input_schema": {"type": "object"}}'
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	manager := &CustomToolManager{
		config: CustomToolConfig{
			Timeout: 5 * time.Second,
		},
	}

	ctx := context.Background()
	_, err = manager.validateTool(ctx, toolPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool name is required")
}

func TestCustomToolManager_ValidateTool_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "slow_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    sleep 10  # Sleep longer than timeout
    echo '{"name": "slow_tool", "description": "A slow tool"}'
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	manager := &CustomToolManager{
		config: CustomToolConfig{
			Timeout: 1 * time.Second, // Short timeout
		},
	}

	ctx := context.Background()
	_, err = manager.validateTool(ctx, toolPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to run description command")
}

func TestCustomTool_Execute_Success(t *testing.T) {
	// Create a temporary executable tool
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "echo_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "echo_tool", "description": "Echoes input", "input_schema": {"type": "object", "properties": {"message": {"type": "string"}}}}'
elif [ "$1" = "run" ]; then
    # Read JSON from stdin and echo the message
    python3 -c "
import json, sys
data = json.load(sys.stdin)
print('Echo:', data.get('message', 'No message'))
"
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	tool := &CustomTool{
		execPath:    toolPath,
		name:        "echo_tool",
		description: "Echoes input",
		timeout:     10 * time.Second,
		maxOutput:   1024 * 100, // 100KB
	}

	ctx := context.Background()
	params := `{"message": "Hello World"}`

	result := tool.Execute(ctx, nil, params)
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Echo: Hello World")
}

func TestCustomTool_Execute_CommandError(t *testing.T) {
	// Create a temporary executable tool that fails
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "failing_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "failing_tool", "description": "Always fails", "input_schema": {"type": "object"}}'
elif [ "$1" = "run" ]; then
    echo "This tool failed" >&2
    exit 1
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	tool := &CustomTool{
		execPath:    toolPath,
		name:        "failing_tool",
		description: "Always fails",
		timeout:     10 * time.Second,
		maxOutput:   1024 * 100, // 100KB
	}

	ctx := context.Background()
	params := `{}`

	result := tool.Execute(ctx, nil, params)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "This tool failed")
}

func TestCustomTool_Execute_Timeout(t *testing.T) {
	// Create a temporary executable tool that takes too long
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "slow_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "slow_tool", "description": "Takes too long", "input_schema": {"type": "object"}}'
elif [ "$1" = "run" ]; then
    sleep 10  # Sleep longer than timeout
    echo "Done"
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	tool := &CustomTool{
		execPath:    toolPath,
		name:        "slow_tool",
		description: "Takes too long",
		timeout:     1 * time.Second, // Short timeout
		maxOutput:   1024 * 100,      // 100KB
	}

	ctx := context.Background()
	params := `{}`

	result := tool.Execute(ctx, nil, params)
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "timed out")
}

func TestCustomTool_Execute_OutputTruncation(t *testing.T) {
	// Create a temporary executable tool that produces large output
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "big_output_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "big_output_tool", "description": "Produces large output", "input_schema": {"type": "object"}}'
elif [ "$1" = "run" ]; then
    # Generate more than maxOutput bytes
    python3 -c "print('x' * 200)"
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	tool := &CustomTool{
		execPath:    toolPath,
		name:        "big_output_tool",
		description: "Produces large output",
		timeout:     10 * time.Second,
		maxOutput:   100, // Only 100 bytes
	}

	ctx := context.Background()
	params := `{}`

	result := tool.Execute(ctx, nil, params)
	assert.False(t, result.IsError())

	// Output should be truncated to maxOutput
	output := result.GetResult()
	assert.Contains(t, output, "[TRUNCATED - Output exceeded 100KB limit]")
}

func TestCustomTool_Execute_JSONError(t *testing.T) {
	// Create a tool that returns a JSON error
	tmpDir := t.TempDir()
	toolPath := filepath.Join(tmpDir, "json_error_tool")

	toolScript := `#!/bin/bash
if [ "$1" = "description" ]; then
    echo '{"name": "json_error_tool", "description": "Returns JSON error", "input_schema": {"type": "object"}}'
elif [ "$1" = "run" ]; then
    echo '{"error": "Something went wrong in the tool"}'
fi
`

	err := os.WriteFile(toolPath, []byte(toolScript), 0755)
	require.NoError(t, err)

	tool := &CustomTool{
		execPath:    toolPath,
		name:        "json_error_tool",
		description: "Returns JSON error",
		timeout:     10 * time.Second,
		maxOutput:   1024 * 100, // 100KB
	}

	ctx := context.Background()
	params := `{}`

	result := tool.Execute(ctx, nil, params)
	assert.True(t, result.IsError())
	assert.Equal(t, "Something went wrong in the tool", result.GetError())
}

func TestCustomTool_InterfaceMethods(t *testing.T) {
	tool := &CustomTool{
		name:        "test_tool",
		description: "A test tool",
		execPath:    "/path/to/tool",
	}

	// Test Name() - should have prefix
	assert.Equal(t, "custom_tool_test_tool", tool.Name())

	// Test Description()
	assert.Equal(t, "A test tool", tool.Description())

	// Test TracingKVs()
	kvs, err := tool.TracingKVs("{}")
	assert.NoError(t, err)
	assert.Len(t, kvs, 3)

	// Test ValidateInput() with valid JSON
	err = tool.ValidateInput(nil, `{"key": "value"}`)
	assert.NoError(t, err)

	// Test ValidateInput() with invalid JSON
	err = tool.ValidateInput(nil, `invalid json`)
	assert.Error(t, err)
}

func TestCustomToolResult_Methods(t *testing.T) {
	// Test successful result
	result := &CustomToolResult{
		toolName:      "custom_tool_test",
		executionTime: 100 * time.Millisecond,
		result:        "Success output",
		err:           "",
	}

	assert.Equal(t, "Success output", result.GetResult())
	assert.Equal(t, "", result.GetError())
	assert.False(t, result.IsError())

	assistantFacing := result.AssistantFacing()
	assert.Contains(t, assistantFacing, "Success output")

	structured := result.StructuredData()
	assert.Equal(t, "custom_tool_test", structured.ToolName)
	assert.True(t, structured.Success)
	assert.NotNil(t, structured.Metadata)

	// Test error result
	errorResult := &CustomToolResult{
		toolName:      "custom_tool_test",
		executionTime: 50 * time.Millisecond,
		result:        "",
		err:           "Command failed",
	}

	assert.Equal(t, "", errorResult.GetResult())
	assert.Equal(t, "Command failed", errorResult.GetError())
	assert.True(t, errorResult.IsError())

	assistantFacing = errorResult.AssistantFacing()
	assert.Contains(t, assistantFacing, "Command failed")

	structured = errorResult.StructuredData()
	assert.Equal(t, "custom_tool_test", structured.ToolName)
	assert.False(t, structured.Success)
	assert.Equal(t, "Command failed", structured.Error)
}

func TestCustomToolManager_GetTool(t *testing.T) {
	manager := &CustomToolManager{
		tools: map[string]*CustomTool{
			"test_tool": {
				name:        "test_tool",
				description: "A test tool",
			},
		},
	}

	// Test getting existing tool
	tool, exists := manager.GetTool("test_tool")
	assert.True(t, exists)
	assert.Equal(t, "test_tool", tool.name)

	// Test getting tool with prefix
	tool, exists = manager.GetTool("custom_tool_test_tool")
	assert.True(t, exists)
	assert.Equal(t, "test_tool", tool.name)

	// Test getting non-existent tool
	_, exists = manager.GetTool("nonexistent")
	assert.False(t, exists)
}

func TestCustomToolManager_ListTools(t *testing.T) {
	manager := &CustomToolManager{
		tools: map[string]*CustomTool{
			"tool1": {name: "tool1"},
			"tool2": {name: "tool2"},
		},
	}

	tools := manager.ListTools()
	assert.Len(t, tools, 2)

	// Should return all tools as tooltypes.Tool interface
	for _, tool := range tools {
		assert.Implements(t, (*tooltypes.Tool)(nil), tool)
	}
}

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// Test expansion
	expanded := expandHomePath("~/.kodelet/tools")
	expected := filepath.Join(home, ".kodelet/tools")
	assert.Equal(t, expected, expanded)

	// Test no expansion needed
	path := "/absolute/path"
	expanded = expandHomePath(path)
	assert.Equal(t, path, expanded)
}

func TestLoadCustomToolConfig(t *testing.T) {
	config := loadCustomToolConfig()

	// Check defaults
	assert.True(t, config.Enabled)
	assert.Equal(t, "~/.kodelet/tools", config.GlobalDir)
	assert.Equal(t, "./kodelet-tools", config.LocalDir)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, 102400, config.MaxOutputSize) // 100KB
}
