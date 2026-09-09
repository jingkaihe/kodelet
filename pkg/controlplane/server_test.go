package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationService implements the methods we need for testing
type mockConversationService struct {
	listFunc    func(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error)
	getFunc     func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	forkFunc    func(ctx context.Context, id string) (*conversations.GetConversationResponse, error)
	deleteFunc  func(ctx context.Context, id string) error
	getToolFunc func(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error)
	closeFunc   func() error
}

type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	serverConn, clientConn := net.Pipe()
	reader := bufio.NewReader(serverConn)
	writer := bufio.NewWriter(serverConn)
	return clientConn, bufio.NewReadWriter(reader, writer), nil
}

type disconnectedResponseWriter struct {
	header http.Header
	writes int
}

func newDisconnectedResponseWriter() *disconnectedResponseWriter {
	return &disconnectedResponseWriter{header: make(http.Header)}
}

func (w *disconnectedResponseWriter) Header() http.Header {
	return w.header
}

func (w *disconnectedResponseWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("client disconnected")
}

func (w *disconnectedResponseWriter) WriteHeader(int) {}

func (w *disconnectedResponseWriter) Flush() {}

func (m *mockConversationService) ListConversations(ctx context.Context, req *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return &conversations.ListConversationsResponse{}, nil
}

func (m *mockConversationService) GetConversation(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, id)
	}
	return &conversations.GetConversationResponse{}, nil
}

func (m *mockConversationService) DeleteConversation(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func (m *mockConversationService) ForkConversation(ctx context.Context, id string) (*conversations.GetConversationResponse, error) {
	if m.forkFunc != nil {
		return m.forkFunc(ctx, id)
	}
	return &conversations.GetConversationResponse{}, nil
}

type mockChatRunner struct {
	runFunc func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error)
}

func (m *mockChatRunner) Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, req, sink)
	}
	return "", nil
}

type cancellationCheckingChatRunner struct {
	runCtx      context.Context
	closeCalled atomic.Bool
}

func (r *cancellationCheckingChatRunner) Run(context.Context, ChatRequest, ChatEventSink) (string, error) {
	return "", nil
}

func (r *cancellationCheckingChatRunner) Close() error {
	r.closeCalled.Store(true)
	if r.runCtx.Err() == nil {
		return errors.New("chat runner closed before run context was canceled")
	}
	return nil
}

type testFrontend struct{}

func (testFrontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte("<html><body><div id=\"root\"></div></body></html>"))
}

func (testFrontend) IsPublicPath(path string) bool {
	return strings.HasPrefix(path, "/assets/") || path == "/favicon.ico"
}

func testFrontendHandler() FrontendHandler {
	return testFrontend{}
}

func (m *mockConversationService) GetToolResult(ctx context.Context, conversationID, toolCallID string) (*conversations.GetToolResultResponse, error) {
	if m.getToolFunc != nil {
		return m.getToolFunc(ctx, conversationID, toolCallID)
	}
	return &conversations.GetToolResultResponse{}, nil
}

func (m *mockConversationService) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name          string
		config        *ServerConfig
		expectedError string
	}{
		{
			name: "valid config",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
			},
		},
		{
			name: "ephemeral listener port",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         0,
				CompactRatio: 0.8,
			},
		},
		{
			name: "empty host",
			config: &ServerConfig{
				Host: "",
				Port: 8080,
			},
			expectedError: "host cannot be empty",
		},
		{
			name: "invalid port - too low",
			config: &ServerConfig{
				Host: "localhost",
				Port: -1,
			},
			expectedError: "port must be between 0 and 65535",
		},
		{
			name: "invalid port - too high",
			config: &ServerConfig{
				Host: "localhost",
				Port: 65536,
			},
			expectedError: "port must be between 0 and 65535",
		},
		{
			name: "invalid compact ratio",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: -0.1,
			},
			expectedError: "compact-ratio must be greater than 0.0 and less than or equal to 1.0",
		},
		{
			name: "zero compact ratio",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0,
			},
			expectedError: "compact-ratio must be greater than 0.0 and less than or equal to 1.0",
		},
		{
			name: "web UI token auth does not require runner token auth",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				AuthToken:    "web-secret",
			},
		},
		{
			name: "control-plane workspace rejects cwd",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				CWD:          "/srv/kodelet",
			},
			expectedError: "serve --cwd is no longer supported; use --runner-workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectedError != "" {
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), tt.expectedError)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestServerStopCancelsRunContextBeforeClosingChatRunner(t *testing.T) {
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	runner := &cancellationCheckingChatRunner{runCtx: runCtx}
	server := &Server{
		chatRunner: runner,
		runCtx:     runCtx,
		runCancel:  runCancel,
	}

	require.NoError(t, server.Stop())
	assert.ErrorIs(t, runCtx.Err(), context.Canceled)
	assert.True(t, runner.closeCalled.Load())
}

func TestServerStopClosesOpenConversationStream(t *testing.T) {
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              runCtx,
		runCancel:           runCancel,
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
		shutdownTimeout:     time.Second,
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/conversations/{id}/stream", server.handleStreamConversation).Methods(http.MethodGet)
	httpServer := &http.Server{Handler: router}
	server.server = httpServer

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = httpServer.Close()
	})

	response, err := http.Get("http://" + listener.Addr().String() + "/api/conversations/conv-123/stream")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = response.Body.Close()
	})
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "\n", line)
	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, server.Stop())
	assert.ErrorIs(t, runCtx.Err(), context.Canceled)
	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 0
	}, time.Second, 10*time.Millisecond)
	require.ErrorIs(t, <-serveDone, http.ErrServerClosed)
}

func TestServerCloseLeavesDependenciesOpenWhenHTTPShutdownTimesOut(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		<-releaseHandler
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(listener)
	}()

	var conversationClosed atomic.Bool
	server := &Server{
		server:          httpServer,
		authStore:       store,
		runCtx:          runCtx,
		runCancel:       runCancel,
		shutdownTimeout: 20 * time.Millisecond,
		conversationService: &mockConversationService{closeFunc: func() error {
			conversationClosed.Store(true)
			return nil
		}},
	}
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if response != nil {
			requestErr = errors.WithMessage(response.Body.Close(), "failed to close response body")
		}
		requestDone <- requestErr
	}()
	<-handlerStarted

	err = server.Close()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, conversationClosed.Load())
	assert.ErrorIs(t, runCtx.Err(), context.Canceled)
	require.NoError(t, store.db.PingContext(t.Context()))

	close(releaseHandler)
	require.NoError(t, <-requestDone)
	require.ErrorIs(t, <-serveDone, http.ErrServerClosed)
	server.shutdownTimeout = time.Second
	require.NoError(t, server.Close())
	assert.True(t, conversationClosed.Load())
	assert.ErrorIs(t, runCtx.Err(), context.Canceled)
}

func TestServerConfig_Validate_RejectsWhitespaceAuthToken(t *testing.T) {
	config := &ServerConfig{
		Host:         "localhost",
		Port:         8080,
		CompactRatio: 0.8,
		AuthToken:    "   ",
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth-token cannot be empty")
}

func TestServerConfig_Validate_RejectsCookieUnsafeAuthToken(t *testing.T) {
	config := &ServerConfig{
		Host:         "localhost",
		Port:         8080,
		CompactRatio: 0.8,
		AuthToken:    "secret;token",
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth-token can only contain letters, numbers, and URL-safe punctuation")
}

func TestServerConfig_Validate_RejectsInvalidCORSOrigin(t *testing.T) {
	config := &ServerConfig{
		Host:         "localhost",
		Port:         8080,
		CompactRatio: 0.8,
		CORSOrigins:  []string{"https://example.com/app"},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cors-origin")
}

func TestValidateCORSOriginsAndNormalization(t *testing.T) {
	err := ValidateCORSOrigins([]string{"https://Example.COM:443", "https://example.com:443"})
	require.NoError(t, err)

	normalized, err := normalizeConfiguredCORSOrigins([]string{
		" https://Example.COM:443 ",
		"https://example.com",
		"http://Example.COM:80",
		"http://example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"https://example.com", "http://example.com"}, normalized)

	normalizedOrigin, err := normalizeCORSOrigin("https://[::1]:443")
	require.NoError(t, err)
	assert.Equal(t, "https://[::1]", normalizedOrigin)

	_, err = normalizeConfiguredCORSOrigins([]string{"   "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cors-origin cannot be empty")

	_, err = normalizeCORSOrigin("ftp://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "origin must use http:// or https://")

	_, err = normalizeCORSOrigin("https://example.com/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "origin must not include path")

	assert.Equal(t, "[::1]:3000", normalizedURLHost("[::1]:3000"))
	assert.Equal(t, "[::1]", normalizedURLHost("::1"))
	assert.Equal(t, "127.0.0.1", normalizedURLHost("127.0.0.1"))
	assert.False(t, isLoopbackOrigin("not a url"))
}

func TestNewServerInitializesRoutesAndNormalizesConfig(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	t.Setenv("KODELET_CONVERSATION_STORE_TYPE", "sqlite")
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))
	config := &ServerConfig{
		Host:            "127.0.0.1",
		Port:            1,
		CompactRatio:    0.8,
		AuthToken:       "token",
		RunnerAuthToken: "runner-token",
		CORSOrigins:     []string{"https://Example.com"},
	}

	server, err := NewServer(context.Background(), config, testFrontendHandler())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, server.Stop()) })

	assert.NotNil(t, server.router)
	assert.NotNil(t, server.conversationService)
	assert.NotNil(t, server.chatRunner)
	assert.Empty(t, config.CWD)
	assert.Equal(t, "token", config.AuthToken)
	assert.Equal(t, "runner-token", config.RunnerAuthToken)
	assert.Equal(t, []string{"https://example.com"}, config.CORSOrigins)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: webUIAuthCookieName, Value: "token"})
	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "<html")
}

func TestServerWithoutFrontendUsesMiddlewareAndUnavailableFallback(t *testing.T) {
	server := &Server{
		router: mux.NewRouter(),
		config: &ServerConfig{WebAuthMode: WebAuthModeNone},
	}
	server.setupRoutes()

	t.Run("read returns not found", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("write preserves frontend method contract", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/AUTH/DEVICE/", nil)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
		assert.Equal(t, "GET, HEAD", response.Header().Get("Allow"))
	})

	t.Run("preflight still reaches CORS middleware", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodOptions, "/api/chat", nil)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusOK, response.Code)
	})
}

func TestNewServerRequiresFrontendForBrowserApprovalFlows(t *testing.T) {
	for _, test := range []struct {
		name   string
		config *ServerConfig
	}{
		{
			name: "OIDC",
			config: &ServerConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				WebAuthMode:  WebAuthModeOIDC,
				OIDC: OIDCConfig{
					AllowAnyUser: true,
					Flow:         testOIDCFlow{},
				},
			},
		},
		{
			name: "runner enrollment",
			config: &ServerConfig{
				Host:           "localhost",
				Port:           8080,
				CompactRatio:   0.8,
				WebAuthMode:    WebAuthModeToken,
				AuthToken:      "web-token",
				RunnerAuthMode: RunnerAuthModeEnrollment,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, err := NewServer(t.Context(), test.config, nil)

			require.ErrorContains(t, err, "frontend handler is required")
			assert.Nil(t, server)
		})
	}
}

func TestFrontendCanonicalizesAuthApprovalPaths(t *testing.T) {
	server := &Server{frontendHandler: testFrontendHandler()}
	for _, test := range []struct {
		path     string
		location string
	}{
		{path: "/AUTH/DEVICE/?user_code=ABCD-EFGH", location: "/auth/device?user_code=ABCD-EFGH"},
		{path: "/runner/enroll///", location: "/runner/enroll"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()

			server.serveFrontend(response, request)

			assert.Equal(t, http.StatusPermanentRedirect, response.Code)
			assert.Equal(t, test.location, response.Header().Get("Location"))
		})
	}
}

func TestNewAuthTokenGeneratesUsableToken(t *testing.T) {
	token, err := NewAuthToken()

	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.NotContains(t, token, "=")
}

func TestValidateAuthTokenAcceptsGeneratedToken(t *testing.T) {
	token, err := NewAuthToken()
	require.NoError(t, err)

	assert.NoError(t, ValidateAuthToken(token))
}

func TestServer_corsMiddleware(t *testing.T) {
	server := &Server{config: &ServerConfig{CORSOrigins: []string{"https://app.example.com"}}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := server.corsMiddleware(next)

	t.Run("allows loopback origin by default", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/chat/settings", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), webCSRFHeaderName)
	})

	t.Run("allows ipv4 loopback origin by default", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/chat/settings", nil)
		req.Header.Set("Origin", "http://127.0.0.1:3000")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://127.0.0.1:3000", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allows ipv6 loopback origin by default", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/chat/settings", nil)
		req.Header.Set("Origin", "http://[::1]:3000")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://[::1]:3000", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allows configured origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings", nil)
		req.Header.Set("Origin", "https://app.example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "https://app.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("rejects disallowed preflight", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/chat/settings", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestServer_authMiddleware(t *testing.T) {
	server := &Server{config: &ServerConfig{AuthToken: "secret-token"}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := server.authMiddleware(next)

	t.Run("rejects missing token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "authentication required")
	})

	t.Run("allows bearer token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("sets cookie and redirects tokenized spa request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/c/abc?token=secret-token&view=full", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusFound, w.Code)
		assert.Equal(t, "/c/abc?view=full", w.Header().Get("Location"))
		require.Len(t, w.Result().Cookies(), 1)
		cookie := w.Result().Cookies()[0]
		assert.Equal(t, webUIAuthCookieName, cookie.Name)
		assert.Equal(t, "secret-token", cookie.Value)
		assert.True(t, cookie.HttpOnly)
	})

	t.Run("allows cookie token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings", nil)
		req.AddCookie(&http.Cookie{Name: webUIAuthCookieName, Value: "secret-token"})
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("allows query token for api request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings?token=secret-token", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("rejects bad query token on spa request with text response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/app?token=bad-token", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "invalid authentication token")
	})

	t.Run("passes options through without token", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/chat/settings", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("allows raw token header and token auth scheme", func(t *testing.T) {
		for _, header := range []string{"secret-token", "Token secret-token"} {
			req := httptest.NewRequest("GET", "/api/chat/settings", nil)
			req.Header.Set("Authorization", header)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNoContent, w.Code)
		}
	})

	t.Run("sets secure cookie for forwarded https token request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings?token=secret-token", nil)
		req.Header.Set("X-Forwarded-Proto", "https, http")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, w.Result().Cookies(), 1)
		assert.True(t, w.Result().Cookies()[0].Secure)
	})

	t.Run("uses the first forwarded protocol value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/chat/settings?token=secret-token", nil)
		req.Header.Set("X-Forwarded-Proto", "http, https")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.Len(t, w.Result().Cookies(), 1)
		assert.False(t, w.Result().Cookies()[0].Secure)
	})
}

func TestAuthHelpersAdditionalBranches(t *testing.T) {
	req := httptest.NewRequest("GET", "/?token=first&token=second", nil)
	token, ok := authQueryToken(req)
	assert.True(t, ok)
	assert.Equal(t, "first", token)

	assert.Equal(t, "bearer-value", authHeaderToken(" bearer bearer-value "))
	assert.Equal(t, "token-value", authHeaderToken(" token token-value "))
	assert.Equal(t, "raw-value", authHeaderToken(" raw-value "))
	assert.Empty(t, authHeaderToken("   "))

	server := &Server{frontendHandler: testFrontendHandler()}
	assert.False(t, server.shouldRedirectTokenRequest(httptest.NewRequest("POST", "/app?token=x", nil)))
	wsReq := httptest.NewRequest("GET", "/app?token=x", nil)
	wsReq.Header.Set("Upgrade", "websocket")
	assert.False(t, server.shouldRedirectTokenRequest(wsReq))
	assert.False(t, server.shouldRedirectTokenRequest(httptest.NewRequest("GET", "/api/chat?token=x", nil)))
	assert.False(t, server.shouldRedirectTokenRequest(httptest.NewRequest("GET", "/assets/main.js?token=x", nil)))

	req = httptest.NewRequest("GET", "/?token=x", nil)
	assert.Equal(t, "/", tokenlessURL(req))

	assert.True(t, constantTimeStringEqual("same", "same"))
	assert.False(t, constantTimeStringEqual("short", "longer"))
	assert.False(t, constantTimeStringEqual("same", "diff"))
}

func TestServerConfig_Validate_RejectsLocalCWD(t *testing.T) {
	for _, cwd := range []string{t.TempDir(), "/missing/daemon/workspace", "relative", "   "} {
		config := &ServerConfig{Host: "localhost", Port: 8080, CWD: cwd, CompactRatio: 0.8}
		require.ErrorContains(t, config.Validate(), "serve --cwd is no longer supported; use --runner-workspace")
	}
}

func TestServer_handleListConversations(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	conversationCWD := filepath.Join(homeDir, "workspace", "kodelet")

	mockService := &mockConversationService{
		listFunc: func(_ context.Context, request *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
			assert.Equal(t, "~/workspace/kodelet", request.SearchTerm)
			assert.Empty(t, request.SearchCWDTerm, "never expand a runner path against daemon HOME")
			assert.Equal(t, "~/workspace/kodelet", request.CWD)
			assert.Equal(t, "runner-1", request.RunnerID)
			return &conversations.ListConversationsResponse{
				Conversations: []convtypes.ConversationSummary{
					{
						ID:       "1",
						Summary:  "Test 1",
						Provider: "openai",
						CWD:      conversationCWD,
						Metadata: map[string]any{"platform": "fireworks", "api_mode": "chat_completions"},
					},
					{ID: "2", Summary: "Test 2", Provider: "anthropic"},
				},
				CWDs:  []string{conversationCWD},
				Total: 2,
			}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{"1": newActiveChatRun(func() {})},
	}

	req := httptest.NewRequest(
		"GET",
		"/api/conversations?limit=10&runnerId=runner-1&search="+url.QueryEscape("~/workspace/kodelet")+"&cwd="+url.QueryEscape("~/workspace/kodelet"),
		nil,
	)
	w := httptest.NewRecorder()

	server.handleListConversations(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response conversations.ListConversationsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, 2, len(response.Conversations))
	assert.Equal(t, 2, response.Total)
	assert.Equal(t, "OpenAI", response.Conversations[0].Provider)
	assert.Equal(t, conversationCWD, response.Conversations[0].CWD)
	assert.Equal(t, []string{conversationCWD}, response.CWDs)
	assert.True(t, response.Conversations[0].IsRunning)
	assert.Equal(t, "fireworks", response.Conversations[0].Metadata["platform"])
	assert.Equal(t, "chat_completions", response.Conversations[0].Metadata["api_mode"])
	assert.Equal(t, "Anthropic", response.Conversations[1].Provider)
	assert.False(t, response.Conversations[1].IsRunning)
}

func TestServer_handleGetConversation(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	conversationCWD := filepath.Join(homeDir, "workspace", "project")
	conversationID := "test-id-123"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					CWD:      conversationCWD,
					Summary:  "Test conversation",
					Provider: "openai",
					Metadata: map[string]any{
						"platform":     "codex",
						"api_mode":     "responses",
						"profile":      "legacy-profile",
						"service_tier": "fast",
						conversations.ConfigSnapshotMetadataKey: map[string]any{
							"version":          llmtypes.ConversationConfigSnapshotVersion,
							"profile":          "codex",
							"provider":         "openai",
							"model":            "gpt-test",
							"reasoning_effort": "high",
						},
					},
					RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{conversationID: newActiveChatRun(func() {})},
	}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, conversationID, response.ID)
	assert.Equal(t, "Test conversation", response.Summary)
	assert.Equal(t, "OpenAI", response.Provider)
	assert.Equal(t, conversationCWD, response.CWD)
	assert.True(t, response.CWDLocked)
	assert.Equal(t, "codex", response.Profile)
	assert.True(t, response.ProfileLocked)
	assert.Equal(t, "high", response.ReasoningEffort)
	assert.True(t, response.ReasoningEffortLocked)
	assert.True(t, response.IsRunning)
	assert.Equal(t, 1, response.MessageCount)
}

func TestDaemonHistoryFiltersValidateAndPreserveRunnerPaths(t *testing.T) {
	var calls int
	server := &Server{conversationService: &mockConversationService{
		listFunc: func(_ context.Context, request *conversations.ListConversationsRequest) (*conversations.ListConversationsResponse, error) {
			calls++
			assert.Equal(t, "~/runner-only", request.CWD)
			assert.Empty(t, request.SearchCWDTerm)
			assert.Equal(t, "anthropic", request.Provider)
			assert.Equal(t, "messageCount", request.SortBy)
			assert.Equal(t, "asc", request.SortOrder)
			assert.Equal(t, 2, request.Limit)
			assert.Equal(t, 3, request.Offset)
			require.NotNil(t, request.StartDate)
			require.NotNil(t, request.EndDate)
			assert.Equal(t, "2026-09-05T00:00:00Z", request.StartDate.Format(time.RFC3339Nano))
			assert.Equal(t, "2026-09-05T23:59:59.999999999Z", request.EndDate.Format(time.RFC3339Nano))
			return &conversations.ListConversationsResponse{Conversations: []convtypes.ConversationSummary{{ID: "history", Provider: "anthropic", CWD: "/runner/project"}}}, nil
		},
	}}
	response := httptest.NewRecorder()
	server.handleListConversations(response, httptest.NewRequest(http.MethodGet, "/api/conversations?format=raw&provider=anthropic&cwd=~/runner-only&sortBy=messages&sortOrder=asc&startDate=2026-09-05&endDate=2026-09-05&limit=2&offset=3", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"provider":"anthropic"`)
	assert.Contains(t, response.Body.String(), `"cwd":"/runner/project"`)
	assert.Equal(t, 1, calls)

	for _, query := range []string{"limit=no", "limit=-1", "offset=-1", "offset=bad", "sortBy=unknown", "sortOrder=other", "startDate=bad", "endDate=bad", "startDate=2026-09-06&endDate=2026-09-05"} {
		t.Run(query, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.handleListConversations(response, httptest.NewRequest(http.MethodGet, "/api/conversations?"+query, nil))
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, 1, calls, "invalid filters must not silently query different history")
		})
	}
}

func TestDaemonRawConversationRetainsExportFields(t *testing.T) {
	created := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	server := &Server{conversationService: &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
		if id == "missing" {
			return nil, convtypes.ErrConversationNotFound
		}
		return &conversations.GetConversationResponse{
			ID: id, CWD: "/runner/not-on-daemon", CreatedAt: created, UpdatedAt: created,
			Provider: "anthropic", RawMessages: json.RawMessage(`[{"role":"user","content":"hello"}]`),
			Summary: "original summary", Usage: llmtypes.Usage{InputTokens: 42, InputCost: 0.123},
			Metadata: map[string]any{"custom": "preserved"},
		}, nil
	}}}
	request := httptest.NewRequest(http.MethodGet, "/api/conversations/history?format=raw", nil)
	request = mux.SetURLVars(request, map[string]string{"id": "history"})
	response := httptest.NewRecorder()
	server.handleGetConversation(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var record convtypes.ConversationRecord
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &record))
	assert.Equal(t, "history", record.ID)
	assert.Equal(t, "/runner/not-on-daemon", record.CWD)
	assert.Equal(t, "anthropic", record.Provider)
	assert.Equal(t, "original summary", record.Summary)
	assert.Equal(t, created, record.CreatedAt)
	assert.Equal(t, created, record.UpdatedAt)
	assert.Equal(t, 42, record.Usage.InputTokens)
	assert.Equal(t, 0.123, record.Usage.InputCost)
	assert.Equal(t, "preserved", record.Metadata["custom"])
	assert.JSONEq(t, `[{"role":"user","content":"hello"}]`, string(record.RawMessages))
	response = httptest.NewRecorder()
	server.handleGetConversation(response, mux.SetURLVars(request, map[string]string{"id": "missing"}))
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestServer_handleGetConversationStreamFormatReturnsAuthoritativeEntries(t *testing.T) {
	conversationCWD := t.TempDir()
	expandedPrompt := "Full rendered recipe prompt"
	metadata := conversations.AddSlashCommandDisplay(map[string]any{
		"platform": "openai",
		"api_mode": "responses",
	}, expandedPrompt, "/review target=main", "review")
	server := &Server{
		conversationService: &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{
				ID:          "conversation-1",
				CWD:         conversationCWD,
				Provider:    "openai",
				Summary:     "Review",
				Metadata:    metadata,
				RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"Full rendered recipe prompt"}]`),
			}, nil
		}},
	}
	request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/conversations/conversation-1?format=stream", nil), map[string]string{"id": "conversation-1"})
	recorder := httptest.NewRecorder()

	server.handleGetConversation(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response conversationHistoryResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, conversationCWD, response.CWD)
	require.Len(t, response.Entries, 1)
	assert.Equal(t, "/review target=main", response.Entries[0].Content)
}

func TestServer_handleGetConversationStreamFormatSupportsLegacyResponsesProvider(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{
				ID:          "conversation-1",
				Provider:    "openai-responses",
				RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`),
			}, nil
		}},
	}
	request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/conversations/conversation-1?format=stream", nil), map[string]string{"id": "conversation-1"})
	recorder := httptest.NewRecorder()

	server.handleGetConversation(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response conversationHistoryResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Entries, 1)
	assert.Equal(t, "hello", response.Entries[0].Content)
}

func TestServer_handleGetChatSettings_NeverUsesLocalCWD(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	tmpDir := filepath.Join(homeDir, "workspace", "kodelet")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	server := &Server{
		config: &ServerConfig{CWD: tmpDir},
	}

	req := httptest.NewRequest("GET", "/api/chat/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ChatSettingsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Empty(t, response.DefaultCWD)
	assert.Empty(t, response.DefaultRunnerHostID)
	assert.False(t, response.DefaultRunnerReady)
	assert.NotContains(t, w.Body.String(), `"controlPlaneWorkspaceEnabled"`)
}

func TestServer_ControlPlaneWorkspaceEndpointsDisabled(t *testing.T) {
	originalSettings := viper.AllSettings()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
	viper.Set("extensions.enabled", true)
	viper.Set("extensions.local_dir", ".kodelet/extensions")
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	workspace := t.TempDir()
	t.Chdir(workspace)
	t.Setenv("HOME", workspace)
	marker := filepath.Join(workspace, "local-process-started")
	script := []byte(fmt.Sprintf("#!/bin/sh\nprintf ran > %q\nexit 1\n", marker))
	bin := filepath.Join(workspace, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), script, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "shell"), script, 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", filepath.Join(bin, "shell"))
	extensionsDir := filepath.Join(workspace, ".kodelet", "extensions")
	require.NoError(t, os.MkdirAll(extensionsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(extensionsDir, "kodelet-extension-local"), script, 0o700))
	recipesDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "local-secret.md"), []byte("Local recipe must not be discovered"), 0o600))

	server, err := NewServer(t.Context(), &ServerConfig{Host: "localhost", CompactRatio: 0.8}, nil)
	require.NoError(t, err)
	defer func() { assert.NoError(t, server.Close()) }()
	assert.FileExists(t, filepath.Join(extensionsDir, "kodelet-extension-local"))
	tests := []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{name: "slash commands", path: "/api/chat/slash-commands", handler: server.handleGetSlashCommands},
		{name: "cwd suggestions", path: "/api/chat/cwd-suggestions", handler: server.handleGetCWDHints},
		{name: "git diff", path: "/api/git/diff", handler: server.handleGetGitDiff},
		{name: "terminal", path: "/api/terminal/ws", handler: server.handleTerminalWebsocket},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path+"?cwd="+url.QueryEscape(workspace)+"&q=local", nil)
			w := httptest.NewRecorder()

			tt.handler(w, req)

			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Contains(t, w.Body.String(), "default runner is unavailable")
			assert.NotContains(t, w.Body.String(), "local-secret")
			assert.NoFileExists(t, marker, "no daemon-local git, PTY or extension process")
		})
	}
}

func TestDefaultRunnerWorkspaceDiscoveryAndSettings(t *testing.T) {
	config := embeddedRunnerTestConfig(t)
	workspace := config.EmbeddedRunner.Workspace
	t.Setenv("HOME", workspace) // Even coincident daemon HOME must not abbreviate runner paths.
	shell := filepath.Join(t.TempDir(), "runner-shell")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'runner-terminal:%s\\n' \"$PWD\"\nwhile IFS= read -r line; do printf '%s\\n' \"$line\"; done\n"), 0o700))
	t.Setenv("SHELL", shell)
	recipes := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipes, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipes, "runner-only.md"), []byte("---\ndescription: Runner recipe\n---\nRunner-owned prompt\n"), 0o600))
	child := filepath.Join(workspace, "runner-child")
	require.NoError(t, os.MkdirAll(child, 0o755))
	server, endpoint, stop := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return server.EmbeddedRunnerStatus().Ready }, 5*time.Second, 10*time.Millisecond)
	id := server.EmbeddedRunnerStatus().RunnerID
	runner, ok := server.runnerRegistry.Runner(id)
	require.True(t, ok)

	settings := httptest.NewRecorder()
	server.handleGetChatSettings(settings, httptest.NewRequest(http.MethodGet, "/api/chat/settings", nil))
	require.Equal(t, http.StatusOK, settings.Code, settings.Body.String())
	var response ChatSettingsResponse
	require.NoError(t, json.Unmarshal(settings.Body.Bytes(), &response))
	assert.Equal(t, id, response.DefaultRunnerID)
	assert.True(t, response.DefaultRunnerReady)
	assert.NotEmpty(t, response.DefaultRunnerHostID)
	assert.Equal(t, runner.Host.InstanceID, response.DefaultRunnerHostID)
	assert.Equal(t, runner.Workspace.Path, response.DefaultCWD)
	assert.NotContains(t, settings.Body.String(), `"controlPlaneWorkspaceEnabled"`)
	assert.Contains(t, settings.Body.String(), `"defaultRunnerHostId"`)

	commands := httptest.NewRecorder()
	server.handleGetSlashCommands(commands, httptest.NewRequest(http.MethodGet, "/api/chat/slash-commands", nil))
	require.Equal(t, http.StatusOK, commands.Code, commands.Body.String())
	var discovery protocol.WorkspaceDiscoverResult
	require.NoError(t, json.Unmarshal(commands.Body.Bytes(), &discovery))
	assert.Equal(t, runner.Workspace.Path, discovery.CWD)
	assert.NotEmpty(t, discovery.Digest)
	assert.Contains(t, commands.Body.String(), "runner-only")

	hints := httptest.NewRecorder()
	server.handleGetCWDHints(hints, httptest.NewRequest(http.MethodGet, "/api/chat/cwd-suggestions?q=runner-child", nil))
	require.Equal(t, http.StatusOK, hints.Code, hints.Body.String())
	var directories protocol.WorkspaceCWDHintsResult
	require.NoError(t, json.Unmarshal(hints.Body.Bytes(), &directories))
	assert.Contains(t, directories.Hints, protocol.DirectoryHint{Path: filepath.Join(runner.Workspace.Path, "runner-child")})

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(endpoint, "http")+"/api/terminal/ws", http.Header{"Authorization": {"Bearer web-secret"}})
	require.NoError(t, err)
	defer conn.Close()
	ready := readTerminalReady(t, conn)
	assert.Equal(t, runner.Workspace.Path, ready.CWD)
	physicalWorkspace, err := filepath.EvalSymlinks(runner.Workspace.Path)
	require.NoError(t, err)
	requireTerminalBinaryContains(t, conn, "runner-terminal:"+physicalWorkspace)
	require.NoError(t, conn.Close())

	// Stopped defaults remain identified but cannot authorize workspace calls or
	// advertise a filesystem identity, and are never replaced by a local service.
	stop()
	settings = httptest.NewRecorder()
	server.handleGetChatSettings(settings, httptest.NewRequest(http.MethodGet, "/api/chat/settings", nil))
	response = ChatSettingsResponse{}
	require.NoError(t, json.Unmarshal(settings.Body.Bytes(), &response))
	assert.Equal(t, id, response.DefaultRunnerID)
	assert.False(t, response.DefaultRunnerReady)
	assert.Empty(t, response.DefaultRunnerHostID)
	assert.Empty(t, response.DefaultCWD)
	commands = httptest.NewRecorder()
	server.handleGetSlashCommands(commands, httptest.NewRequest(http.MethodGet, "/api/chat/slash-commands", nil))
	assert.Equal(t, http.StatusServiceUnavailable, commands.Code)
}

func TestDefaultRunnerWorkspaceNeverFallsBackToAnotherRunner(t *testing.T) {
	server := newRunnerTestServer(t, "")
	server.config.EmbeddedRunner = &EmbeddedRunnerConfig{}
	var calls []string
	var registrations []protocol.RegisterResult
	for _, name := range []string{"embedded", "external"} {
		link := newRunnerAPITestLink()
		link.call = func(_ context.Context, method string, params, result any) error {
			calls = append(calls, name)
			assert.Equal(t, protocol.MethodWorkspaceGitDiff, method)
			assert.Equal(t, protocol.WorkspaceGitDiffParams{}, params)
			*result.(*protocol.WorkspaceGitDiffResult) = protocol.WorkspaceGitDiffResult{CWD: "/runner/" + name}
			return nil
		}
		registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
			ProtocolVersions: []int{protocol.Version},
			Host:             protocol.Host{InstanceID: name, Hostname: "same-hostname", OS: "linux", Arch: "amd64"},
			Workspace:        protocol.Workspace{Path: "/runner/" + name, Name: name},
			Capabilities:     protocol.RunnerCapabilities{WorkspaceGitDiff: true},
		}, link)
		require.NoError(t, err)
		require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
		registrations = append(registrations, registration)
	}
	server.embeddedStatus.RunnerID = registrations[0].RunnerID
	request := func(query string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleGetGitDiff(response, httptest.NewRequest(http.MethodGet, "/api/git/diff"+query, nil))
		assert.Equal(t, status, response.Code, response.Body.String())
	}
	request("", http.StatusOK)
	registration := registrations[0]
	server.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, errors.New("offline"))
	request("", http.StatusServiceUnavailable)
	request("?runnerId="+registrations[1].RunnerID, http.StatusOK)
	request("?runnerId=unknown", http.StatusNotFound)
	request("?conversationId=unbound", http.StatusBadRequest)
	request("?runnerId="+registrations[1].RunnerID+"&cwd=/daemon/local", http.StatusBadRequest)
	assert.Equal(t, []string{"embedded", "external"}, calls)
}

func TestServer_handleGetConversationOpenAIChatCompletionsSkipsSystemAndPreservesThinking(t *testing.T) {
	conversationID := "test-openai-chat-completions"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					Metadata: map[string]any{"api_mode": "chat_completions"},
					RawMessages: json.RawMessage(`[
						{"role":"system","content":"SECRET SYSTEM PROMPT"},
						{"role":"user","content":"hello"},
						{"role":"assistant","content":"Hi there!","reasoning_content":"\ninternal reasoning"}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 2)

	assert.Equal(t, "user", response.Messages[0].Role)
	assert.Equal(t, "hello", response.Messages[0].Content)
	assert.Equal(t, "assistant", response.Messages[1].Role)
	assert.Equal(t, "Hi there!", response.Messages[1].Content)
	assert.Equal(t, "internal reasoning", response.Messages[1].ThinkingText)
}

func TestServer_handleGetConversationIncludesPendingSteer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", homeDir)
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	conversationID := "test-pending-steer"
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	_, err = steerStore.Enqueue(context.Background(), conversationID, "Use this screenshot", []string{"data:image/png;base64,aGVsbG8="})
	require.NoError(t, err)

	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:          conversationID,
					Provider:    "openai",
					RawMessages: json.RawMessage(`[{"role":"user","content":"hello"}]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}
	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response WebConversationResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.PendingSteer, 1)
	assert.Equal(t, "user", response.PendingSteer[0].Role)

	contentBytes, err := json.Marshal(response.PendingSteer[0].Content)
	require.NoError(t, err)
	assert.Contains(t, string(contentBytes), `"text":"Use this screenshot"`)
	assert.Contains(t, string(contentBytes), `"media_type":"image/png"`)
}

func TestServer_handleGetPendingSteer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", homeDir)
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	conversationID := "test-get-pending-steer"
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	_, err = steerStore.Enqueue(context.Background(), conversationID, "Queued guidance", nil)
	require.NoError(t, err)

	server := &Server{router: mux.NewRouter()}
	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID+"/steer", nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetPendingSteer(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response []WebMessage
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response, 1)
	assert.Equal(t, "Queued guidance", response[0].Content)
}

// Regression test: when the config is supplied only through KODELET_CONFIG_FILE
// (as in the containerised control plane, where $HOME/.kodelet is an empty
// volume), the web UI profile picker used to list nothing but "default".
func TestGetWebUIProfileOptionsHonoursConfigFileEnv(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	t.Setenv("HOME", t.TempDir())
	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	repoDir := t.TempDir()
	require.NoError(t, os.Chdir(repoDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldCWD)) })
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, llm.RepoConfigFile), []byte("profiles:\n  repo-only:\n    provider: anthropic\n"), 0o644))

	overridePath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte("profile: deep\nprofiles:\n  flair:\n    provider: anthropic\n  deep:\n    provider: openai\n"), 0o644))
	t.Setenv(llm.ConfigFileEnv, overridePath)
	t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)

	viper.Reset()
	viper.Set("profile", "deep")

	options := getWebUIProfileOptions()

	require.Len(t, options, 3)
	assert.Equal(t, ChatProfileOption{Name: "default", Scope: webUIBuiltInProfileScope}, options[0])
	assert.Equal(t, ChatProfileOption{Name: "deep", Scope: webUIOverrideProfileScope, Active: true}, options[1])
	assert.Equal(t, ChatProfileOption{Name: "flair", Scope: webUIOverrideProfileScope}, options[2])
}

func TestGetWebUIProfileOptionsPreservesOverridePrecedence(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".kodelet", "config.yaml"), []byte("profiles:\n  global-only:\n    provider: anthropic\n  shared:\n    provider: anthropic\n"), 0o644))

	repoDir := t.TempDir()
	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldCWD)) })
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, llm.RepoConfigFile), []byte("profiles:\n  repo-only:\n    provider: openai\n  shared:\n    provider: openai\n"), 0o644))

	overridePath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte("profiles:\n  override-only:\n    provider: openai\n  shared:\n    provider: openai\n"), 0o644))
	t.Setenv(llm.ConfigFileEnv, overridePath)
	t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)

	viper.Reset()
	viper.Set("profile", "shared")

	options := getWebUIProfileOptions()

	require.Len(t, options, 5)
	assert.Equal(t, ChatProfileOption{Name: "default", Scope: webUIBuiltInProfileScope}, options[0])
	assert.Equal(t, ChatProfileOption{Name: "global-only", Scope: webUIGlobalProfileScope}, options[1])
	assert.Equal(t, ChatProfileOption{Name: "override-only", Scope: webUIOverrideProfileScope}, options[2])
	assert.Equal(t, ChatProfileOption{Name: "repo-only", Scope: webUIRepoProfileScope}, options[3])
	assert.Equal(t, ChatProfileOption{Name: "shared", Scope: webUIOverrideProfileScope, Active: true}, options[4])
}

func TestServer_handleGetChatSettings(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("profile", "work")
	viper.Set("provider", "openai")
	viper.Set("reasoning_effort", "medium")
	viper.Set("allowed_reasoning_efforts", []string{"medium"})
	viper.Set("profiles", map[string]any{
		"work": map[string]any{
			"reasoning_effort":          "high",
			"allowed_reasoning_efforts": []string{"low", "high"},
		},
		"anthropic": map[string]any{
			"provider":                  "anthropic",
			"reasoning_effort":          "max",
			"allowed_reasoning_efforts": []string{"medium", "max"},
		},
	})

	server := &Server{router: mux.NewRouter()}
	req := httptest.NewRequest("GET", "/api/chat/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ChatSettingsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "work", response.CurrentProfile)
	assert.Equal(t, "high", response.ReasoningEffort)
	assert.Equal(t, []string{"low", "high"}, response.ReasoningEffortOptions)
	require.NotEmpty(t, response.Profiles)
	assert.Equal(t, "default", response.Profiles[0].Name)

	req = httptest.NewRequest("GET", "/api/chat/settings?profile=anthropic", nil)
	w = httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", response.CurrentProfile)
	assert.Equal(t, "max", response.ReasoningEffort)
	assert.Equal(t, []string{"medium", "max"}, response.ReasoningEffortOptions)
}

func TestServer_handleGetChatSettingsUsesProviderReasoningEffortsWithoutAllowlist(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("reasoning_effort", "medium")

	server := &Server{router: mux.NewRouter()}
	req := httptest.NewRequest("GET", "/api/chat/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetChatSettings(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response ChatSettingsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "medium", response.ReasoningEffort)
	assert.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, response.ReasoningEffortOptions)
}

func TestServer_handleGetConversationPreservesImageContent(t *testing.T) {
	conversationID := "test-image-conv"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					RawMessages: json.RawMessage(`[
						{
							"role":"user",
							"content":[
								{"type":"text","text":"what is in the image?"},
								{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}
							]
						}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)

	contentBytes, err := json.Marshal(response.Messages[0].Content)
	require.NoError(t, err)
	assert.Contains(t, string(contentBytes), `"type":"image"`)
	assert.Contains(t, string(contentBytes), `"media_type":"image/png"`)
	assert.Contains(t, string(contentBytes), `"data":"aGVsbG8="`)
}

func TestServer_handleGetConversationLegacyOpenAIResponses(t *testing.T) {
	conversationID := "test-id-legacy"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:          conversationID,
					Summary:     "Legacy Test conversation",
					Provider:    "openai-responses",
					Metadata:    map[string]any{},
					RawMessages: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, conversationID, response.ID)
	assert.Equal(t, "OpenAI", response.Provider)
}

func TestServer_handleGetConversationOpenAIResponsesPreservesImageContent(t *testing.T) {
	conversationID := "test-openai-responses-image"
	mockService := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == conversationID {
				return &conversations.GetConversationResponse{
					ID:       conversationID,
					Provider: "openai",
					Metadata: map[string]any{"api_mode": "responses"},
					RawMessages: json.RawMessage(`[
						{
							"type":"message",
							"role":"user",
							"content":"what is in the image?",
							"raw_item":{
								"role":"user",
								"content":[
									{"type":"input_image","image_url":"data:image/png;base64,aGVsbG8="},
									{"type":"input_text","text":"what is in the image?"}
								]
							}
						}
					]`),
				}, nil
			}
			return nil, errors.New("conversation not found")
		},
	}

	server := &Server{conversationService: mockService, router: mux.NewRouter()}

	req := httptest.NewRequest("GET", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleGetConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response WebConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)

	contentBytes, err := json.Marshal(response.Messages[0].Content)
	require.NoError(t, err)
	assert.Contains(t, string(contentBytes), `"type":"image"`)
	assert.Contains(t, string(contentBytes), `"media_type":"image/png"`)
	assert.Contains(t, string(contentBytes), `"data":"aGVsbG8="`)
	assert.Contains(t, string(contentBytes), `"text":"what is in the image?"`)
}

func TestServer_handleDeleteConversation(t *testing.T) {
	conversationID := "test-id-123"
	deleteCalled := false

	mockService := &mockConversationService{
		deleteFunc: func(_ context.Context, id string) error {
			deleteCalled = true
			assert.Equal(t, conversationID, id)
			return nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("DELETE", "/api/conversations/"+conversationID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleDeleteConversation(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, deleteCalled)
}

func TestServer_handleForkConversation(t *testing.T) {
	conversationID := "test-id-123"
	mockService := &mockConversationService{
		forkFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			assert.Equal(t, conversationID, id)
			return &conversations.GetConversationResponse{ID: "forked-456"}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/"+conversationID+"/fork", nil)
	req = mux.SetURLVars(req, map[string]string{"id": conversationID})
	w := httptest.NewRecorder()

	server.handleForkConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response forkConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "forked-456", response.ConversationID)
}

func TestServer_handleForkConversationReturnsNotFound(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{
			forkFunc: func(_ context.Context, _ string) (*conversations.GetConversationResponse, error) {
				return nil, errors.Wrap(convtypes.ErrConversationNotFound, "failed to load conversation")
			},
		},
		router: mux.NewRouter(),
	}

	req := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/conversations/missing/fork", nil),
		map[string]string{"id": "missing"},
	)
	w := httptest.NewRecorder()

	server.handleForkConversation(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServer_handleChat(t *testing.T) {
	var capturedRequest ChatRequest
	requestCtx, requestCancel := context.WithCancel(context.Background())
	defer requestCancel()
	runnerStarted := make(chan struct{})
	allowFinish := make(chan struct{})

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-123",
					Role:           "assistant",
				})
				require.NoError(t, err)
				close(runnerStarted)
				<-allowFinish
				require.NoError(t, ctx.Err())
				return "conv-123", nil
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	req = req.WithContext(requestCtx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleChat(w, req)
		close(done)
	}()

	<-runnerStarted
	requestCancel()
	close(allowFinish)
	<-done

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-ndjson", w.Header().Get("Content-Type"))
	assert.Equal(t, "hello", capturedRequest.Message)
	assert.NotEmpty(t, capturedRequest.ConversationID)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)
	assert.Equal(t, "conv-123", firstEvent.ConversationID)

	var doneEvent ChatEvent
	err = json.Unmarshal([]byte(lines[1]), &doneEvent)
	require.NoError(t, err)
	assert.Equal(t, "done", doneEvent.Kind)
	assert.Equal(t, "conv-123", doneEvent.ConversationID)
}

func TestServer_handleChatContinuesAfterInitiatingStreamDisconnect(t *testing.T) {
	const conversationID = "conv-123"
	subscriber := newSubscriberEventSink()
	t.Cleanup(subscriber.Close)

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatSubscribers: map[string]map[*subscriberEventSink]struct{}{
			conversationID: {subscriber: {}},
		},
		chatRunner: &mockChatRunner{
			runFunc: func(ctx context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
				require.NoError(t, sink.Send(ChatEvent{Kind: "text-delta", ConversationID: conversationID, Role: "assistant", Delta: "first"}))
				require.NoError(t, sink.Send(ChatEvent{Kind: "text-delta", ConversationID: conversationID, Role: "assistant", Delta: "second"}))
				require.NoError(t, ctx.Err())
				return conversationID, nil
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hello","conversationId":"conv-123"}`))
	w := newDisconnectedResponseWriter()

	server.handleChat(w, req)

	assert.False(t, server.isActiveChat(conversationID))
	assert.Equal(t, 2, w.writes)
	events := make([]ChatEvent, 0, 5)
	for range 5 {
		select {
		case event := <-subscriber.ch:
			events = append(events, event)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for broadcast chat event")
		}
	}
	assert.Equal(t, []string{"conversation", "user-message", "text-delta", "text-delta", "done"}, []string{
		events[0].Kind,
		events[1].Kind,
		events[2].Kind,
		events[3].Kind,
		events[4].Kind,
	})
	assert.Equal(t, "first", events[2].Delta)
	assert.Equal(t, "second", events[3].Delta)
}

func TestServer_handleChatMarksCancelledCompletion(t *testing.T) {
	const conversationID = "conv-123"
	runnerStarted := make(chan struct{})
	subscriber := newSubscriberEventSink()
	t.Cleanup(subscriber.Close)

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatSubscribers: map[string]map[*subscriberEventSink]struct{}{
			conversationID: {subscriber: {}},
		},
		chatRunner: &mockChatRunner{
			runFunc: func(ctx context.Context, _ ChatRequest, _ ChatEventSink) (string, error) {
				close(runnerStarted)
				<-ctx.Done()
				return conversationID, ctx.Err()
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hello","conversationId":"conv-123"}`))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleChat(w, req)
		close(done)
	}()

	<-runnerStarted
	require.True(t, server.cancelActiveChat(conversationID))
	<-done

	var initiatingCompletion ChatEvent
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(w.Body.String())), &initiatingCompletion))
	assert.Equal(t, "done", initiatingCompletion.Kind)
	assert.True(t, initiatingCompletion.Cancelled)

	events := make([]ChatEvent, 0, 3)
	for range 3 {
		select {
		case event := <-subscriber.ch:
			events = append(events, event)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for cancelled completion event")
		}
	}
	assert.Equal(t, []string{"conversation", "user-message", "done"}, []string{events[0].Kind, events[1].Kind, events[2].Kind})
	assert.True(t, events[2].Cancelled)
}

func TestServer_handleChatWithImageContent(t *testing.T) {
	var capturedRequest ChatRequest

	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{Kind: "conversation", ConversationID: "conv-img", Role: "assistant"})
				require.NoError(t, err)
				return "conv-img", nil
			},
		},
		router: mux.NewRouter(),
	}

	reqBody := `{"message":"describe this image","content":[{"type":"text","text":"describe this image"},{"type":"image","source":{"data":"aGVsbG8=","media_type":"image/png"}}]}`
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, capturedRequest.Content, 2)
	assert.Equal(t, "image", capturedRequest.Content[1].Type)
	assert.Equal(t, "image/png", capturedRequest.Content[1].Source.MediaType)
}

func TestServer_handleChatWithProfile(t *testing.T) {
	var capturedRequest ChatRequest

	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				capturedRequest = req
				err := sink.Send(ChatEvent{Kind: "conversation", ConversationID: "conv-profile", Role: "assistant"})
				require.NoError(t, err)
				return "conv-profile", nil
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello","profile":"anthropic","reasoningEffort":"high"}`))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "anthropic", capturedRequest.Profile)
	assert.Equal(t, "high", capturedRequest.ReasoningEffort)
}

func TestServer_handleChatRunnerError(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-err",
					Role:           "assistant",
				})
				require.NoError(t, err)
				return "conv-err", errors.New("boom")
			},
		},
		router: mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()

	server.handleChat(w, req)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var errorEvent ChatEvent
	err := json.Unmarshal([]byte(lines[1]), &errorEvent)
	require.NoError(t, err)
	assert.Equal(t, "error", errorEvent.Kind)
	assert.Equal(t, "conv-err", errorEvent.ConversationID)
	assert.Equal(t, "boom", errorEvent.Error)
}

func TestServer_handleChatRejectsConcurrentRunForConversation(t *testing.T) {
	runnerStarted := make(chan struct{})
	allowFinish := make(chan struct{})
	done := make(chan struct{})

	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              context.Background(),
		activeChats:         make(map[string]*activeChatRun),
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
				close(runnerStarted)
				<-allowFinish
				return req.ConversationID, nil
			},
		},
		router: mux.NewRouter(),
	}

	firstReq := httptest.NewRequest(
		"POST",
		"/api/chat",
		strings.NewReader(`{"message":"hello","conversationId":"conv-123"}`),
	)
	firstW := httptest.NewRecorder()
	go func() {
		server.handleChat(firstW, firstReq)
		close(done)
	}()

	<-runnerStarted

	secondReq := httptest.NewRequest(
		"POST",
		"/api/chat",
		strings.NewReader(`{"message":"again","conversationId":"conv-123"}`),
	)
	secondW := httptest.NewRecorder()
	server.handleChat(secondW, secondReq)

	assert.Equal(t, http.StatusConflict, secondW.Code)
	assert.Contains(t, secondW.Body.String(), "conversation already has an active run")

	close(allowFinish)
	<-done
}

func TestServer_handleStopConversation(t *testing.T) {
	var cancelled atomic.Bool

	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         make(map[string]*activeChatRun),
	}
	var run *activeChatRun
	run = newActiveChatRun(func() {
		cancelled.Store(true)
		run.markDone()
	})
	server.activeChats["conv-123"] = run

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/stop", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleStopConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, cancelled.Load())

	var response stopConversationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "conv-123", response.ConversationID)
	assert.True(t, response.Stopped)
	assert.False(t, server.isActiveChat("conv-123"))
	assert.True(t, server.activeChats["conv-123"].stopRequested)
}

func TestServer_handleStopConversationWaitsForActiveTurnCleanup(t *testing.T) {
	cancelled := make(chan struct{})
	server := &Server{activeChats: make(map[string]*activeChatRun)}
	run := newActiveChatRun(func() { close(cancelled) })
	run.turnID = "turn-1"
	server.activeChats["conv-123"] = run
	req := httptest.NewRequest("POST", "/api/conversations/conv-123/stop?turnId=turn-1", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()
	returned := make(chan struct{})

	go func() {
		server.handleStopConversation(w, req)
		close(returned)
	}()
	<-cancelled
	select {
	case <-returned:
		t.Fatal("stop response returned before active turn cleanup completed")
	default:
	}
	run.markDone()
	<-returned

	assert.Equal(t, http.StatusOK, w.Code)
	var response stopConversationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.True(t, response.Stopped)
}

func TestServer_handleStopConversationBlocksMatchingTurnBeforeRegistration(t *testing.T) {
	server := &Server{activeChats: make(map[string]*activeChatRun)}
	req := httptest.NewRequest("POST", "/api/conversations/conv-123/stop?turnId=turn-1", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleStopConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response stopConversationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.True(t, response.Stopped)
	run := newActiveChatRun(func() {})
	run.turnID = "turn-1"
	assert.False(t, server.registerActiveChat("conv-123", run))
	assert.False(t, server.registerActiveChat("conv-123", run), "a cancelled turn must remain blocked for the tombstone lifetime")
}

func TestServer_handleRespondUIInput(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)
	detach := broker.setOwner(t.Context(), "client-1", sink)
	defer detach()
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         make(map[string]*activeChatRun),
	}
	server.activeChats["conv-123"] = &activeChatRun{
		cancel:  func() {},
		done:    make(chan struct{}),
		uiInput: broker,
	}

	resultCh := make(chan extensions.UIInputResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Input(context.Background(), extensions.UIInputRequest{ID: "input-1", Title: "Choose"})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	require.Eventually(t, func() bool { return len(sink.Events()) == 1 }, time.Second, 10*time.Millisecond)
	requestID := sink.Events()[0].UIInput.ID

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/ui-input/"+requestID, strings.NewReader(`{"status":"submitted","value":"2"}`))
	req.Header.Set(chat.ClientIDHeader, "client-1")
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123", "requestId": requestID})
	w := httptest.NewRecorder()

	server.handleRespondUIInput(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result := <-resultCh:
		assert.Equal(t, extensions.UIInputStatusSubmitted, result.Status)
		assert.Equal(t, "2", result.Value)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ui input response")
	}
}

func TestServer_handleStreamConversation(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)

	server.broadcastChatEvent("conv-123", ChatEvent{Kind: "text-delta", ConversationID: "conv-123", Delta: "hi", Role: "assistant"})
	server.closeChatSubscribers("conv-123")
	<-done
	assert.Equal(t, "true", w.Header().Get(chat.ConversationStreamActiveHeader))

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)

	var secondEvent ChatEvent
	err = json.Unmarshal([]byte(lines[1]), &secondEvent)
	require.NoError(t, err)
	assert.Equal(t, "text-delta", secondEvent.Kind)
	assert.Equal(t, "hi", secondEvent.Delta)
}

func TestServer_handleStreamConversationForwardsUsageEvents(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
		activeChats:         map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)

	server.broadcastChatEvent("conv-123", ChatEvent{
		Kind:           "usage",
		ConversationID: "conv-123",
		Role:           "assistant",
		Usage: &llmtypes.Usage{
			InputTokens:  120,
			OutputTokens: 45,
		},
	})
	server.closeChatSubscribers("conv-123")
	<-done

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var usageEvent ChatEvent
	err := json.Unmarshal([]byte(lines[1]), &usageEvent)
	require.NoError(t, err)
	assert.Equal(t, "usage", usageEvent.Kind)
	if assert.NotNil(t, usageEvent.Usage) {
		assert.Equal(t, 120, usageEvent.Usage.InputTokens)
		assert.Equal(t, 45, usageEvent.Usage.OutputTokens)
	}
}

func TestServer_handleStreamConversationSendsPersistentWidgetSnapshot(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		activeChats:         make(map[string]*activeChatRun),
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}
	server.extensionUI = newWebExtensionUIHost(server.emitExtensionUIEvent)
	source := testWebExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 1}}
	_, err := server.extensionUI.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
		ScopeID:   "conv-123",
		ID:        "background-agents",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame: extensions.UIFrame{
			Sequence: 1,
			Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "1 active"}}}},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)
	server.closeChatSubscribers("conv-123")
	<-done

	var event ChatEvent
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(w.Body.String())), &event))
	assert.Equal(t, "ui-widgets", event.Kind)
	assert.Regexp(t, `^\d+:1$`, event.UIWidgetRevision)
	require.Len(t, event.UIWidgets, 1)
	assert.Equal(t, "subagent", event.UIWidgets[0].ExtensionID)
	assert.Equal(t, uint64(1), event.UIWidgets[0].Frame.Sequence)
}

func TestServer_handleStreamConversationSendsEmptyPersistentWidgetSnapshot(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		activeChats:         make(map[string]*activeChatRun),
		chatSubscribers:     make(map[string]map[*subscriberEventSink]struct{}),
	}
	server.extensionUI = newWebExtensionUIHost(server.emitExtensionUIEvent)

	req := httptest.NewRequest("GET", "/api/conversations/conv-123/stream", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleStreamConversation(w, req)
		close(done)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conv-123"]) == 1
	}, time.Second, 10*time.Millisecond)
	server.closeChatSubscribers("conv-123")
	<-done

	var event ChatEvent
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(w.Body.String())), &event))
	assert.Equal(t, "ui-widgets", event.Kind)
	assert.Regexp(t, `^\d+:0$`, event.UIWidgetRevision)
	assert.Empty(t, event.UIWidgets)
}

func TestServer_handleStreamConversationFollowsFutureChatRun(t *testing.T) {
	const conversationID = "conv-123"
	server := &Server{
		conversationService: &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{ID: conversationID}, nil
		}},
		chatRunner: &mockChatRunner{runFunc: func(_ context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
			require.NoError(t, sink.Send(ChatEvent{
				Kind:           "text-delta",
				ConversationID: conversationID,
				Role:           "assistant",
				Delta:          "hello from the runner",
			}))
			return conversationID, nil
		}},
		activeChats:           make(map[string]*activeChatRun),
		pendingChatStops:      make(map[string]time.Time),
		deletingConversations: make(map[string]struct{}),
		chatSubscribers:       make(map[string]map[*subscriberEventSink]struct{}),
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversationID+"/stream", nil)
	streamRequest = mux.SetURLVars(streamRequest, map[string]string{"id": conversationID})
	streamRecorder := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		server.handleStreamConversation(streamRecorder, streamRequest)
		close(streamDone)
	}()

	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers[conversationID]) == 1
	}, time.Second, 10*time.Millisecond)

	chatRequest := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"sent from the tui","conversationId":"conv-123","runnerId":"runner-1"}`))
	chatRecorder := httptest.NewRecorder()
	server.handleChat(chatRecorder, chatRequest)
	assert.Equal(t, http.StatusOK, chatRecorder.Code)
	assert.False(t, server.isActiveChat(conversationID))

	server.chatSubscribersMu.Lock()
	assert.Len(t, server.chatSubscribers[conversationID], 1, "the idle watcher should survive turn completion")
	server.chatSubscribersMu.Unlock()

	server.closeChatSubscribers(conversationID)
	<-streamDone

	lines := strings.Split(strings.TrimSpace(streamRecorder.Body.String()), "\n")
	require.Len(t, lines, 4)
	events := make([]ChatEvent, 0, len(lines))
	for _, line := range lines {
		var event ChatEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		events = append(events, event)
	}

	assert.Equal(t, []string{"conversation", "user-message", "text-delta", "done"}, []string{
		events[0].Kind,
		events[1].Kind,
		events[2].Kind,
		events[3].Kind,
	})
	assert.Equal(t, "sent from the tui", events[1].Content)
	assert.Equal(t, "hello from the runner", events[2].Delta)
}

func TestServerMultipleConversationStreamsDetachAndObserveCancellation(t *testing.T) {
	const conversationID = "conv-123"
	firstEventSent := make(chan struct{})
	continueAfterDetach := make(chan struct{})
	afterDetachEventSent := make(chan struct{})
	runnerErrors := make(chan error, 1)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	server := &Server{
		conversationService: &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{ID: conversationID}, nil
		}},
		runCtx:          runCtx,
		activeChats:     make(map[string]*activeChatRun),
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
		chatRunner: &mockChatRunner{runFunc: func(ctx context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
			if err := sink.Send(ChatEvent{Kind: "text-delta", ConversationID: conversationID, Role: "assistant", Delta: "before detach"}); err != nil {
				runnerErrors <- err
				return conversationID, err
			}
			close(firstEventSent)
			select {
			case <-continueAfterDetach:
			case <-ctx.Done():
				return conversationID, ctx.Err()
			}
			if err := sink.Send(ChatEvent{Kind: "tool-use", ConversationID: conversationID, Role: "assistant", ToolCallID: "call-1", ToolName: "bash", Input: `{"cmd":"df -h"}`}); err != nil {
				runnerErrors <- err
				return conversationID, err
			}
			close(afterDetachEventSent)
			<-ctx.Done()
			return conversationID, ctx.Err()
		}},
	}
	waitForSignal := func(ch <-chan struct{}, description string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", description)
		}
	}

	startStream := func() (context.CancelFunc, *httptest.ResponseRecorder, <-chan struct{}) {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversationID+"/stream", nil).WithContext(ctx)
		req = mux.SetURLVars(req, map[string]string{"id": conversationID})
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			server.handleStreamConversation(recorder, req)
			close(done)
		}()
		return cancel, recorder, done
	}

	firstCancel, firstRecorder, firstDone := startStream()
	defer firstCancel()
	secondCancel, secondRecorder, secondDone := startStream()
	defer secondCancel()
	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers[conversationID]) == 2
	}, time.Second, 10*time.Millisecond)

	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"inspect disk","conversationId":"conv-123"}`))
	chatRecorder := httptest.NewRecorder()
	chatDone := make(chan struct{})
	go func() {
		server.handleChat(chatRecorder, chatReq)
		close(chatDone)
	}()
	waitForSignal(firstEventSent, "the first streamed event")

	firstCancel()
	waitForSignal(firstDone, "the first stream to detach")
	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers[conversationID]) == 1
	}, time.Second, 10*time.Millisecond)
	close(continueAfterDetach)
	waitForSignal(afterDetachEventSent, "the post-detach event")

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStop()
	stopReq := httptest.NewRequest(http.MethodPost, "/api/conversations/"+conversationID+"/stop", nil).WithContext(stopCtx)
	stopReq = mux.SetURLVars(stopReq, map[string]string{"id": conversationID})
	stopRecorder := httptest.NewRecorder()
	server.handleStopConversation(stopRecorder, stopReq)
	require.Equal(t, http.StatusOK, stopRecorder.Code)
	waitForSignal(chatDone, "the cancelled chat request")
	server.closeChatSubscribers(conversationID)
	waitForSignal(secondDone, "the second stream to close")
	select {
	case err := <-runnerErrors:
		require.NoError(t, err)
	default:
	}

	decodeEvents := func(body string) []ChatEvent {
		lines := strings.Split(strings.TrimSpace(body), "\n")
		events := make([]ChatEvent, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var event ChatEvent
			require.NoError(t, json.Unmarshal([]byte(line), &event))
			events = append(events, event)
		}
		return events
	}
	firstEvents := decodeEvents(firstRecorder.Body.String())
	for _, event := range firstEvents {
		assert.NotEqual(t, "tool-use", event.Kind, "detached stream received later events")
	}
	secondEvents := decodeEvents(secondRecorder.Body.String())
	kinds := make([]string, 0, len(secondEvents))
	for _, event := range secondEvents {
		kinds = append(kinds, event.Kind)
	}
	require.Equal(t, []string{"conversation", "user-message", "text-delta", "tool-use", "done"}, kinds)
	assert.True(t, secondEvents[len(secondEvents)-1].Cancelled)
}

func TestServer_handleDeleteConversationRejectsActiveRun(t *testing.T) {
	deleteCalled := false

	server := &Server{
		conversationService: &mockConversationService{
			deleteFunc: func(_ context.Context, _ string) error {
				deleteCalled = true
				return nil
			},
		},
		activeChats:     map[string]*activeChatRun{"conv-123": newActiveChatRun(func() {})},
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
		router:          mux.NewRouter(),
	}

	deleteReq := httptest.NewRequest("DELETE", "/api/conversations/conv-123", nil)
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{"id": "conv-123"})
	deleteW := httptest.NewRecorder()

	server.handleDeleteConversation(deleteW, deleteReq)

	assert.Equal(t, http.StatusConflict, deleteW.Code)
	assert.Contains(t, deleteW.Body.String(), "conversation is actively running")
	assert.False(t, deleteCalled)
}

func TestServer_handleChatThroughMiddleware(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		chatRunner: &mockChatRunner{
			runFunc: func(_ context.Context, _ ChatRequest, sink ChatEventSink) (string, error) {
				err := sink.Send(ChatEvent{
					Kind:           "conversation",
					ConversationID: "conv-middleware",
					Role:           "assistant",
				})
				require.NoError(t, err)
				return "conv-middleware", nil
			},
		},
		router: mux.NewRouter(),
	}
	server.setupRoutes()

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	require.Len(t, lines, 2)

	var firstEvent ChatEvent
	err := json.Unmarshal([]byte(lines[0]), &firstEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation", firstEvent.Kind)
	assert.Equal(t, "conv-middleware", firstEvent.ConversationID)
}

func TestResponseWriter_HijackDelegates(t *testing.T) {
	recorder := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	rw := &responseWriter{ResponseWriter: recorder, statusCode: http.StatusOK}

	conn, _, err := rw.Hijack()
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
}

func TestResponseWriter_WriteHeaderAndHijackUnsupported(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: recorder, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, rw.statusCode)
	assert.Equal(t, http.StatusCreated, recorder.Code)

	conn, brw, err := rw.Hijack()
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Nil(t, brw)
	assert.Contains(t, err.Error(), "does not support hijacking")
}

func TestServer_handleGetToolResult(t *testing.T) {
	conversationID := "conv-123"
	toolCallID := "tool-456"

	mockService := &mockConversationService{
		getToolFunc: func(_ context.Context, convID, toolID string) (*conversations.GetToolResultResponse, error) {
			assert.Equal(t, conversationID, convID)
			assert.Equal(t, toolCallID, toolID)
			return &conversations.GetToolResultResponse{
				ToolCallID: toolCallID,
				Result: tools.StructuredToolResult{
					ToolName: "TestTool",
				},
			}, nil
		},
	}

	server := &Server{
		conversationService: mockService,
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/conversations/%s/tools/%s", conversationID, toolCallID), nil)
	req = mux.SetURLVars(req, map[string]string{
		"id":         conversationID,
		"toolCallId": toolCallID,
	})
	w := httptest.NewRecorder()

	server.handleGetToolResult(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response conversations.GetToolResultResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, toolCallID, response.ToolCallID)
	assert.Equal(t, "TestTool", response.Result.ToolName)
}

func TestServer_handleSteerConversation(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", homeDir)
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(`{"message":"Please focus on error handling"}`))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Success        bool   `json:"success"`
		ConversationID string `json:"conversation_id"`
		Queued         bool   `json:"queued"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.Equal(t, "conv-123", response.ConversationID)
	assert.True(t, response.Queued)

	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	pending, err := steerStore.Peek(context.Background(), "conv-123")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "Please focus on error handling", pending[0].Content)
	assert.Empty(t, pending[0].Images)
}

func TestServer_handleSteerConversationWithImageContent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", homeDir)
	require.NoError(t, db.RunMigrations(context.Background(), migrations.All()))
	originalHome := os.Getenv("HOME")
	require.NoError(t, os.Setenv("HOME", homeDir))
	defer func() {
		if originalHome == "" {
			os.Unsetenv("HOME")
			return
		}
		require.NoError(t, os.Setenv("HOME", originalHome))
	}()

	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	reqBody := `{"message":"Use this screenshot","content":[{"type":"text","text":"Use this screenshot"},{"type":"image","source":{"data":"aGVsbG8=","media_type":"image/png"}}]}`
	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(reqBody))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	pending, err := steerStore.Peek(context.Background(), "conv-123")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "Use this screenshot", pending[0].Content)
	assert.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, pending[0].Images)
}

func TestServer_handleSteerConversationRejectsImageOnlyContent(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	reqBody := `{"message":"","content":[{"type":"image","source":{"data":"aGVsbG8=","media_type":"image/png"}}]}`
	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(reqBody))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "message cannot be empty")
}

func TestServer_handleSteerConversationRequiresMessage(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	req := httptest.NewRequest("POST", "/api/conversations/conv-123/steer", strings.NewReader(`{"message":"   "}`))
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "message cannot be empty")
}

func TestServer_handleSteerConversationRejectsMessagesThatAreTooLong(t *testing.T) {
	server := &Server{
		conversationService: &mockConversationService{},
		router:              mux.NewRouter(),
	}

	message := strings.Repeat("a", steer.MaxMessageLength+1)
	req := httptest.NewRequest(
		"POST",
		"/api/conversations/conv-123/steer",
		strings.NewReader(fmt.Sprintf(`{"message":"%s"}`, message)),
	)
	req = mux.SetURLVars(req, map[string]string{"id": "conv-123"})
	w := httptest.NewRecorder()

	server.handleSteerConversation(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "message must be 10000 characters or fewer")
}

func TestTerminalOriginAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:3000")
	server := &Server{config: &ServerConfig{}}
	assert.True(t, server.terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "http://evil.com")
	assert.False(t, server.terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "https://app.example.com")
	server = &Server{config: &ServerConfig{CORSOrigins: []string{"https://app.example.com"}}}
	assert.True(t, server.terminalOriginAllowed(req))
}

func TestBoundedTerminalDimensions(t *testing.T) {
	assert.Equal(t, defaultTerminalRows, boundedTerminalRows(0))
	assert.Equal(t, maxTerminalRows, boundedTerminalRows(maxTerminalRows+50))
	assert.Equal(t, 40, boundedTerminalRows(40))

	assert.Equal(t, defaultTerminalCols, boundedTerminalCols(0))
	assert.Equal(t, maxTerminalCols, boundedTerminalCols(maxTerminalCols+50))
	assert.Equal(t, 80, boundedTerminalCols(80))
}

func readTerminalReady(t *testing.T, conn *websocket.Conn) terminalMessage {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		require.NoError(t, conn.SetReadDeadline(deadline))
		messageType, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		if messageType != websocket.TextMessage {
			continue
		}

		var message terminalMessage
		require.NoError(t, json.Unmarshal(payload, &message))
		require.NotEqual(t, "exit", message.Type, "terminal exited before it was ready")
		if message.Type == "ready" {
			return message
		}
	}
}

func requireTerminalBinaryContains(t *testing.T, conn *websocket.Conn, expected string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var output strings.Builder
	for {
		require.NoError(t, conn.SetReadDeadline(deadline))
		messageType, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		if messageType != websocket.BinaryMessage {
			continue
		}

		output.Write(payload)
		if strings.Contains(output.String(), expected) {
			return
		}
	}
}

func TestDisplayProviderName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected string
	}{
		{
			name:     "openai",
			provider: "openai",
			expected: "OpenAI",
		},
		{
			name:     "openai responses legacy",
			provider: "openai-responses",
			expected: "OpenAI",
		},
		{
			name:     "anthropic",
			provider: "anthropic",
			expected: "Anthropic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, displayProviderName(tt.provider))
		})
	}
}

func TestExtractProviderMetadata(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		metadata         map[string]any
		expectedPlatform string
		expectedAPIMode  string
	}{
		{
			name:             "openai metadata normalized",
			provider:         "openai",
			metadata:         map[string]any{"platform": "Fireworks", "api_mode": "responses"},
			expectedPlatform: "fireworks",
			expectedAPIMode:  "responses",
		},
		{
			name:             "legacy openai responses defaults to responses mode",
			provider:         "openai-responses",
			metadata:         map[string]any{},
			expectedPlatform: "",
			expectedAPIMode:  "responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, apiMode := extractProviderMetadata(tt.provider, tt.metadata)
			assert.Equal(t, tt.expectedPlatform, platform)
			assert.Equal(t, tt.expectedAPIMode, apiMode)
		})
	}
}

func TestServer_convertToWebMessages(t *testing.T) {
	server := &Server{}

	tests := []struct {
		name          string
		rawMessages   json.RawMessage
		provider      string
		toolResults   map[string]tools.StructuredToolResult
		expectedMsgs  int
		checkToolCall bool
		expectedTool  string
	}{
		{
			name:          "anthropic messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":[{"type":"text","text":"Let me help"},{"type":"tool_use","id":"tool-123","name":"TestTool","input":{"arg":"value"}}]}]`),
			provider:      "anthropic",
			expectedMsgs:  1,
			checkToolCall: true,
			expectedTool:  "TestTool",
		},
		{
			name:          "openai messages with tool calls",
			rawMessages:   json.RawMessage(`[{"role":"assistant","content":"Let me help","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			provider:      "openai",
			expectedMsgs:  1,
			checkToolCall: true,
			expectedTool:  "TestTool",
		},
		{
			name:         "simple text messages",
			rawMessages:  json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]},{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]}]`),
			provider:     "anthropic",
			expectedMsgs: 2,
		},
		{
			name:         "empty messages should be filtered out",
			rawMessages:  json.RawMessage(`[{"role":"user","content":[]},{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]},{"role":"user","content":[]}]`),
			provider:     "anthropic",
			expectedMsgs: 1,
		},
		{
			name:         "empty messages with tool calls should be preserved",
			rawMessages:  json.RawMessage(`[{"role":"user","content":""},{"role":"assistant","content":"","tool_calls":[{"id":"tool-123","function":{"name":"TestTool","arguments":"{\"arg\":\"value\"}"}}]}]`),
			provider:     "openai",
			expectedMsgs: 1,
		},
		{
			name:         "openai chat completions tool result messages are filtered out",
			rawMessages:  json.RawMessage(`[{"role":"assistant","content":"","tool_calls":[{"id":"tool-123","function":{"name":"bash","arguments":"{\"command\":\"nproc\"}"}}]},{"role":"tool","tool_call_id":"tool-123","content":"24\nMem: 31Gi"},{"role":"assistant","content":"This machine has 24 CPU cores."}]`),
			provider:     "openai",
			expectedMsgs: 2,
		},
		{
			name:          "openai responses messages with reasoning and tool calls",
			rawMessages:   json.RawMessage(`[{"type":"message","role":"user","content":"Hello"},{"type":"reasoning","role":"assistant","content":"Analyzing request"},{"type":"function_call","call_id":"tool-123","name":"TestTool","arguments":"{\"arg\":\"value\"}"},{"type":"function_call_output","call_id":"tool-123","output":"{\"ok\":true}"},{"type":"message","role":"assistant","content":"Done"}]`),
			provider:      "openai-responses",
			toolResults:   map[string]tools.StructuredToolResult{"tool-123": {ToolName: "TestTool"}},
			expectedMsgs:  4,
			checkToolCall: true,
			expectedTool:  "TestTool",
		},
		{
			name:         "openai responses preserves consecutive reasoning items",
			rawMessages:  json.RawMessage(`[{"type":"message","role":"user","content":"Hello"},{"type":"reasoning","role":"assistant","content":"First thought"},{"type":"reasoning","role":"assistant","content":"Second thought"},{"type":"reasoning","role":"assistant","content":"Third thought"},{"type":"message","role":"assistant","content":"Done"}]`),
			provider:     "openai-responses",
			expectedMsgs: 5,
		},
		{
			name:          "openai responses native web search tool calls",
			rawMessages:   json.RawMessage(`[{"type":"message","role":"user","content":"Look up the latest notes"},{"type":"web_search_call","call_id":"search-123","status":"completed","action":"search","content":"kodelet web ui search"}]`),
			provider:      "openai-responses",
			toolResults:   map[string]tools.StructuredToolResult{"search-123": {ToolName: "openai_web_search"}},
			expectedMsgs:  2,
			checkToolCall: true,
			expectedTool:  "openai_web_search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := server.convertToWebMessages(tt.rawMessages, tt.provider, nil, tt.toolResults)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMsgs, len(messages))

			if tt.checkToolCall && len(messages) > 0 {
				foundToolCall := false
				for _, msg := range messages {
					if len(msg.ToolCalls) > 0 {
						assert.Equal(t, tt.expectedTool, msg.ToolCalls[0].Function.Name)
						foundToolCall = true
						break
					}
				}
				assert.True(t, foundToolCall, "expected at least one message with tool calls")
			}

			if tt.name == "openai responses preserves consecutive reasoning items" {
				require.Len(t, messages, 5)
				assert.Equal(t, []string{"First thought"}, messages[1].ThinkingTexts)
				assert.Equal(t, []string{"Second thought"}, messages[2].ThinkingTexts)
				assert.Equal(t, []string{"Third thought"}, messages[3].ThinkingTexts)
			}
		})
	}
}

func TestServer_convertToWebMessagesAppliesMessageDisplay(t *testing.T) {
	server := &Server{}
	expandedPrompt := "Full rendered recipe prompt"
	metadata := conversations.AddSlashCommandDisplay(nil, expandedPrompt, "/init focus", "init")

	messages, err := server.convertToWebMessages(
		json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Full rendered recipe prompt"}]}]`),
		"anthropic",
		metadata,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	blocks, ok := messages[0].Content.([]WebContentBlock)
	require.True(t, ok)
	require.Len(t, blocks, 1)
	assert.Equal(t, "slash-command", blocks[0].Type)
	assert.Equal(t, "/init focus", blocks[0].Text)
	assert.Equal(t, "init", blocks[0].Command)

	goalPrompt := "<goal_context>\nContinue working toward the active thread goal.\n</goal_context>"
	metadata = conversations.AddMessageDisplay(nil, goalPrompt, "Objective: find cores", conversations.MessageDisplayKindGoal, "goal")
	messages, err = server.convertToWebMessages(
		json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"<goal_context>\nContinue working toward the active thread goal.\n</goal_context>"}]}]`),
		"anthropic",
		metadata,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	blocks, ok = messages[0].Content.([]WebContentBlock)
	require.True(t, ok)
	require.Len(t, blocks, 1)
	assert.Equal(t, "goal", blocks[0].Type)
	assert.Equal(t, "Objective: find cores", blocks[0].Text)
	assert.Equal(t, "goal", blocks[0].Command)

	plainPrompt := "Internal transcription prompt"
	metadata = conversations.AddMessageDisplay(nil, plainPrompt, "What should I make for breakfast?", "", "")
	messages, err = server.convertToWebMessages(
		json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Internal transcription prompt"}]}]`),
		"anthropic",
		metadata,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	blocks, ok = messages[0].Content.([]WebContentBlock)
	require.True(t, ok)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "What should I make for breakfast?", blocks[0].Text)
	assert.Empty(t, blocks[0].Command)
}

func TestServer_convertToWebMessagesHidesRepeatedGoalContext(t *testing.T) {
	server := &Server{}
	goalPrompt := "<goal_context>\nContinue working toward the active thread goal.\n</goal_context>"
	metadata := conversations.AddMessageDisplay(nil, goalPrompt, "Objective: find cores", conversations.MessageDisplayKindGoal, "goal")

	messages, err := server.convertToWebMessages(
		json.RawMessage(`[
			{"role":"user","content":[{"type":"text","text":"<goal_context>\nContinue working toward the active thread goal.\n</goal_context>"}]},
			{"role":"user","content":[{"type":"text","text":"<goal_context>\nContinue working toward the active thread goal.\n</goal_context>"}]}
		]`),
		"anthropic",
		metadata,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	blocks, ok := messages[0].Content.([]WebContentBlock)
	require.True(t, ok)
	require.Len(t, blocks, 1)
	assert.Equal(t, "goal", blocks[0].Type)
	assert.Equal(t, "Objective: find cores", blocks[0].Text)
}

func TestServer_Close(t *testing.T) {
	closeCalled := false
	mockService := &mockConversationService{
		closeFunc: func() error {
			closeCalled = true
			return nil
		},
	}

	server := &Server{
		conversationService: mockService,
	}

	err := server.Close()
	assert.NoError(t, err)
	assert.True(t, closeCalled)
}
