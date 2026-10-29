package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHealthTestServer(config *ServerConfig) *Server {
	server := &Server{router: mux.NewRouter(), config: config}
	server.setupRoutes()
	return server
}

func TestHealthProbesBypassAuthentication(t *testing.T) {
	for _, test := range []struct {
		name   string
		config *ServerConfig
	}{
		{name: "token", config: &ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-secret"}},
		{name: "oidc", config: &ServerConfig{WebAuthMode: WebAuthModeOIDC}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newHealthTestServer(test.config)
			for _, path := range []string{healthzPath, readyzPath} {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				assert.Equal(t, http.StatusOK, response.Code, path)
				assert.Equal(t, "ok\n", response.Body.String(), "%s must not expose server details", path)
				assert.Equal(t, "no-store", response.Header().Get("Cache-Control"), path)
				assert.Empty(t, response.Header().Get("Location"), "%s must not redirect to sign-in", path)

				response = httptest.NewRecorder()
				server.ServeHTTP(response, httptest.NewRequest(http.MethodHead, path, nil))
				assert.Equal(t, http.StatusOK, response.Code, path)
			}

			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			assert.Equal(t, http.StatusUnauthorized, response.Code, "detailed status must stay authenticated")
		})
	}
}

func TestHealthProbePathsRequireAuthenticationForOtherMethods(t *testing.T) {
	server := newHealthTestServer(&ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-secret"})
	for _, path := range []string{healthzPath, readyzPath} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			assert.Equal(t, http.StatusUnauthorized, response.Code, "%s %s must not reach the frontend fallback unauthenticated", method, path)
		}
	}
}

func TestReadyzReportsDrainingWhileHealthzStaysLive(t *testing.T) {
	server := newHealthTestServer(&ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-secret"})
	require.NoError(t, server.drainExecutions(t.Context()))

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, readyzPath, nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "1", response.Header().Get("Retry-After"))
	assert.Equal(t, "stopping\n", response.Body.String())

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, healthzPath, nil))
	assert.Equal(t, http.StatusOK, response.Code, "draining is not a liveness failure")
}

func TestReadyzIgnoresEmbeddedRunnerAvailability(t *testing.T) {
	server := newHealthTestServer(&ServerConfig{EmbeddedRunner: &EmbeddedRunnerConfig{}})
	server.runCtx = t.Context()
	server.embeddedRunnerError(assert.AnError)
	require.False(t, server.EmbeddedRunnerStatus().Ready)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, readyzPath, nil))
	assert.Equal(t, http.StatusOK, response.Code, "API readiness must not depend on the built-in runner")
}

func TestHealthProbesLogAtDebugLevel(t *testing.T) {
	testLogger, hook := logrustest.NewNullLogger()
	testLogger.SetLevel(logrus.DebugLevel)
	ctx := logger.WithLogger(t.Context(), logrus.NewEntry(testLogger))
	server := newHealthTestServer(&ServerConfig{WebAuthMode: WebAuthModeToken, AuthToken: "web-secret"})

	for _, path := range []string{healthzPath, readyzPath, "/api/status"} {
		server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	}

	levels := map[string]logrus.Level{}
	for _, entry := range hook.AllEntries() {
		if entry.Message == "HTTP request" {
			levels[entry.Data["path"].(string)] = entry.Level
		}
	}
	assert.Equal(t, map[string]logrus.Level{
		healthzPath:   logrus.DebugLevel,
		readyzPath:    logrus.DebugLevel,
		"/api/status": logrus.InfoLevel,
	}, levels)
}
