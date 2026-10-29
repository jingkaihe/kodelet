package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthProbes(t *testing.T) {
	// OIDC would redirect unauthenticated GETs to sign-in, so a 200 proves the bypass.
	server := &Server{router: mux.NewRouter(), config: &ServerConfig{WebAuthMode: WebAuthModeOIDC}}
	server.setupRoutes()
	serve := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}

	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, healthzPath, http.StatusOK},
		{http.MethodHead, readyzPath, http.StatusOK},
		{http.MethodPost, healthzPath, http.StatusUnauthorized},
		{http.MethodGet, "/api/status", http.StatusUnauthorized},
	} {
		assert.Equal(t, test.status, serve(test.method, test.path).Code, "%s %s", test.method, test.path)
	}
	assert.Equal(t, "ok\n", serve(http.MethodGet, healthzPath).Body.String())

	require.NoError(t, server.drainExecutions(t.Context()))
	assert.Equal(t, http.StatusServiceUnavailable, serve(http.MethodGet, readyzPath).Code)
	assert.Equal(t, http.StatusOK, serve(http.MethodGet, healthzPath).Code)
}
