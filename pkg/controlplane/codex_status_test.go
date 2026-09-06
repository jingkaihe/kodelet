package controlplane

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codexStatusTransport func(*http.Request) (*http.Response, error)

func (f codexStatusTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexStatusUsesServerCredentialsWithoutReturningTokens(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	credentials := &auth.CodexCredentials{AccessToken: "server-access-secret", RefreshToken: "server-refresh-secret", AccountID: "account-1234567890", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	_, err := auth.SaveCodexCredentials(credentials)
	require.NoError(t, err)
	previous := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = previous })
	statusCode := http.StatusOK
	http.DefaultClient = &http.Client{Transport: codexStatusTransport(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "Bearer server-access-secret", r.Header.Get("Authorization"))
		assert.Equal(t, "account-1234567890", r.Header.Get("ChatGPT-Account-ID"))
		return &http.Response{StatusCode: statusCode, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000}},"credits":{"has_credits":true,"balance":"42"}}`))}, nil
	})}
	server := &Server{}
	response := httptest.NewRecorder()
	server.handleCodexStatus(response, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	var status chat.CodexStatus
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))
	assert.True(t, status.Connected)
	assert.True(t, status.CanRefresh)
	assert.Equal(t, "acco...7890", status.AccountID)
	require.NotNil(t, status.Usage)
	assert.Equal(t, "pro", status.Usage.PlanType)
	assert.Equal(t, float64(25), status.Usage.Snapshots[0].Primary.UsedPercent)
	for _, secret := range []string{credentials.AccessToken, credentials.RefreshToken, credentials.AccountID} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	statusCode = http.StatusServiceUnavailable
	response = httptest.NewRecorder()
	server.handleCodexStatus(response, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))
	assert.True(t, status.Connected, "usage failures must not hide connection status")
	assert.Contains(t, status.UsageMessage, "Live usage is unavailable")
	for _, role := range []Role{RoleUser, RoleRunnerAdmin, RoleTerminal} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request = request.WithContext(contextWithPrincipal(request.Context(), Principal{ID: "non-admin", Roles: []string{string(role)}}))
		response = httptest.NewRecorder()
		server.requireRole(RoleAdmin, server.handleCodexStatus)(response, request)
		assert.Equal(t, http.StatusForbidden, response.Code)
	}
}

func TestCodexStatusMissingAndInvalidSignIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	service := defaultCodexProviderAuthService{}
	status, err := service.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, status.Connected)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "codex-credentials.json"), []byte("invalid"), 0o600))
	_, err = service.Status(t.Context())
	require.Error(t, err)
	assert.Equal(t, "****", maskedCodexAccountID("short"))
}
