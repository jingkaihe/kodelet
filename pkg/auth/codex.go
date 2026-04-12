// Package auth provides authentication mechanisms for various AI providers.
// This file implements Codex CLI authentication for the ChatGPT backend API.
package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/pkg/errors"
)

// CodexTokens represents the OAuth tokens stored by the Codex CLI.
type CodexTokens struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

// CodexAuthFile represents the authentication file created by the Codex CLI.
// This file is located at ~/.kodelet/codex-credentials.json and is created by running `kodelet codex login`.
type CodexAuthFile struct {
	Tokens       CodexTokens `json:"tokens"`
	OpenAIAPIKey string      `json:"OPENAI_API_KEY,omitempty"`
}

// CodexCredentials contains the resolved credentials for making Codex API calls.
type CodexCredentials struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	AccountID    string
	ExpiresAt    int64
}

const (
	// CodexAPIBaseURL is the endpoint for the Codex Responses API.
	CodexAPIBaseURL = "https://chatgpt.com/backend-api/codex"

	// CodexOriginator identifies the client making requests.
	// Using the official Codex CLI originator for compatibility.
	CodexOriginator = "kodelet"

	// OAuth configuration for OpenAI Codex
	codexAuthIssuer   = "https://auth.openai.com"
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexTokenURL     = "https://auth.openai.com/oauth/token"
	codexRedirectURI  = "http://localhost:1455/auth/callback"
	codexScope        = "openid profile email offline_access"
	codexJWTClaimPath = "https://api.openai.com/auth"

	// codexTokenRefreshThreshold is the duration before token expiry when we should refresh
	codexTokenRefreshThreshold = 10 * time.Minute

	// codexDeviceCodeTimeout matches the official Codex CLI device auth window.
	codexDeviceCodeTimeout = 15 * time.Minute
)

// CodexDeviceCode contains the device authorization details shown to the user.
type CodexDeviceCode struct {
	VerificationURL string
	UserCode        string
	deviceAuthID    string
	interval        time.Duration
}

type codexDeviceCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	IntervalRaw  json.RawMessage `json:"interval"`
}

type codexDeviceAuthorizationResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	CodeChallenge     string `json:"code_challenge"`
}

// codexAuthFilePath returns the path to the Codex auth file.
func codexAuthFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	return filepath.Join(home, ".kodelet", "codex-credentials.json"), nil
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
			return nil, errors.New("codex auth file not found, please login first with 'kodelet codex login'")
		}
		return nil, errors.Wrap(err, "failed to open codex auth file")
	}
	defer f.Close()

	var authFile CodexAuthFile
	if err := json.NewDecoder(f).Decode(&authFile); err != nil {
		return nil, errors.Wrap(err, "failed to decode codex auth file")
	}

	// Codex requests require ChatGPT OAuth credentials.
	if authFile.Tokens.AccessToken != "" && authFile.Tokens.AccountID != "" {
		return &CodexCredentials{
			IDToken:      authFile.Tokens.IDToken,
			AccessToken:  authFile.Tokens.AccessToken,
			RefreshToken: authFile.Tokens.RefreshToken,
			AccountID:    authFile.Tokens.AccountID,
			ExpiresAt:    authFile.Tokens.ExpiresAt,
		}, nil
	}

	return nil, errors.New("codex auth file contains no valid OAuth credentials, please login first with 'kodelet codex login'")
}

// CodexHeader returns the HTTP request options for Codex API calls.
// These headers are required for authentication with the ChatGPT backend API.
func CodexHeader(ctx context.Context) ([]option.RequestOption, error) {
	creds, err := GetCodexCredentialsForRequest(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get codex credentials")
	}

	return CodexHeaderWithCredentials(creds), nil
}

// GetCodexCredentialsForRequest returns credentials suitable for making Codex API calls.
// OAuth access tokens are refreshed when they are within the configured refresh threshold.
func GetCodexCredentialsForRequest(ctx context.Context) (*CodexCredentials, error) {
	creds, err := GetCodexCredentials()
	if err != nil {
		return nil, err
	}

	refreshThreshold := time.Now().Add(codexTokenRefreshThreshold).Unix()
	if creds.ExpiresAt <= 0 || creds.ExpiresAt > refreshThreshold {
		return creds, nil
	}

	if creds.RefreshToken == "" {
		return nil, errors.New("token expired and no refresh token available, please login again")
	}

	refreshed, err := RefreshCodexToken(ctx, creds.RefreshToken)
	if err != nil {
		return nil, errors.Wrap(err, "failed to refresh token")
	}

	if refreshed.IDToken == "" {
		refreshed.IDToken = creds.IDToken
	}

	if _, err := SaveCodexCredentials(refreshed); err != nil {
		return nil, errors.Wrap(err, "failed to save refreshed credentials")
	}

	return refreshed, nil
}

// CodexHeaderWithCredentials returns the HTTP request options for Codex API calls
// using the provided OAuth credentials. Returns nil if credentials are nil or incomplete.
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
		}
	}

	return nil
}

// IsCodexOAuthEnabled returns true if OAuth credentials are available.
func IsCodexOAuthEnabled(creds *CodexCredentials) bool {
	return creds != nil && creds.AccessToken != "" && creds.AccountID != ""
}

// CodexOAuthServer handles the local OAuth callback server.
type CodexOAuthServer struct {
	server    *http.Server
	code      string
	state     string
	mu        sync.Mutex
	done      chan struct{}
	cancelled bool
}

// GenerateCodexAuthURL generates an OAuth2 authorization URL for OpenAI Codex authentication.
// It returns the auth URL, PKCE verifier, state, and any error encountered.
func GenerateCodexAuthURL() (authURL string, verifier string, state string, err error) {
	pkceParams := generatePKCEParams()
	state = randomString(16)

	u, err := url.Parse(codexAuthorizeURL)
	if err != nil {
		return "", "", "", errors.Wrap(err, "failed to parse auth endpoint")
	}

	query := url.Values{
		"response_type":              {"code"},
		"client_id":                  {codexClientID},
		"redirect_uri":               {codexRedirectURI},
		"scope":                      {codexScope},
		"code_challenge":             {pkceParams.Challenge},
		"code_challenge_method":      {pkceParams.ChallengeMethod},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"kodelet"},
	}

	u.RawQuery = query.Encode()
	return u.String(), pkceParams.Verifier, state, nil
}

func buildCodexIssuerURL(issuer string, path string) string {
	return strings.TrimRight(issuer, "/") + path
}

func parseCodexDeviceCodeInterval(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 {
		return 0, nil
	}

	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		seconds, err := time.ParseDuration(strings.TrimSpace(stringValue) + "s")
		if err != nil {
			return 0, err
		}
		return seconds, nil
	}

	var intValue int64
	if err := json.Unmarshal(raw, &intValue); err == nil {
		return time.Duration(intValue) * time.Second, nil
	}

	return 0, errors.Errorf("invalid device code interval: %s", string(raw))
}

func requestCodexDeviceCode(ctx context.Context, issuer string) (*CodexDeviceCode, error) {
	requestBody, err := json.Marshal(map[string]string{"client_id": codexClientID})
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode device code request")
	}

	endpoint := buildCodexIssuerURL(issuer, "/api/accounts/deviceauth/usercode")
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create device code request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to request device code")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read device code response")
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("device code login is not enabled for Codex. Use the browser login instead")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("device code request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var deviceResp codexDeviceCodeResponse
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return nil, errors.Wrap(err, "failed to parse device code response")
	}

	userCode := deviceResp.UserCode
	if userCode == "" {
		userCode = deviceResp.UserCodeAlt
	}
	if userCode == "" || deviceResp.DeviceAuthID == "" {
		return nil, errors.New("device code response missing required fields")
	}

	interval, err := parseCodexDeviceCodeInterval(deviceResp.IntervalRaw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse device code polling interval")
	}

	return &CodexDeviceCode{
		VerificationURL: buildCodexIssuerURL(issuer, "/codex/device"),
		UserCode:        userCode,
		deviceAuthID:    deviceResp.DeviceAuthID,
		interval:        interval,
	}, nil
}

// RequestCodexDeviceCode starts the device authorization flow for Codex.
func RequestCodexDeviceCode(ctx context.Context) (*CodexDeviceCode, error) {
	return requestCodexDeviceCode(ctx, codexAuthIssuer)
}

func pollCodexDeviceAuthorization(ctx context.Context, issuer string, deviceCode *CodexDeviceCode) (*codexDeviceAuthorizationResponse, error) {
	if deviceCode == nil {
		return nil, errors.New("device code is required")
	}

	deadline := time.Now().Add(codexDeviceCodeTimeout)
	endpoint := buildCodexIssuerURL(issuer, "/api/accounts/deviceauth/token")

	for {
		requestBody, err := json.Marshal(map[string]string{
			"device_auth_id": deviceCode.deviceAuthID,
			"user_code":      deviceCode.UserCode,
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to encode device authorization poll request")
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(requestBody))
		if err != nil {
			return nil, errors.Wrap(err, "failed to create device authorization poll request")
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, errors.Wrap(err, "failed to poll device authorization")
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, errors.Wrap(readErr, "failed to read device authorization response")
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var authResp codexDeviceAuthorizationResponse
			if err := json.Unmarshal(body, &authResp); err != nil {
				return nil, errors.Wrap(err, "failed to parse device authorization response")
			}
			if authResp.AuthorizationCode == "" || authResp.CodeVerifier == "" {
				return nil, errors.New("device authorization response missing required fields")
			}
			return &authResp, nil
		case http.StatusForbidden, http.StatusNotFound:
			if time.Now().After(deadline) {
				return nil, errors.New("device authorization timed out after 15 minutes")
			}
			wait := deviceCode.interval
			if remaining := time.Until(deadline); wait > remaining {
				wait = remaining
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		default:
			trimmed := strings.TrimSpace(string(body))
			if trimmed == "" {
				return nil, errors.Errorf("device authorization failed with status %d", resp.StatusCode)
			}
			return nil, errors.Errorf("device authorization failed with status %d: %s", resp.StatusCode, trimmed)
		}
	}
}

// StartCodexOAuthServer starts a local HTTP server to receive the OAuth callback.
// It returns a server that can be used to wait for the authorization code.
func StartCodexOAuthServer(expectedState string) (*CodexOAuthServer, error) {
	srv := &CodexOAuthServer{
		state: expectedState,
		done:  make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", srv.handleCallback)

	srv.server = &http.Server{
		Addr:              "127.0.0.1:1455",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		if err := srv.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Give the server a moment to start
	select {
	case err := <-errChan:
		return nil, errors.Wrap(err, "failed to start OAuth callback server")
	case <-time.After(100 * time.Millisecond):
		return srv, nil
	}
}

func (s *CodexOAuthServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := r.URL.Query().Get("state")
	if state != s.state {
		http.Error(w, "State mismatch", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	s.code = code

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Authentication successful</title>
</head>
<body>
  <p>Authentication successful. Return to your terminal to continue.</p>
</body>
</html>`))

	close(s.done)
}

// WaitForCode waits for the authorization code from the OAuth callback.
// It returns the code or an error if the wait times out or is cancelled.
func (s *CodexOAuthServer) WaitForCode(timeout time.Duration) (string, error) {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cancelled {
			return "", errors.New("OAuth flow was cancelled")
		}
		return s.code, nil
	case <-time.After(timeout):
		return "", errors.New("timeout waiting for authorization code")
	}
}

// Cancel cancels the OAuth flow.
func (s *CodexOAuthServer) Cancel() {
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// Close shuts down the OAuth callback server.
func (s *CodexOAuthServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// codexTokenResponse represents the OAuth token response from OpenAI.
type codexTokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func codexExpiresAt(expiresIn int64) int64 {
	if expiresIn <= 0 {
		return 0
	}

	return time.Now().Add(time.Duration(expiresIn) * time.Second).Unix()
}

func exchangeCodexCode(ctx context.Context, tokenURL string, code string, verifier string, redirectURI string) (*CodexCredentials, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
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

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, errors.Wrap(err, "failed to parse token response")
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, errors.New("token response missing required fields")
	}

	accountID := extractCodexAccountID(tokenResp.AccessToken)
	if accountID == "" {
		return nil, errors.New("failed to extract account ID from access token")
	}

	return &CodexCredentials{
		IDToken:      tokenResp.IDToken,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		AccountID:    accountID,
		ExpiresAt:    codexExpiresAt(tokenResp.ExpiresIn),
	}, nil
}

// ExchangeCodexCode exchanges an authorization code for Codex access credentials.
func ExchangeCodexCode(ctx context.Context, code string, verifier string) (*CodexCredentials, error) {
	return exchangeCodexCode(ctx, codexTokenURL, code, verifier, codexRedirectURI)
}

func completeCodexDeviceCodeLogin(ctx context.Context, issuer string, deviceCode *CodexDeviceCode) (*CodexCredentials, error) {
	authResp, err := pollCodexDeviceAuthorization(ctx, issuer, deviceCode)
	if err != nil {
		return nil, err
	}

	redirectURI := buildCodexIssuerURL(issuer, "/deviceauth/callback")
	return exchangeCodexCode(ctx, buildCodexIssuerURL(issuer, "/oauth/token"), authResp.AuthorizationCode, authResp.CodeVerifier, redirectURI)
}

// CompleteCodexDeviceCodeLogin waits for the device authorization flow to complete
// and exchanges the resulting authorization code for Codex credentials.
func CompleteCodexDeviceCodeLogin(ctx context.Context, deviceCode *CodexDeviceCode) (*CodexCredentials, error) {
	return completeCodexDeviceCodeLogin(ctx, codexAuthIssuer, deviceCode)
}

// extractCodexAccountID extracts the ChatGPT account ID from the JWT access token.
func extractCodexAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}

	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return ""
		}
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	authClaims, ok := claims[codexJWTClaimPath].(map[string]any)
	if !ok {
		return ""
	}

	accountID, ok := authClaims["chatgpt_account_id"].(string)
	if !ok {
		return ""
	}

	return accountID
}

// RefreshCodexToken refreshes the Codex access token using the refresh token.
func RefreshCodexToken(ctx context.Context, refreshToken string) (*CodexCredentials, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", codexTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create refresh token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send refresh token request")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read refresh token response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, errors.Wrap(err, "failed to parse refresh token response")
	}

	if tokenResp.AccessToken == "" {
		return nil, errors.New("refresh token response missing access token")
	}

	if tokenResp.RefreshToken == "" {
		tokenResp.RefreshToken = refreshToken
	}

	accountID := extractCodexAccountID(tokenResp.AccessToken)
	if accountID == "" {
		return nil, errors.New("failed to extract account ID from refreshed access token")
	}

	return &CodexCredentials{
		IDToken:      tokenResp.IDToken,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		AccountID:    accountID,
		ExpiresAt:    codexExpiresAt(tokenResp.ExpiresIn),
	}, nil
}

// SaveCodexCredentials saves Codex credentials to the auth file.
// Returns the file path where credentials were saved.
func SaveCodexCredentials(creds *CodexCredentials) (string, error) {
	authPath, err := codexAuthFilePath()
	if err != nil {
		return "", err
	}

	// Ensure the directory exists
	dir := filepath.Dir(authPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", errors.Wrap(err, "failed to create codex directory")
	}

	authFile := CodexAuthFile{
		Tokens: CodexTokens{
			IDToken:      creds.IDToken,
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			AccountID:    creds.AccountID,
			ExpiresAt:    creds.ExpiresAt,
		},
	}

	// Write to a temporary file first for atomic operation
	tempFile, err := os.CreateTemp(dir, "auth-*.tmp")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temporary auth file")
	}
	tempPath := tempFile.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(authFile); err != nil {
		tempFile.Close()
		return "", errors.Wrap(err, "failed to write auth file")
	}

	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return "", errors.Wrap(err, "failed to sync auth file")
	}

	if err := tempFile.Close(); err != nil {
		return "", errors.Wrap(err, "failed to close temporary auth file")
	}

	// Set restrictive permissions before renaming
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return "", errors.Wrap(err, "failed to set auth file permissions")
	}

	// Atomic rename
	if err := os.Rename(tempPath, authPath); err != nil {
		return "", errors.Wrap(err, "failed to save auth file")
	}

	success = true
	return authPath, nil
}

// DeleteCodexCredentials removes the Codex auth file.
func DeleteCodexCredentials() error {
	authPath, err := codexAuthFilePath()
	if err != nil {
		return err
	}

	if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove codex auth file")
	}

	return nil
}

// GetCodexAccessToken retrieves a valid Codex access token, refreshing if necessary.
func GetCodexAccessToken(ctx context.Context) (string, error) {
	creds, err := GetCodexCredentialsForRequest(ctx)
	if err != nil {
		return "", err
	}

	// If no OAuth tokens, return error
	if creds.AccessToken == "" || creds.AccountID == "" {
		return "", errors.New("no valid OAuth credentials, please login first with 'kodelet codex login'")
	}

	return creds.AccessToken, nil
}
