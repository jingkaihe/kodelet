package sysprompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatMCPServers(t *testing.T) {
	t.Run("Format with MCP servers in code mode", func(t *testing.T) {
		promptCtx := NewPromptContext(nil)
		promptCtx.MCPExecutionMode = "code"
		promptCtx.MCPServers = []string{"grafana", "lsp"}

		result := promptCtx.FormatMCPServers()

		assert.Contains(t, result, "MCP Servers Available")
		assert.Contains(t, result, "grafana")
		assert.Contains(t, result, "lsp")
		assert.Contains(t, result, ".kodelet/mcp/servers/")
	})

	t.Run("Empty result when execution mode is not code", func(t *testing.T) {
		promptCtx := NewPromptContext(nil)
		promptCtx.MCPExecutionMode = "direct"
		promptCtx.MCPServers = []string{"grafana"}

		result := promptCtx.FormatMCPServers()
		assert.Empty(t, result)
	})

	t.Run("Empty result when no servers", func(t *testing.T) {
		promptCtx := NewPromptContext(nil)
		promptCtx.MCPExecutionMode = "code"
		promptCtx.MCPServers = []string{}

		result := promptCtx.FormatMCPServers()
		assert.Empty(t, result)
	})
}

func TestMCPTemplateHelpers(t *testing.T) {
	promptCtx := NewPromptContext(nil)
	promptCtx.MCPExecutionMode = "code"
	promptCtx.MCPServers = []string{"grafana", "lsp"}

	assert.True(t, promptCtx.HasMCPServers())
	assert.Equal(t, "grafana, lsp", promptCtx.MCPServersCSV())

	promptCtx.MCPExecutionMode = "direct"
	assert.False(t, promptCtx.HasMCPServers())
}

func TestLoadMCPServers(t *testing.T) {
	t.Run("Load servers from directory", func(t *testing.T) {
		// Create temporary workspace directory
		tmpDir := t.TempDir()
		serversDir := filepath.Join(tmpDir, "servers")
		require.NoError(t, os.MkdirAll(serversDir, 0o755))

		// Create grafana server directory with index.ts
		grafanaDir := filepath.Join(serversDir, "grafana")
		require.NoError(t, os.MkdirAll(grafanaDir, 0o755))

		grafanaIndex := `// index.ts - Auto-generated exports for MCP tools
export { getDatasourceByName } from './getDatasourceByName.js';
`
		require.NoError(t, os.WriteFile(filepath.Join(grafanaDir, "index.ts"), []byte(grafanaIndex), 0o644))

		// Create lsp server directory with index.ts
		lspDir := filepath.Join(serversDir, "lsp")
		require.NoError(t, os.MkdirAll(lspDir, 0o755))

		lspIndex := `// index.ts - Auto-generated exports for MCP tools
export { definition } from './definition.js';
`
		require.NoError(t, os.WriteFile(filepath.Join(lspDir, "index.ts"), []byte(lspIndex), 0o644))

		// Load servers
		servers := loadMCPServers(tmpDir)

		// Verify results - we only care about server names, not individual tools
		assert.Len(t, servers, 2)
		assert.ElementsMatch(t, []string{"grafana", "lsp"}, servers)
	})

	t.Run("Return empty list when directory doesn't exist", func(t *testing.T) {
		servers := loadMCPServers("/nonexistent/directory")
		assert.Empty(t, servers)
	})

	t.Run("Return empty list when workspace dir is empty string", func(t *testing.T) {
		servers := loadMCPServers("")
		assert.Empty(t, servers)
	})
}

func TestWithMCPConfig(t *testing.T) {
	t.Run("Set MCP config on context", func(t *testing.T) {
		// Create temporary workspace directory
		tmpDir := t.TempDir()
		serversDir := filepath.Join(tmpDir, "servers")
		require.NoError(t, os.MkdirAll(serversDir, 0o755))

		// Create a test server
		testDir := filepath.Join(serversDir, "test")
		require.NoError(t, os.MkdirAll(testDir, 0o755))

		testIndex := `export { testTool } from './testTool.js';`
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "index.ts"), []byte(testIndex), 0o644))

		promptCtx := NewPromptContext(nil)
		promptCtx.WithMCPConfig("code", tmpDir)

		assert.Equal(t, "code", promptCtx.MCPExecutionMode)
		assert.Len(t, promptCtx.MCPServers, 1)
		assert.Equal(t, "test", promptCtx.MCPServers[0])
	})

	t.Run("Don't load servers when execution mode is not code", func(t *testing.T) {
		tmpDir := t.TempDir()

		promptCtx := NewPromptContext(nil)
		promptCtx.WithMCPConfig("direct", tmpDir)

		assert.Equal(t, "direct", promptCtx.MCPExecutionMode)
		assert.Empty(t, promptCtx.MCPServers)
	})
}

func TestSystemPromptWithMCPServers(t *testing.T) {
	t.Run("Include MCP servers in system prompt", func(t *testing.T) {
		// Create temporary workspace directory
		tmpDir := t.TempDir()
		serversDir := filepath.Join(tmpDir, "servers")
		require.NoError(t, os.MkdirAll(serversDir, 0o755))

		// Create a test server
		testDir := filepath.Join(serversDir, "testserver")
		require.NoError(t, os.MkdirAll(testDir, 0o755))

		testIndex := `export { myTool } from './myTool.js';`
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "index.ts"), []byte(testIndex), 0o644))

		// Create prompt context with MCP configuration
		promptCtx := NewPromptContext(nil)
		promptCtx.WithMCPConfig("code", tmpDir)

		renderer := NewRenderer(TemplateFS)
		prompt, err := renderer.RenderSystemPrompt(promptCtx)

		require.NoError(t, err)
		assert.Contains(t, prompt, "MCP Servers Available")
		assert.Contains(t, prompt, "testserver")
		// Should NOT contain individual tool names
		assert.NotContains(t, prompt, "myTool")
	})

	t.Run("Don't include MCP servers when execution mode is not code", func(t *testing.T) {
		promptCtx := NewPromptContext(nil)
		promptCtx.WithMCPConfig("direct", ".kodelet/mcp")

		renderer := NewRenderer(TemplateFS)
		prompt, err := renderer.RenderSystemPrompt(promptCtx)

		require.NoError(t, err)
		// Should not contain MCP servers section
		assert.False(t, strings.Contains(prompt, "MCP Servers Available"))
	})
}
