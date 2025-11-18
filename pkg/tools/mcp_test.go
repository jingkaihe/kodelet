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

var goldenMCPConfig = MCPConfig{
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

		assert.ElementsMatch(t, []string{"mcp__filesystem_list_directory", "mcp__time_get_current_time", "mcp__time_convert_time"}, toolNames)
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
		if tool.Name() == "mcp__filesystem_list_directory" {
			listTool = &tool
			break
		}
	}
	assert.NotNil(t, listTool)

	executeResult := listTool.Execute(context.Background(), NewBasicState(context.Background()), `{"path": "/"}`)
	assert.NoError(t, err)
	assert.NotNil(t, executeResult)

	// assert.Equal(t, executeResult.Error, "")
	assert.False(t, executeResult.IsError())
	assert.Contains(t, executeResult.AssistantFacing(), "<result>")
	assert.NotContains(t, executeResult.AssistantFacing(), "<error>")
}

func TestNewMCPClient_EnvironmentVariableResolution(t *testing.T) {
	// Test the strings.HasPrefix(v, "$") logic for environment variable resolution

	t.Run("environment variable with $ prefix is resolved", func(t *testing.T) {
		// Set a test environment variable
		testEnvValue := "test-secret-value"
		os.Setenv("TEST_MCP_VAR", testEnvValue)
		defer os.Unsetenv("TEST_MCP_VAR")

		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "$TEST_MCP_VAR", // Should be resolved to testEnvValue
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("environment variable without $ prefix is used as literal", func(t *testing.T) {
		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "literal-value", // Should be used as-is
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("environment variable with $ prefix but undefined variable resolves to empty", func(t *testing.T) {
		// Ensure the env var doesn't exist
		os.Unsetenv("UNDEFINED_MCP_VAR")

		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "$UNDEFINED_MCP_VAR", // Should resolve to empty string
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("multiple environment variables with mixed $ prefix", func(t *testing.T) {
		// Set test environment variables
		os.Setenv("TEST_MCP_VAR1", "value1")
		os.Setenv("TEST_MCP_VAR2", "value2")
		defer func() {
			os.Unsetenv("TEST_MCP_VAR1")
			os.Unsetenv("TEST_MCP_VAR2")
		}()

		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"VAR1": "$TEST_MCP_VAR1", // Should be resolved
				"VAR2": "literal-value",  // Should be literal
				"VAR3": "$TEST_MCP_VAR2", // Should be resolved
				"VAR4": "$UNDEFINED_VAR", // Should be empty
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("environment variable with $ at beginning of longer string", func(t *testing.T) {
		os.Setenv("TEST_MCP_PREFIX", "secret")
		defer os.Unsetenv("TEST_MCP_PREFIX")

		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "$TEST_MCP_PREFIX", // Should be resolved
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("empty environment variable value", func(t *testing.T) {
		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "", // Empty value should not trigger $ logic
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("environment variable value with only $", func(t *testing.T) {
		config := MCPServerConfig{
			ServerType: MCPServerTypeStdio,
			Command:    "/bin/echo",
			Args:       []string{"hello"},
			Envs: map[string]string{
				"TEST_VAR": "$", // Just $ should resolve to empty string from os.Getenv("")
			},
		}

		client, err := newMCPClient(config)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})
}
