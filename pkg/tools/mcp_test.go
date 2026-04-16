package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMCPServerEnv = "KODELET_TEST_MCP_SERVER"

func maybeServeTestMCPServer() bool {
	serverKind := os.Getenv(testMCPServerEnv)
	if serverKind == "" {
		return false
	}

	mcpServer := server.NewMCPServer("test-"+serverKind, "1.0.0")

	switch serverKind {
	case "filesystem":
		mcpServer.AddTool(
			mcp.NewTool("list_directory", mcp.WithString("path", mcp.Required())),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				path, _ := req.GetArguments()["path"].(string)
				if path == "" {
					return mcp.NewToolResultError("path is required"), nil
				}

				entries, err := os.ReadDir(path)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}

				lines := make([]string, 0, len(entries))
				for _, entry := range entries {
					prefix := "[FILE]"
					if entry.IsDir() {
						prefix = "[DIR]"
					}
					lines = append(lines, prefix+" "+entry.Name())
				}

				return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
			},
		)
	case "time":
		mcpServer.AddTool(
			mcp.NewTool("get_current_time", mcp.WithString("timezone", mcp.Required())),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				timezone, _ := req.GetArguments()["timezone"].(string)
				if timezone == "" {
					return mcp.NewToolResultError("timezone is required"), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("current time in %s is 2024-01-01T00:00:00Z", timezone)), nil
			},
		)

		mcpServer.AddTool(
			mcp.NewTool(
				"convert_time",
				mcp.WithString("source_timezone", mcp.Required()),
				mcp.WithString("time", mcp.Required()),
				mcp.WithString("target_timezone", mcp.Required()),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := req.GetArguments()
				sourceTimezone, _ := args["source_timezone"].(string)
				timeValue, _ := args["time"].(string)
				targetTimezone, _ := args["target_timezone"].(string)
				if sourceTimezone == "" || timeValue == "" || targetTimezone == "" {
					return mcp.NewToolResultError("source_timezone, time, and target_timezone are required"), nil
				}

				return mcp.NewToolResultText(fmt.Sprintf("%s in %s is %s in %s", timeValue, sourceTimezone, timeValue, targetTimezone)), nil
			},
		)
	default:
		fmt.Fprintf(os.Stderr, "unknown test MCP server %q\n", serverKind)
		os.Exit(1)
	}

	if err := server.ServeStdio(mcpServer); err != nil {
		fmt.Fprintf(os.Stderr, "failed to serve test MCP server: %v\n", err)
		os.Exit(1)
	}

	return true
}

func goldenMCPConfig(t *testing.T) MCPConfig {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)

	return MCPConfig{
		Servers: map[string]MCPServerConfig{
			"filesystem": {
				Command:       exe,
				Envs:          map[string]string{testMCPServerEnv: "filesystem"},
				ToolWhiteList: []string{"list_directory"},
			},
			"time": {
				Command:       exe,
				Envs:          map[string]string{testMCPServerEnv: "time"},
				ToolWhiteList: []string{"get_current_time", "convert_time"},
			},
		},
	}
}

func newStreamableHTTPTestServer(t *testing.T) string {
	t.Helper()

	mcpServer := server.NewMCPServer("test-http-server", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("get_current_time", mcp.WithDescription("Get the current time")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("2024-01-01T00:00:00Z"), nil
		},
	)

	testServer := server.NewTestStreamableHTTPServer(mcpServer)
	t.Cleanup(testServer.Close)

	return testServer.URL
}

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

	// Test with valid streamable HTTP config
	t.Run("valid http config", func(t *testing.T) {
		config := MCPConfig{
			Servers: map[string]MCPServerConfig{
				"test-http": {
					ServerType: MCPServerTypeHTTP,
					BaseURL:    "http://example.com/mcp",
					Headers: map[string]string{
						"Authorization": "Bearer test-token",
					},
					ToolWhiteList: []string{"tool1", "tool2"},
				},
			},
		}

		_, err := NewMCPManager(config)
		assert.NoError(t, err)
	})

	t.Run("valid streamable http alias config", func(t *testing.T) {
		config := MCPConfig{
			Servers: map[string]MCPServerConfig{
				"test-http": {
					ServerType: "streamable_http",
					BaseURL:    "http://example.com/mcp",
				},
			},
		}

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

func TestMCPManager_Initialize(t *testing.T) {
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
		config := goldenMCPConfig(t)
		manager, err := NewMCPManager(config)
		assert.NoError(t, err)

		err = manager.Initialize(context.Background())
		assert.NoError(t, err)

		defer manager.Close(context.Background())
	})
}

func TestMCPManager_ListMCPTools(t *testing.T) {
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
		config := goldenMCPConfig(t)
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
	t.Run("valid config", func(t *testing.T) {
		config := goldenMCPConfig(t)
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
	allowedDir := t.TempDir()
	config := goldenMCPConfig(t)
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

	executeResult := listTool.Execute(context.Background(), NewBasicState(context.Background()), `{"path": "`+allowedDir+`"}`)
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

func TestMCPManager_Clone(t *testing.T) {
	original := &MCPManager{
		clients: map[string]*client.Client{
			"example": nil,
		},
		whiteList: map[string][]string{
			"example": {"tool-a", "tool-b"},
		},
		owned: map[string]bool{
			"example": true,
		},
	}

	cloned := original.Clone()

	assert.NotNil(t, cloned)
	assert.NotSame(t, original, cloned)
	assert.Equal(t, original.clients, cloned.clients)
	assert.Equal(t, original.whiteList, cloned.whiteList)
	assert.False(t, cloned.owned["example"])

	cloned.whiteList["example"][0] = "changed"
	assert.Equal(t, "tool-a", original.whiteList["example"][0])
}

type noopTransport struct {
	closeCalls atomic.Int32
}

func (t *noopTransport) Start(context.Context) error {
	return nil
}

func (t *noopTransport) SendRequest(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	return nil, nil //nolint:nilnil // stub implementation for testing
}

func (t *noopTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error {
	return nil
}

func (t *noopTransport) SetNotificationHandler(func(mcp.JSONRPCNotification)) {}

func (t *noopTransport) Close() error {
	t.closeCalls.Add(1)
	return nil
}

func (t *noopTransport) GetSessionId() string { //nolint:revive,staticcheck // method name defined by mcp-go transport interface
	return ""
}

func TestMCPManager_CloseOnlyClosesOwnedClients(t *testing.T) {
	sharedTransport := &noopTransport{}
	sessionTransport := &noopTransport{}

	configured := &MCPManager{
		clients: map[string]*client.Client{
			"configured": client.NewClient(sharedTransport),
		},
		whiteList: map[string][]string{
			"configured": nil,
		},
		owned: map[string]bool{
			"configured": true,
		},
	}

	sessionOnly := &MCPManager{
		clients: map[string]*client.Client{
			"session": client.NewClient(sessionTransport),
		},
		whiteList: map[string][]string{
			"session": nil,
		},
		owned: map[string]bool{
			"session": true,
		},
	}

	combined := configured.Clone()
	combined.Merge(sessionOnly)

	require.NoError(t, combined.Close(context.Background()))
	assert.Equal(t, int32(0), sharedTransport.closeCalls.Load())
	assert.Equal(t, int32(1), sessionTransport.closeCalls.Load())

	require.NoError(t, configured.Close(context.Background()))
	assert.Equal(t, int32(1), sharedTransport.closeCalls.Load())
}

func TestMCPManager_StreamableHTTPTransport(t *testing.T) {
	serverURL := newStreamableHTTPTestServer(t)

	config := MCPConfig{
		Servers: map[string]MCPServerConfig{
			"time": {
				ServerType:    MCPServerTypeHTTP,
				BaseURL:       serverURL,
				ToolWhiteList: []string{"get_current_time"},
			},
		},
	}

	manager, err := NewMCPManager(config)
	require.NoError(t, err)

	err = manager.Initialize(context.Background())
	require.NoError(t, err)
	defer manager.Close(context.Background())

	mcpTools, err := manager.ListMCPTools(context.Background())
	require.NoError(t, err)
	require.Len(t, mcpTools, 1)
	assert.Equal(t, "mcp__time_get_current_time", mcpTools[0].Name())

	result := (&mcpTools[0]).Execute(context.Background(), NewBasicState(context.Background()), `{}`)
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "2024-01-01T00:00:00Z")
}

func TestMCPManager_StreamableHTTPClosePreservesHeaders(t *testing.T) {
	const authHeader = "Bearer test-token"

	mcpServer := server.NewMCPServer("test-http-server", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("get_current_time", mcp.WithDescription("Get the current time")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("2024-01-01T00:00:00Z"), nil
		},
	)

	streamableHandler := server.NewStreamableHTTPServer(mcpServer)
	deleteAuthorized := make(chan struct{}, 1)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != authHeader {
			http.Error(w, "missing auth header", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			select {
			case deleteAuthorized <- struct{}{}:
			default:
			}
		}
		streamableHandler.ServeHTTP(w, r)
	}))
	defer testServer.Close()

	manager, err := NewMCPManager(MCPConfig{
		Servers: map[string]MCPServerConfig{
			"time": {
				ServerType: MCPServerTypeHTTP,
				BaseURL:    testServer.URL,
				Headers: map[string]string{
					"Authorization": authHeader,
				},
			},
		},
	})
	require.NoError(t, err)

	require.NoError(t, manager.Initialize(context.Background()))
	_, err = manager.ListMCPTools(context.Background())
	require.NoError(t, err)
	require.NoError(t, manager.Close(context.Background()))

	select {
	case <-deleteAuthorized:
	case <-time.After(2 * time.Second):
		t.Fatal("expected authenticated DELETE request during MCP manager close")
	}
}
