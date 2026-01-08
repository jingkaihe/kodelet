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

	t.Run("multi-account credentials exist", func(t *testing.T) {
		// Create the credentials directory and file
		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		credsFile := filepath.Join(credsDir, "anthropic-credentials.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("{}"), 0o644))

		exists, err := GetAnthropicCredentialsExists()
		assert.NoError(t, err)
		assert.True(t, exists)

		// Clean up for next test
		require.NoError(t, os.Remove(credsFile))
	})

	t.Run("legacy credentials exist", func(t *testing.T) {
		// Create the credentials directory and legacy file
		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		legacyFile := filepath.Join(credsDir, "anthropic-subscription.json")
		require.NoError(t, os.WriteFile(legacyFile, []byte("{}"), 0o644))

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

	expectedPath := filepath.Join(tempDir, ".kodelet", "anthropic-credentials.json")
	assert.Equal(t, expectedPath, filePath)

	// Verify file exists and has correct content (multi-account format)
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	var savedCredsFile AnthropicCredentialsFile
	require.NoError(t, json.Unmarshal(data, &savedCredsFile))

	// Should use email prefix as alias
	assert.Equal(t, "test", savedCredsFile.DefaultAccount)
	savedCreds, exists := savedCredsFile.Accounts["test"]
	require.True(t, exists)

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

	t.Run("no accounts exist", func(t *testing.T) {
		_, err := AnthropicAccessToken(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no Anthropic accounts found")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		// Create the credentials directory and file with invalid JSON
		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		credsFile := filepath.Join(credsDir, "anthropic-credentials.json")
		require.NoError(t, os.WriteFile(credsFile, []byte("invalid json"), 0o644))

		_, err := AnthropicAccessToken(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode credentials file")

		// Clean up for next test
		require.NoError(t, os.Remove(credsFile))
	})

	t.Run("valid token not expired", func(t *testing.T) {
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "test",
			Accounts: map[string]AnthropicCredentials{
				"test": {
					Email:        "test@example.com",
					Scope:        "user:inference user:profile",
					AccessToken:  "valid_access_token",
					RefreshToken: "refresh_token_456",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(), // Not expired
				},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		token, err := AnthropicAccessToken(ctx, "")
		assert.NoError(t, err)
		assert.Equal(t, "valid_access_token", token)
	})

	t.Run("get token for specific alias", func(t *testing.T) {
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "default",
			Accounts: map[string]AnthropicCredentials{
				"default": {
					Email:        "default@example.com",
					AccessToken:  "default_token",
					RefreshToken: "refresh_token",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
				"work": {
					Email:        "work@company.com",
					AccessToken:  "work_token",
					RefreshToken: "refresh_token",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		// Get token for specific alias
		token, err := AnthropicAccessToken(ctx, "work")
		assert.NoError(t, err)
		assert.Equal(t, "work_token", token)

		// Empty alias should return default
		token, err = AnthropicAccessToken(ctx, "")
		assert.NoError(t, err)
		assert.Equal(t, "default_token", token)
	})
}

func TestAnthropicHeader(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("returns headers for valid account", func(t *testing.T) {
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "test",
			Accounts: map[string]AnthropicCredentials{
				"test": {
					Email:        "test@example.com",
					AccessToken:  "test_access_token_123",
					RefreshToken: "refresh_token",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		headers, err := AnthropicHeader(ctx, "")
		require.NoError(t, err)
		require.NotNil(t, headers)
		require.Len(t, headers, 4, "should return 4 request options")
	})

	t.Run("returns headers for specific alias", func(t *testing.T) {
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "default",
			Accounts: map[string]AnthropicCredentials{
				"default": {
					Email:        "default@example.com",
					AccessToken:  "default_token",
					RefreshToken: "refresh_token",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
				"work": {
					Email:        "work@company.com",
					AccessToken:  "work_token",
					RefreshToken: "refresh_token",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		headers, err := AnthropicHeader(ctx, "work")
		require.NoError(t, err)
		require.NotNil(t, headers)
		require.Len(t, headers, 4, "should return 4 request options")
	})

	t.Run("error for non-existent account", func(t *testing.T) {
		_, err := AnthropicHeader(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestAnthropicHeaderWithToken(t *testing.T) {
	accessToken := "test_access_token_123"
	headers := AnthropicHeaderWithToken(accessToken)

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

	// Create expired credentials in multi-account format
	credsFile := &AnthropicCredentialsFile{
		DefaultAccount: "test",
		Accounts: map[string]AnthropicCredentials{
			"test": {
				Email:        "test@example.com",
				Scope:        "user:inference user:profile",
				AccessToken:  "expired_token",
				RefreshToken: "refresh_token",
				ExpiresAt:    time.Now().Add(-time.Hour).Unix(), // Already expired
			},
		},
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	filePath := filepath.Join(credsDir, "anthropic-credentials.json")
	data, err := json.Marshal(credsFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, data, 0o644))

	// This will fail because we can't mock the refresh endpoint,
	// but it tests that the expiration logic is working
	_, err = AnthropicAccessToken(ctx, "")
	assert.Error(t, err)
	// The error should be related to the refresh attempt, not file reading
	assert.Contains(t, err.Error(), "refresh token")
}

// Tests for multi-account credential storage

func TestGenerateAliasFromEmail(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"test@example.com", "test"},
		{"john.doe@company.org", "john.doe"},
		{"", "default"},
		{"@nodomain.com", "default"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := GenerateAliasFromEmail(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSaveAnthropicCredentialsWithAlias(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("save first account becomes default", func(t *testing.T) {
		creds := &AnthropicCredentials{
			Email:        "work@company.com",
			Scope:        "user:inference user:profile",
			AccessToken:  "access_token_work",
			RefreshToken: "refresh_token_work",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}

		filePath, err := SaveAnthropicCredentialsWithAlias("work", creds)
		require.NoError(t, err)
		assert.Contains(t, filePath, "anthropic-credentials.json")

		// Verify it became the default
		defaultAlias, err := GetDefaultAnthropicAccount()
		require.NoError(t, err)
		assert.Equal(t, "work", defaultAlias)
	})

	t.Run("save second account does not change default", func(t *testing.T) {
		creds := &AnthropicCredentials{
			Email:        "personal@gmail.com",
			Scope:        "user:inference user:profile",
			AccessToken:  "access_token_personal",
			RefreshToken: "refresh_token_personal",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}

		_, err := SaveAnthropicCredentialsWithAlias("personal", creds)
		require.NoError(t, err)

		// Default should still be work
		defaultAlias, err := GetDefaultAnthropicAccount()
		require.NoError(t, err)
		assert.Equal(t, "work", defaultAlias)
	})

	t.Run("save without alias uses email prefix", func(t *testing.T) {
		creds := &AnthropicCredentials{
			Email:        "auto@domain.com",
			Scope:        "user:inference user:profile",
			AccessToken:  "access_token_auto",
			RefreshToken: "refresh_token_auto",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}

		_, err := SaveAnthropicCredentialsWithAlias("", creds)
		require.NoError(t, err)

		// Should be retrievable by email prefix
		retrieved, err := GetAnthropicCredentialsByAlias("auto")
		require.NoError(t, err)
		assert.Equal(t, "auto@domain.com", retrieved.Email)
	})
}

func TestGetAnthropicCredentialsByAlias(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Set up multi-account credentials
	credsFile := &AnthropicCredentialsFile{
		DefaultAccount: "work",
		Accounts: map[string]AnthropicCredentials{
			"work": {
				Email:        "work@company.com",
				Scope:        "user:inference user:profile",
				AccessToken:  "access_token_work",
				RefreshToken: "refresh_token_work",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			},
			"personal": {
				Email:        "personal@gmail.com",
				Scope:        "user:inference user:profile",
				AccessToken:  "access_token_personal",
				RefreshToken: "refresh_token_personal",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			},
		},
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	filePath := filepath.Join(credsDir, "anthropic-credentials.json")
	data, err := json.Marshal(credsFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, data, 0o644))

	t.Run("get by alias", func(t *testing.T) {
		creds, err := GetAnthropicCredentialsByAlias("personal")
		require.NoError(t, err)
		assert.Equal(t, "personal@gmail.com", creds.Email)
		assert.Equal(t, "access_token_personal", creds.AccessToken)
	})

	t.Run("get default with empty alias", func(t *testing.T) {
		creds, err := GetAnthropicCredentialsByAlias("")
		require.NoError(t, err)
		assert.Equal(t, "work@company.com", creds.Email)
	})

	t.Run("error on non-existent alias", func(t *testing.T) {
		_, err := GetAnthropicCredentialsByAlias("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAnthropicAccounts(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("empty list when no accounts", func(t *testing.T) {
		accounts, err := ListAnthropicAccounts()
		require.NoError(t, err)
		assert.Empty(t, accounts)
	})

	t.Run("list multiple accounts", func(t *testing.T) {
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "work",
			Accounts: map[string]AnthropicCredentials{
				"work": {
					Email:     "work@company.com",
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				},
				"personal": {
					Email:     "personal@gmail.com",
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		accounts, err := ListAnthropicAccounts()
		require.NoError(t, err)
		assert.Len(t, accounts, 2)

		// Check that default is marked correctly
		var defaultCount int
		for _, acc := range accounts {
			if acc.IsDefault {
				defaultCount++
				assert.Equal(t, "work", acc.Alias)
			}
		}
		assert.Equal(t, 1, defaultCount)
	})
}

func TestSetDefaultAnthropicAccount(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Set up multi-account credentials
	credsFile := &AnthropicCredentialsFile{
		DefaultAccount: "work",
		Accounts: map[string]AnthropicCredentials{
			"work":     {Email: "work@company.com"},
			"personal": {Email: "personal@gmail.com"},
		},
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	filePath := filepath.Join(credsDir, "anthropic-credentials.json")
	data, err := json.Marshal(credsFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, data, 0o644))

	t.Run("set valid default", func(t *testing.T) {
		err := SetDefaultAnthropicAccount("personal")
		require.NoError(t, err)

		defaultAlias, err := GetDefaultAnthropicAccount()
		require.NoError(t, err)
		assert.Equal(t, "personal", defaultAlias)
	})

	t.Run("error on non-existent alias", func(t *testing.T) {
		err := SetDefaultAnthropicAccount("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRemoveAnthropicAccount(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	t.Run("remove non-default account", func(t *testing.T) {
		// Set up multi-account credentials
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "work",
			Accounts: map[string]AnthropicCredentials{
				"work":     {Email: "work@company.com"},
				"personal": {Email: "personal@gmail.com"},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(credsDir, 0o755))

		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		err = RemoveAnthropicAccount("personal")
		require.NoError(t, err)

		// Verify personal is removed
		_, err = GetAnthropicCredentialsByAlias("personal")
		assert.Error(t, err)

		// Default should still be work
		defaultAlias, err := GetDefaultAnthropicAccount()
		require.NoError(t, err)
		assert.Equal(t, "work", defaultAlias)
	})

	t.Run("remove default account sets new default", func(t *testing.T) {
		// Reset credentials with fresh accounts
		credsFile := &AnthropicCredentialsFile{
			DefaultAccount: "work",
			Accounts: map[string]AnthropicCredentials{
				"work":     {Email: "work@company.com"},
				"personal": {Email: "personal@gmail.com"},
			},
		}

		credsDir := filepath.Join(tempDir, ".kodelet")
		filePath := filepath.Join(credsDir, "anthropic-credentials.json")
		data, err := json.Marshal(credsFile)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filePath, data, 0o644))

		err = RemoveAnthropicAccount("work")
		require.NoError(t, err)

		// Default should now be personal (the only remaining account)
		defaultAlias, err := GetDefaultAnthropicAccount()
		require.NoError(t, err)
		assert.Equal(t, "personal", defaultAlias)
	})

	t.Run("error on non-existent alias", func(t *testing.T) {
		err := RemoveAnthropicAccount("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMigrateFromLegacyCredentials(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Create legacy credentials file
	legacyCreds := AnthropicCredentials{
		Email:        "legacy@example.com",
		Scope:        "user:inference user:profile",
		AccessToken:  "legacy_access_token",
		RefreshToken: "legacy_refresh_token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	legacyFile := filepath.Join(credsDir, "anthropic-subscription.json")
	data, err := json.Marshal(legacyCreds)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(legacyFile, data, 0o644))

	// Accessing credentials should trigger migration
	creds, err := GetAnthropicCredentialsByAlias("")
	require.NoError(t, err)
	assert.Equal(t, "legacy@example.com", creds.Email)
	assert.Equal(t, "legacy_access_token", creds.AccessToken)

	// Verify the multi-account file was created
	multiFile := filepath.Join(credsDir, "anthropic-credentials.json")
	_, err = os.Stat(multiFile)
	assert.NoError(t, err)

	// Verify default was set to email prefix
	defaultAlias, err := GetDefaultAnthropicAccount()
	require.NoError(t, err)
	assert.Equal(t, "legacy", defaultAlias)
}

func TestAnthropicAccessTokenForAlias(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Set up multi-account credentials
	credsFile := &AnthropicCredentialsFile{
		DefaultAccount: "work",
		Accounts: map[string]AnthropicCredentials{
			"work": {
				Email:        "work@company.com",
				AccessToken:  "work_access_token",
				RefreshToken: "work_refresh_token",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			},
			"personal": {
				Email:        "personal@gmail.com",
				AccessToken:  "personal_access_token",
				RefreshToken: "personal_refresh_token",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			},
		},
	}

	credsDir := filepath.Join(tempDir, ".kodelet")
	require.NoError(t, os.MkdirAll(credsDir, 0o755))

	filePath := filepath.Join(credsDir, "anthropic-credentials.json")
	data, err := json.Marshal(credsFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, data, 0o644))

	t.Run("get token for specific alias", func(t *testing.T) {
		token, err := AnthropicAccessTokenForAlias(ctx, "personal")
		require.NoError(t, err)
		assert.Equal(t, "personal_access_token", token)
	})

	t.Run("get token for default account", func(t *testing.T) {
		token, err := AnthropicAccessTokenForAlias(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "work_access_token", token)
	})

	t.Run("backward compatible - AnthropicAccessTokenForAlias calls AnthropicAccessToken", func(t *testing.T) {
		// AnthropicAccessTokenForAlias is now just an alias for AnthropicAccessToken
		token, err := AnthropicAccessToken(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "work_access_token", token)
	})
}
