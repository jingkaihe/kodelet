package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{
		Name: "echo", Description: "Echo input",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
			return nil, err
		}
		cwd, _ := os.Getwd()
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "echo"},
				&mcp.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			},
			StructuredContent: map[string]any{"input": input, "cwd": cwd, "env": os.Getenv("MCP_FIXTURE_VALUE")},
			IsError:           input["fail"] == true,
		}, nil
	})
	server.AddTool(&mcp.Tool{Name: "hidden", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	return server
}

func TestMCPServerProcess(_ *testing.T) {
	if os.Getenv("KODELET_MCP_TEST_SERVER") != "1" {
		return
	}
	if err := testServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBridgeTransports(t *testing.T) {
	for _, kind := range []string{"stdio", "http", "sse"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("MCP_HEADER", "fixture-token")
			t.Setenv("MCP_FIXTURE_VALUE", "inherited")
			cwd := t.TempDir()
			config := serverConfig{Type: kind, ToolWhiteList: []string{"echo"}}
			if kind == "stdio" {
				executable, err := os.Executable()
				require.NoError(t, err)
				config.Command = executable
				config.Args = []string{"-test.run=^TestMCPServerProcess$"}
				config.Envs = map[string]string{"KODELET_MCP_TEST_SERVER": "1", "MCP_FIXTURE_VALUE": "${MCP_HEADER}"}
			} else {
				server := testServer()
				getServer := func(*http.Request) *mcp.Server { return server }
				var handler http.Handler = mcp.NewStreamableHTTPHandler(getServer, nil)
				if kind == "sse" {
					handler = mcp.NewSSEHandler(getServer, nil)
				}
				httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
					handler.ServeHTTP(w, r)
				}))
				t.Cleanup(httpServer.Close)
				config.URL = httpServer.URL
				config.Headers = map[string]string{"Authorization": "Bearer $MCP_HEADER"}
			}
			ext := extension.New(extension.Metadata{Name: "mcp"})
			closeServers := registerMCP(t.Context(), ext, mcpConfig{Servers: map[string]serverConfig{
				"fixture": config, "broken": {Type: "invalid"},
			}}, cwd, serverStartupTimeout)
			t.Cleanup(closeServers)
			host := extensiontest.New(ext, nil)
			t.Cleanup(host.Close)
			registration, err := host.Initialize(t.Context(), nil)
			require.NoError(t, err)
			require.Len(t, registration.Tools, 1)
			assert.Equal(t, "mcp__fixture_echo", registration.Tools[0].Name)
			assert.Equal(t, "mcp/fixture", registration.Tools[0].Group)
			assert.Equal(t, map[string]any{"type": "object"}, registration.Tools[0].OutputSchema)
			require.NotNil(t, registration.Tools[0].TimeoutInSec)
			assert.Equal(t, toolTimeout.Seconds(), *registration.Tools[0].TimeoutInSec)
			result, err := host.ExecuteTool(t.Context(), "mcp__fixture_echo", map[string]any{"hello": "world"})
			require.NoError(t, err)
			assert.Empty(t, result.Error)
			assert.Equal(t, "echo[image:image/png]", result.Content)
			assert.Equal(t, "mcp", result.Data["kind"])
			assert.Equal(t, "fixture", result.Data["serverName"])
			require.Len(t, result.Attachments, 1)
			assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("image")), result.Attachments[0].Data)
			structured := result.StructuredContent.(map[string]any)
			assert.Equal(t, map[string]any{"hello": "world"}, structured["input"])
			if kind == "stdio" {
				assert.Equal(t, cwd, structured["cwd"])
				assert.Equal(t, "fixture-token", structured["env"])
			}
			result, err = host.ExecuteTool(t.Context(), "mcp__fixture_echo", map[string]any{"fail": true})
			require.NoError(t, err)
			assert.Equal(t, result.Content, result.Error)
			host.Close()
			closeServers() // Session-end and process cleanup are idempotent.
		})
	}
}

func TestStartupRetainsHealthyServersWhenOthersStall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := mcpConfig{Servers: map[string]serverConfig{}}
	var started sync.WaitGroup
	started.Add(3)
	allStarted := make(chan struct{})
	go func() { started.Wait(); close(allStarted) }()
	release := make(chan struct{})
	defer close(release)
	for _, fixture := range []struct{ name, stall string }{
		{"a-connect", "initialize"}, {"b-list", "tools/list"}, {"healthy", ""},
	} {
		server := testServer()
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == fixture.stall || (fixture.stall == "" && method == "initialize") {
					started.Done()
					select {
					case <-allStarted:
					case <-ctx.Done():
						t.Error("all servers must start before any startup deadline expires")
						return nil, ctx.Err()
					case <-release:
						return nil, context.Canceled
					}
					if fixture.stall != "" {
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-release:
							return nil, context.Canceled
						}
					}
				}
				return next(ctx, method, req)
			}
		})
		getServer := func(*http.Request) *mcp.Server { return server }
		var handler http.Handler = mcp.NewStreamableHTTPHandler(getServer, nil)
		kind := "http"
		if fixture.stall != "initialize" {
			kind = "sse"
			handler = mcp.NewSSEHandler(getServer, nil)
		}
		httpServer := httptest.NewServer(handler)
		t.Cleanup(httpServer.Close)
		config.Servers[fixture.name] = serverConfig{Type: kind, URL: httpServer.URL, ToolWhiteList: []string{"echo"}}
	}
	ext := extension.New(extension.Metadata{Name: "mcp"})
	closeServers := registerMCP(t.Context(), ext, config, t.TempDir(), time.Second)
	t.Cleanup(closeServers)
	host := extensiontest.New(ext, nil)
	t.Cleanup(host.Close)
	registration, err := host.Initialize(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, registration.Tools, 1)
	assert.Equal(t, "mcp__healthy_echo", registration.Tools[0].Name)
	result, err := host.ExecuteTool(t.Context(), "mcp__healthy_echo", map[string]any{})
	require.NoError(t, err, "the healthy SSE connection must outlive its startup deadline")
	assert.Empty(t, result.Error)
	assert.Equal(t, "echo[image:image/png]", result.Content)
}

func TestLoadConfig(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KODELET_EXTENSION_WORKSPACE_CWD", cwd)
	t.Setenv("MCP_CLIENT_ID", "client")
	config, err := loadConfig(cwd)
	require.NoError(t, err)
	assert.Empty(t, config.Servers)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "mcp.json"), []byte(`{
  "oauth": {"interactive": "never", "open_browser": true, "scopes": ["global"]},
  "mcpServers": {"global": {"command": "global"}, "override": {"command": "old"}}
}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "mcp.json"), []byte(`{
  "oauth": {"open_browser": false},
  "mcpServers": {"override": {"server_type": "streamable_http", "url": "https://example.com/mcp",
    "oauth": {"client_id": "${MCP_CLIENT_ID}", "scopes": ["read"], "open_browser": true}}}
}`), 0o600))
	workspace, err := workspaceCWD()
	require.NoError(t, err)
	config, err = loadConfig(workspace)
	require.NoError(t, err)
	require.Len(t, config.Servers, 2)
	assert.Empty(t, config.Servers["override"].Command)
	kind, err := config.Servers["override"].transportType()
	require.NoError(t, err)
	assert.Equal(t, "http", kind)
	oauth, err := config.Servers["override"].oauthConfig(config.OAuth)
	require.NoError(t, err)
	assert.Equal(t, "never", oauth.Interactive)
	require.NotNil(t, oauth.OpenBrowser)
	assert.True(t, *oauth.OpenBrowser)
	assert.Equal(t, "client", oauth.ClientID)
	assert.Equal(t, []string{"read"}, oauth.Scopes)
	assert.False(t, *config.OAuth.OpenBrowser)
	assert.Equal(t, []string{"global"}, config.OAuth.Scopes)

	// Malformed user config is an error, not an empty installation.
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "mcp.json"), []byte(`{`), 0o600))
	_, err = loadConfig(cwd)
	require.ErrorContains(t, err, "decode MCP config")
}

func TestConvertResourceAndEmptyError(t *testing.T) {
	result := convertResult("server", "tool", nil, &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///test", Text: "resource"}},
		&mcp.AudioContent{Data: []byte("audio"), MIMEType: "audio/wav"},
	}}, time.Millisecond)
	assert.Contains(t, result.Content, "resource")
	assert.Contains(t, result.Content, "audio/wav")
	result = convertResult("server", "tool", nil, &mcp.CallToolResult{IsError: true}, 0)
	assert.Equal(t, "MCP tool server.tool returned an error", result.Error)
}
