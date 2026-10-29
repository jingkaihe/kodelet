// Package debugserver serves pprof on a dedicated, unauthenticated listener.
package debugserver

import (
	"context"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

// ValidateAddress checks a host:port debug address without binding it.
func ValidateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.Errorf("invalid debug address %q: use host:port, for example 127.0.0.1:6060", address)
	}
	if number, err := strconv.Atoi(port); err != nil || number < 0 || number > 65535 {
		return errors.Errorf("invalid debug address %q: port must be between 0 and 65535", address)
	}
	if host == "" {
		// Go would bind every interface; require that to be spelled out.
		return errors.Errorf("debug address %q has no host: use 127.0.0.1:%s for this host only, or 0.0.0.0:%s for all interfaces", address, port, port)
	}
	return nil
}

// Server is a running debug listener.
type Server struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
}

// Start serves profiling endpoints on address in the background until Close.
func Start(ctx context.Context, address string) (*Server, error) {
	if err := ValidateAddress(address); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.Wrapf(err, "could not listen on debug address %q", address)
	}
	s := &Server{
		listener: listener,
		server: &http.Server{
			Handler:           handler(),
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
	tcp, ok := s.listener.Addr().(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// URL returns the browsable index URL; wildcard binds use localhost.
func (s *Server) URL() string {
	return indexURL(s.listener.Addr())
}

// Close stops immediately, aborting in-flight profiles.
func (s *Server) Close() error {
	err := s.server.Close()
	<-s.done
	return errors.Wrap(err, "failed to close the debug listener")
}

// handler serves net/http/pprof except cmdline, since flags can hold tokens.
// The package's DefaultServeMux registrations are unreachable; Kodelet never serves it.
func handler() http.Handler {
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

func indexURL(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return "http://" + addr.String() + "/debug/pprof/"
	}
	host := tcp.IP.String()
	if tcp.IP.IsUnspecified() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port)) + "/debug/pprof/"
}
