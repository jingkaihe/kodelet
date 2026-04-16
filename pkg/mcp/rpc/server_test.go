package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMCPServerEnv = "KODELET_TEST_MCP_SERVER"

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

func TestMCPRPCServer_Shutdown(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

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
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
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
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
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
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

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
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

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
