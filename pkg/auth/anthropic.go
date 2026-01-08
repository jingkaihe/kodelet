// Package auth provides authentication mechanisms for various AI providers.
// It handles OAuth2 flows, credential storage, and token management for
// Anthropic Claude and GitHub Copilot integrations.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/pkg/errors"
	"golang.org/x/oauth2/authhandler"
)

// AnthropicTokenResponse represents the OAuth2 token response from Anthropic's authentication endpoint.
type AnthropicTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	}
}

// AnthropicCredentials stores the authentication credentials for Anthropic Claude API.
type AnthropicCredentials struct {
	Email        string `json:"email"`
	Scope        string `json:"scope"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// AnthropicCredentialsFile stores multiple Anthropic accounts with a default selection.
type AnthropicCredentialsFile struct {
	DefaultAccount string                          `json:"default"`
	Accounts       map[string]AnthropicCredentials `json:"accounts"`
}

// AnthropicAccountInfo represents summary information about an account for listing.
type AnthropicAccountInfo struct {
	Alias     string
	Email     string
	ExpiresAt int64
	IsDefault bool
}

const (
	anthropicClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthEndpoint  = "https://claude.ai/oauth/authorize"
	anthropicRedirectURI   = "https://console.anthropic.com/oauth/code/callback"
	anthropicTokenEndpoint = "https://console.anthropic.com/v1/oauth/token"

	// tokenRefreshThreshold is the duration before token expiry when we should refresh
	tokenRefreshThreshold = 10 * time.Minute
)

// ValidateAlias checks if an alias is valid for use as an account identifier.
// Valid aliases cannot contain whitespace, path separators, or be empty.
func ValidateAlias(alias string) error {
	if alias == "" {
		return errors.New("alias cannot be empty")
	}
	if strings.ContainsAny(alias, " \t\n\r/\\") {
		return errors.New("alias cannot contain whitespace or path separators")
	}
	if len(alias) > 64 {
		return errors.New("alias cannot be longer than 64 characters")
	}
	return nil
}

// anthropicCredentialsFilePath returns the path to the multi-account credentials file.
func anthropicCredentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(home, ".kodelet", "anthropic-credentials.json"), nil
}

// legacyAnthropicCredentialsFilePath returns the path to the legacy single-account credentials file.
func legacyAnthropicCredentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(home, ".kodelet", "anthropic-subscription.json"), nil
}

// readAnthropicCredentialsFile reads the multi-account credentials file.
// If the file doesn't exist, it returns an empty credentials file.
// It also handles migration from the legacy single-account file.
func readAnthropicCredentialsFile() (*AnthropicCredentialsFile, error) {
	filePath, err := anthropicCredentialsFilePath()
	if err != nil {
		return nil, err
	}

	// Check if the multi-account file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Try to migrate from legacy file
		legacyPath, err := legacyAnthropicCredentialsFilePath()
		if err != nil {
			return nil, err
		}

		if _, err := os.Stat(legacyPath); err == nil {
			// Legacy file exists, migrate it
			return migrateFromLegacyCredentials(legacyPath)
		}

		// No credentials file exists, return empty
		return &AnthropicCredentialsFile{
			Accounts: make(map[string]AnthropicCredentials),
		}, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open credentials file")
	}
	defer f.Close()

	var credsFile AnthropicCredentialsFile
	if err := json.NewDecoder(f).Decode(&credsFile); err != nil {
		return nil, errors.Wrap(err, "failed to decode credentials file")
	}

	if credsFile.Accounts == nil {
		credsFile.Accounts = make(map[string]AnthropicCredentials)
	}

	return &credsFile, nil
}

// writeAnthropicCredentialsFile writes the multi-account credentials file.
// Uses atomic write pattern (write to temp file, then rename) to prevent corruption.
func writeAnthropicCredentialsFile(credsFile *AnthropicCredentialsFile) error {
	if credsFile == nil {
		return errors.New("credentials file cannot be nil")
	}

	// Ensure Accounts map is initialized
	if credsFile.Accounts == nil {
		credsFile.Accounts = make(map[string]AnthropicCredentials)
	}

	filePath, err := anthropicCredentialsFilePath()
	if err != nil {
		return err
	}

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create credentials directory")
	}

	// Write to a temporary file first for atomic operation
	tempFile, err := os.CreateTemp(dir, "anthropic-credentials-*.tmp")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary credentials file")
	}
	tempPath := tempFile.Name()

	// Clean up temp file on error
	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(credsFile); err != nil {
		tempFile.Close()
		return errors.Wrap(err, "failed to write credentials")
	}

	// Ensure data is flushed to disk
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return errors.Wrap(err, "failed to sync credentials file")
	}

	if err := tempFile.Close(); err != nil {
		return errors.Wrap(err, "failed to close temporary credentials file")
	}

	// Atomic rename
	if err := os.Rename(tempPath, filePath); err != nil {
		return errors.Wrap(err, "failed to save credentials file")
	}

	success = true
	return nil
}

// migrateFromLegacyCredentials reads the legacy single-account file and converts it to multi-account format.
func migrateFromLegacyCredentials(legacyPath string) (*AnthropicCredentialsFile, error) {
	f, err := os.Open(legacyPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open legacy credentials file")
	}
	defer f.Close()

	var creds AnthropicCredentials
	if err := json.NewDecoder(f).Decode(&creds); err != nil {
		return nil, errors.Wrap(err, "failed to decode legacy credentials file")
	}

	// Generate alias from email prefix
	alias := GenerateAliasFromEmail(creds.Email)

	credsFile := &AnthropicCredentialsFile{
		DefaultAccount: alias,
		Accounts: map[string]AnthropicCredentials{
			alias: creds,
		},
	}

	// Save the migrated credentials
	if err := writeAnthropicCredentialsFile(credsFile); err != nil {
		return nil, errors.Wrap(err, "failed to save migrated credentials")
	}

	return credsFile, nil
}

// GenerateAliasFromEmail extracts the prefix (part before @) from an email address to use as an alias.
func GenerateAliasFromEmail(email string) string {
	if email == "" {
		return "default"
	}
	if idx := strings.Index(email, "@"); idx > 0 {
		return email[:idx]
	}
	return email
}

// SaveAnthropicCredentialsWithAlias saves credentials for a specific account alias.
// If this is the first account, it will be set as default.
// Returns the file path where credentials were saved.
func SaveAnthropicCredentialsWithAlias(alias string, creds *AnthropicCredentials) (string, error) {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return "", err
	}

	// If no alias specified, generate from email
	if alias == "" {
		alias = GenerateAliasFromEmail(creds.Email)
	}

	// Validate the alias
	if err := ValidateAlias(alias); err != nil {
		return "", errors.Wrap(err, "invalid alias")
	}

	// Save the account
	credsFile.Accounts[alias] = *creds

	// If this is the first account or no default is set, make it default
	if credsFile.DefaultAccount == "" || len(credsFile.Accounts) == 1 {
		credsFile.DefaultAccount = alias
	}

	if err := writeAnthropicCredentialsFile(credsFile); err != nil {
		return "", err
	}

	filePath, err := anthropicCredentialsFilePath()
	if err != nil {
		return "", err
	}

	return filePath, nil
}

// GetAnthropicCredentialsByAlias retrieves credentials for a specific account alias.
// If alias is empty, returns the default account credentials.
func GetAnthropicCredentialsByAlias(alias string) (*AnthropicCredentials, error) {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return nil, err
	}

	if len(credsFile.Accounts) == 0 {
		return nil, errors.New("no Anthropic accounts found, please login first with 'kodelet anthropic login'")
	}

	// If no alias specified, use default
	if alias == "" {
		alias = credsFile.DefaultAccount
		if alias == "" {
			return nil, errors.New("no default account set")
		}
	}

	creds, exists := credsFile.Accounts[alias]
	if !exists {
		return nil, errors.Errorf("account '%s' not found", alias)
	}

	return &creds, nil
}

// ListAnthropicAccounts returns information about all stored Anthropic accounts.
func ListAnthropicAccounts() ([]AnthropicAccountInfo, error) {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return nil, err
	}

	var accounts []AnthropicAccountInfo
	for alias, creds := range credsFile.Accounts {
		accounts = append(accounts, AnthropicAccountInfo{
			Alias:     alias,
			Email:     creds.Email,
			ExpiresAt: creds.ExpiresAt,
			IsDefault: alias == credsFile.DefaultAccount,
		})
	}

	return accounts, nil
}

// SetDefaultAnthropicAccount sets the default account alias.
func SetDefaultAnthropicAccount(alias string) error {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return err
	}

	if _, exists := credsFile.Accounts[alias]; !exists {
		return errors.Errorf("account '%s' not found", alias)
	}

	credsFile.DefaultAccount = alias
	return writeAnthropicCredentialsFile(credsFile)
}

// GetDefaultAnthropicAccount returns the alias of the default account.
func GetDefaultAnthropicAccount() (string, error) {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return "", err
	}

	if credsFile.DefaultAccount == "" {
		return "", errors.New("no default account set")
	}

	return credsFile.DefaultAccount, nil
}

// AccountExists checks if an account with the given alias exists.
func AccountExists(alias string) (bool, error) {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return false, err
	}

	_, exists := credsFile.Accounts[alias]
	return exists, nil
}

// RemoveAnthropicAccount removes an account by alias.
// If removing the default account, clears the default (or sets to another account if available).
func RemoveAnthropicAccount(alias string) error {
	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return err
	}

	if _, exists := credsFile.Accounts[alias]; !exists {
		return errors.Errorf("account '%s' not found", alias)
	}

	delete(credsFile.Accounts, alias)

	// If we removed the default account, set a new default if possible
	if credsFile.DefaultAccount == alias {
		credsFile.DefaultAccount = ""
		for newAlias := range credsFile.Accounts {
			credsFile.DefaultAccount = newAlias
			break
		}
	}

	return writeAnthropicCredentialsFile(credsFile)
}

// RenameAnthropicAccount renames an account from oldAlias to newAlias.
// If the account being renamed is the default, updates the default to the new alias.
func RenameAnthropicAccount(oldAlias, newAlias string) error {
	if oldAlias == newAlias {
		return errors.New("old and new alias are the same")
	}

	// Validate the new alias
	if err := ValidateAlias(newAlias); err != nil {
		return errors.Wrap(err, "invalid new alias")
	}

	credsFile, err := readAnthropicCredentialsFile()
	if err != nil {
		return err
	}

	creds, exists := credsFile.Accounts[oldAlias]
	if !exists {
		return errors.Errorf("account '%s' not found", oldAlias)
	}

	if _, exists := credsFile.Accounts[newAlias]; exists {
		return errors.Errorf("account '%s' already exists", newAlias)
	}

	delete(credsFile.Accounts, oldAlias)
	credsFile.Accounts[newAlias] = creds

	if credsFile.DefaultAccount == oldAlias {
		credsFile.DefaultAccount = newAlias
	}

	return writeAnthropicCredentialsFile(credsFile)
}

func randomString(n int) string {
	data := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		panic(err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

func generatePKCEParams() *authhandler.PKCEParams {
	verifier := randomString(32)
	sha := sha256.Sum256([]byte(verifier))
	challenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(sha[:])
	return &authhandler.PKCEParams{
		Challenge:       challenge,
		ChallengeMethod: "S256",
		Verifier:        verifier,
	}
}

// GenerateAnthropicAuthURL generates an OAuth2 authorization URL for Anthropic authentication.
// It returns the auth URL, PKCE verifier, and any error encountered.
func GenerateAnthropicAuthURL() (authURL string, verifier string, err error) {
	pkceParams := generatePKCEParams()

	scopes := []string{
		"user:inference",
		"user:profile",
	}

	query := url.Values{
		"client_id":             {anthropicClientID},
		"redirect_uri":          {anthropicRedirectURI},
		"response_type":         {"code"},
		"code":                  {"true"},
		"code_challenge":        {pkceParams.Challenge},
		"code_challenge_method": {pkceParams.ChallengeMethod},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {pkceParams.Verifier},
	}

	u, err := url.Parse(anthropicAuthEndpoint)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to parse auth endpoint")
	}

	u.RawQuery = query.Encode()
	return u.String(), pkceParams.Verifier, nil
}

// ExchangeAnthropicCode exchanges an authorization code for Anthropic access credentials.
// The code parameter should be in the format "code#state".
func ExchangeAnthropicCode(ctx context.Context, code string, verifier string) (*AnthropicCredentials, error) {
	// Parse the code to extract code and state
	splits := strings.Split(code, "#")
	if len(splits) != 2 {
		return nil, errors.New("invalid authorization code format - expected format: code#state")
	}

	actualCode, state := splits[0], splits[1]
	if state != verifier {
		return nil, errors.New("invalid state parameter - please try the authentication process again")
	}

	// Prepare token exchange request
	payload := map[string]string{
		"code":          actualCode,
		"state":         state,
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"redirect_uri":  anthropicRedirectURI,
		"code_verifier": verifier,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token request payload")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicTokenEndpoint, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create token request")
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send token request")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read token response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResponse AnthropicTokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return nil, errors.Wrap(err, "failed to parse token response")
	}

	return &AnthropicCredentials{
		AccessToken:  tokenResponse.AccessToken,
		RefreshToken: tokenResponse.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second).Unix(),
		Email:        tokenResponse.Account.EmailAddress,
		Scope:        tokenResponse.Scope,
	}, nil
}

// GetAnthropicCredentialsExists checks if Anthropic credentials file exists in the user's home directory.
// It checks both the new multi-account file and the legacy single-account file.
func GetAnthropicCredentialsExists() (bool, error) {
	// Check multi-account file first
	multiPath, err := anthropicCredentialsFilePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(multiPath); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, errors.Wrap(err, "failed to check if anthropic credentials file exists")
	}

	// Check legacy file
	legacyPath, err := legacyAnthropicCredentialsFilePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, errors.Wrap(err, "failed to check if anthropic credentials file exists")
	}

	return false, nil
}

// SaveAnthropicCredentials saves Anthropic credentials to the multi-account storage.
// Uses the email prefix as the alias. If this is the first account, it becomes the default.
// Returns the file path where credentials were saved.
func SaveAnthropicCredentials(creds *AnthropicCredentials) (string, error) {
	return SaveAnthropicCredentialsWithAlias("", creds)
}

// refreshAnthropicTokenForAlias refreshes the token for a specific account alias.
func refreshAnthropicTokenForAlias(ctx context.Context, alias string, creds *AnthropicCredentials) (*AnthropicCredentials, error) {
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     anthropicClientID,
		"refresh_token": creds.RefreshToken,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal refresh token request payload")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicTokenEndpoint, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create refresh token request")
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send refresh token request")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read refresh token response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("refresh token failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResponse AnthropicTokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return nil, errors.Wrap(err, "failed to parse token response")
	}

	refreshed := &AnthropicCredentials{
		AccessToken:  tokenResponse.AccessToken,
		RefreshToken: tokenResponse.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second).Unix(),
		Email:        creds.Email,
		Scope:        creds.Scope,
	}

	// Use the provided alias for saving, or generate from email if empty
	saveAlias := alias
	if saveAlias == "" {
		saveAlias = GenerateAliasFromEmail(creds.Email)
	}

	if _, err := SaveAnthropicCredentialsWithAlias(saveAlias, refreshed); err != nil {
		return nil, errors.Wrap(err, "failed to save anthropic credentials")
	}

	return refreshed, nil
}

// AnthropicAccessToken retrieves a valid Anthropic access token for the specified account alias.
// If alias is empty, uses the default account.
// It automatically handles token refresh when the token is within 10 minutes of expiration.
func AnthropicAccessToken(ctx context.Context, alias string) (string, error) {
	creds, err := GetAnthropicCredentialsByAlias(alias)
	if err != nil {
		return "", err
	}

	// Get the actual alias being used (for saving after refresh)
	actualAlias := alias
	if actualAlias == "" {
		actualAlias, _ = GetDefaultAnthropicAccount()
	}

	// Refresh token before expiration using configured threshold
	refreshThreshold := time.Now().Add(tokenRefreshThreshold).Unix()
	if creds.ExpiresAt > refreshThreshold {
		return creds.AccessToken, nil
	}

	refreshed, err := refreshAnthropicTokenForAlias(ctx, actualAlias, creds)
	if err != nil {
		return "", err
	}

	return refreshed.AccessToken, nil
}

// AnthropicAccessTokenForAlias is an alias for AnthropicAccessToken for backward compatibility.
// Deprecated: Use AnthropicAccessToken directly.
func AnthropicAccessTokenForAlias(ctx context.Context, alias string) (string, error) {
	return AnthropicAccessToken(ctx, alias)
}

// AnthropicHeader retrieves an access token for the specified account alias and returns
// the HTTP request options for Anthropic API calls.
// If alias is empty, uses the default account.
func AnthropicHeader(ctx context.Context, alias string) ([]option.RequestOption, error) {
	accessToken, err := AnthropicAccessToken(ctx, alias)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get access token for Anthropic header")
	}
	return []option.RequestOption{
		option.WithHeader("User-Agent", "claude-cli/1.0.30 (external, cli)"),
		option.WithAuthToken(accessToken),
		option.WithHeaderAdd("anthropic-beta", "oauth-2025-04-20"),
		option.WithHeaderDel("X-Api-Key"),
	}, nil
}

// AnthropicHeaderWithToken returns the HTTP request options for Anthropic API calls with a pre-fetched access token.
// This is useful when you already have the token and want to avoid another lookup.
func AnthropicHeaderWithToken(accessToken string) []option.RequestOption {
	return []option.RequestOption{
		option.WithHeader("User-Agent", "claude-cli/1.0.30 (external, cli)"),
		option.WithAuthToken(accessToken),
		option.WithHeaderAdd("anthropic-beta", "oauth-2025-04-20"),
		option.WithHeaderDel("X-Api-Key"),
	}
}

// AnthropicSystemPrompt returns the system prompt text blocks for Anthropic Claude interactions.
func AnthropicSystemPrompt() []anthropic.TextBlockParam {
	return []anthropic.TextBlockParam{
		{
			Text: "You are Claude Code, Anthropic's official CLI for Claude.",
		},
		{
			Text: "You are not Claude Code, you are kodelet.",
		},
	}
}
