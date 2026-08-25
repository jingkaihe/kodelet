package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	providerauth "github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCodexCompletion struct {
	credentials *providerauth.CodexCredentials
	err         error
}

type fakeCodexProviderAuthService struct {
	mu             sync.Mutex
	connected      bool
	deviceCode     *providerauth.CodexDeviceCode
	requestErr     error
	requestStarted chan struct{}
	releaseRequest chan struct{}
	completion     chan fakeCodexCompletion
	requestOnce    sync.Once
	saveCount      int
}

type fakeAnthropicProviderAuthService struct {
	mu               sync.Mutex
	connected        bool
	authorizationURL string
	verifier         string
	generateStarted  chan struct{}
	releaseGenerate  chan struct{}
	generateOnce     sync.Once
	exchangeStarted  chan struct{}
	releaseExchange  chan struct{}
	exchangeOnce     sync.Once
	exchangeCode     string
	exchangeVerifier string
	credentials      *providerauth.AnthropicCredentials
	exchangeErr      error
	saveErr          error
	saveCount        int
}

func (f *fakeCodexProviderAuthService) Connected() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected, nil
}

func (f *fakeCodexProviderAuthService) RequestDeviceCode(ctx context.Context) (*providerauth.CodexDeviceCode, error) {
	if f.requestStarted != nil {
		f.requestOnce.Do(func() { close(f.requestStarted) })
	}
	if f.releaseRequest != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.releaseRequest:
		}
	}
	return f.deviceCode, f.requestErr
}

func (f *fakeCodexProviderAuthService) CompleteDeviceCodeLogin(ctx context.Context, _ *providerauth.CodexDeviceCode) (*providerauth.CodexCredentials, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-f.completion:
		return result.credentials, result.err
	}
}

func (f *fakeCodexProviderAuthService) SaveCredentials(_ *providerauth.CodexCredentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = true
	f.saveCount++
	return nil
}

func (f *fakeAnthropicProviderAuthService) Connected() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected, nil
}

func (f *fakeAnthropicProviderAuthService) GenerateAuthURL() (string, string, error) {
	if f.generateStarted != nil {
		f.generateOnce.Do(func() { close(f.generateStarted) })
	}
	if f.releaseGenerate != nil {
		<-f.releaseGenerate
	}
	return f.authorizationURL, f.verifier, nil
}

func (f *fakeAnthropicProviderAuthService) ExchangeCode(ctx context.Context, code string, verifier string) (*providerauth.AnthropicCredentials, error) {
	if f.exchangeStarted != nil {
		f.exchangeOnce.Do(func() { close(f.exchangeStarted) })
	}
	if f.releaseExchange != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.releaseExchange:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeCode = code
	f.exchangeVerifier = verifier
	return f.credentials, f.exchangeErr
}

func (f *fakeAnthropicProviderAuthService) SaveCredentials(_ *providerauth.AnthropicCredentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.connected = true
	f.saveCount++
	return nil
}

func testCodexDeviceCode() *providerauth.CodexDeviceCode {
	return &providerauth.CodexDeviceCode{
		VerificationURL: "https://auth.openai.test/codex/device",
		UserCode:        "CODE-123",
	}
}

func TestServerHandleGetCodexProvider(t *testing.T) {
	server := &Server{codexAuth: &fakeCodexProviderAuthService{connected: true}}
	response := httptest.NewRecorder()

	server.handleGetCodexProvider(response, httptest.NewRequest(http.MethodGet, "/api/providers/codex", nil))

	assert.Equal(t, http.StatusOK, response.Code)
	var payload providerConnectionResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	assert.True(t, payload.Connected)
}

func TestServerAllowsOnlyOneCodexDeviceLoginInFlight(t *testing.T) {
	service := &fakeCodexProviderAuthService{
		deviceCode:     testCodexDeviceCode(),
		requestStarted: make(chan struct{}),
		releaseRequest: make(chan struct{}),
		completion:     make(chan fakeCodexCompletion),
	}
	server := &Server{codexAuth: service, runCtx: t.Context()}
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		server.handleStartCodexDeviceLogin(firstResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))
		close(firstDone)
	}()

	select {
	case <-service.requestStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "first device login did not start")
	}

	secondResponse := httptest.NewRecorder()
	server.handleStartCodexDeviceLogin(secondResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))
	assert.Equal(t, http.StatusConflict, secondResponse.Code)

	close(service.releaseRequest)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		require.FailNow(t, "first device login did not return its device code")
	}
	assert.Equal(t, http.StatusOK, firstResponse.Code)

	thirdResponse := httptest.NewRecorder()
	server.handleStartCodexDeviceLogin(thirdResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))
	assert.Equal(t, http.StatusConflict, thirdResponse.Code)
}

func TestServerCodexDeviceLoginCompletes(t *testing.T) {
	service := &fakeCodexProviderAuthService{
		deviceCode: testCodexDeviceCode(),
		completion: make(chan fakeCodexCompletion, 1),
	}
	server := &Server{codexAuth: service, runCtx: t.Context()}
	startResponse := httptest.NewRecorder()
	server.handleStartCodexDeviceLogin(startResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))

	var login codexDeviceLoginResponse
	require.NoError(t, json.Unmarshal(startResponse.Body.Bytes(), &login))
	assert.Equal(t, codexDeviceLoginPending, login.Status)
	assert.Equal(t, "CODE-123", login.UserCode)
	service.completion <- fakeCodexCompletion{credentials: &providerauth.CodexCredentials{
		AccessToken: "access-token",
		AccountID:   "account-id",
	}}

	require.Eventually(t, func() bool {
		statusResponse := httptest.NewRecorder()
		statusRequest := mux.SetURLVars(
			httptest.NewRequest(http.MethodGet, "/api/providers/codex/device-login/"+login.ID, nil),
			map[string]string{"id": login.ID},
		)
		server.handleGetCodexDeviceLogin(statusResponse, statusRequest)
		var status codexDeviceLoginResponse
		return statusResponse.Code == http.StatusOK && json.Unmarshal(statusResponse.Body.Bytes(), &status) == nil && status.Status == codexDeviceLoginConnected
	}, time.Second, 10*time.Millisecond)

	service.mu.Lock()
	assert.Equal(t, 1, service.saveCount)
	service.mu.Unlock()
}

func TestServerCancelsCodexDeviceLogin(t *testing.T) {
	service := &fakeCodexProviderAuthService{
		deviceCode: testCodexDeviceCode(),
		completion: make(chan fakeCodexCompletion),
	}
	server := &Server{codexAuth: service, runCtx: t.Context()}
	startResponse := httptest.NewRecorder()
	server.handleStartCodexDeviceLogin(startResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))
	var login codexDeviceLoginResponse
	require.NoError(t, json.Unmarshal(startResponse.Body.Bytes(), &login))

	cancelResponse := httptest.NewRecorder()
	cancelRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodDelete, "/api/providers/codex/device-login/"+login.ID, nil),
		map[string]string{"id": login.ID},
	)
	server.handleCancelCodexDeviceLogin(cancelResponse, cancelRequest)
	assert.Equal(t, http.StatusNoContent, cancelResponse.Code)

	require.Eventually(t, func() bool {
		server.codexDeviceLoginMu.Lock()
		defer server.codexDeviceLoginMu.Unlock()
		return server.codexDeviceLogin != nil && server.codexDeviceLogin.Status == codexDeviceLoginCanceled
	}, time.Second, 10*time.Millisecond)
}

func TestServerHandleGetAnthropicProvider(t *testing.T) {
	server := &Server{anthropicAuth: &fakeAnthropicProviderAuthService{connected: true}}
	response := httptest.NewRecorder()

	server.handleGetAnthropicProvider(response, httptest.NewRequest(http.MethodGet, "/api/providers/anthropic", nil))

	assert.Equal(t, http.StatusOK, response.Code)
	var payload providerConnectionResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	assert.Equal(t, "anthropic", payload.Provider)
	assert.True(t, payload.Connected)
}

func TestServerAllowsOnlyOneAnthropicOAuthLoginInFlight(t *testing.T) {
	service := &fakeAnthropicProviderAuthService{
		authorizationURL: "https://claude.ai/oauth/authorize?test=1",
		verifier:         "verifier-123",
		generateStarted:  make(chan struct{}),
		releaseGenerate:  make(chan struct{}),
	}
	server := &Server{anthropicAuth: service}
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		server.handleStartAnthropicOAuthLogin(firstResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
		close(firstDone)
	}()

	select {
	case <-service.generateStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "first Anthropic login did not start")
	}

	secondResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(secondResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
	assert.Equal(t, http.StatusConflict, secondResponse.Code)

	close(service.releaseGenerate)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		require.FailNow(t, "first Anthropic login did not return its authorization URL")
	}
	assert.Equal(t, http.StatusOK, firstResponse.Code)

	var login anthropicOAuthLoginResponse
	require.NoError(t, json.Unmarshal(firstResponse.Body.Bytes(), &login))
	thirdResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(thirdResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
	assert.Equal(t, http.StatusConflict, thirdResponse.Code)

	cancelResponse := httptest.NewRecorder()
	cancelRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodDelete, "/api/providers/anthropic/oauth-login/"+login.ID, nil),
		map[string]string{"id": login.ID},
	)
	server.handleCancelAnthropicOAuthLogin(cancelResponse, cancelRequest)
	assert.Equal(t, http.StatusNoContent, cancelResponse.Code)

	fourthResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(fourthResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
	assert.Equal(t, http.StatusOK, fourthResponse.Code)
}

func TestServerCompletesAnthropicOAuthLogin(t *testing.T) {
	service := &fakeAnthropicProviderAuthService{
		authorizationURL: "https://claude.ai/oauth/authorize?test=1",
		verifier:         "verifier-123",
		credentials: &providerauth.AnthropicCredentials{
			Email:        "user@example.com",
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
		},
	}
	server := &Server{anthropicAuth: service}
	startResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(startResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))

	var login anthropicOAuthLoginResponse
	require.NoError(t, json.Unmarshal(startResponse.Body.Bytes(), &login))
	assert.Equal(t, anthropicOAuthLoginPending, login.Status)
	assert.Equal(t, service.authorizationURL, login.AuthorizationURL)

	completeResponse := httptest.NewRecorder()
	completeRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login/"+login.ID+"/complete", strings.NewReader(`{"code":"authorization-code#verifier-123"}`)),
		map[string]string{"id": login.ID},
	)
	server.handleCompleteAnthropicOAuthLogin(completeResponse, completeRequest)

	assert.Equal(t, http.StatusOK, completeResponse.Code)
	var completed anthropicOAuthLoginResponse
	require.NoError(t, json.Unmarshal(completeResponse.Body.Bytes(), &completed))
	assert.Equal(t, anthropicOAuthLoginConnected, completed.Status)
	service.mu.Lock()
	assert.Equal(t, "authorization-code#verifier-123", service.exchangeCode)
	assert.Equal(t, "verifier-123", service.exchangeVerifier)
	assert.Equal(t, 1, service.saveCount)
	service.mu.Unlock()
}

func TestServerKeepsAnthropicLoginExclusiveWhileCompleting(t *testing.T) {
	service := &fakeAnthropicProviderAuthService{
		authorizationURL: "https://claude.ai/oauth/authorize?test=1",
		verifier:         "verifier-123",
		credentials:      &providerauth.AnthropicCredentials{AccessToken: "access-token"},
		exchangeStarted:  make(chan struct{}),
		releaseExchange:  make(chan struct{}),
	}
	server := &Server{anthropicAuth: service}
	startResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(startResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))

	var login anthropicOAuthLoginResponse
	require.NoError(t, json.Unmarshal(startResponse.Body.Bytes(), &login))
	completeResponse := httptest.NewRecorder()
	completeDone := make(chan struct{})
	go func() {
		completeRequest := mux.SetURLVars(
			httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login/"+login.ID+"/complete", strings.NewReader(`{"code":"authorization-code#state"}`)),
			map[string]string{"id": login.ID},
		)
		server.handleCompleteAnthropicOAuthLogin(completeResponse, completeRequest)
		close(completeDone)
	}()

	select {
	case <-service.exchangeStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "Anthropic code exchange did not start")
	}

	secondResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(secondResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
	assert.Equal(t, http.StatusConflict, secondResponse.Code)

	close(service.releaseExchange)
	select {
	case <-completeDone:
	case <-time.After(time.Second):
		require.FailNow(t, "Anthropic code exchange did not finish")
	}
	assert.Equal(t, http.StatusOK, completeResponse.Code)
}

func TestServerRejectsEmptyAnthropicAuthorizationCode(t *testing.T) {
	service := &fakeAnthropicProviderAuthService{
		authorizationURL: "https://claude.ai/oauth/authorize?test=1",
		verifier:         "verifier-123",
	}
	server := &Server{anthropicAuth: service}
	startResponse := httptest.NewRecorder()
	server.handleStartAnthropicOAuthLogin(startResponse, httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login", nil))
	var login anthropicOAuthLoginResponse
	require.NoError(t, json.Unmarshal(startResponse.Body.Bytes(), &login))

	completeResponse := httptest.NewRecorder()
	completeRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/providers/anthropic/oauth-login/"+login.ID+"/complete", strings.NewReader(`{"code":"  "}`)),
		map[string]string{"id": login.ID},
	)
	server.handleCompleteAnthropicOAuthLogin(completeResponse, completeRequest)

	assert.Equal(t, http.StatusBadRequest, completeResponse.Code)
}
