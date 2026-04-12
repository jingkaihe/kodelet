package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTestHome(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	t.Cleanup(func() {
		require.NoError(t, os.Setenv("HOME", originalHome))
	})
	require.NoError(t, os.Setenv("HOME", tempDir))
}

func TestRefreshingAuthRoundTripper(t *testing.T) {
	t.Run("authorizes cloned request", func(t *testing.T) {
		var seenReq *http.Request

		rt := &RefreshingAuthRoundTripper{
			Base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				seenReq = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(nil),
				}, nil
			}),
			Authorizer: AuthorizerFunc(func(req *http.Request) error {
				req.Header.Set("X-Test-Auth", "fresh-token")
				return nil
			}),
		}

		req := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
		resp, err := rt.RoundTrip(req)

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, seenReq)
		assert.Equal(t, "fresh-token", seenReq.Header.Get("X-Test-Auth"))
		assert.Empty(t, req.Header.Get("X-Test-Auth"), "original request should not be mutated")
	})

	t.Run("returns error for nil receiver", func(t *testing.T) {
		var rt *RefreshingAuthRoundTripper
		req := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)

		resp, err := rt.RoundTrip(req)

		assert.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refreshing auth round tripper is nil")
	})

	t.Run("propagates authorizer error", func(t *testing.T) {
		rt := &RefreshingAuthRoundTripper{
			Authorizer: AuthorizerFunc(func(req *http.Request) error {
				return assert.AnError
			}),
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)

		resp, err := rt.RoundTrip(req)

		assert.Nil(t, resp)
		require.ErrorIs(t, err, assert.AnError)
	})
}

func TestAnthropicSubscriptionAuthorizer(t *testing.T) {
	setTestHome(t)

	_, err := SaveAnthropicCredentialsWithAlias("work", &AnthropicCredentials{
		Email:        "work@example.com",
		AccessToken:  "anthropic-access-token",
		RefreshToken: "anthropic-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	req.Header.Set("X-Api-Key", "stale-key")

	err = AnthropicSubscriptionAuthorizer("work").Authorize(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer anthropic-access-token", req.Header.Get("Authorization"))
	assert.Equal(t, "claude-cli/2.1.2 (external, cli)", req.Header.Get("User-Agent"))
	assert.Equal(t, "oauth-2025-04-20", req.Header.Get("anthropic-beta"))
	assert.Empty(t, req.Header.Get("X-Api-Key"))
}

func TestCopilotAuthorizer(t *testing.T) {
	setTestHome(t)

	_, err := SaveCopilotCredentials(&CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "copilot-access-token",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://api.githubcopilot.com/chat/completions", nil)
	req.Header.Set("x-api-key", "should-be-removed")

	err = CopilotAuthorizer().Authorize(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer copilot-access-token", req.Header.Get("Authorization"))
	assert.Equal(t, "GithubCopilot/1.342.0", req.Header.Get("User-Agent"))
	assert.Equal(t, "vscode/1.102.0", req.Header.Get("Editor-Version"))
	assert.Equal(t, CopilotInitiatorUser, req.Header.Get("X-Initiator"))
	assert.Empty(t, req.Header.Get("x-api-key"))
}

func TestCopilotAuthorizerWithInitiatorOverride(t *testing.T) {
	setTestHome(t)

	_, err := SaveCopilotCredentials(&CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "copilot-access-token",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://api.githubcopilot.com/v1/messages", nil)
	err = CopilotAuthorizerWithInitiator(CopilotInitiatorAgent).Authorize(req)
	require.NoError(t, err)
	assert.Equal(t, CopilotInitiatorAgent, req.Header.Get("X-Initiator"))

	req2 := httptest.NewRequest(http.MethodPost, "https://api.githubcopilot.com/v1/messages", nil)
	req2.Header.Set("X-Initiator", CopilotInitiatorAgent)
	err = CopilotAuthorizer().Authorize(req2)
	require.NoError(t, err)
	assert.Equal(t, CopilotInitiatorAgent, req2.Header.Get("X-Initiator"))
}

func TestCopilotAnthropicHeaders(t *testing.T) {
	headers := CopilotAnthropicHeaders()
	assert.Equal(t, "GitHubCopilotChat/0.26.7", headers["User-Agent"])
	assert.Equal(t, "vscode/1.102.0", headers["Editor-Version"])
	assert.Equal(t, "copilot-chat/0.26.7", headers["Editor-Plugin-Version"])
	assert.Equal(t, "vscode-chat", headers["Copilot-Integration-Id"])
	assert.Equal(t, "conversation-panel", headers["OpenAI-Intent"])
	assert.Equal(t, "2025-04-01", headers["X-GitHub-Api-Version"])
	assert.Equal(t, "electron-fetch", headers["X-Vscode-User-Agent-Library-Version"])
}

func TestCopilotAnthropicAuthorizer(t *testing.T) {
	setTestHome(t)

	_, err := SaveCopilotCredentials(&CopilotCredentials{
		AccessToken:    "github-oauth-token",
		CopilotToken:   "copilot-access-token",
		CopilotExpires: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://api.githubcopilot.com/v1/messages", nil)
	req.Header.Set("X-Initiator", CopilotInitiatorAgent)
	err = CopilotAnthropicAuthorizer().Authorize(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer copilot-access-token", req.Header.Get("Authorization"))
	assert.Equal(t, "GitHubCopilotChat/0.26.7", req.Header.Get("User-Agent"))
	assert.Equal(t, "vscode/1.102.0", req.Header.Get("Editor-Version"))
	assert.Equal(t, "copilot-chat/0.26.7", req.Header.Get("Editor-Plugin-Version"))
	assert.Equal(t, "vscode-chat", req.Header.Get("Copilot-Integration-Id"))
	assert.Equal(t, CopilotInitiatorAgent, req.Header.Get("X-Initiator"))
}

func TestCopilotHeaderMap(t *testing.T) {
	headers := CopilotHeaderMap(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent})
	assert.Equal(t, CopilotInitiatorAgent, headers["X-Initiator"])

	headers = CopilotHeaderMap(llmtypes.MessageOpt{})
	assert.Equal(t, CopilotInitiatorUser, headers["X-Initiator"])
}

func TestCopilotOpenAIRequestOptions(t *testing.T) {
	opts := CopilotOpenAIRequestOptions(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent})
	require.Len(t, opts, 1)
}

func TestCopilotAnthropicRequestOptions(t *testing.T) {
	opts := CopilotAnthropicRequestOptions(llmtypes.MessageOpt{Initiator: llmtypes.InitiatorAgent})
	require.Len(t, opts, 1)
}

func TestCodexAuthorizer(t *testing.T) {
	t.Run("uses oauth credentials", func(t *testing.T) {
		setTestHome(t)

		_, err := SaveCodexCredentials(&CodexCredentials{
			AccessToken:  "codex-access-token",
			RefreshToken: "codex-refresh-token",
			AccountID:    "acct_123",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
		err = CodexAuthorizer().Authorize(req)

		require.NoError(t, err)
		assert.Equal(t, "Bearer codex-access-token", req.Header.Get("Authorization"))
		assert.Equal(t, "acct_123", req.Header.Get("ChatGPT-Account-ID"))
		assert.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
		assert.Equal(t, CodexOriginator, req.Header.Get("originator"))
	})

	t.Run("rejects api key only credentials", func(t *testing.T) {
		setTestHome(t)

		authPath, err := codexAuthFilePath()
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(authPath), 0o755))

		data, err := json.Marshal(CodexAuthFile{OpenAIAPIKey: "codex-api-key"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(authPath, data, 0o600))

		req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
		err = CodexAuthorizer().Authorize(req)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no valid OAuth credentials")
		assert.Empty(t, req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("ChatGPT-Account-ID"))
	})
}

func TestAPIKeyAuthorizerHelpers(t *testing.T) {
	t.Run("openai env helper returns error when missing", func(t *testing.T) {
		t.Setenv("KODELET_TEST_OPENAI_KEY", "")

		authorizer, err := OpenAIAPIKeyAuthorizerFromEnv("KODELET_TEST_OPENAI_KEY")

		assert.Nil(t, authorizer)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "KODELET_TEST_OPENAI_KEY environment variable is required")
	})

	t.Run("openai env helper injects bearer token", func(t *testing.T) {
		t.Setenv("KODELET_TEST_OPENAI_KEY", "openai-api-key")

		authorizer, err := OpenAIAPIKeyAuthorizerFromEnv("KODELET_TEST_OPENAI_KEY")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
		err = authorizer.Authorize(req)

		require.NoError(t, err)
		assert.Equal(t, "Bearer openai-api-key", req.Header.Get("Authorization"))
	})

	t.Run("anthropic env helper injects api key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "anthropic-api-key")

		req := httptest.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/models", nil)
		req.Header.Set("Authorization", "Bearer stale-token")

		err := AnthropicAPIKeyAuthorizerFromEnv().Authorize(req)

		require.NoError(t, err)
		assert.Equal(t, "anthropic-api-key", req.Header.Get("X-Api-Key"))
		assert.Empty(t, req.Header.Get("Authorization"))
	})
}

func TestRequestOptionHelpers(t *testing.T) {
	authorizer := AuthorizerFunc(func(req *http.Request) error { return nil })

	assert.Nil(t, AnthropicRequestOptionsWithAuthorizer(nil))
	assert.Len(t, AnthropicRequestOptionsWithAuthorizer(authorizer), 1)
	assert.Nil(t, OpenAIRequestOptionsWithAuthorizer(nil))
	assert.Len(t, OpenAIRequestOptionsWithAuthorizer(authorizer), 1)
}
