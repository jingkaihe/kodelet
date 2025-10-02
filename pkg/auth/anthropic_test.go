package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

			// Verify proper base64URL encoding (no padding)
			assert.NotContains(t, str, "=")
			assert.NotContains(t, str, "+")
			assert.NotContains(t, str, "/")

			// Decode and verify byte length matches input
			decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(str)
			require.NoError(t, err)
			assert.Equal(t, n, len(decoded), "decoded bytes should match requested length")

			// Verify randomness - generate multiple strings and ensure uniqueness
			seen := make(map[string]bool)
			for i := 0; i < 10; i++ {
				s := randomString(n)
				assert.False(t, seen[s], "random string should be unique")
				seen[s] = true
			}
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
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		credsFile := filepath.Join(credsDir, "anthropic-subscription.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("{}"), 0o644))

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
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		credsFile := filepath.Join(credsDir, "anthropic-subscription.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("invalid json"), 0o644))

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
		require.NoError(t, os.WriteFile(credsFile, data, 0o644))

		token, err := AnthropicAccessToken(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "valid_access_token", token)
	})
}

func TestAnthropicHeader(t *testing.T) {
	accessToken := "test_access_token_123"
	headers := AnthropicHeader(accessToken)

	// Verify the function returns request options
	require.NotNil(t, headers)
	require.Len(t, headers, 4, "should return 4 request options")

	// Note: We can't easily test the actual header values without
	// access to the internal option.RequestOption structure.
	// This would require creating an actual HTTP request with these options
	// and inspecting the headers, which is better suited for integration tests.
}

// Test that expired credentials trigger refresh
func TestAnthropicAccessToken_ExpiredToken(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Create expired credentials
	creds := AnthropicCredentials{
		Email:        "test@example.com",
		Scope:        "user:inference user:profile",
		AccessToken:  "expired_token",
		RefreshToken: "refresh_token",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(), // Already expired
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	credsFile := filepath.Join(credsDir, "anthropic-subscription.json")
	data, err := json.Marshal(creds)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(credsFile, data, 0o644))

	// This will fail because we can't mock the refresh endpoint,
	// but it tests that the expiration logic is working
	_, err = AnthropicAccessToken(ctx)
	assert.Error(t, err)
	// The error should be related to the refresh attempt, not file reading
	assert.Contains(t, err.Error(), "refresh token")
}
