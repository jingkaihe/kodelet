package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestNewMCPManager(t *testing.T) {
	// Test with empty config
	t.Run("empty config", func(t *testing.T) {
		config := MCPConfig{
			Servers: make(map[string]MCPServerConfig),
		}
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)
		assert.NotNil(t, manager)
		assert.Empty(t, manager.clients)
		assert.Empty(t, manager.whiteList)
	})

	// Test with valid SSE config
	t.Run("valid sse config", func(t *testing.T) {
		config := MCPConfig{
			Servers: map[string]MCPServerConfig{
				"test-sse": {
					ServerType: MCPServerTypeSSE,
					BaseURL:    "http://example.com/sse",
					Headers: map[string]string{
						"Authorization": "Bearer test-token",
					},
					ToolWhiteList: []string{"tool1", "tool2"},
				},
			},
		}

		// This will fail because it tries to create an actual SSE client
		// In a real test, you would mock the transport layer
		_, err := NewMCPManager(config)
		assert.NoError(t, err)
	})

	// Test with invalid configuration
	t.Run("invalid config", func(t *testing.T) {
		config := MCPConfig{
			Servers: map[string]MCPServerConfig{
				"invalid": {
					ServerType: "invalid-type",
				},
			},
		}
		manager, err := NewMCPManager(config)
		assert.Error(t, err)
		assert.Nil(t, manager)
	})

	// Test with missing required fields
	t.Run("missing required fields", func(t *testing.T) {
		config := MCPConfig{
			Servers: map[string]MCPServerConfig{
				"missing-url": {
					ServerType: MCPServerTypeSSE,
					// Missing BaseURL
				},
				"missing-command": {
					ServerType: MCPServerTypeStdio,
					// Missing Command
				},
			},
		}

		manager, err := NewMCPManager(config)
		assert.Error(t, err)
		assert.Nil(t, manager)
	})
}

var (
	goldenMCPConfig = MCPConfig{
		Servers: map[string]MCPServerConfig{
			"filesystem": {
				Command: "docker",
				Args: []string{
					"run",
					"-i",
					"--rm",
					"mcp/filesystem",
					"/",
				},
				ToolWhiteList: []string{"list_directory"},
			},
			"time": {
				Command: "docker",
				Args: []string{
					"run",
					"-i",
					"--rm",
					"mcp/time",
				},
				ToolWhiteList: []string{"get_current_time", "convert_time"},
			},
		},
	}
)

func TestMCPManager_Initialize(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}
	// Test with empty config
	t.Run("empty config", func(t *testing.T) {
		config := MCPConfig{
			Servers: make(map[string]MCPServerConfig),
		}
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)
		assert.NotNil(t, manager)
		assert.Empty(t, manager.clients)
		assert.Empty(t, manager.whiteList)
	})

	t.Run("valid config", func(t *testing.T) {
		config := goldenMCPConfig
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)

		err = manager.Initialize(context.Background())
		assert.NoError(t, err)

		defer manager.Close(context.Background())
	})
}

func TestMCPManager_ListMCPTools(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}

	t.Run("empty config", func(t *testing.T) {
		config := MCPConfig{
			Servers: make(map[string]MCPServerConfig),
		}
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)

		err = manager.Initialize(context.Background())
		assert.NoError(t, err)

		defer manager.Close(context.Background())

		tools, err := manager.ListMCPTools(context.Background())
		assert.NoError(t, err)
		assert.Empty(t, tools)
	})

	t.Run("valid config", func(t *testing.T) {
		config := goldenMCPConfig
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)

		err = manager.Initialize(context.Background())
		assert.NoError(t, err)

		defer manager.Close(context.Background())

		tools, err := manager.ListMCPTools(context.Background())
		assert.NoError(t, err)
		assert.NotEmpty(t, tools)

		var toolNames []string
		for _, tool := range tools {
			toolNames = append(toolNames, tool.Name())
		}

		assert.ElementsMatch(t, []string{"mcp_list_directory", "mcp_get_current_time", "mcp_convert_time"}, toolNames)
	})
}

func TestMCPTool_GenerateSchema(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}
	t.Run("valid config", func(t *testing.T) {
		config := goldenMCPConfig
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)

		err = manager.Initialize(context.Background())
		assert.NoError(t, err)

		defer manager.Close(context.Background())

		tools, err := manager.ListMCPTools(context.Background())
		assert.NoError(t, err)
		assert.NotEmpty(t, tools)

		for _, tool := range tools {
			schema := tool.GenerateSchema()
			getJSON, err := json.Marshal(schema)
			assert.NoError(t, err)

			expectedSchema, err := json.Marshal(tool.mcpToolInputSchema)
			assert.NoError(t, err)

			assert.JSONEq(t, string(expectedSchema), string(getJSON))
		}
	})
}

func TestMCPTool_Execute(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}

	config := goldenMCPConfig
	manager, err := NewMCPManager(config)
	assert.NoError(t, err)

	err = manager.Initialize(context.Background())
	assert.NoError(t, err)

	defer manager.Close(context.Background())

	tools, err := manager.ListMCPTools(context.Background())
	assert.NoError(t, err)
	assert.NotEmpty(t, tools)

	var listTool tooltypes.Tool
	for _, tool := range tools {
		if tool.Name() == "mcp_list_directory" {
			listTool = &tool
			break
		}
	}
	assert.NotNil(t, listTool)

	executeResult := listTool.Execute(context.Background(), NewBasicState(context.Background()), `{"path": "/"}`)
	assert.NoError(t, err)
	assert.NotNil(t, executeResult)

	assert.Equal(t, executeResult.Error, "")
	assert.Contains(t, executeResult.String(), "<result>")
	assert.NotContains(t, executeResult.String(), "<error>")
}
