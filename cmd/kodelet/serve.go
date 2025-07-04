package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/spf13/cobra"
)

// ServeConfig holds configuration for the serve command
type ServeConfig struct {
	Host string
	Port int
}

// NewServeConfig creates a new ServeConfig with default values
func NewServeConfig() *ServeConfig {
	return &ServeConfig{
		Host: "localhost",
		Port: 8080,
	}
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web UI server for viewing conversations",
	Long: `Start a local web server that provides a web interface for browsing and viewing 
your conversation history. The web UI offers an intuitive way to explore conversations
with syntax highlighting, tool result visualization, and search capabilities.

The server will be available at http://localhost:8080 by default.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getServeConfigFromFlags(cmd)
		runServeCommand(ctx, config)
	},
}

func init() {
	// Add serve command flags
	defaults := NewServeConfig()
	serveCmd.Flags().String("host", defaults.Host, "Host to bind the web server to")
	serveCmd.Flags().Int("port", defaults.Port, "Port to bind the web server to")
}

// getServeConfigFromFlags extracts serve configuration from command flags
func getServeConfigFromFlags(cmd *cobra.Command) *ServeConfig {
	config := NewServeConfig()

	if host, err := cmd.Flags().GetString("host"); err == nil {
		config.Host = host
	}
	if port, err := cmd.Flags().GetInt("port"); err == nil {
		config.Port = port
	}

	return config
}

// runServeCommand starts the web UI server
func runServeCommand(ctx context.Context, config *ServeConfig) {
	logger.G(ctx).WithFields(map[string]interface{}{
		"host": config.Host,
		"port": config.Port,
	}).Info("Starting web UI server")

	// Create server configuration
	serverConfig := &webui.ServerConfig{
		Host: config.Host,
		Port: config.Port,
	}

	// Create the web server
	server, err := webui.NewServer(serverConfig)
	if err != nil {
		presenter.Error(err, "Failed to create web server")
		os.Exit(1)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			logger.G(ctx).WithError(closeErr).Error("Failed to close web server")
		}
	}()

	// Create a context that cancels on interrupt signals
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start the server
	presenter.Success(fmt.Sprintf("Web UI server starting on http://%s:%d", config.Host, config.Port))
	presenter.Info("Press Ctrl+C to stop the server")

	// Start server and wait for shutdown
	if err := server.Start(ctx); err != nil {
		logger.G(ctx).WithError(err).Error("Web server error")
		presenter.Error(err, "Web server failed")
		os.Exit(1)
	}

	presenter.Info("Web server stopped")
}