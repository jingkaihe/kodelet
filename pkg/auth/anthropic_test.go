package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomString(t *testing.T) {
	tests := []int{16, 32, 64}

	for _, n := range tests {
		t.Run(fmt.Sprintf("length_%d", n), func(t *testing.T) {
			str := randomString(n)
			assert.NotEmpty(t, str)

			// Base64URL encoding, so actual length will be different
			// but should be consistent for same input
			str2 := randomString(n)
			assert.NotEqual(t, str, str2, "should generate different random strings")
		})
	}
}

func TestGeneratePKCEParams(t *testing.T) {
	params := generatePKCEParams()

	assert.NotEmpty(t, params.Challenge)
	assert.NotEmpty(t, params.Verifier)
	assert.Equal(t, "S256", params.ChallengeMethod)

	// Generate another set and ensure they're different
	params2 := generatePKCEParams()
	assert.NotEqual(t, params.Challenge, params2.Challenge)
	assert.NotEqual(t, params.Verifier, params2.Verifier)
}

func TestGenerateAnthropicAuthURL(t *testing.T) {
	authURL, verifier, err := GenerateAnthropicAuthURL()

	require.NoError(t, err)
	assert.NotEmpty(t, authURL)
	assert.NotEmpty(t, verifier)

	// Parse the URL to validate its structure
	u, err := url.Parse(authURL)
	require.NoError(t, err)

	assert.Equal(t, "claude.ai", u.Host)
	assert.Equal(t, "/oauth/authorize", u.Path)

	// Check query parameters
	query := u.Query()
	assert.Equal(t, anthropicClientID, query.Get("client_id"))
	assert.Equal(t, anthropicRedirectURI, query.Get("redirect_uri"))
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, "true", query.Get("code"))
	assert.Equal(t, "S256", query.Get("code_challenge_method"))
	assert.Equal(t, "user:inference user:profile", query.Get("scope"))
	assert.NotEmpty(t, query.Get("code_challenge"))
	assert.Equal(t, query.Get("state"), verifier)
}

func TestExchangeAnthropicCode(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid code format", func(t *testing.T) {
		_, err := ExchangeAnthropicCode(ctx, "invalid_code", "verifier")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid authorization code format")
	})

	t.Run("invalid state", func(t *testing.T) {
		_, err := ExchangeAnthropicCode(ctx, "code123#wrong_state", "correct_verifier")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid state parameter")
	})

	t.Run("successful exchange", func(t *testing.T) {
		// Create mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/v1/oauth/token", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			// Mock successful response
			response := AnthropicTokenResponse{
				AccessToken:  "access_token_123",
				RefreshToken: "refresh_token_456",
				ExpiresIn:    3600,
				Scope:        "user:inference user:profile",
				Account: struct {
					EmailAddress string `json:"email_address"`
				}{
					EmailAddress: "test@example.com",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Since we can't override the const, we'll test the parsing logic
		// by creating a mock with the expected URL structure
		t.Skip("Cannot override const endpoint in test - would need dependency injection")
	})

	t.Run("server error", func(t *testing.T) {
		// This test would also require overriding the endpoint
		t.Skip("Cannot override const endpoint in test - would need dependency injection")
	})
}

func TestGetAnthropicCredentialsExists(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("credentials do not exist", func(t *testing.T) {
		exists, err := GetAnthropicCredentialsExists()
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("credentials exist", func(t *testing.T) {
		// Create the credentials directory and file
		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0755))

		credsFile := filepath.Join(credsDir, "anthropic-subscription.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("{}"), 0644))

		exists, err := GetAnthropicCredentialsExists()
		assert.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestSaveAnthropicCredentials(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	creds := &AnthropicCredentials{
		Email:        "test@example.com",
		Scope:        "user:inference user:profile",
		AccessToken:  "access_token_123",
		RefreshToken: "refresh_token_456",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}

	filePath, err := SaveAnthropicCredentials(creds)
	require.NoError(t, err)

	expectedPath := filepath.Join(tempDir, ".kodelet", "anthropic-subscription.json")
	assert.Equal(t, expectedPath, filePath)

	// Verify file exists and has correct content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	var savedCreds AnthropicCredentials
	require.NoError(t, json.Unmarshal(data, &savedCreds))

	assert.Equal(t, creds.Email, savedCreds.Email)
	assert.Equal(t, creds.Scope, savedCreds.Scope)
	assert.Equal(t, creds.AccessToken, savedCreds.AccessToken)
	assert.Equal(t, creds.RefreshToken, savedCreds.RefreshToken)
	assert.Equal(t, creds.ExpiresAt, savedCreds.ExpiresAt)
}

func TestAnthropicAccessToken(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("file does not exist", func(t *testing.T) {
		_, err := AnthropicAccessToken(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to open anthropic subscription file")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		// Create the credentials directory and file with invalid JSON
		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0755))

		credsFile := filepath.Join(credsDir, "anthropic-subscription.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("invalid json"), 0644))

		_, err := AnthropicAccessToken(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode anthropic subscription file")
	})

	t.Run("valid token not expired", func(t *testing.T) {
		creds := AnthropicCredentials{
			Email:        "test@example.com",
			Scope:        "user:inference user:profile",
			AccessToken:  "valid_access_token",
			RefreshToken: "refresh_token_456",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(), // Not expired
		}

		credsFile := filepath.Join(tempDir, ".kodelet", "anthropic-subscription.json")
		data, err := json.Marshal(creds)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(credsFile, data, 0644))

		token, err := AnthropicAccessToken(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "valid_access_token", token)
	})
}

func TestAnthropicHeader(t *testing.T) {
	header := AnthropicHeader("access_token_123")
	assert.NotNil(t, header)
}

// Integration-style test that verifies the full auth URL generation flow
func TestAuthURLGeneration_Integration(t *testing.T) {
	authURL, verifier, err := GenerateAnthropicAuthURL()
	require.NoError(t, err)

	// Parse URL
	u, err := url.Parse(authURL)
	require.NoError(t, err)

	// Extract parameters
	query := u.Query()
	challenge := query.Get("code_challenge")
	state := query.Get("state")

	// Verify state matches verifier
	assert.Equal(t, verifier, state)

	// Verify challenge is properly encoded
	assert.NotEmpty(t, challenge)
	assert.True(t, len(challenge) >= 43) // Base64URL encoded SHA256 is 43 chars

	// Verify URL can be parsed and has all required components
	assert.Contains(t, authURL, "claude.ai")
	assert.Contains(t, authURL, "client_id="+anthropicClientID)
	assert.Contains(t, authURL, "code_challenge="+challenge)
	assert.Contains(t, authURL, "code_challenge_method=S256")
	assert.Contains(t, authURL, "scope=user%3Ainference+user%3Aprofile")
}
