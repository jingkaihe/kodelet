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

type AnthropicTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	}
}

type AnthropicCredentials struct {
	Email        string `json:"email"`
	Scope        string `json:"scope"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

const (
	anthropicClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthEndpoint  = "https://claude.ai/oauth/authorize"
	anthropicRedirectURI   = "https://console.anthropic.com/oauth/code/callback"
	anthropicTokenEndpoint = "https://console.anthropic.com/v1/oauth/token"
)

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

func GetAnthropicCredentialsExists() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, errors.Wrap(err, "failed to get user home directory")
	}
	filePath := filepath.Join(home, ".kodelet", "anthropic-subscription.json")
	_, err = os.Stat(filePath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, errors.Wrap(err, "failed to check if anthropic credentials file exists")
}

func SaveAnthropicCredentials(creds *AnthropicCredentials) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	filePath := filepath.Join(home, ".kodelet", "anthropic-subscription.json")

	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", errors.Wrap(err, "failed to create credentials directory")
	}

	f, err := os.Create(filePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to create credentials file")
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(creds); err != nil {
		return "", errors.Wrap(err, "failed to write credentials")
	}

	return filePath, nil
}

func refreshAnthropicToken(ctx context.Context, creds *AnthropicCredentials) (*AnthropicCredentials, error) {
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

	if _, err := SaveAnthropicCredentials(refreshed); err != nil {
		return nil, errors.Wrap(err, "failed to save anthropic credentials")
	}

	return refreshed, nil
}

func AnthropicAccessToken(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	filePath := filepath.Join(home, ".kodelet", "anthropic-subscription.json")

	f, err := os.Open(filePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to open anthropic subscription file")
	}
	defer f.Close()

	var creds AnthropicCredentials
	if err := json.NewDecoder(f).Decode(&creds); err != nil {
		return "", errors.Wrap(err, "failed to decode anthropic subscription file")
	}

	// Refresh token 10 minutes before expiration
	refreshThreshold := time.Now().Add(10 * time.Minute).Unix()
	if creds.ExpiresAt > refreshThreshold {
		return creds.AccessToken, nil
	}

	refreshed, err := refreshAnthropicToken(ctx, &creds)
	if err != nil {
		return "", err
	}

	return refreshed.AccessToken, nil
}

func AnthropicHeader(accessToken string) []option.RequestOption {
	return []option.RequestOption{
		option.WithHeader("User-Agent", "claude-cli/1.0.30 (external, cli)"),
		option.WithAuthToken(accessToken),
		option.WithHeader("anthropic-beta", "oauth-2025-04-20"),
		option.WithHeaderDel("X-Api-Key"),
	}
}

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
