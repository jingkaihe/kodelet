// Package auth provides authentication mechanisms for various AI providers.
// This file implements Codex CLI authentication for the ChatGPT backend API.
package auth

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openai/openai-go/v3/option"
	"github.com/pkg/errors"
)

// CodexTokens represents the OAuth tokens stored by the Codex CLI.
type CodexTokens struct {
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
}

// CodexAuthFile represents the authentication file created by the Codex CLI.
// This file is located at ~/.codex/auth.json and is created by running `codex login`.
type CodexAuthFile struct {
	Tokens       CodexTokens `json:"tokens"`
	OpenAIAPIKey string      `json:"OPENAI_API_KEY,omitempty"`
}

// CodexCredentials contains the resolved credentials for making Codex API calls.
type CodexCredentials struct {
	AccessToken string
	AccountID   string
	APIKey      string // Fallback OpenAI API key if tokens are not available
}

const (
	// CodexAPIBaseURL is the endpoint for the Codex Responses API.
	CodexAPIBaseURL = "https://chatgpt.com/backend-api/codex"

	// CodexOriginator identifies the client making requests.
	// Using the official Codex CLI originator for compatibility.
	CodexOriginator = "kodelet"
)

// codexAuthFilePath returns the path to the Codex auth file.
func codexAuthFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

// GetCodexCredentialsExists checks if the Codex auth file exists.
func GetCodexCredentialsExists() (bool, error) {
	authPath, err := codexAuthFilePath()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(authPath); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to check if codex auth file exists")
	}

	return true, nil
}

// GetCodexCredentials reads and returns the Codex credentials from the auth file.
func GetCodexCredentials() (*CodexCredentials, error) {
	authPath, err := codexAuthFilePath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("codex auth file not found, please login first with 'codex login'")
		}
		return nil, errors.Wrap(err, "failed to open codex auth file")
	}
	defer f.Close()

	var authFile CodexAuthFile
	if err := json.NewDecoder(f).Decode(&authFile); err != nil {
		return nil, errors.Wrap(err, "failed to decode codex auth file")
	}

	// Prefer OAuth tokens over API key
	if authFile.Tokens.AccessToken != "" && authFile.Tokens.AccountID != "" {
		return &CodexCredentials{
			AccessToken: authFile.Tokens.AccessToken,
			AccountID:   authFile.Tokens.AccountID,
		}, nil
	}

	// Fall back to API key if tokens are not available
	if authFile.OpenAIAPIKey != "" {
		return &CodexCredentials{
			APIKey: authFile.OpenAIAPIKey,
		}, nil
	}

	return nil, errors.New("codex auth file contains no valid credentials")
}

// CodexHeader returns the HTTP request options for Codex API calls.
// These headers are required for authentication with the ChatGPT backend API.
func CodexHeader() ([]option.RequestOption, error) {
	creds, err := GetCodexCredentials()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get codex credentials")
	}

	return CodexHeaderWithCredentials(creds), nil
}

// CodexHeaderWithCredentials returns the HTTP request options for Codex API calls
// using the provided credentials. Returns nil if credentials are nil or empty.
func CodexHeaderWithCredentials(creds *CodexCredentials) []option.RequestOption {
	if creds == nil {
		return nil
	}

	if creds.AccessToken != "" && creds.AccountID != "" {
		// Use OAuth tokens - set Authorization header with Bearer token
		return []option.RequestOption{
			option.WithBaseURL(CodexAPIBaseURL),
			option.WithHeader("Authorization", "Bearer "+creds.AccessToken),
			option.WithHeader("ChatGPT-Account-ID", creds.AccountID),
			option.WithHeader("OpenAI-Beta", "responses=experimental"),
			option.WithHeader("originator", CodexOriginator),
			option.WithHeader("Accept", "text/event-stream"),
		}
	}

	// Fall back to API key (standard OpenAI API)
	if creds.APIKey != "" {
		return []option.RequestOption{
			option.WithAPIKey(creds.APIKey),
		}
	}

	return nil
}

// IsCodexOAuthEnabled returns true if OAuth credentials are available.
func IsCodexOAuthEnabled(creds *CodexCredentials) bool {
	return creds != nil && creds.AccessToken != "" && creds.AccountID != ""
}
