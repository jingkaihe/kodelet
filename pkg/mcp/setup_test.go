package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultWorkspaceDir(t *testing.T) {
	projectDir := t.TempDir()

	workspaceDir, err := DefaultWorkspaceDir(projectDir)
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(workspaceDir, filepath.Join(homeDir, ".kodelet", "mcp", "cache")+string(filepath.Separator)))
	assert.Len(t, filepath.Base(workspaceDir), shortHashLength)
}

func TestResolveWorkspaceDir_WithOverride(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("mcp.code_execution.workspace_dir", "./custom-mcp-cache")

	workspaceDir, err := ResolveWorkspaceDir("")
	require.NoError(t, err)

	expected, err := filepath.Abs("./custom-mcp-cache")
	require.NoError(t, err)
	assert.Equal(t, expected, workspaceDir)
}

func TestGetSocketPath_DefaultUsesTempDirAndShortHash(t *testing.T) {
	t.Cleanup(viper.Reset)

	socketPath, err := GetSocketPath("20260305T220000-1234567890abcdef")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(os.TempDir(), "mcp-"+shortHash("20260305T220000-1234567890abcdef")+".sock"), socketPath)
	assert.LessOrEqual(t, len(socketPath), len(filepath.Join(os.TempDir(), "mcp-")+strings.Repeat("a", shortHashLength)+".sock"))
}

func TestGetStandaloneSocketPath_DefaultUsesWorkspaceDir(t *testing.T) {
	t.Cleanup(viper.Reset)

	projectDir := t.TempDir()
	socketPath, err := GetStandaloneSocketPath(projectDir)
	require.NoError(t, err)

	workspaceDir, err := DefaultWorkspaceDir(projectDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(workspaceDir, standaloneSocketFilename), socketPath)
}

func TestGetStandaloneSocketPath_WithOverride(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("mcp.code_execution.socket_path", "./custom-mcp.sock")

	socketPath, err := GetStandaloneSocketPath("")
	require.NoError(t, err)

	expected, err := filepath.Abs("./custom-mcp.sock")
	require.NoError(t, err)
	assert.Equal(t, expected, socketPath)
}

func TestResolveRPCTransport_DefaultsToUnix(t *testing.T) {
	t.Cleanup(viper.Reset)

	assert.Equal(t, rpcTransportUnix, resolveRPCTransport())
}

func TestResolveRPCTransport_NormalizesConfiguredValue(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("mcp.code_execution.rpc_transport", " HTTP ")

	assert.Equal(t, rpcTransportHTTP, resolveRPCTransport())
}

func TestNewBearerToken(t *testing.T) {
	token, err := newBearerToken()
	require.NoError(t, err)

	assert.NotEmpty(t, token)
	assert.NotContains(t, token, "=")
}

func TestShortHashAndFileExists(t *testing.T) {
	hash := shortHash("project")
	assert.Len(t, hash, shortHashLength)
	assert.Equal(t, hash, shortHash("project"))
	assert.NotEqual(t, hash, shortHash("other-project"))

	path := filepath.Join(t.TempDir(), "client.ts")
	assert.False(t, fileExists(path))
	require.NoError(t, os.WriteFile(path, []byte("export {}"), 0o644))
	assert.True(t, fileExists(path))
}

func TestDefaultWorkspaceDirUsesWorkingDirectoryWhenProjectDirEmpty(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(project))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldCWD))
	})

	workspaceDir, err := DefaultWorkspaceDir("   ")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kodelet", "mcp", "cache", shortHash(project)), workspaceDir)
}

func TestSocketPathOverrideAndSetupExecutionModeEarlyBranches(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("mcp.code_execution.socket_path", "./override.sock")

	socketPath, err := GetSocketPath("session")
	require.NoError(t, err)
	expected, err := filepath.Abs("./override.sock")
	require.NoError(t, err)
	assert.Equal(t, expected, socketPath)

	viper.Set("mcp.execution_mode", "direct")
	setup, err := SetupExecutionMode(context.Background(), nil, "session", t.TempDir())
	assert.Nil(t, setup)
	require.ErrorIs(t, err, ErrDirectMode)
}

func TestSetupExecutionModeRejectsUnsupportedTransportAfterWorkspaceSetup(t *testing.T) {
	t.Cleanup(viper.Reset)
	workspace := t.TempDir()
	viper.Set("mcp.execution_mode", "code")
	viper.Set("mcp.code_execution.workspace_dir", workspace)
	viper.Set("mcp.code_execution.regenerate_on_startup", false)
	viper.Set("mcp.code_execution.rpc_transport", "pipe")
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "client.ts"), []byte("export {}"), 0o644))

	setup, err := SetupExecutionMode(context.Background(), nil, "session", t.TempDir())

	assert.Nil(t, setup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported MCP RPC transport "pipe"`)
}
