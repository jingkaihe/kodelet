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
	"time"

	"github.com/pkg/errors"
)

type CopilotDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type CopilotTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type CopilotExchangeResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

type CopilotCredentials struct {
	AccessToken     string `json:"access_token"`
	CopilotToken    string `json:"copilot_token"`
	Scope           string `json:"scope"`
	CopilotExpires  int64  `json:"copilot_expires_at"`
}

const (
	copilotClientID      = "Iv1.b507a08c87ecfe98"
	copilotDeviceURL     = "https://github.com/login/device/code"
	copilotTokenURL      = "https://github.com/login/oauth/access_token"
	copilotExchangeURL   = "https://api.github.com/copilot_internal/v2/token"
)

var (
	copilotScopes = []string{"read:user", "user:email", "copilot"}
)

func GenerateCopilotDeviceFlow(ctx context.Context) (*CopilotDeviceCodeResponse, error) {
	data := url.Values{}
	data.Set("client_id", copilotClientID)
	data.Set("scope", strings.Join(copilotScopes, " "))

	req, err := http.NewRequestWithContext(ctx, "POST", copilotDeviceURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create device flow request")
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "kodelet")
	req.Header.Set("Accept", "application/json")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send device flow request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("device flow failed with status %d: %s", resp.StatusCode, string(body))
	}

	var deviceResp CopilotDeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, errors.Wrap(err, "failed to decode device flow response")
	}

	return &deviceResp, nil
}

func PollCopilotToken(ctx context.Context, deviceCode string, interval int) (*CopilotTokenResponse, error) {
	data := url.Values{
		"client_id":   {copilotClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "POST", copilotTokenURL, strings.NewReader(data.Encode()))
			if err != nil {
				return nil, errors.Wrap(err, "failed to create token request")
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")

			client := http.DefaultClient
			resp, err := client.Do(req)
			if err != nil {
				continue // Retry on network errors
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue // Retry on read errors
			}

			var tokenResp CopilotTokenResponse
			if err := json.Unmarshal(body, &tokenResp); err != nil {
				continue // Retry on decode errors
			}

			switch tokenResp.Error {
			case "":
				// Success
				return &tokenResp, nil
			case "authorization_pending":
				// Continue polling
				continue
			case "slow_down":
				// Increase polling interval
				ticker.Reset(time.Duration(interval+5) * time.Second)
				continue
			case "expired_token", "access_denied":
				// Terminal errors
				return nil, errors.Errorf("authentication failed: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
			default:
				// Unknown error, continue polling
				continue
			}
		}
	}
}

func ExchangeCopilotToken(ctx context.Context, accessToken string) (*CopilotExchangeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", copilotExchangeURL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create copilot token request")
	}

	// Set required headers for Copilot API
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Editor-Version", "kodelet/1.0.0")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("Accept", "application/json")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to exchange copilot token")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("copilot token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var copilotToken CopilotExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&copilotToken); err != nil {
		return nil, errors.Wrap(err, "failed to decode copilot token response")
	}

	return &copilotToken, nil
}

func GetCopilotCredentialsExists() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, errors.Wrap(err, "failed to get user home directory")
	}
	filePath := filepath.Join(home, ".kodelet", "copilot-subscription.json")
	_, err = os.Stat(filePath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, errors.Wrap(err, "failed to check if copilot credentials file exists")
}

func SaveCopilotCredentials(creds *CopilotCredentials) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	filePath := filepath.Join(home, ".kodelet", "copilot-subscription.json")

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

func DeleteCopilotCredentials() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "failed to get user home directory")
	}

	filePath := filepath.Join(home, ".kodelet", "copilot-subscription.json")
	
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to delete copilot credentials file")
	}

	return nil
}



func refreshCopilotExchangeToken(ctx context.Context, creds *CopilotCredentials) (*CopilotCredentials, error) {
	// Re-exchange the OAuth access token for a new Copilot token
	copilotResp, err := ExchangeCopilotToken(ctx, creds.AccessToken)
	if err != nil {
		return nil, errors.Wrap(err, "failed to refresh copilot exchange token")
	}

	refreshed := &CopilotCredentials{
		AccessToken:     creds.AccessToken,
		CopilotToken:    copilotResp.Token,
		Scope:           creds.Scope,
		CopilotExpires:  copilotResp.ExpiresAt,
	}

	if _, err := SaveCopilotCredentials(refreshed); err != nil {
		return nil, errors.Wrap(err, "failed to save refreshed copilot credentials")
	}

	return refreshed, nil
}

func CopilotAccessToken(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}
	filePath := filepath.Join(home, ".kodelet", "copilot-subscription.json")

	f, err := os.Open(filePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to open copilot subscription file")
	}
	defer f.Close()

	var creds CopilotCredentials
	if err := json.NewDecoder(f).Decode(&creds); err != nil {
		return "", errors.Wrap(err, "failed to decode copilot subscription file")
	}

	// Check if Copilot token needs refresh (10 minutes before expiration)
	refreshThreshold := time.Now().Add(10 * time.Minute).Unix()
	if creds.CopilotExpires > refreshThreshold {
		return creds.CopilotToken, nil
	}

	// Try to refresh the Copilot token
	refreshed, err := refreshCopilotExchangeToken(ctx, &creds)
	if err != nil {
		return "", err
	}

	return refreshed.CopilotToken, nil
}