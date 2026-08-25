package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	var payload codexProviderResponse
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
		t.Fatal("first device login did not start")
	}

	secondResponse := httptest.NewRecorder()
	server.handleStartCodexDeviceLogin(secondResponse, httptest.NewRequest(http.MethodPost, "/api/providers/codex/device-login", nil))
	assert.Equal(t, http.StatusConflict, secondResponse.Code)

	close(service.releaseRequest)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first device login did not return its device code")
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
