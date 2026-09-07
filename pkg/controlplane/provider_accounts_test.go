package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderAccountsDaemonAdministration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, alias := range []string{"work", "personal"} {
		_, err := auth.SaveAnthropicCredentialsWithAlias(alias, &auth.AnthropicCredentials{Email: alias + "@example.com", AccessToken: "secret-access", RefreshToken: "secret-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()})
		require.NoError(t, err)
	}
	server := &Server{router: mux.NewRouter(), config: &ServerConfig{AuthToken: "admin", WebAuthMode: WebAuthModeOIDC}}
	server.setupRoutes()
	daemon := httptest.NewServer(server.router)
	defer daemon.Close()
	client, err := chat.NewClient(daemon.URL, "admin", "")
	require.NoError(t, err)
	accounts, err := client.AnthropicAccounts(t.Context())
	require.NoError(t, err)
	require.Len(t, accounts.Accounts, 2)
	encoded, err := json.Marshal(accounts)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret")
	assert.NotContains(t, string(encoded), "Token")
	for _, mutation := range []chat.AnthropicAccountMutation{
		{Action: "default", Alias: "personal"},
		{Action: "rename", Alias: "personal", NewAlias: "home"},
		{Action: "remove", Alias: "work"},
	} {
		require.NoError(t, client.MutateAnthropicAccount(t.Context(), mutation))
	}
	accounts, err = client.AnthropicAccounts(t.Context())
	require.NoError(t, err)
	require.Len(t, accounts.Accounts, 1)
	assert.Equal(t, "home", accounts.Accounts[0].Alias)
	assert.True(t, accounts.Accounts[0].IsDefault)
	require.NoError(t, client.MutateAnthropicAccount(t.Context(), chat.AnthropicAccountMutation{Action: "logout"}))
	accounts, err = client.AnthropicAccounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, accounts.Accounts)
	_, err = client.AnthropicAccountUsage(t.Context(), "missing")
	require.ErrorContains(t, err, "404")
}

func TestProviderAccountsRejectBeforeEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credentialsPath, err := auth.SaveAnthropicCredentialsWithAlias("work", &auth.AnthropicCredentials{Email: "work@example.com", AccessToken: "untouched"})
	require.NoError(t, err)
	before, err := os.ReadFile(credentialsPath)
	require.NoError(t, err)
	server := &Server{}
	for _, body := range []string{`null`, `[]`, `{"action":"logout","alias":null}`, `{"action":"remove","alias":""}`, `{"action":"rename","alias":"work","newAlias":"../bad"}`, `{"action":"logout","unknown":true}`, `{"action":"logout"} {}`, `{"action":"logout","alias":"` + strings.Repeat("x", 17000) + `"}`} {
		response := httptest.NewRecorder()
		server.handleAnthropicAccounts(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		assert.Equal(t, http.StatusBadRequest, response.Code, "%.80s", body)
	}
	for _, body := range []string{`null`, `{"alias":null}`, `{"alias":"../bad"}`, `{"alias":"work","unknown":true}`, `{} {}`} {
		response := httptest.NewRecorder()
		server.handleAnthropicAccountUsage(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	for _, status := range []anthropicOAuthLoginStatus{anthropicOAuthLoginStarting, anthropicOAuthLoginPending, anthropicOAuthLoginCompleting} {
		server.anthropicOAuthLogin = &anthropicOAuthLoginSession{ID: "active", Status: status, ExpiresAt: time.Now().Add(time.Minute)}
		response := httptest.NewRecorder()
		server.handleAnthropicAccounts(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"logout"}`)))
		assert.Equal(t, http.StatusConflict, response.Code)
	}
	for _, role := range []Role{RoleUser, RoleRunnerAdmin, RoleTerminal} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"logout"}`))
		request = request.WithContext(contextWithPrincipal(request.Context(), Principal{ID: "non-admin", Roles: []string{string(role)}}))
		for _, handler := range []http.HandlerFunc{server.handleAnthropicAccounts, server.handleAnthropicAccountUsage} {
			response := httptest.NewRecorder()
			server.requireRole(RoleAdmin, handler)(response, request)
			assert.Equal(t, http.StatusForbidden, response.Code)
		}
	}
	after, err := os.ReadFile(credentialsPath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.FileExists(t, filepath.Join(home, ".kodelet", "anthropic-credentials.json"))
}

type aliasSavingAnthropicAuth struct {
	*fakeAnthropicProviderAuthService
	alias string
}

func (f *aliasSavingAnthropicAuth) SaveCredentialsWithAlias(credentials *auth.AnthropicCredentials, alias string) error {
	f.alias = alias
	return defaultAnthropicProviderAuthService{}.SaveCredentialsWithAlias(credentials, alias)
}

func TestProviderAccountsOAuthAliasDoesNotReplaceDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := auth.SaveAnthropicCredentialsWithAlias("existing", &auth.AnthropicCredentials{Email: "existing@example.com", AccessToken: "original"})
	require.NoError(t, err)
	for _, alias := range []string{"named", ""} {
		service := &aliasSavingAnthropicAuth{fakeAnthropicProviderAuthService: &fakeAnthropicProviderAuthService{credentials: &auth.AnthropicCredentials{Email: "derived@example.com", AccessToken: "new"}}}
		server := &Server{anthropicAuth: service, anthropicOAuthLogin: &anthropicOAuthLoginSession{ID: "login", Status: anthropicOAuthLoginPending, Verifier: "server-only", ExpiresAt: time.Now().Add(time.Minute)}}
		for _, body := range []string{`{"code":"code","alias":"bad name"}`, `{"code":"code","alias":null}`, `{"code":"code","unknown":true}`, `{"code":"code"} {}`} {
			response := httptest.NewRecorder()
			request := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), map[string]string{"id": "login"})
			server.handleCompleteAnthropicOAuthLogin(response, request)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Empty(t, service.exchangeCode, "invalid input must not consume the code")
		}
		body, err := json.Marshal(map[string]string{"code": "code", "alias": alias})
		require.NoError(t, err)
		response := httptest.NewRecorder()
		server.handleCompleteAnthropicOAuthLogin(response, mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))), map[string]string{"id": "login"}))
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"connected"`)
		assert.NotContains(t, response.Body.String(), "server-only")
		assert.Equal(t, alias, service.alias)
		assert.Equal(t, "server-only", service.exchangeVerifier)
	}
	accounts, err := auth.ListAnthropicAccounts()
	require.NoError(t, err)
	assert.Len(t, accounts, 3)
	credentials, err := auth.GetAnthropicCredentialsByAlias("existing")
	require.NoError(t, err)
	assert.Equal(t, "original", credentials.AccessToken)
	alias, err := auth.GetDefaultAnthropicAccount()
	require.NoError(t, err)
	assert.Equal(t, "existing", alias)
}

func TestProviderAccountsExpiredLoginCannotResurrectLogout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	service := &fakeAnthropicProviderAuthService{}
	server := &Server{anthropicAuth: service, anthropicOAuthLogin: &anthropicOAuthLoginSession{ID: "expired", Status: anthropicOAuthLoginPending, ExpiresAt: time.Now().Add(-time.Minute)}}
	response := httptest.NewRecorder()
	server.handleAnthropicAccounts(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"logout"}`)))
	require.Equal(t, http.StatusNoContent, response.Code)
	response = httptest.NewRecorder()
	server.handleCompleteAnthropicOAuthLogin(response, mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"code"}`)), map[string]string{"id": "expired"}))
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Empty(t, service.exchangeCode)
	assert.Zero(t, service.saveCount)
}
