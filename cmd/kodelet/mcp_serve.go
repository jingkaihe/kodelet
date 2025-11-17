package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/mcp/rpc"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type MCPServeConfig struct {
	SocketPath string
}

func NewMCPServeConfig() *MCPServeConfig {
	return &MCPServeConfig{
		SocketPath: ".kodelet/mcp.sock",
	}
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a standalone MCP RPC server",
	Long: `Start a standalone MCP (Model Context Protocol) RPC server that listens on a Unix socket.

This server allows code execution environments to call MCP tools via RPC over a Unix socket.
The server will continue running until interrupted with Ctrl+C.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		config := getMCPServeConfigFromFlags(cmd)
		runMCPServeCommand(ctx, config)
	},
}

func init() {
	defaults := NewMCPServeConfig()
	mcpServeCmd.Flags().String("socket", defaults.SocketPath, "Path to Unix socket for RPC communication")
}

func getMCPServeConfigFromFlags(cmd *cobra.Command) *MCPServeConfig {
	config := NewMCPServeConfig()

	if socketPath, err := cmd.Flags().GetString("socket"); err == nil {
		config.SocketPath = socketPath
	}

	// Allow override from viper config
	if viper.IsSet("mcp.code_execution.socket_path") {
		config.SocketPath = viper.GetString("mcp.code_execution.socket_path")
	}

	return config
}

func validateMCPServeConfig(config *MCPServeConfig) error {
	if config.SocketPath == "" {
		return errors.New("socket path cannot be empty")
	}

	// Ensure the directory exists
	dir := filepath.Dir(config.SocketPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create socket directory")
	}

	return nil
}

func runMCPServeCommand(ctx context.Context, config *MCPServeConfig) {
	if err := validateMCPServeConfig(config); err != nil {
		presenter.Error(err, "invalid MCP server configuration")
		os.Exit(1)
	}

	// Convert to absolute path
	absSocketPath, err := filepath.Abs(config.SocketPath)
	if err != nil {
		presenter.Error(err, "failed to resolve socket path")
		os.Exit(1)
	}

	logger.G(ctx).WithField("socket", absSocketPath).Info("Starting MCP RPC server")

	// Create MCP manager
	mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
	if err != nil {
		presenter.Error(err, "failed to create MCP manager")
		os.Exit(1)
	}

	if mcpManager == nil {
		presenter.Error(errors.New("no MCP tools configured"), "Please configure MCP servers in your config file")
		os.Exit(1)
	}

	// Create RPC server
	rpcServer, err := rpc.NewMCPRPCServer(mcpManager, absSocketPath)
	if err != nil {
		presenter.Error(err, "failed to create MCP RPC server")
		os.Exit(1)
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- rpcServer.Start(ctx)
	}()

	presenter.Success("MCP RPC server started successfully")
	presenter.Info("Socket: " + absSocketPath)
	presenter.Info("Press Ctrl+C to stop the server")

	// Wait for server error or context cancellation
	select {
	case err := <-serverErr:
		if err != nil {
			logger.G(ctx).WithError(err).Error("MCP RPC server error")
			presenter.Error(err, "MCP RPC server failed")
			os.Exit(1)
		}
	case <-ctx.Done():
		presenter.Info("Shutdown signal received, stopping server...")
		// Graceful shutdown with timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := rpcServer.Shutdown(shutdownCtx); err != nil {
			logger.G(ctx).WithError(err).Error("failed to shutdown MCP RPC server gracefully")
			presenter.Error(err, "server shutdown error")
			os.Exit(1)
		}
	}

	presenter.Info("MCP RPC server stopped")
}
