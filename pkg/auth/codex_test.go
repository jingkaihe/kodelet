package auth

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCodexAuthFilePath(t *testing.T) {
	// The function is unexported, so we test it indirectly through GetCodexCredentialsExists
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// The path should be under ~/.kodelet/codex-credentials.json
	expectedPath := filepath.Join(tempDir, ".kodelet", "codex-credentials.json")

	// Create the auth file
	require.NoError(t, os.MkdirAll(filepath.Dir(expectedPath), 0o755))
	require.NoError(t, os.WriteFile(expectedPath, []byte("{}"), 0o644))

	// Verify GetCodexCredentialsExists finds the file
	exists, err := GetCodexCredentialsExists()
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestGetCodexCredentialsExists(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("credentials do not exist", func(t *testing.T) {
		exists, err := GetCodexCredentialsExists()
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("credentials exist", func(t *testing.T) {
		// Create the credentials directory and file
		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		require.NoError(t, os.WriteFile(authFile, []byte("{}"), 0o644))

		exists, err := GetCodexCredentialsExists()
		assert.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestGetCodexCredentials(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("file does not exist", func(t *testing.T) {
		_, err := GetCodexCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		require.NoError(t, os.WriteFile(authFile, []byte("invalid json"), 0o644))

		_, err := GetCodexCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode")

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("OAuth tokens present", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:     "test_id_token",
				AccessToken: "test_access_token",
				AccountID:   "test_account_id",
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		creds, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Equal(t, "test_id_token", creds.IDToken)
		assert.Equal(t, "test_access_token", creds.AccessToken)
		assert.Equal(t, "test_account_id", creds.AccountID)
		assert.Empty(t, creds.APIKey)

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("API key fallback", func(t *testing.T) {
		authData := CodexAuthFile{
			OpenAIAPIKey: "sk-test-api-key",
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		creds, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Empty(t, creds.AccessToken)
		assert.Empty(t, creds.AccountID)
		assert.Equal(t, "sk-test-api-key", creds.APIKey)

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("OAuth tokens preferred over API key", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:     "oauth_id_token",
				AccessToken: "oauth_access_token",
				AccountID:   "oauth_account_id",
			},
			OpenAIAPIKey: "sk-fallback-key",
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		creds, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Equal(t, "oauth_id_token", creds.IDToken)
		assert.Equal(t, "oauth_access_token", creds.AccessToken)
		assert.Equal(t, "oauth_account_id", creds.AccountID)
		assert.Empty(t, creds.APIKey) // Should not include API key when OAuth is available

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("no valid credentials", func(t *testing.T) {
		authData := CodexAuthFile{
			// Empty - no tokens and no API key
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		_, err = GetCodexCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid credentials")

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("partial OAuth tokens - missing account ID", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				AccessToken: "only_access_token",
				// AccountID is missing
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		_, err = GetCodexCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid credentials")

		// Clean up for next test
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("partial OAuth tokens - missing access token", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				// AccessToken is missing
				AccountID: "only_account_id",
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		_, err = GetCodexCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no valid credentials")
	})
}

func TestCodexHeader(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("returns headers for OAuth credentials", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				AccessToken: "test_access_token",
				AccountID:   "test_account_id",
				ExpiresAt:   time.Now().Add(30 * time.Minute).Unix(),
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		headers, err := CodexHeader(context.Background())
		require.NoError(t, err)
		require.NotNil(t, headers)
		assert.Len(t, headers, 5, "should return 5 request options for OAuth")

		// Clean up
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("returns headers for API key credentials", func(t *testing.T) {
		authData := CodexAuthFile{
			OpenAIAPIKey: "sk-test-api-key",
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		headers, err := CodexHeader(context.Background())
		require.NoError(t, err)
		require.NotNil(t, headers)
		assert.Len(t, headers, 1, "should return 1 request option for API key")

		// Clean up
		require.NoError(t, os.Remove(authFile))
	})

	t.Run("error when no credentials", func(t *testing.T) {
		// Ensure no credentials file exists
		kodeletDir := filepath.Join(tempDir, ".kodelet")
		os.RemoveAll(kodeletDir)

		_, err := CodexHeader(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get codex credentials")
	})

	t.Run("does not refresh OAuth credentials when expiry is unknown", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:      "persisted_id_token",
				AccessToken:  "current_access_token",
				RefreshToken: "refresh-123",
				AccountID:    "test_account_id",
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		originalClient := http.DefaultClient
		http.DefaultClient = &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected token refresh request to %s", req.URL.String())
				return nil, assert.AnError
			}),
		}
		defer func() { http.DefaultClient = originalClient }()

		creds, err := GetCodexCredentialsForRequest(context.Background())
		require.NoError(t, err)

		assert.Equal(t, "persisted_id_token", creds.IDToken)
		assert.Equal(t, "current_access_token", creds.AccessToken)
		assert.Equal(t, "refresh-123", creds.RefreshToken)
		assert.Equal(t, "test_account_id", creds.AccountID)
		assert.Zero(t, creds.ExpiresAt)
	})

	t.Run("refreshes OAuth credentials when nearing expiry", func(t *testing.T) {
		refreshAccessToken := makeTestCodexJWT(t, "test_account_id")

		authData := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:      "persisted_id_token",
				AccessToken:  "expiring_access_token",
				RefreshToken: "refresh-123",
				AccountID:    "test_account_id",
				ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/oauth/token", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "refresh-123", r.Form.Get("refresh_token"))
			assert.Equal(t, codexClientID, r.Form.Get("client_id"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh-456","expires_in":3600}`, refreshAccessToken)
		}))
		defer server.Close()

		originalClient := http.DefaultClient
		http.DefaultClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(ctx, network, server.Listener.Addr().String())
				},
			},
		}
		defer func() { http.DefaultClient = originalClient }()

		creds, err := GetCodexCredentialsForRequest(context.Background())
		require.NoError(t, err)

		assert.Equal(t, "persisted_id_token", creds.IDToken)
		assert.Equal(t, refreshAccessToken, creds.AccessToken)
		assert.Equal(t, "refresh-456", creds.RefreshToken)
		assert.Equal(t, "test_account_id", creds.AccountID)
		assert.Greater(t, creds.ExpiresAt, time.Now().Add(30*time.Minute).Unix())

		persisted, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Equal(t, "persisted_id_token", persisted.IDToken)
		assert.Equal(t, refreshAccessToken, persisted.AccessToken)
		assert.Equal(t, "refresh-456", persisted.RefreshToken)
		assert.Equal(t, "test_account_id", persisted.AccountID)
	})

	t.Run("preserves refresh token when refresh response does not rotate it", func(t *testing.T) {
		refreshAccessToken := makeTestCodexJWT(t, "test_account_id")

		authData := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:      "persisted_id_token",
				AccessToken:  "expiring_access_token",
				RefreshToken: "refresh-123",
				AccountID:    "test_account_id",
				ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/oauth/token", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "refresh-123", r.Form.Get("refresh_token"))
			assert.Equal(t, codexClientID, r.Form.Get("client_id"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600}`, refreshAccessToken)
		}))
		defer server.Close()

		originalClient := http.DefaultClient
		http.DefaultClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(ctx, network, server.Listener.Addr().String())
				},
			},
		}
		defer func() { http.DefaultClient = originalClient }()

		creds, err := GetCodexCredentialsForRequest(context.Background())
		require.NoError(t, err)

		assert.Equal(t, "persisted_id_token", creds.IDToken)
		assert.Equal(t, refreshAccessToken, creds.AccessToken)
		assert.Equal(t, "refresh-123", creds.RefreshToken)
		assert.Equal(t, "test_account_id", creds.AccountID)
		assert.Greater(t, creds.ExpiresAt, time.Now().Add(30*time.Minute).Unix())

		persisted, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Equal(t, "persisted_id_token", persisted.IDToken)
		assert.Equal(t, refreshAccessToken, persisted.AccessToken)
		assert.Equal(t, "refresh-123", persisted.RefreshToken)
		assert.Equal(t, "test_account_id", persisted.AccountID)
	})
}

func TestCodexHeaderWithCredentials(t *testing.T) {
	t.Run("nil credentials returns nil", func(t *testing.T) {
		headers := CodexHeaderWithCredentials(nil)
		assert.Nil(t, headers)
	})

	t.Run("OAuth credentials return proper headers", func(t *testing.T) {
		creds := &CodexCredentials{
			AccessToken: "oauth_token",
			AccountID:   "account_123",
		}

		headers := CodexHeaderWithCredentials(creds)
		require.NotNil(t, headers)
		assert.Len(t, headers, 5, "should return 5 request options for OAuth")
	})

	t.Run("API key credentials return API key option", func(t *testing.T) {
		creds := &CodexCredentials{
			APIKey: "sk-test-key",
		}

		headers := CodexHeaderWithCredentials(creds)
		require.NotNil(t, headers)
		assert.Len(t, headers, 1, "should return 1 request option for API key")
	})

	t.Run("empty credentials return nil", func(t *testing.T) {
		creds := &CodexCredentials{}

		headers := CodexHeaderWithCredentials(creds)
		assert.Nil(t, headers)
	})

	t.Run("partial OAuth credentials return nil", func(t *testing.T) {
		// Only access token, no account ID
		creds := &CodexCredentials{
			AccessToken: "only_token",
		}

		headers := CodexHeaderWithCredentials(creds)
		assert.Nil(t, headers)

		// Only account ID, no access token
		creds = &CodexCredentials{
			AccountID: "only_account",
		}

		headers = CodexHeaderWithCredentials(creds)
		assert.Nil(t, headers)
	})

	t.Run("OAuth preferred over API key", func(t *testing.T) {
		// Note: With the current struct, a credential can have both OAuth and API key
		// but the implementation prefers OAuth
		creds := &CodexCredentials{
			AccessToken: "oauth_token",
			AccountID:   "account_123",
			APIKey:      "sk-fallback",
		}

		headers := CodexHeaderWithCredentials(creds)
		require.NotNil(t, headers)
		// Should return OAuth headers (5 options), not API key (1 option)
		assert.Len(t, headers, 5, "should use OAuth headers when both are present")
	})
}

func TestIsCodexOAuthEnabled(t *testing.T) {
	t.Run("nil credentials returns false", func(t *testing.T) {
		assert.False(t, IsCodexOAuthEnabled(nil))
	})

	t.Run("empty credentials returns false", func(t *testing.T) {
		creds := &CodexCredentials{}
		assert.False(t, IsCodexOAuthEnabled(creds))
	})

	t.Run("API key only returns false", func(t *testing.T) {
		creds := &CodexCredentials{
			APIKey: "sk-test-key",
		}
		assert.False(t, IsCodexOAuthEnabled(creds))
	})

	t.Run("partial OAuth returns false", func(t *testing.T) {
		// Only access token
		creds := &CodexCredentials{
			AccessToken: "only_token",
		}
		assert.False(t, IsCodexOAuthEnabled(creds))

		// Only account ID
		creds = &CodexCredentials{
			AccountID: "only_account",
		}
		assert.False(t, IsCodexOAuthEnabled(creds))
	})

	t.Run("full OAuth credentials returns true", func(t *testing.T) {
		creds := &CodexCredentials{
			AccessToken: "oauth_token",
			AccountID:   "account_123",
		}
		assert.True(t, IsCodexOAuthEnabled(creds))
	})

	t.Run("full OAuth with API key returns true", func(t *testing.T) {
		creds := &CodexCredentials{
			AccessToken: "oauth_token",
			AccountID:   "account_123",
			APIKey:      "sk-fallback",
		}
		assert.True(t, IsCodexOAuthEnabled(creds))
	})
}

func TestCodexConstants(t *testing.T) {
	t.Run("API base URL is set", func(t *testing.T) {
		assert.NotEmpty(t, CodexAPIBaseURL)
		assert.Contains(t, CodexAPIBaseURL, "chatgpt.com")
	})

	t.Run("originator is set", func(t *testing.T) {
		assert.NotEmpty(t, CodexOriginator)
		assert.Equal(t, "kodelet", CodexOriginator)
	})
}

func TestCodexAuthFileStructure(t *testing.T) {
	t.Run("JSON serialization matches expected format", func(t *testing.T) {
		authFile := CodexAuthFile{
			Tokens: CodexTokens{
				IDToken:     "test_id_token",
				AccessToken: "test_token",
				AccountID:   "test_account",
			},
			OpenAIAPIKey: "sk-key",
		}

		data, err := json.Marshal(authFile)
		require.NoError(t, err)

		var unmarshaled map[string]any
		require.NoError(t, json.Unmarshal(data, &unmarshaled))

		// Check structure matches Codex CLI format
		tokens, ok := unmarshaled["tokens"].(map[string]any)
		require.True(t, ok, "tokens should be an object")
		assert.Equal(t, "test_id_token", tokens["id_token"])
		assert.Equal(t, "test_token", tokens["access_token"])
		assert.Equal(t, "test_account", tokens["account_id"])
		assert.Equal(t, "sk-key", unmarshaled["OPENAI_API_KEY"])
	})

	t.Run("JSON deserialization from Codex CLI format", func(t *testing.T) {
		// Simulate the format that Codex CLI creates
		jsonData := `{
			"tokens": {
				"id_token": "real_id_token",
				"access_token": "real_token",
				"account_id": "real_account"
			},
			"OPENAI_API_KEY": "sk-real-key"
		}`

		var authFile CodexAuthFile
		require.NoError(t, json.Unmarshal([]byte(jsonData), &authFile))

		assert.Equal(t, "real_id_token", authFile.Tokens.IDToken)
		assert.Equal(t, "real_token", authFile.Tokens.AccessToken)
		assert.Equal(t, "real_account", authFile.Tokens.AccountID)
		assert.Equal(t, "sk-real-key", authFile.OpenAIAPIKey)
	})
}

func TestCodexCredentialsFilePermissions(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("can read file with restricted permissions", func(t *testing.T) {
		authData := CodexAuthFile{
			Tokens: CodexTokens{
				AccessToken: "secure_token",
				AccountID:   "secure_account",
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o700))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		// Write with restricted permissions (600)
		require.NoError(t, os.WriteFile(authFile, data, 0o600))

		creds, err := GetCodexCredentials()
		require.NoError(t, err)
		assert.Equal(t, "secure_token", creds.AccessToken)
	})
}

func TestParseCodexDeviceCodeInterval(t *testing.T) {
	t.Run("parses string interval", func(t *testing.T) {
		interval, err := parseCodexDeviceCodeInterval(json.RawMessage(`"5"`))
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, interval)
	})

	t.Run("parses numeric interval", func(t *testing.T) {
		interval, err := parseCodexDeviceCodeInterval(json.RawMessage(`3`))
		require.NoError(t, err)
		assert.Equal(t, 3*time.Second, interval)
	})

	t.Run("empty interval defaults to zero", func(t *testing.T) {
		interval, err := parseCodexDeviceCodeInterval(nil)
		require.NoError(t, err)
		assert.Zero(t, interval)
	})

	t.Run("invalid interval returns error", func(t *testing.T) {
		_, err := parseCodexDeviceCodeInterval(json.RawMessage(`{"bad":true}`))
		assert.Error(t, err)
	})
}

func TestRequestCodexDeviceCode(t *testing.T) {
	t.Run("returns device code details", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/accounts/deviceauth/usercode", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"device-auth-123","user_code":"CODE-123","interval":"2"}`))
		}))
		defer server.Close()

		deviceCode, err := requestCodexDeviceCode(context.Background(), server.URL)
		require.NoError(t, err)
		require.NotNil(t, deviceCode)
		assert.Equal(t, server.URL+"/codex/device", deviceCode.VerificationURL)
		assert.Equal(t, "CODE-123", deviceCode.UserCode)
		assert.Equal(t, "device-auth-123", deviceCode.deviceAuthID)
		assert.Equal(t, 2*time.Second, deviceCode.interval)
	})

	t.Run("supports legacy usercode field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"device-auth-123","usercode":"CODE-123","interval":"0"}`))
		}))
		defer server.Close()

		deviceCode, err := requestCodexDeviceCode(context.Background(), server.URL)
		require.NoError(t, err)
		assert.Equal(t, "CODE-123", deviceCode.UserCode)
	})

	t.Run("not found indicates unsupported device auth", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer server.Close()

		_, err := requestCodexDeviceCode(context.Background(), server.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device code login is not enabled")
	})
}

func TestCompleteCodexDeviceCodeLogin(t *testing.T) {
	t.Run("polls and exchanges device code", func(t *testing.T) {
		pollAttempts := 0
		accessToken := makeTestCodexJWT(t, "acct_device")

		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/accounts/deviceauth/token":
				pollAttempts++
				if pollAttempts == 1 {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"authorization_code":"auth-code-123","code_verifier":"verifier-123","code_challenge":"challenge-123"}`))
			case "/oauth/token":
				require.NoError(t, r.ParseForm())
				assert.Equal(t, "authorization_code", r.Form.Get("grant_type"))
				assert.Equal(t, "auth-code-123", r.Form.Get("code"))
				assert.Equal(t, "verifier-123", r.Form.Get("code_verifier"))
				assert.Equal(t, server.URL+"/deviceauth/callback", r.Form.Get("redirect_uri"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh-123","expires_in":3600}`, accessToken)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		deviceCode := &CodexDeviceCode{
			VerificationURL: server.URL + "/codex/device",
			UserCode:        "CODE-123",
			deviceAuthID:    "device-auth-123",
			interval:        0,
		}

		creds, err := completeCodexDeviceCodeLogin(context.Background(), server.URL, deviceCode)
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.Equal(t, "acct_device", creds.AccountID)
		assert.Equal(t, "refresh-123", creds.RefreshToken)
		assert.GreaterOrEqual(t, pollAttempts, 2)
	})

	t.Run("terminal poll errors are surfaced", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authorization_declined"}`))
		}))
		defer server.Close()

		deviceCode := &CodexDeviceCode{UserCode: "CODE-123", deviceAuthID: "device-auth-123", interval: 0}
		_, err := completeCodexDeviceCodeLogin(context.Background(), server.URL, deviceCode)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device authorization failed with status 401")
	})
}

func makeTestCodexJWT(t *testing.T, accountID string) string {
	t.Helper()

	header, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{
		codexJWTClaimPath: map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	require.NoError(t, err)

	return encodeJWTPart(header) + "." + encodeJWTPart(payload) + ".sig"
}

func encodeJWTPart(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
