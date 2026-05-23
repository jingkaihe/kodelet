package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeRuntimeEnv_UnixTransport(t *testing.T) {
	t.Setenv("MCP_RPC_URL", "http://stale")
	t.Setenv("MCP_RPC_TOKEN", "stale-token")

	runtime := NewNodeRuntime("/workspace", "/tmp/kodelet.sock")
	env := runtime.env()

	assert.Contains(t, env, "MCP_RPC_TRANSPORT=unix")
	assert.Contains(t, env, "MCP_RPC_SOCKET=/tmp/kodelet.sock")
	assert.NotContains(t, env, "MCP_RPC_URL=http://stale")
	assert.NotContains(t, env, "MCP_RPC_TOKEN=stale-token")
}

func TestNodeRuntimeEnv_HTTPTransport(t *testing.T) {
	runtime := NewNodeRuntimeWithRPC("/workspace", "http", "", "http://127.0.0.1:12345/", "test-token")
	env := runtime.env()

	assert.Contains(t, env, "MCP_RPC_TRANSPORT=http")
	assert.Contains(t, env, "MCP_RPC_URL=http://127.0.0.1:12345/")
	assert.Contains(t, env, "MCP_RPC_TOKEN=test-token")
	assert.False(t, containsPrefix(env, "MCP_RPC_SOCKET="))
}

func TestNodeRuntimeIdentityAndAvailability(t *testing.T) {
	runtime := NewNodeRuntimeWithRPC("/workspace", "", "", "", "")

	assert.Equal(t, "/workspace", runtime.WorkspaceDir())
	assert.Equal(t, "node-tsx", runtime.Name())

	err := CheckAvailability(context.Background())
	if err != nil {
		assert.Contains(t, err.Error(), "Node.js/tsx is not available")
	}
}

func TestNodeRuntimeExecuteMissingScriptReturnsOutputAndError(t *testing.T) {
	if err := CheckAvailability(context.Background()); err != nil {
		t.Skip(err)
	}

	runtime := NewNodeRuntimeWithRPC(t.TempDir(), "unix", "/tmp/test.sock", "", "")
	output, err := runtime.Execute(context.Background(), "missing.ts")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "code execution failed")
	assert.NotEmpty(t, output)
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
