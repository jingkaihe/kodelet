package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMCPServerEnv = "KODELET_TEST_MCP_SERVER"

func shortSocketPath(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	if len(name) > 20 {
		name = name[:20]
	}
	return fmt.Sprintf("/tmp/%s-%d.sock", name, time.Now().UnixNano())
}

// createTestMCPManager creates an MCPManager for testing
func createTestMCPManager(t *testing.T) *tools.MCPManager {
	t.Helper()
	// Create an MCPManager with empty configuration
	manager, err := tools.NewMCPManager(tools.MCPConfig{
		Servers: map[string]tools.MCPServerConfig{},
	})
	require.NoError(t, err)
	return manager
}

func timeMCPConfig(t *testing.T) tools.MCPConfig {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)

	return tools.MCPConfig{
		Servers: map[string]tools.MCPServerConfig{
			"time": {
				Command: exe,
				Envs:    map[string]string{testMCPServerEnv: "time"},
			},
		},
	}
}

func filesystemMCPConfig(t *testing.T) tools.MCPConfig {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)

	return tools.MCPConfig{
		Servers: map[string]tools.MCPServerConfig{
			"filesystem": {
				Command:       exe,
				Envs:          map[string]string{testMCPServerEnv: "filesystem"},
				ToolWhiteList: []string{"list_directory"},
			},
		},
	}
}

type autoOAuthRPCPrompter struct {
	authURL atomic.Value
}

func (p *autoOAuthRPCPrompter) PromptMCPOAuth(ctx context.Context, _ string, authURL string) error {
	p.authURL.Store(authURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func TestNewMCPRPCServer(t *testing.T) {
	tests := []struct {
		name        string
		socketPath  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "successful creation",
			socketPath: filepath.Join(t.TempDir(), "test.sock"),
			wantErr:    false,
		},
		{
			name:        "invalid socket path",
			socketPath:  "/invalid/path/that/does/not/exist/test.sock",
			wantErr:     true,
			errContains: "failed to create unix socket listener",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := createTestMCPManager(t)
			server, err := NewMCPRPCServer(manager, tt.socketPath)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, server)
			assert.Equal(t, tt.socketPath, server.SocketPath())
			assert.NotNil(t, server.listener)
			assert.NotNil(t, server.server)
			assert.Equal(t, manager, server.mcpManager)

			// Clean up
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}
}

func TestNewMCPHTTPRPCServer(t *testing.T) {
	manager := createTestMCPManager(t)
	server, err := NewMCPHTTPRPCServer(manager, "test-token")
	require.NoError(t, err)
	require.NotNil(t, server)

	assert.Empty(t, server.SocketPath())
	assert.Equal(t, "test-token", server.BearerToken())
	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/$`, server.EndpointURL())
	assert.NotNil(t, server.listener)
	assert.NotNil(t, server.server)
	assert.Equal(t, manager, server.mcpManager)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func TestNewMCPHTTPRPCServer_RequiresBearerToken(t *testing.T) {
	manager := createTestMCPManager(t)
	server, err := NewMCPHTTPRPCServer(manager, " ")

	require.Error(t, err)
	assert.Nil(t, server)
	assert.Contains(t, err.Error(), "bearer token cannot be empty")
}

func TestMCPRPCServer_HandleMCPCall_MethodNotAllowed(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager: manager,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Contains(t, w.Body.String(), "method not allowed")
}

func TestMCPRPCServer_HandleMCPCall_InvalidJSON(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager: manager,
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMCPRPCServer_HandleMCPCall_Unauthorized(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager:  manager,
		bearerToken: "test-token",
	}

	rpcReq := MCPRPCRequest{
		Tool:      "test_tool",
		Arguments: map[string]any{},
	}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestMCPRPCServer_HandleMCPCall_Authorized(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager:  manager,
		bearerToken: "test-token",
	}

	rpcReq := MCPRPCRequest{
		Tool:      "test_tool",
		Arguments: map[string]any{},
	}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMCPRPCServer_HandleMCPCall_ToolNotFound(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager: manager,
	}

	rpcReq := MCPRPCRequest{
		Tool:      "nonexistent_tool",
		Arguments: map[string]any{},
	}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "tool not found")
}

// TestMCPRPCServer_HandleMCPCall_MCPManagerError tests error handling
// Note: With real MCPManager, this tests tool not found scenario
func TestMCPRPCServer_HandleMCPCall_EmptyManager(t *testing.T) {
	manager := createTestMCPManager(t)
	server := &MCPRPCServer{
		mcpManager: manager,
	}

	rpcReq := MCPRPCRequest{
		Tool:      "test_tool",
		Arguments: map[string]any{},
	}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	server.handleMCPCall(w, req)

	// Empty manager will have no tools, so tool not found
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMCPRPCServer_SocketPath(t *testing.T) {
	expectedPath := "/tmp/test.sock"
	server := &MCPRPCServer{
		socketPath: expectedPath,
	}

	assert.Equal(t, expectedPath, server.SocketPath())
}

func TestMCPRPCServer_EndpointURLAndBearerToken(t *testing.T) {
	server := &MCPRPCServer{
		endpointURL: "http://127.0.0.1:12345/",
		bearerToken: "test-token",
	}

	assert.Equal(t, "http://127.0.0.1:12345/", server.EndpointURL())
	assert.Equal(t, "test-token", server.BearerToken())
}

func TestMCPRPCServer_Shutdown(t *testing.T) {
	socketPath := shortSocketPath(t)

	manager := createTestMCPManager(t)
	server, err := NewMCPRPCServer(manager, socketPath)
	require.NoError(t, err)

	// Start server in background
	go func() {
		_ = server.Start(context.Background())
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify socket exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err)

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = server.Shutdown(ctx)
	assert.NoError(t, err)

	// Verify socket is removed
	_, err = os.Stat(socketPath)
	assert.True(t, os.IsNotExist(err))
}

func TestMCPRPCServer_HandleMCPCall_FullIntegration(t *testing.T) {
	config := timeMCPConfig(t)
	manager, err := tools.NewMCPManager(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = manager.Initialize(ctx)
	require.NoError(t, err)
	defer func() {
		_ = manager.Close(ctx)
	}()

	// Create RPC server
	socketPath := shortSocketPath(t)
	rpcServer, err := NewMCPRPCServer(manager, socketPath)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rpcServer.Shutdown(shutdownCtx)
	}()

	tests := []struct {
		name           string
		toolName       string
		arguments      map[string]any
		expectError    bool
		validateResult func(t *testing.T, response map[string]any)
	}{
		{
			name:     "successful tool execution - get_current_time",
			toolName: "get_current_time",
			arguments: map[string]any{
				"timezone": "UTC",
			},
			expectError: false,
			validateResult: func(t *testing.T, response map[string]any) {
				// isError may be nil/false for successful calls
				if isError, ok := response["isError"].(bool); ok {
					assert.False(t, isError)
				}
				content := response["content"].([]any)
				require.NotEmpty(t, content)
				firstContent := content[0].(map[string]any)
				assert.Equal(t, "text", firstContent["type"])
				// Should contain time information
				text := firstContent["text"].(string)
				assert.Contains(t, text, "time")
			},
		},
		{
			name:     "tool execution with invalid arguments",
			toolName: "convert_time",
			arguments: map[string]any{
				"invalid_arg": "value",
			},
			expectError: true,
			validateResult: func(t *testing.T, response map[string]any) {
				// MCP tools return errors as isError: true, not HTTP errors
				// The actual behavior depends on the tool implementation
				content := response["content"].([]any)
				require.NotEmpty(t, content)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpcReq := MCPRPCRequest{
				Server:    "time",
				Tool:      tt.toolName,
				Arguments: tt.arguments,
			}
			reqBody, err := json.Marshal(rpcReq)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
			w := httptest.NewRecorder()

			rpcServer.handleMCPCall(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

			var response map[string]any
			err = json.NewDecoder(w.Body).Decode(&response)
			require.NoError(t, err)

			tt.validateResult(t, response)
		})
	}
}

func TestMCPRPCServer_HandleMCPCall_AutoOAuthHTTPServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prompter := &autoOAuthRPCPrompter{}
	t.Cleanup(tools.SetDefaultMCPOAuthPrompter(prompter))

	const accessToken = "rpc-oauth-access-token"
	mcpServer := server.NewMCPServer("test-rpc-http-oauth-server", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("get_current_time", mcp.WithDescription("Get the current time")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("2024-01-01T00:00:00Z"), nil
		},
	)
	streamableHandler := server.NewStreamableHTTPServer(mcpServer)

	var tokenRequestCount atomic.Int32
	var testServer *httptest.Server
	testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_servers": []string{testServer.URL},
				"resource":              testServer.URL,
				"scopes_supported":      []string{"mcp.read"},
			})
			return
		case "/.well-known/openid-configuration", "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 testServer.URL,
				"authorization_endpoint": testServer.URL + "/authorize",
				"token_endpoint":         testServer.URL + "/token",
				"registration_endpoint":  testServer.URL + "/register",
			})
			return
		case "/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "registered-client"})
			return
		case "/authorize":
			redirectURI := r.URL.Query().Get("redirect_uri")
			redirectURL, err := url.Parse(redirectURI)
			require.NoError(t, err)
			query := redirectURL.Query()
			query.Set("code", "oauth-code")
			query.Set("state", r.URL.Query().Get("state"))
			query.Set("iss", testServer.URL)
			redirectURL.RawQuery = query.Encode()
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
			return
		case "/token":
			tokenRequestCount.Add(1)
			require.NoError(t, r.ParseForm())
			assert.Equal(t, testServer.URL, r.Form.Get("resource"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  accessToken,
				"token_type":    "Bearer",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
			})
			return
		}

		if r.Header.Get("Authorization") != "Bearer "+accessToken {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="mcp", resource_metadata="%s/.well-known/oauth-protected-resource", scope="mcp.read"`, testServer.URL))
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}

		streamableHandler.ServeHTTP(w, r)
	}))
	defer testServer.Close()

	manager, err := tools.NewMCPManager(tools.MCPConfig{
		Servers: map[string]tools.MCPServerConfig{
			"time": {
				ServerType:    tools.MCPServerTypeHTTP,
				URL:           testServer.URL,
				ToolWhiteList: []string{"get_current_time"},
			},
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Initialize(ctx))
	defer func() { _ = manager.Close(ctx) }()

	rpcServer := &MCPRPCServer{mcpManager: manager}
	rpcReq := MCPRPCRequest{Server: "time", Tool: "get_current_time", Arguments: map[string]any{}}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()
	rpcServer.handleMCPCall(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), tokenRequestCount.Load())
	assert.NotEmpty(t, prompter.authURL.Load())
	assert.Contains(t, w.Body.String(), "2024-01-01T00:00:00Z")
}

func TestMCPRPCServer_HandleMCPCall_ResponseFormat(t *testing.T) {
	allowedDir := t.TempDir()
	config := filesystemMCPConfig(t)
	manager, err := tools.NewMCPManager(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = manager.Initialize(ctx)
	require.NoError(t, err)
	defer func() {
		_ = manager.Close(ctx)
	}()

	// Create RPC server
	socketPath := shortSocketPath(t)
	rpcServer, err := NewMCPRPCServer(manager, socketPath)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rpcServer.Shutdown(shutdownCtx)
	}()

	// Make a real MCP call
	rpcReq := MCPRPCRequest{
		Server: "filesystem",
		Tool:   "list_directory",
		Arguments: map[string]any{
			"path": allowedDir,
		},
	}
	reqBody, err := json.Marshal(rpcReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	rpcServer.handleMCPCall(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify response structure matches MCPRPCResponse
	var response map[string]any
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	// Verify required fields exist
	_, hasContent := response["content"]
	assert.True(t, hasContent, "response should have 'content' field")

	if isError, hasIsError := response["isError"].(bool); hasIsError {
		assert.False(t, isError, "successful response should not be marked as error")
	}

	// Verify content structure
	content := response["content"].([]any)
	require.NotEmpty(t, content, "content should not be empty")

	firstContent := content[0].(map[string]any)
	assert.Contains(t, firstContent, "type", "content item should have 'type' field")
	assert.Contains(t, firstContent, "text", "content item should have 'text' field")
}

func TestNewMCPRPCServer_RemovesExistingSocket(t *testing.T) {
	socketPath := shortSocketPath(t)

	// Create a dummy socket file
	err := os.WriteFile(socketPath, []byte("dummy"), 0o644)
	require.NoError(t, err)

	// Verify it exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err)

	// Create new server (should remove existing socket)
	manager := createTestMCPManager(t)
	server, err := NewMCPRPCServer(manager, socketPath)
	require.NoError(t, err)
	require.NotNil(t, server)

	// Clean up
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func TestMCPRPCServer_ServerConfiguration(t *testing.T) {
	socketPath := shortSocketPath(t)

	manager := createTestMCPManager(t)
	server, err := NewMCPRPCServer(manager, socketPath)
	require.NoError(t, err)

	// Verify timeouts are set correctly
	assert.Equal(t, 30*time.Second, server.server.ReadTimeout)
	assert.Equal(t, 30*time.Second, server.server.WriteTimeout)
	assert.NotNil(t, server.server.Handler)

	// Clean up
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
