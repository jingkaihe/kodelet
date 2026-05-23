package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMCPServeConfigFromFlags(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	cmd := &cobra.Command{Use: "serve"}
	cmd.Flags().String("socket", "/tmp/from-flag.sock", "")

	config := getMCPServeConfigFromFlags(cmd)
	assert.Equal(t, "/tmp/from-flag.sock", config.SocketPath)

	viper.Set("mcp.code_execution.socket_path", "/tmp/from-viper.sock")
	config = getMCPServeConfigFromFlags(cmd)
	assert.Equal(t, "/tmp/from-viper.sock", config.SocketPath)
}

func TestValidateMCPServeConfig(t *testing.T) {
	t.Run("empty socket", func(t *testing.T) {
		err := validateMCPServeConfig(&MCPServeConfig{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "socket path cannot be empty")
	})

	t.Run("creates socket directory", func(t *testing.T) {
		socketPath := filepath.Join(t.TempDir(), "nested", "server.sock")
		require.NoError(t, validateMCPServeConfig(&MCPServeConfig{SocketPath: socketPath}))

		info, err := os.Stat(filepath.Dir(socketPath))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}
