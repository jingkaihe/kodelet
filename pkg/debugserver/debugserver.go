// Package debugserver serves pprof on a dedicated, unauthenticated listener,
// loopback-only unless remote access is allowed.
package debugserver

import (
	"context"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/pkg/errors"
)

// Config selects the debug listener address.
type Config struct {
	// Address is a host:port pair; empty disables the listener.
	Address string
	// AllowRemote permits non-loopback addresses.
	AllowRemote bool
}

// Enabled reports whether a debug listener is configured.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.Address) != ""
}

// Validate checks the configuration without binding the address.
func (c Config) Validate() error {
	if !c.Enabled() {
		if c.AllowRemote {
			return errors.New("--debug-allow-remote requires --debug-addr")
		}
		return nil
	}
	address := strings.TrimSpace(c.Address)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.Errorf("invalid debug address %q: use host:port, for example 127.0.0.1:6060", address)
	}
	if number, err := strconv.Atoi(port); err != nil || number < 0 || number > 65535 {
		return errors.Errorf("invalid debug address %q: port must be between 0 and 65535", address)
	}
	if !c.AllowRemote && !controlplaneurl.IsLoopbackHostname(host) {
		return errors.Errorf("debug address %q is not a loopback address; profiling endpoints are unauthenticated, so bind 127.0.0.1 or [::1], or pass --debug-allow-remote after restricting network access", address)
	}
	return nil
}

// Server is a running debug listener.
type Server struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
}

// Start serves profiling endpoints in the background until Close.
func Start(ctx context.Context, config Config) (*Server, error) {
	if !config.Enabled() {
		return nil, errors.New("debug address is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	address := strings.TrimSpace(config.Address)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.Wrapf(err, "could not listen on debug address %q", address)
	}
	// Hostnames resolve at bind time, so recheck the bound address.
	if !config.AllowRemote && !isLoopbackAddr(listener.Addr()) {
		_ = listener.Close()
		return nil, errors.Errorf("debug address %q resolved to non-loopback address %s; bind 127.0.0.1 or [::1] instead", address, listener.Addr())
	}
	s := &Server{
		listener: listener,
		server: &http.Server{
			Handler:           Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       time.Minute,
			// No WriteTimeout: profiles stream for their requested duration.
		},
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.G(ctx).WithError(err).Error("debug listener stopped unexpectedly")
		}
	}()
	return s, nil
}

// Addr returns the bound address, including the port chosen for port zero.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Loopback reports whether the listener is reachable only from this host.
func (s *Server) Loopback() bool {
	return isLoopbackAddr(s.listener.Addr())
}

// Close stops immediately, aborting in-flight profiles.
func (s *Server) Close() error {
	err := s.server.Close()
	<-s.done
	return errors.Wrap(err, "failed to close the debug listener")
}

// Handler serves net/http/pprof except cmdline, since flags can hold tokens.
// The package's DefaultServeMux registrations are unreachable; Kodelet never serves it.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", handleCmdlineDisabled)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return mux
}

func handleCmdlineDisabled(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, "cmdline is disabled because command-line flags can contain credentials", http.StatusNotFound)
}

func isLoopbackAddr(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}
