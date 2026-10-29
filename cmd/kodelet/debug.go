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
	"github.com/spf13/viper"
)

const defaultDebugAddr = "127.0.0.1:6060"

// debugConfig is the resolved profiling listener setting.
type debugConfig struct {
	Address  string // empty disables the listener
	Explicit bool   // from --debug-addr; bind failures are fatal
}

// addDebugFlags registers the profiling flag for serve and runner start.
func addDebugFlags(cmd *cobra.Command) {
	cmd.Flags().String("debug-addr", "", "Serve unauthenticated Go pprof endpoints on host:port, e.g. 127.0.0.1:6060; overrides debug.enabled/debug.addr, and an empty value disables")
}

// resolveDebugConfig applies --debug-addr over the debug config section.
func resolveDebugConfig(cmd *cobra.Command) debugConfig {
	if flag := cmd.Flags().Lookup("debug-addr"); flag != nil && flag.Changed {
		return debugConfig{Address: strings.TrimSpace(flag.Value.String()), Explicit: true}
	}
	if !viper.GetBool("debug.enabled") {
		return debugConfig{}
	}
	address := strings.TrimSpace(viper.GetString("debug.addr"))
	if address == "" {
		address = defaultDebugAddr
	}
	return debugConfig{Address: address}
}

func (c debugConfig) validate() error {
	if c.Address == "" {
		return nil
	}
	return debugserver.ValidateAddress(c.Address)
}

// debugListener holds the optional profiling listener; the zero value is disabled.
type debugListener struct {
	server  *debugserver.Server
	bindErr error
}

// startDebugListener validates the configuration and starts the listener when enabled.
func startDebugListener(ctx context.Context, config debugConfig) (debugListener, error) {
	if err := config.validate(); err != nil {
		return debugListener{}, err
	}
	if config.Address == "" {
		return debugListener{}, nil
	}
	server, err := debugserver.Start(ctx, config.Address)
	if err != nil {
		if config.Explicit {
			return debugListener{}, errors.Wrap(err, "failed to start the debug listener")
		}
		// The server and every runner share one config, so a taken port only disables profiling here.
		logger.G(ctx).WithError(err).Warn("debug profiling disabled")
		return debugListener{bindErr: err}, nil
	}
	logger.G(ctx).WithField("address", server.Addr().String()).Info("Debug profiling listener started")
	return debugListener{server: server}, nil
}

func (l debugListener) report() {
	switch {
	case l.bindErr != nil:
		presenter.Warning(fmt.Sprintf("Debug profiling disabled: %v; pass --debug-addr to choose another address", l.bindErr))
	case l.server != nil:
		presenter.Info(fmt.Sprintf("Debug profiling endpoints: %s", l.server.URL()))
		if !l.server.Loopback() {
			presenter.Warning(fmt.Sprintf("Debug profiling endpoints are unauthenticated and listening on %s; restrict network access to this port", l.server.Addr()))
		}
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
