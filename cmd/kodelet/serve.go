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
	Host               string
	Port               int
	CWD                string
	CompactRatio       float64
	DisableAutoCompact bool
}

func NewServeConfig() *ServeConfig {
	return &ServeConfig{
		Host:               "localhost",
		Port:               8080,
		CWD:                "",
		CompactRatio:       0.8,
		DisableAutoCompact: false,
	}
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web UI server for chatting with kodelet",
	Long: `Start a local web server that provides an interactive chat interface for kodelet.
The web UI lets you continue conversations, inspect tool activity, and browse recent
chat history from the browser while still using the same embedded assets in the binary.

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
	serveCmd.Flags().String("cwd", defaults.CWD, "Default working directory for new web conversations")
	serveCmd.Flags().Float64("compact-ratio", defaults.CompactRatio, "Context window utilization ratio to trigger auto-compact (0.0-1.0)")
	serveCmd.Flags().Bool("disable-auto-compact", defaults.DisableAutoCompact, "Disable auto-compact functionality")
}

func getServeConfigFromFlags(cmd *cobra.Command) *ServeConfig {
	config := NewServeConfig()

	if host, err := cmd.Flags().GetString("host"); err == nil {
		config.Host = host
	}
	if port, err := cmd.Flags().GetInt("port"); err == nil {
		config.Port = port
	}
	if cwd, err := cmd.Flags().GetString("cwd"); err == nil {
		config.CWD = strings.TrimSpace(cwd)
	}
	if compactRatio, err := cmd.Flags().GetFloat64("compact-ratio"); err == nil {
		config.CompactRatio = compactRatio
	}
	if disableAutoCompact, err := cmd.Flags().GetBool("disable-auto-compact"); err == nil {
		config.DisableAutoCompact = disableAutoCompact
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

	if config.CompactRatio < 0.0 || config.CompactRatio > 1.0 {
		return errors.New("compact-ratio must be between 0.0 and 1.0")
	}

	return nil
}

func runServeCommand(ctx context.Context, config *ServeConfig) {
	if err := validateServeConfig(config); err != nil {
		presenter.Error(err, "invalid server configuration")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]any{
		"host": config.Host,
		"port": config.Port,
	}).Info("Starting web UI server")

	serverConfig := &webui.ServerConfig{
		Host:               config.Host,
		Port:               config.Port,
		CWD:                config.CWD,
		CompactRatio:       config.CompactRatio,
		DisableAutoCompact: config.DisableAutoCompact,
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
