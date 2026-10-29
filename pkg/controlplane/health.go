package controlplane

import (
	"io"
	"net/http"
)

const (
	healthzPath = "/healthz" // liveness
	readyzPath  = "/readyz"  // admitting new work
)

// isHealthProbeRequest matches public probes; only GET and HEAD qualify.
func isHealthProbeRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	return r.URL.Path == healthzPath || r.URL.Path == readyzPath
}

// handleHealthz reports liveness only, independent of runners, providers, and storage.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeHealthProbe(w, http.StatusOK, "ok")
}

// handleReadyz returns 503 once shutdown starts draining; runner state is ignored.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	s.activeChatsMu.Lock()
	stopping := s.stopping
	s.activeChatsMu.Unlock()
	if stopping {
		w.Header().Set("Retry-After", "1")
		writeHealthProbe(w, http.StatusServiceUnavailable, "stopping")
		return
	}
	writeHealthProbe(w, http.StatusOK, "ok")
}

// writeHealthProbe writes a minimal public response with no server details.
func writeHealthProbe(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}
