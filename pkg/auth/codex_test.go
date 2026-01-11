package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			},
		}

		kodeletDir := filepath.Join(tempDir, ".kodelet")
		require.NoError(t, os.MkdirAll(kodeletDir, 0o755))

		authFile := filepath.Join(kodeletDir, "codex-credentials.json")
		data, err := json.Marshal(authData)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authFile, data, 0o644))

		headers, err := CodexHeader()
		require.NoError(t, err)
		require.NotNil(t, headers)
		assert.Len(t, headers, 6, "should return 6 request options for OAuth")

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

		headers, err := CodexHeader()
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

		_, err := CodexHeader()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get codex credentials")
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
		assert.Len(t, headers, 6, "should return 6 request options for OAuth")
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
		// Should return OAuth headers (6 options), not API key (1 option)
		assert.Len(t, headers, 6, "should use OAuth headers when both are present")
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
		assert.Equal(t, "test_token", tokens["access_token"])
		assert.Equal(t, "test_account", tokens["account_id"])
		assert.Equal(t, "sk-key", unmarshaled["OPENAI_API_KEY"])
	})

	t.Run("JSON deserialization from Codex CLI format", func(t *testing.T) {
		// Simulate the format that Codex CLI creates
		jsonData := `{
			"tokens": {
				"access_token": "real_token",
				"account_id": "real_account"
			},
			"OPENAI_API_KEY": "sk-real-key"
		}`

		var authFile CodexAuthFile
		require.NoError(t, json.Unmarshal([]byte(jsonData), &authFile))

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
