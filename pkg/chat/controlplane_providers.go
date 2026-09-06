package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/pkg/errors"
)

// AnthropicAccountSummary never contains provider tokens or credentials.
type AnthropicAccountSummary struct {
	Alias     string `json:"alias"`
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expiresAt"`
	IsDefault bool   `json:"isDefault"`
}

type AnthropicAccounts struct {
	Accounts []AnthropicAccountSummary `json:"accounts"`
}

type AnthropicAccountMutation struct {
	Action   string `json:"action"`
	Alias    string `json:"alias,omitempty"`
	NewAlias string `json:"newAlias,omitempty"`
}

func (m AnthropicAccountMutation) Validate() error {
	switch m.Action {
	case "logout":
		if m.Alias != "" || m.NewAlias != "" {
			return errors.New("logout does not accept aliases")
		}
	case "default", "remove", "rename":
		if err := auth.ValidateAlias(m.Alias); err != nil {
			return err
		}
		if m.Action == "rename" {
			return auth.ValidateAlias(m.NewAlias)
		}
		if m.NewAlias != "" {
			return errors.New("newAlias is only accepted for rename")
		}
	default:
		return errors.New("unsupported account action")
	}
	return nil
}

type AnthropicLogin struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	Message          string `json:"message,omitempty"`
}

type AnthropicUsageWindow struct {
	Status      string  `json:"status"`
	Utilization float64 `json:"utilization"`
	ResetTime   string  `json:"reset_time"`
	ResetUnix   int64   `json:"reset_unix"`
}

type AnthropicAccountUsage struct {
	Account  string               `json:"account"`
	Email    string               `json:"email"`
	Window5h AnthropicUsageWindow `json:"window_5h"`
	Window7d AnthropicUsageWindow `json:"window_7d"`
}

func (r *ControlPlaneChatRunner) AnthropicAccounts(ctx context.Context) (AnthropicAccounts, error) {
	var result AnthropicAccounts
	err := r.providerRequest(ctx, "anthropic", http.MethodGet, []string{"accounts"}, nil, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) MutateAnthropicAccount(ctx context.Context, mutation AnthropicAccountMutation) error {
	if err := mutation.Validate(); err != nil {
		return err
	}
	return r.providerRequest(ctx, "anthropic", http.MethodPost, []string{"accounts"}, mutation, nil)
}

func (r *ControlPlaneChatRunner) AnthropicAccountUsage(ctx context.Context, alias string) (AnthropicAccountUsage, error) {
	var result AnthropicAccountUsage
	if alias != "" {
		if err := auth.ValidateAlias(alias); err != nil {
			return result, err
		}
	}
	err := r.providerRequest(ctx, "anthropic", http.MethodPost, []string{"accounts", "usage"}, struct {
		Alias string `json:"alias,omitempty"`
	}{alias}, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) StartAnthropicLogin(ctx context.Context) (AnthropicLogin, error) {
	var result AnthropicLogin
	err := r.providerRequest(ctx, "anthropic", http.MethodPost, []string{"oauth-login"}, nil, &result)
	return result, err
}

// CompleteAnthropicLogin explicitly selects an alias (empty means derive from
// the returned email) rather than overwriting the browser's default account.
func (r *ControlPlaneChatRunner) CompleteAnthropicLogin(ctx context.Context, id, code, alias string) (AnthropicLogin, error) {
	var result AnthropicLogin
	if id == "" || code == "" || len(code) > 8192 {
		return result, errors.New("provide a login ID and an authorization code of at most 8192 bytes")
	}
	if alias != "" {
		if err := auth.ValidateAlias(alias); err != nil {
			return result, err
		}
	}
	err := r.providerRequest(ctx, "anthropic", http.MethodPost, []string{"oauth-login", id, "complete"}, struct {
		Code  string  `json:"code"`
		Alias *string `json:"alias"`
	}{code, &alias}, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) CancelAnthropicLogin(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("login id is required")
	}
	return r.providerRequest(ctx, "anthropic", http.MethodDelete, []string{"oauth-login", id}, nil, nil)
}

// ProviderConnection contains only daemon connection status, never credentials.
type ProviderConnection struct {
	Provider  string `json:"provider"`
	Connected bool   `json:"connected"`
}

// ProviderDeviceLogin is the public part of a daemon-owned device login.
type ProviderDeviceLogin struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	Message         string `json:"message,omitempty"`
}

func (r *ControlPlaneChatRunner) ProviderConnection(ctx context.Context, provider string) (ProviderConnection, error) {
	var result ProviderConnection
	err := r.providerRequest(ctx, provider, http.MethodGet, nil, nil, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) StartProviderDeviceLogin(ctx context.Context, provider string) (ProviderDeviceLogin, error) {
	var result ProviderDeviceLogin
	if provider != "codex" && provider != "copilot" {
		return result, errors.New("provider does not support device login")
	}
	err := r.providerRequest(ctx, provider, http.MethodPost, []string{"device-login"}, nil, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) ProviderDeviceLogin(ctx context.Context, provider, id string) (ProviderDeviceLogin, error) {
	var result ProviderDeviceLogin
	if id == "" || (provider != "codex" && provider != "copilot") {
		return result, errors.New("device login provider and id are required")
	}
	err := r.providerRequest(ctx, provider, http.MethodGet, []string{"device-login", id}, nil, &result)
	return result, err
}

func (r *ControlPlaneChatRunner) CancelProviderDeviceLogin(ctx context.Context, provider, id string) error {
	if id == "" || (provider != "codex" && provider != "copilot") {
		return errors.New("device login provider and id are required")
	}
	return r.providerRequest(ctx, provider, http.MethodDelete, []string{"device-login", id}, nil, nil)
}

func (r *ControlPlaneChatRunner) providerRequest(ctx context.Context, provider, method string, path []string, input, result any) error {
	if provider != "anthropic" && provider != "codex" && provider != "copilot" {
		return errors.New("unsupported provider")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	endpoint, err := controlPlaneEndpointURL(r.baseURL, append([]string{"api", "providers", provider}, path...)...)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	r.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return errors.Wrap(err, "could not confirm the account request; check the account status and server logs before trying again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return controlPlaneResponseError(response)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
}
