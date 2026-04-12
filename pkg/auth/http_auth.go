package auth

import (
	"net/http"
	"os"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/pkg/errors"
)

const (
	CopilotBaseURL         = "https://api.githubcopilot.com"
	copilotChatUserAgent   = "GitHubCopilotChat/0.26.7"
	copilotOpenAIUserAgent = "GithubCopilot/1.342.0"
	copilotEditorVersion   = "vscode/1.102.0"
	copilotPluginVersion   = "copilot-chat/0.26.7"
	copilotIntegrationID   = "vscode-chat"
	copilotGitHubAPIVer    = "2025-04-01"
	copilotFetchLibrary    = "electron-fetch"
	CopilotInitiatorUser   = "user"
	CopilotInitiatorAgent  = "agent"
)

// HTTPAuthorizer applies request-time authentication to outgoing HTTP requests.
// Implementations may refresh tokens before updating the request headers.
type HTTPAuthorizer interface {
	Authorize(*http.Request) error
}

// AuthorizerFunc adapts a function into an HTTPAuthorizer.
type AuthorizerFunc func(*http.Request) error

// Authorize applies the authorizer function.
func (f AuthorizerFunc) Authorize(req *http.Request) error {
	return f(req)
}

// RefreshingAuthRoundTripper injects authentication immediately before the request is sent.
type RefreshingAuthRoundTripper struct {
	Base       http.RoundTripper
	Authorizer HTTPAuthorizer
}

// RoundTrip applies auth to a cloned request and sends it using the underlying transport.
func (t *RefreshingAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil {
		return nil, errors.New("refreshing auth round tripper is nil")
	}

	transport := t.Base
	if transport == nil {
		transport = http.DefaultTransport
	}

	clonedReq := req.Clone(req.Context())
	clonedReq.Header = req.Header.Clone()

	if t.Authorizer != nil {
		if err := t.Authorizer.Authorize(clonedReq); err != nil {
			return nil, err
		}
	}

	return transport.RoundTrip(clonedReq)
}

// HTTPClientWithAuthorizer returns an HTTP client that authorizes each outgoing request.
func HTTPClientWithAuthorizer(authorizer HTTPAuthorizer) *http.Client {
	return &http.Client{Transport: &RefreshingAuthRoundTripper{Authorizer: authorizer}}
}

// AnthropicSubscriptionAuthorizer returns a request authorizer for Anthropic subscription auth.
func AnthropicSubscriptionAuthorizer(alias string) HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		accessToken, err := AnthropicAccessToken(req.Context(), alias)
		if err != nil {
			return errors.Wrap(err, "failed to get anthropic access token")
		}

		req.Header.Set("User-Agent", "claude-cli/2.1.2 (external, cli)")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Add("anthropic-beta", "oauth-2025-04-20")
		req.Header.Del("X-Api-Key")
		return nil
	})
}

// AnthropicStaticAPIKeyAuthorizer returns a request authorizer for Anthropic API key auth.
func AnthropicStaticAPIKeyAuthorizer(apiKey string) HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		if apiKey == "" {
			return nil
		}

		req.Header.Set("X-Api-Key", apiKey)
		req.Header.Del("Authorization")
		return nil
	})
}

// CopilotAuthorizer returns a request authorizer for GitHub Copilot-backed OpenAI calls.
func CopilotAuthorizer() HTTPAuthorizer {
	return CopilotAuthorizerWithInitiator("")
}

// CopilotAuthorizerWithInitiator returns a request authorizer for GitHub Copilot-backed calls.
// When initiator is empty, requests default to a user-initiated origin.
func CopilotAuthorizerWithInitiator(initiator string) HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		token, err := CopilotAccessToken(req.Context())
		if err != nil {
			return errors.Wrap(err, "failed to get copilot access token")
		}

		resolvedInitiator := initiator
		if resolvedInitiator == "" {
			resolvedInitiator = req.Header.Get("X-Initiator")
		}
		if resolvedInitiator == "" {
			resolvedInitiator = CopilotInitiatorUser
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", copilotOpenAIUserAgent)
		req.Header.Set("Editor-Version", copilotEditorVersion)
		req.Header.Set("X-Initiator", resolvedInitiator)
		req.Header.Del("x-api-key")
		req.Header.Del("X-Api-Key")
		return nil
	})
}

// CopilotAnthropicHeaders returns the static headers expected by Copilot's Anthropic-compatible API.
func CopilotAnthropicHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                          copilotChatUserAgent,
		"Editor-Version":                      copilotEditorVersion,
		"Editor-Plugin-Version":               copilotPluginVersion,
		"Copilot-Integration-Id":              copilotIntegrationID,
		"OpenAI-Intent":                       "conversation-panel",
		"X-GitHub-Api-Version":                copilotGitHubAPIVer,
		"X-Vscode-User-Agent-Library-Version": copilotFetchLibrary,
	}
}

// CopilotAnthropicAuthorizer returns a request authorizer for GitHub Copilot's Anthropic-compatible API.
func CopilotAnthropicAuthorizer() HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		token, err := CopilotAccessToken(req.Context())
		if err != nil {
			return errors.Wrap(err, "failed to get copilot access token")
		}

		resolvedInitiator := req.Header.Get("X-Initiator")
		if resolvedInitiator == "" {
			resolvedInitiator = CopilotInitiatorUser
		}

		req.Header.Set("Authorization", "Bearer "+token)
		for key, value := range CopilotAnthropicHeaders() {
			req.Header.Set(key, value)
		}
		req.Header.Set("X-Initiator", resolvedInitiator)
		req.Header.Del("x-api-key")
		req.Header.Del("X-Api-Key")
		return nil
	})
}

// OpenAIStaticAPIKeyAuthorizer returns a request authorizer for static OpenAI API key auth.
func OpenAIStaticAPIKeyAuthorizer(apiKey string) HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		if apiKey == "" {
			return nil
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)
		return nil
	})
}

// CodexAuthorizer returns a request authorizer for Codex OAuth auth.
func CodexAuthorizer() HTTPAuthorizer {
	return AuthorizerFunc(func(req *http.Request) error {
		creds, err := GetCodexCredentialsForRequest(req.Context())
		if err != nil {
			return errors.Wrap(err, "failed to get codex credentials")
		}

		if creds.AccessToken != "" && creds.AccountID != "" {
			req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
			req.Header.Set("ChatGPT-Account-ID", creds.AccountID)
			req.Header.Set("OpenAI-Beta", "responses=experimental")
			req.Header.Set("originator", CodexOriginator)
			return nil
		}

		return errors.New("no valid codex OAuth credentials available")
	})
}

// AnthropicRequestOptionsWithAuthorizer builds Anthropic SDK options using request-time auth.
func AnthropicRequestOptionsWithAuthorizer(authorizer HTTPAuthorizer) []anthropicoption.RequestOption {
	if authorizer == nil {
		return nil
	}

	return []anthropicoption.RequestOption{
		anthropicoption.WithHTTPClient(HTTPClientWithAuthorizer(authorizer)),
	}
}

// OpenAIRequestOptionsWithAuthorizer builds openai-go SDK options using request-time auth.
func OpenAIRequestOptionsWithAuthorizer(authorizer HTTPAuthorizer) []openaioption.RequestOption {
	if authorizer == nil {
		return nil
	}

	return []openaioption.RequestOption{
		openaioption.WithHTTPClient(HTTPClientWithAuthorizer(authorizer)),
	}
}

// CopilotInitiator resolves the Copilot initiator from message options.
func CopilotInitiator(opt llmtypes.MessageOpt) string {
	return opt.ResolvedInitiator()
}

// CopilotHeaderMap returns Copilot request headers derived from message options.
func CopilotHeaderMap(opt llmtypes.MessageOpt) map[string]string {
	return map[string]string{"X-Initiator": CopilotInitiator(opt)}
}

// CopilotOpenAIRequestOptions returns OpenAI SDK request options for Copilot initiator headers.
func CopilotOpenAIRequestOptions(opt llmtypes.MessageOpt) []openaioption.RequestOption {
	return []openaioption.RequestOption{openaioption.WithHeader("X-Initiator", CopilotInitiator(opt))}
}

// CopilotAnthropicRequestOptions returns Anthropic SDK request options for Copilot initiator headers.
func CopilotAnthropicRequestOptions(opt llmtypes.MessageOpt) []anthropicoption.RequestOption {
	return []anthropicoption.RequestOption{anthropicoption.WithHeader("X-Initiator", CopilotInitiator(opt))}
}

// OpenAIAPIKeyAuthorizerFromEnv returns a static API key authorizer for the given env var.
func OpenAIAPIKeyAuthorizerFromEnv(envVar string) (HTTPAuthorizer, error) {
	apiKey := os.Getenv(envVar)
	if apiKey == "" {
		return nil, errors.Errorf("%s environment variable is required", envVar)
	}
	return OpenAIStaticAPIKeyAuthorizer(apiKey), nil
}

// AnthropicAPIKeyAuthorizerFromEnv returns a static API key authorizer for ANTHROPIC_API_KEY.
func AnthropicAPIKeyAuthorizerFromEnv() HTTPAuthorizer {
	return AnthropicStaticAPIKeyAuthorizer(os.Getenv("ANTHROPIC_API_KEY"))
}
