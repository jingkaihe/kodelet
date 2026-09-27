package chat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientServerStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/kodelet/api/status", r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"apiReady": true,
			"version": "0.6.24-beta",
			"gitCommit": "e9f7b86",
			"buildTime": "2026-09-27T10:00:00Z",
			"instanceId": "ignored"
		}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/kodelet", "secret", "")
	require.NoError(t, err)

	status, err := client.ServerStatus(t.Context())

	require.NoError(t, err)
	assert.Equal(t, ServerStatus{
		Version:   "0.6.24-beta",
		GitCommit: "e9f7b86",
		BuildTime: "2026-09-27T10:00:00Z",
		APIReady:  true,
	}, status)
}

func TestClientServerStatusReportsHTTPFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"sign in again"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "")
	require.NoError(t, err)

	_, err = client.ServerStatus(t.Context())

	var httpErr *ControlPlaneHTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
	assert.Contains(t, err.Error(), "sign in again")
}
