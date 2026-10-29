package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/debugserver"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// addDebugFlags registers profiling flags for serve and runner start.
func addDebugFlags(cmd *cobra.Command) {
	cmd.Flags().String("debug-addr", "", "Serve unauthenticated Go pprof endpoints on this host:port, for example 127.0.0.1:6060 (default: disabled)")
	cmd.Flags().Bool("debug-allow-remote", false, "Allow --debug-addr to bind a non-loopback address; restrict network access to that port first")
}

// applyDebugFlags lets explicitly set flags override inherited settings.
func applyDebugFlags(cmd *cobra.Command, config *debugserver.Config) {
	if address, err := cmd.Flags().GetString("debug-addr"); err == nil && cmd.Flags().Changed("debug-addr") {
		config.Address = strings.TrimSpace(address)
	}
	if allowRemote, err := cmd.Flags().GetBool("debug-allow-remote"); err == nil && cmd.Flags().Changed("debug-allow-remote") {
		config.AllowRemote = allowRemote
	}
}

// debugListener holds the optional profiling listener; the zero value is disabled.
type debugListener struct {
	server *debugserver.Server
}

// startDebugListener validates the configuration and starts the listener when enabled.
func startDebugListener(ctx context.Context, config debugserver.Config) (debugListener, error) {
	if err := config.Validate(); err != nil {
		return debugListener{}, err
	}
	if !config.Enabled() {
		return debugListener{}, nil
	}
	server, err := debugserver.Start(ctx, config)
	if err != nil {
		return debugListener{}, errors.Wrap(err, "failed to start the debug listener")
	}
	logger.G(ctx).WithField("address", server.Addr().String()).Info("Debug profiling listener started")
	return debugListener{server: server}, nil
}

func (l debugListener) report() {
	if l.server == nil {
		return
	}
	presenter.Info(fmt.Sprintf("Debug profiling endpoints: http://%s/debug/pprof/", l.server.Addr()))
	if !l.server.Loopback() {
		presenter.Warning("Debug profiling endpoints are unauthenticated and reachable beyond this host; restrict network access to this port")
	}
}

func (l debugListener) close(ctx context.Context) {
	if l.server == nil {
		return
	}
	if err := l.server.Close(); err != nil {
		logger.G(ctx).WithError(err).Warn("failed to close the debug listener")
	}
}
