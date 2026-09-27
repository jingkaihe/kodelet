package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
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

func TestClientRunnerStatusSelectionAndPermissions(t *testing.T) {
	for _, test := range []struct {
		name, targetID, clientID, defaultID, role, wantID string
		authorized, authFailure                           bool
	}{
		{
			name: "target overrides client and default", targetID: "target", clientID: "client", defaultID: "default",
			role: "terminal", wantID: "target", authorized: true,
		},
		{
			name: "client overrides default", clientID: "client", defaultID: "default",
			role: "admin", wantID: "client", authorized: true,
		},
		{name: "offline default and unprivileged user", defaultID: "default", role: "user", wantID: "default"},
		{name: "auth error preserves runner but fails closed", targetID: "target", role: "admin", wantID: "target", authFailure: true},
		{name: "no runner selected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/chat/settings":
					require.NoError(t, json.NewEncoder(w).Encode(ControlPlaneChatSettings{DefaultRunnerID: test.defaultID}))
				case strings.HasPrefix(r.URL.Path, "/api/runners/"):
					require.NoError(t, json.NewEncoder(w).Encode(runnerregistry.Runner{
						ID: strings.TrimPrefix(r.URL.Path, "/api/runners/"), Status: runnerregistry.RunnerStatusOffline,
					}))
				case r.URL.Path == "/api/auth/me":
					assert.NotEmpty(t, test.wantID, "no selection should not require authorization")
					if test.authFailure {
						w.WriteHeader(http.StatusUnauthorized)
					}
					require.NoError(t, json.NewEncoder(w).Encode(map[string][]string{"roles": {test.role}}))
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "", test.clientID)
			require.NoError(t, err)

			status, err := client.RunnerStatus(t.Context(), WorkspaceTarget{RunnerID: test.targetID})

			if test.authFailure {
				require.ErrorContains(t, err, "could not load runner permissions")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.authorized, status.TerminalAuthorized)
			if test.wantID == "" {
				assert.Nil(t, status.Runner)
			} else {
				require.NotNil(t, status.Runner, "auth failures must preserve runner details")
				assert.Equal(t, test.wantID, status.Runner.ID)
				assert.Equal(t, runnerregistry.RunnerStatusOffline, status.Runner.Status)
			}
		})
	}
}
