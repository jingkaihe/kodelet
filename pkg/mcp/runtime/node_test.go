package runtime

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
