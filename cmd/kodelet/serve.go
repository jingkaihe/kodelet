package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ServeConfig struct {
	Host string
	Port int
}

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
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		config := getServeConfigFromFlags(cmd)
		runServeCommand(ctx, config)
	},
}

func init() {
	defaults := NewServeConfig()
	serveCmd.Flags().String("host", defaults.Host, "Host to bind the web server to")
	serveCmd.Flags().Int("port", defaults.Port, "Port to bind the web server to")
}

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

func validateServeConfig(config *ServeConfig) error {
	if config.Host == "" {
		return errors.New("host cannot be empty")
	}

	if config.Host != "localhost" && config.Host != "0.0.0.0" {
		if ip := net.ParseIP(config.Host); ip == nil {
			if strings.Contains(config.Host, " ") || strings.Contains(config.Host, ":") {
				return fmt.Errorf("invalid host: %s", config.Host)
			}
		}
	}

	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", config.Port)
	}

	if config.Port < 1024 {
		logger.G(context.Background()).WithField("port", config.Port).Warn("using privileged port (< 1024) may require elevated permissions")
	}

	return nil
}

func runServeCommand(ctx context.Context, config *ServeConfig) {
	if err := validateServeConfig(config); err != nil {
		presenter.Error(err, "invalid server configuration")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"host": config.Host,
		"port": config.Port,
	}).Info("Starting web UI server")

	serverConfig := &webui.ServerConfig{
		Host: config.Host,
		Port: config.Port,
	}

	server, err := webui.NewServer(ctx, serverConfig)
	if err != nil {
		presenter.Error(err, "failed to create web server")
		os.Exit(1)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			logger.G(ctx).WithError(closeErr).Error("failed to close web server")
		}
	}()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	presenter.Success(fmt.Sprintf("Web UI server starting on http://%s:%d", config.Host, config.Port))
	presenter.Info("Press Ctrl+C to stop the server")

	if err := server.Start(ctx); err != nil {
		logger.G(ctx).WithError(err).Error("web server error")
		presenter.Error(err, "web server failed")
		os.Exit(1)
	}

	presenter.Info("Web server stopped")
}
