package mcp

import (
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
