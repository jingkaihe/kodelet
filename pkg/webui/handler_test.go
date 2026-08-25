package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerServesEmbeddedSPA(t *testing.T) {
	handler, err := NewHandler()
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-cache")
	assert.Contains(t, response.Body.String(), `<div id="root"></div>`)
}

func TestHandlerRejectsNonReadMethods(t *testing.T) {
	handler, err := NewHandler()
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodPost, "/chat", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	assert.Equal(t, "GET, HEAD", response.Header().Get("Allow"))
}

func TestHandlerComposesWithControlPlaneRoutesAndAuthentication(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))

	frontend, err := NewHandler()
	require.NoError(t, err)
	server, err := controlplane.NewServer(t.Context(), &controlplane.ServerConfig{
		Host:         "127.0.0.1",
		Port:         1,
		CompactRatio: 0.8,
		AuthToken:    "web-token",
		WebAuthMode:  controlplane.WebAuthModeToken,
	}, frontend)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, server.Close()) })

	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	rootRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL+"/", nil)
	require.NoError(t, err)
	rootRequest.Header.Set("Authorization", "Bearer web-token")
	rootResponse, err := http.DefaultClient.Do(rootRequest)
	require.NoError(t, err)
	defer rootResponse.Body.Close()
	rootBody, err := io.ReadAll(rootResponse.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rootResponse.StatusCode)
	assert.Contains(t, string(rootBody), `<div id="root"></div>`)

	apiRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL+"/api/auth/me", nil)
	require.NoError(t, err)
	apiRequest.Header.Set("Authorization", "Bearer web-token")
	apiResponse, err := http.DefaultClient.Do(apiRequest)
	require.NoError(t, err)
	defer apiResponse.Body.Close()
	apiBody, err := io.ReadAll(apiResponse.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, apiResponse.StatusCode)
	assert.Contains(t, apiResponse.Header.Get("Content-Type"), "application/json")
	assert.Contains(t, string(apiBody), `"id":"token"`)

	assetEntries, err := embedFS.ReadDir("dist/assets")
	require.NoError(t, err)
	var assetName string
	for _, entry := range assetEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".js") {
			assetName = entry.Name()
			break
		}
	}
	require.NotEmpty(t, assetName)
	assetRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL+"/assets/"+assetName, nil)
	require.NoError(t, err)
	assetResponse, err := http.DefaultClient.Do(assetRequest)
	require.NoError(t, err)
	defer assetResponse.Body.Close()
	assert.Equal(t, http.StatusOK, assetResponse.StatusCode)
}

func TestHandlerIdentifiesPublicStaticResources(t *testing.T) {
	handler, err := NewHandler()
	require.NoError(t, err)

	assert.True(t, handler.IsPublicPath("/assets/app.js"))
	assert.True(t, handler.IsPublicPath("/favicon.ico"))
	assert.False(t, handler.IsPublicPath("/"))
	assert.False(t, handler.IsPublicPath("/api/chat"))
}
