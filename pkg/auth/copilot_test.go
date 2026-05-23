package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopilotCredentialsLifecycle(t *testing.T) {
	setTestHome(t)

	exists, err := GetCopilotCredentialsExists()
	require.NoError(t, err)
	assert.False(t, exists)

	creds := &CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "copilot-token",
		Scope:          "copilot",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	}

	path, err := SaveCopilotCredentials(creds)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(os.Getenv("HOME"), ".kodelet", "copilot-subscription.json"), path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "\n  \"access_token\": \"github-oauth-token\"")
	tempFiles, err := filepath.Glob(filepath.Join(filepath.Dir(path), "copilot-subscription-*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, tempFiles, "temporary files should not be left behind")

	var saved CopilotCredentials
	require.NoError(t, json.Unmarshal(raw, &saved))
	assert.Equal(t, creds, &saved)

	exists, err = GetCopilotCredentialsExists()
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, DeleteCopilotCredentials())
	exists, err = GetCopilotCredentialsExists()
	require.NoError(t, err)
	assert.False(t, exists)

	assert.NoError(t, DeleteCopilotCredentials(), "deleting missing credentials should be idempotent")
}

func TestCopilotAccessTokenRefreshesExpiredCredentials(t *testing.T) {
	setTestHome(t)

	_, err := SaveCopilotCredentials(&CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "expired-copilot-token",
		Scope:          "copilot",
		CopilotExpires: time.Now().Add(time.Minute).Unix(),
	})
	require.NoError(t, err)

	var calls int32
	setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		assert.Equal(t, http.MethodGet, req.Method)
		assert.Equal(t, copilotExchangeURL, req.URL.String())
		assert.Equal(t, "Bearer github-oauth-token", req.Header.Get("Authorization"))
		assert.Equal(t, "kodelet/1.0.0", req.Header.Get("Editor-Version"))
		assert.Equal(t, "vscode-chat", req.Header.Get("Copilot-Integration-Id"))
		assert.Equal(t, "application/json", req.Header.Get("Accept"))

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":"fresh-copilot-token","expires_at":4102444800}`)),
		}, nil
	})})

	token, err := CopilotAccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "fresh-copilot-token", token)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".kodelet", "copilot-subscription.json"))
	require.NoError(t, err)
	var saved CopilotCredentials
	require.NoError(t, json.Unmarshal(raw, &saved))
	assert.Equal(t, "github-oauth-token", saved.AccessToken)
	assert.Equal(t, "fresh-copilot-token", saved.CopilotToken)
	assert.Equal(t, "copilot", saved.Scope)
	assert.Equal(t, int64(4102444800), saved.CopilotExpires)
}

func TestCopilotAccessTokenReturnsCachedTokenWithoutRefresh(t *testing.T) {
	setTestHome(t)

	_, err := SaveCopilotCredentials(&CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "cached-copilot-token",
		Scope:          "copilot",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected Copilot token exchange request to %s", req.URL.String())
		return nil, assert.AnError
	})})

	token, err := CopilotAccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cached-copilot-token", token)
}

func TestGenerateCopilotDeviceFlow(t *testing.T) {
	t.Run("sends expected request and decodes response", func(t *testing.T) {
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, copilotDeviceURL, req.URL.String())
			assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))
			assert.Equal(t, "kodelet", req.Header.Get("User-Agent"))
			assert.Equal(t, "application/json", req.Header.Get("Accept"))

			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			values, err := parseFormBody(string(body))
			require.NoError(t, err)
			assert.Equal(t, copilotClientID, values.Get("client_id"))
			assert.Equal(t, strings.Join(copilotScopes, " "), values.Get("scope"))

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"device_code":"device-123","user_code":"USER-123","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)),
			}, nil
		})})

		resp, err := GenerateCopilotDeviceFlow(context.Background())
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "device-123", resp.DeviceCode)
		assert.Equal(t, "USER-123", resp.UserCode)
		assert.Equal(t, "https://github.com/login/device", resp.VerificationURI)
		assert.Equal(t, 900, resp.ExpiresIn)
		assert.Equal(t, 5, resp.Interval)
	})

	t.Run("includes response body in status errors", func(t *testing.T) {
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad_scope"}`)),
			}, nil
		})})

		resp, err := GenerateCopilotDeviceFlow(context.Background())
		assert.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device flow failed with status 400")
		assert.Contains(t, err.Error(), "bad_scope")
	})
}

func TestExchangeCopilotToken(t *testing.T) {
	t.Run("sends expected headers and decodes response", func(t *testing.T) {
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodGet, req.Method)
			assert.Equal(t, copilotExchangeURL, req.URL.String())
			assert.Equal(t, "Bearer github-token", req.Header.Get("Authorization"))
			assert.Equal(t, "kodelet/1.0.0", req.Header.Get("Editor-Version"))
			assert.Equal(t, "vscode-chat", req.Header.Get("Copilot-Integration-Id"))
			assert.Equal(t, "application/json", req.Header.Get("Accept"))

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"token":"copilot-token","expires_at":4102444800}`)),
			}, nil
		})})

		resp, err := ExchangeCopilotToken(context.Background(), "github-token")
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "copilot-token", resp.Token)
		assert.Equal(t, int64(4102444800), resp.ExpiresAt)
	})

	t.Run("returns status error", func(t *testing.T) {
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     "401 Unauthorized",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("bad token")),
			}, nil
		})})

		resp, err := ExchangeCopilotToken(context.Background(), "github-token")
		assert.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "copilot token exchange failed with status 401")
		assert.Contains(t, err.Error(), "bad token")
	})
}

func TestPollCopilotToken(t *testing.T) {
	t.Run("returns token after pending response", func(t *testing.T) {
		var calls int32
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			call := atomic.AddInt32(&calls, 1)
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, copilotTokenURL, req.URL.String())
			assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))
			assert.Equal(t, "application/json", req.Header.Get("Accept"))

			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			values, err := parseFormBody(string(body))
			require.NoError(t, err)
			assert.Equal(t, copilotClientID, values.Get("client_id"))
			assert.Equal(t, "device-123", values.Get("device_code"))
			assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", values.Get("grant_type"))

			response := `{"error":"authorization_pending","error_description":"waiting"}`
			if call == 2 {
				response = `{"access_token":"github-access-token","token_type":"bearer","scope":"copilot"}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(response)),
			}, nil
		})})

		resp, err := PollCopilotToken(context.Background(), "device-123", 1)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "github-access-token", resp.AccessToken)
		assert.Equal(t, "bearer", resp.TokenType)
		assert.Equal(t, "copilot", resp.Scope)
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	})

	t.Run("returns terminal access denied error", func(t *testing.T) {
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"access_denied","error_description":"no thanks"}`)),
			}, nil
		})})

		resp, err := PollCopilotToken(context.Background(), "device-123", 1)
		assert.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authentication failed: access_denied - no thanks")
	})
}

func parseFormBody(body string) (url.Values, error) {
	return url.ParseQuery(body)
}
