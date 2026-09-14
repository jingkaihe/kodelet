package controlplane

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/browser"
	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBrowserAPITestServer(t *testing.T) (*Server, protocol.RegisterResult, *runnerAPITestLink) {
	t.Helper()
	s := newRunnerTestServer(t, "")
	s.config.BrowserEnabled = true
	s.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return nil, convtypes.ErrConversationNotFound
	}}
	s.router = mux.NewRouter()
	s.setupRoutes()
	link := newRunnerAPITestLink()
	link.call = func(_ context.Context, method string, raw any, result any) error {
		if method == protocol.MethodWorkspaceBrowserOpen {
			params := raw.(protocol.WorkspaceBrowserParams)
			*result.(*browser.Info) = browser.Info{SessionID: "session-" + params.ConversationID, ConversationID: params.ConversationID, CWD: params.CWD, DevTools: true}
		}
		return nil
	}
	registration, err := s.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities:     protocol.RunnerCapabilities{WorkspaceBrowser: true, WorkspaceCWD: true},
		Host:             protocol.Host{InstanceID: "browser-test-host", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, s.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation,
		protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
	t.Cleanup(func() { s.closeBrowserHandles("", 0) })
	return s, registration, link
}

func openBrowserTestHandle(t *testing.T, s *Server, registration protocol.RegisterResult, owner, conversationID string) *browserHandle {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/browser/session?runnerId="+registration.RunnerID+"&conversationId="+conversationID, nil)
	r = r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal(owner)))
	w := httptest.NewRecorder()
	s.requireBrowser(s.handleBrowserOpen)(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body browserHandle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body.ID)
	assert.NotContains(t, w.Body.String(), "relayToken")
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	return s.browserHandles[body.ID]
}

func browserRequest(t *testing.T, method string, handle *browserHandle, owner string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "/api/browser/"+handle.ID, nil)
	r = mux.SetURLVars(r, map[string]string{"id": handle.ID})
	return r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal(owner)))
}

func TestBrowserHandlesOwnershipStopAndExpiry(t *testing.T) {
	s, registration, link := newBrowserAPITestServer(t)
	a := openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	assert.Same(t, a, openBrowserTestHandle(t, s, registration, "alice", "conversation-1"))
	b := openBrowserTestHandle(t, s, registration, "bob", "conversation-1")
	assert.NotEqual(t, a.ID, b.ID)
	other := openBrowserTestHandle(t, s, registration, "alice", "conversation-2")
	assert.Equal(t, a.CWD, other.CWD)
	assert.NotEqual(t, a.SessionID, other.SessionID)
	otherAttachment, err := s.newBrowserAttachment(t.Context(), other)
	require.NoError(t, err)
	t.Cleanup(func() { s.releaseBrowserAttachment(otherAttachment) })

	w := httptest.NewRecorder()
	assert.Nil(t, s.browserHandleForRequest(w, browserRequest(t, http.MethodGet, a, "bob")))
	assert.Equal(t, http.StatusNotFound, w.Code)

	attachment, err := s.newBrowserAttachment(t.Context(), a)
	require.NoError(t, err)
	s.expireBrowserHandle(a)
	assert.Contains(t, s.browserHandles, a.ID, "active attachments retain their handle")
	s.releaseBrowserAttachment(attachment)
	s.expireBrowserHandle(a)
	assert.NotContains(t, s.browserHandles, a.ID)

	a = openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	attachment, err = s.newBrowserAttachment(t.Context(), b)
	require.NoError(t, err)
	t.Cleanup(func() { s.releaseBrowserAttachment(attachment) })
	link.call = func(_ context.Context, method string, raw any, _ any) error {
		assert.Equal(t, protocol.MethodWorkspaceBrowserStop, method)
		assert.Equal(t, protocol.WorkspaceBrowserParams{ConversationID: "conversation-1", CWD: a.CWD, SessionID: a.SessionID}, raw)
		return nil
	}
	w = httptest.NewRecorder()
	s.requireBrowser(s.handleBrowserStop)(w, browserRequest(t, http.MethodDelete, a, "alice"))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, map[string]*browserHandle{other.ID: other}, s.browserHandles, "stop affects all viewers of this conversation, but not another conversation")
	assert.Len(t, s.browserTickets, 1)
	assert.Error(t, attachment.ctx.Err())
	assert.NoError(t, otherAttachment.ctx.Err())
}

func TestBrowserRequiresConversationScope(t *testing.T) {
	for _, test := range []struct {
		name, query string
		saved       bool
		storeErr    error
		want        int
	}{
		{name: "missing conversation", want: http.StatusBadRequest},
		{name: "invalid conversation", query: "&conversationId=..", want: http.StatusBadRequest},
		{name: "draft cannot override directory", query: "&conversationId=draft&cwd=/other", want: http.StatusBadRequest},
		{name: "saved conversation cannot bypass affinity", query: "&conversationId=saved", saved: true, want: http.StatusBadRequest},
		{name: "store failure is not a draft", query: "&conversationId=draft", storeErr: errors.New("store unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, registration, link := newBrowserAPITestServer(t)
			if test.saved || test.storeErr != nil {
				s.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
					return &conversations.GetConversationResponse{CWD: "/workspace"}, test.storeErr
				}}
			}
			link.call = func(context.Context, string, any, any) error {
				assert.Fail(t, "invalid scope must not reach the runner")
				return nil
			}
			r := httptest.NewRequest(http.MethodPost, "/api/browser/session?runnerId="+registration.RunnerID+test.query, nil)
			r = r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("alice")))
			w := httptest.NewRecorder()
			s.handleBrowserOpen(w, r)
			assert.Equal(t, test.want, w.Code, w.Body.String())
			assert.Empty(t, s.browserHandles)
		})
	}
	for _, returnedID := range []string{"", "another-conversation"} {
		t.Run("runner returned scope "+returnedID, func(t *testing.T) {
			s, registration, link := newBrowserAPITestServer(t)
			link.call = func(_ context.Context, _ string, _ any, result any) error {
				*result.(*browser.Info) = browser.Info{SessionID: "session", ConversationID: returnedID, CWD: "/workspace"}
				return nil
			}
			r := httptest.NewRequest(http.MethodPost, "/api/browser/session?runnerId="+registration.RunnerID+"&conversationId=draft", nil)
			r = r.WithContext(contextWithPrincipal(r.Context(), administrativePrincipal("alice")))
			w := httptest.NewRecorder()
			s.handleBrowserOpen(w, r)
			assert.Equal(t, http.StatusBadGateway, w.Code)
			assert.Empty(t, s.browserHandles, "never expose another conversation's or an older unscoped browser")
		})
	}
}

func TestBrowserCapabilityRequiresServerAndPrincipalPermission(t *testing.T) {
	s, registration, _ := newBrowserAPITestServer(t)
	for _, test := range []struct {
		name    string
		enabled bool
		roles   []string
		allowed bool
	}{
		{"disabled admin", false, []string{"admin"}, false},
		{"enabled user", true, []string{"user"}, false},
		{"enabled terminal", true, []string{"user", "terminal"}, true},
		{"enabled admin", true, []string{"admin"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s.config.BrowserEnabled = test.enabled
			r := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
			r = r.WithContext(contextWithPrincipal(r.Context(), Principal{ID: "user", Roles: test.roles}))
			w := httptest.NewRecorder()
			s.handleListRunners(w, r)
			var listed runnerListResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
			require.Len(t, listed.Runners, 1)
			assert.Equal(t, test.allowed, listed.Runners[0].WorkspaceBrowser)
			w = httptest.NewRecorder()
			s.handleGetRunner(w, mux.SetURLVars(r, map[string]string{"id": registration.RunnerID}))
			var runner runnerregistry.Runner
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runner))
			assert.Equal(t, test.allowed, runner.WorkspaceBrowser)
			actual, found := s.runnerRegistry.Runner(registration.RunnerID)
			require.True(t, found)
			assert.True(t, actual.WorkspaceBrowser, "UI filtering must not mutate the registered capability")
			w = httptest.NewRecorder()
			s.requireBrowser(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })(w, r)
			if test.allowed {
				assert.Equal(t, http.StatusNoContent, w.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, w.Code)
			}
		})
	}
}

func TestBrowserOIDCRequiresRoleAndCSRF(t *testing.T) {
	s, registration, _ := newBrowserAPITestServer(t)
	store, _ := newAuthStoreTest(t)
	s.authStore = store
	s.config.WebAuthMode = WebAuthModeOIDC
	terminal, csrf, err := store.CreateWebSession(t.Context(), "issuer", "terminal", "Terminal", "term@example.test", []string{"user", "terminal"}, time.Hour)
	require.NoError(t, err)
	user, userCSRF, err := store.CreateWebSession(t.Context(), "issuer", "user", "User", "user@example.test", []string{"user"}, time.Hour)
	require.NoError(t, err)
	for _, test := range []struct {
		name, token, csrf string
		want              int
	}{
		{"unauthenticated", "", "", http.StatusUnauthorized},
		{"missing csrf", terminal, "", http.StatusForbidden},
		{"wrong csrf", terminal, "wrong", http.StatusForbidden},
		{"wrong role", user, userCSRF, http.StatusForbidden},
		{"authorized", terminal, csrf, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/browser/session?runnerId="+registration.RunnerID+"&conversationId=conversation-1", nil)
			if test.token != "" {
				r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: test.token})
			}
			if test.csrf != "" {
				r.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: test.csrf})
				r.Header.Set(webCSRFHeaderName, test.csrf)
			}
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, r)
			assert.Equal(t, test.want, w.Code, w.Body.String())
		})
	}

	for _, path := range []string{"/api/browser/relay", "/api/browser/relay/", "/api/browser/other/ws"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer not-a-ticket")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code, path)
	}
}

func TestBrowserOriginIsExplicitAndTrusted(t *testing.T) {
	s := &Server{config: &ServerConfig{CORSOrigins: []string{"https://trusted.example"}}}
	for _, test := range []struct {
		origin string
		want   bool
	}{
		{"", false},
		{"null", false},
		{"https://portal.example", true},
		{"https://trusted.example", true},
		{"https://evil.example", false},
		{"https://portal.example/path", false},
		{"https://user@portal.example", false},
		{"file://portal.example", false},
		{"https://portal.example?query", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "https://portal.example/api/browser/id/ws", nil)
		r.Header.Set("Origin", test.origin)
		assert.Equal(t, test.want, s.browserOriginAllowed(r), test.origin)
	}
}

func TestBrowserRelayTicketsSingleUseExpiryAndGeneration(t *testing.T) {
	s, registration, _ := newBrowserAPITestServer(t)
	handle := openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	for _, test := range []string{"single use", "expired", "canceled", "stale generation"} {
		t.Run(test, func(t *testing.T) {
			a, err := s.newBrowserAttachment(t.Context(), handle)
			require.NoError(t, err)
			defer s.releaseBrowserAttachment(a)
			switch test {
			case "expired":
				a.expires = time.Now().Add(-time.Second)
			case "canceled":
				a.cancel()
			case "stale generation":
				s.runnerRegistry.Detach(registration.RunnerID, registration.ConnectionID, registration.Generation, nil)
			}
			r := httptest.NewRequest(http.MethodGet, protocol.BrowserRelayEndpoint, nil)
			r.Header.Set("Authorization", "Bearer "+a.token)
			w := httptest.NewRecorder()
			s.handleBrowserRelay(w, r)
			if test == "single use" {
				assert.Equal(t, http.StatusBadRequest, w.Code, "a valid ticket gets to websocket upgrade")
			} else {
				assert.Equal(t, http.StatusUnauthorized, w.Code)
			}
			w = httptest.NewRecorder()
			s.handleBrowserRelay(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code, "failed upgrades also consume the ticket")
			assert.NotContains(t, s.browserTickets, a.token)
		})
	}
	w := httptest.NewRecorder()
	assert.Nil(t, s.browserHandleForRequest(w, browserRequest(t, http.MethodGet, handle, "alice")))
	assert.Equal(t, http.StatusConflict, w.Code)
	s.RunnerUIDetached(runnerregistry.UIRequestIdentity{RunnerID: registration.RunnerID, Generation: registration.Generation})
	assert.Empty(t, s.browserHandles)
}

func TestBrowserDraftContinuityAndWorkspaceRevalidation(t *testing.T) {
	s, registration, _ := newBrowserAPITestServer(t)
	handle := openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	require.NoError(t, s.runnerRegistry.BindConversationWithEnvironmentProfile(t.Context(), handle.conversationID, registration.RunnerID, ""))
	// The first turn reserves affinity before its conversation record is saved.
	assert.Same(t, handle, openBrowserTestHandle(t, s, registration, "alice", "conversation-1"))
	w := httptest.NewRecorder()
	assert.Same(t, handle, s.browserHandleForRequest(w, browserRequest(t, http.MethodGet, handle, "alice")))
	r := httptest.NewRequest(http.MethodPost, "/api/browser/session?conversationId=conversation-1", nil)
	target, targetErr := s.resolveBrowserTarget(r)
	require.Nil(t, targetErr)
	assert.Equal(t, registration.RunnerID, target.Runner.ID, "a reserved draft must not fall back to another default runner")
	assert.Equal(t, handle.CWD, target.CWD)
	for _, query := range []string{"&runnerId=another-runner", "&cwd=/different"} {
		r = httptest.NewRequest(http.MethodPost, "/api/browser/session?conversationId=conversation-1"+query, nil)
		_, targetErr = s.resolveBrowserTarget(r)
		require.NotNil(t, targetErr)
		assert.Equal(t, http.StatusBadRequest, targetErr.status)
	}
	cwd := "/workspace"
	s.conversationService = &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
		return &conversations.GetConversationResponse{CWD: cwd}, nil
	}}
	assert.Same(t, handle, openBrowserTestHandle(t, s, registration, "alice", "conversation-1"), "saving the draft retains its browser")
	w = httptest.NewRecorder()
	assert.Same(t, handle, s.browserHandleForRequest(w, browserRequest(t, http.MethodGet, handle, "alice")))
	cwd = "/different"
	w = httptest.NewRecorder()
	assert.Nil(t, s.browserHandleForRequest(w, browserRequest(t, http.MethodGet, handle, "alice")))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestBrowserAttachmentLimitAndShutdown(t *testing.T) {
	s, registration, _ := newBrowserAPITestServer(t)
	handle := openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	var attachments []*browserAttachment
	for range maxBrowserAttachments {
		a, err := s.newBrowserAttachment(t.Context(), handle)
		require.NoError(t, err)
		attachments = append(attachments, a)
	}
	_, err := s.newBrowserAttachment(t.Context(), handle)
	require.ErrorContains(t, err, "too many")
	s.closeBrowserHandles("", 0)
	assert.Empty(t, s.browserHandles)
	assert.Empty(t, s.browserTickets)
	for _, a := range attachments {
		assert.Error(t, a.ctx.Err())
		s.releaseBrowserAttachment(a)
	}
	_, err = s.newBrowserAttachment(t.Context(), handle)
	assert.Error(t, err)
}

func TestBrowserDevToolsAssetsStreamBoundedChunks(t *testing.T) {
	s, registration, link := newBrowserAPITestServer(t)
	handle := openBrowserTestHandle(t, s, registration, "alice", "conversation-1")
	calls := 0
	link.call = func(_ context.Context, method string, raw any, result any) error {
		assert.Equal(t, protocol.MethodWorkspaceBrowserAsset, method)
		params := raw.(protocol.WorkspaceBrowserAssetParams)
		assert.Equal(t, "entrypoints/inspector/inspector.js", params.Path)
		assert.Equal(t, int64(calls*5), params.Offset)
		*result.(*browser.AssetChunk) = browser.AssetChunk{Data: []byte("hello"), ContentType: "text/javascript", EOF: calls == 1}
		calls++
		return nil
	}
	r := browserRequest(t, http.MethodGet, handle, "alice")
	r = mux.SetURLVars(r, map[string]string{"id": handle.ID, "path": "entrypoints/inspector/inspector.js"})
	w := httptest.NewRecorder()
	s.handleBrowserAsset(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hellohello", w.Body.String())
	assert.Equal(t, "text/javascript", w.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, 2, calls)
	link.call = func(_ context.Context, _ string, _ any, result any) error {
		*result.(*browser.AssetChunk) = browser.AssetChunk{Data: make([]byte, 128*1024+1), EOF: true}
		return nil
	}
	w = httptest.NewRecorder()
	s.handleBrowserAsset(w, r)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestBrowserCDPUsesIndependentStreamingConnection(t *testing.T) {
	s, registration, link := newBrowserAPITestServer(t)
	handle := openBrowserTestHandle(t, s, registration, "anonymous", "conversation-1")
	server := httptest.NewServer(s.router)
	t.Cleanup(server.Close)
	remoteClosed := make(chan struct{})
	ticketUsed := make(chan string, 1)
	link.call = func(ctx context.Context, method string, raw any, _ any) error {
		if method != protocol.MethodWorkspaceBrowserConnect {
			return nil
		}
		params := raw.(protocol.WorkspaceBrowserConnectParams)
		assert.Equal(t, handle.conversationID, params.ConversationID)
		assert.Equal(t, handle.CWD, params.CWD)
		assert.Equal(t, handle.SessionID, params.SessionID)
		conn, response, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+protocol.BrowserRelayEndpoint,
			http.Header{"Authorization": {"Bearer " + params.RelayToken}})
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			return err
		}
		ticketUsed <- params.RelayToken
		go func() {
			defer close(remoteClosed)
			defer conn.Close()
			for {
				kind, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(kind, payload); err != nil {
					return
				}
			}
		}()
		return nil
	}
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/browser/" + handle.ID + "/ws"
	_, response, err := websocket.DefaultDialer.DialContext(t.Context(), endpoint, http.Header{"Origin": {"https://evil.example"}})
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
	_ = response.Body.Close()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), endpoint, http.Header{"Origin": {server.URL}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// This frame exceeds the ordinary runner JSON-RPC limit, proving it is not a tool update/result.
	payload := []byte(`{"id":1,"result":"` + strings.Repeat("x", 5*1024*1024) + `"}`)
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, payload))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	kind, received, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, kind)
	assert.Equal(t, payload, received)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+protocol.BrowserRelayEndpoint, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+<-ticketUsed)
	response, err = server.Client().Do(request)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	_ = response.Body.Close()

	require.NoError(t, conn.Close())
	select {
	case <-remoteClosed:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "browser disconnect did not close outbound runner relay")
	}
	require.Eventually(t, func() bool {
		s.browserMu.Lock()
		defer s.browserMu.Unlock()
		return len(handle.attachments) == 0 && len(s.browserTickets) == 0
	}, time.Second, time.Millisecond)
	assert.Contains(t, s.browserHandles, handle.ID, "detaching does not stop the conversation browser")
}

// This fixture uses temporary auth storage and a fake runner echoing CDP over
// loopback WebSockets. It neither launches Chrome nor contacts an existing daemon.
type browserAuthTestFixture struct {
	server       *Server
	registration protocol.RegisterResult
	http         *httptest.Server
	relays       chan (<-chan struct{})
	stops        atomic.Int32
}

func newBrowserAuthTestFixture(t *testing.T) *browserAuthTestFixture {
	t.Helper()
	s, registration, link := newBrowserAPITestServer(t)
	store, _ := newAuthStoreTest(t)
	store.now = time.Now
	s.authStore = store
	s.config.WebAuthMode = WebAuthModeOIDC
	f := &browserAuthTestFixture{server: s, registration: registration, relays: make(chan (<-chan struct{}), maxBrowserAttachments)}
	link.call = func(ctx context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodWorkspaceBrowserOpen:
			request := params.(protocol.WorkspaceBrowserParams)
			*result.(*browser.Info) = browser.Info{SessionID: "retained-runner-session", ConversationID: request.ConversationID, CWD: request.CWD}
		case protocol.MethodWorkspaceBrowserStop:
			f.stops.Add(1)
		case protocol.MethodWorkspaceBrowserConnect:
			request := params.(protocol.WorkspaceBrowserConnectParams)
			conn, response, err := websocket.DefaultDialer.DialContext(ctx,
				"ws"+strings.TrimPrefix(f.http.URL, "http")+protocol.BrowserRelayEndpoint,
				http.Header{"Authorization": {"Bearer " + request.RelayToken}})
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err != nil {
				return err
			}
			closed := make(chan struct{})
			f.relays <- closed
			go func() {
				defer close(closed)
				defer conn.Close()
				for {
					kind, payload, err := conn.ReadMessage()
					if err != nil || conn.WriteMessage(kind, payload) != nil {
						return
					}
				}
			}()
		}
		return nil
	}
	f.http = httptest.NewServer(s.router)
	t.Cleanup(func() {
		s.closeBrowserHandles("", 0)
		f.http.Close()
	})
	return f
}

func (f *browserAuthTestFixture) session(t *testing.T, duration time.Duration) (http.Header, string) {
	t.Helper()
	token, csrf, err := f.server.authStore.CreateWebSession(t.Context(), "issuer", "same-user", "User", "user@example.test", []string{"user", "terminal"}, duration)
	require.NoError(t, err)
	return http.Header{
		"Cookie":          {webSessionCookieName + "=" + token + "; " + webCSRFCookieName + "=" + csrf},
		webCSRFHeaderName: {csrf},
	}, token
}

func (f *browserAuthTestFixture) request(t *testing.T, method, path string, headers http.Header) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, f.http.URL+path, nil)
	require.NoError(t, err)
	r.Header = headers.Clone()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func (f *browserAuthTestFixture) open(t *testing.T, headers http.Header) browserHandle {
	t.Helper()
	response := f.request(t, http.MethodPost, "/api/browser/session?runnerId="+f.registration.RunnerID+"&conversationId=conversation-1", headers)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var handle browserHandle
	require.NoError(t, json.NewDecoder(response.Body).Decode(&handle))
	return handle
}

type browserAuthTestSocket struct {
	closed      chan struct{}
	relayClosed <-chan struct{}
	err         error // Published by closing closed.
}

func (f *browserAuthTestFixture) dial(t *testing.T, handle browserHandle, headers http.Header) *browserAuthTestSocket {
	t.Helper()
	headers = headers.Clone()
	headers.Set("Origin", f.http.URL)
	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(),
		"ws"+strings.TrimPrefix(f.http.URL, "http")+"/api/browser/"+handle.ID+"/ws", headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*browserAuthCheckInterval+5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"method":"Runtime.enable"}`)))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)
	socket := &browserAuthTestSocket{closed: make(chan struct{}), relayClosed: <-f.relays}
	go func() {
		defer close(socket.closed)
		for {
			_, _, err := conn.ReadMessage() // Processes server pings and responds normally.
			if err != nil {
				socket.err = err
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-socket.closed:
				return
			case <-ticker.C:
				// Keep the server read deadline fresh. Authentication must expire anyway.
				if conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second)) != nil {
					return
				}
			}
		}
	}()
	return socket
}

func (socket *browserAuthTestSocket) assertClosed(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-socket.closed:
		require.Error(t, socket.err)
		var netErr net.Error
		if errors.As(socket.err, &netErr) {
			assert.False(t, netErr.Timeout(), "authentication must close the socket, not the test read deadline")
		}
	case <-time.After(timeout):
		require.FailNow(t, "browser socket survived invalid authentication")
	}
	select {
	case <-socket.relayClosed:
	case <-time.After(time.Second):
		require.FailNow(t, "browser auth cancellation did not close the runner relay")
	}
}

func TestBrowserOIDCLogoutClosesOnlyOriginatingSession(t *testing.T) {
	f := newBrowserAuthTestFixture(t)
	firstAuth, firstToken := f.session(t, time.Hour)
	otherAuth, _ := f.session(t, time.Hour)
	handle := f.open(t, firstAuth)
	otherHandle := f.open(t, otherAuth)
	require.Equal(t, handle.ID, otherHandle.ID, "one principal can share a browser handle across logins")
	first := f.dial(t, handle, firstAuth)
	anotherTab := f.dial(t, handle, firstAuth)
	otherDevice := f.dial(t, otherHandle, otherAuth)
	response := f.request(t, http.MethodGet, "/auth/logout", firstAuth)
	require.Equal(t, http.StatusFound, response.StatusCode)
	first.assertClosed(t, time.Second) // Must not wait for the periodic revocation check.
	anotherTab.assertClosed(t, time.Second)
	_, found, err := f.server.authStore.LoadWebSession(t.Context(), firstToken)
	require.NoError(t, err)
	assert.False(t, found)
	select {
	case <-otherDevice.closed:
		require.FailNow(t, "logout disconnected another login session for the same principal")
	default:
	}
	assert.Zero(t, f.stops.Load(), "logout must not issue browser.stop")
	freshAuth, _ := f.session(t, time.Hour)
	reopened := f.open(t, freshAuth)
	assert.Equal(t, handle.SessionID, reopened.SessionID)
	f.dial(t, reopened, freshAuth)
	response = f.request(t, http.MethodPost, "/api/browser/session?runnerId="+f.registration.RunnerID+"&conversationId=conversation-1", firstAuth)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestBrowserOIDCExpiryClosesActiveWebsocket(t *testing.T) {
	f := newBrowserAuthTestFixture(t)
	headers, _ := f.session(t, time.Second)
	handle := f.open(t, headers)
	socket := f.dial(t, handle, headers)
	// The expiry timer must fire before the five-second revalidation interval,
	// regardless of the continuous pong traffic generated by the fixture.
	socket.assertClosed(t, 2*time.Second)
	assert.Zero(t, f.stops.Load())
	fresh, _ := f.session(t, time.Hour)
	reopened := f.open(t, fresh)
	assert.Equal(t, handle.SessionID, reopened.SessionID)
	f.dial(t, reopened, fresh)
}

func TestBrowserOIDCExpiryCannotBeExtendedByRevalidation(t *testing.T) {
	f := newBrowserAuthTestFixture(t)
	headers, token := f.session(t, browserAuthCheckInterval+time.Second)
	handle := f.open(t, headers)
	socket := f.dial(t, handle, headers)
	// The next successful revalidation sees a longer lifetime, but an existing
	// attachment must remain bounded to the lifetime with which it was admitted.
	_, err := f.server.authStore.db.ExecContext(t.Context(), `UPDATE web_auth_sessions SET expires_at = ? WHERE token_sha256 = ?`, time.Now().Add(time.Hour), authHash(token))
	require.NoError(t, err)
	socket.assertClosed(t, browserAuthCheckInterval+2*time.Second)
	assert.Zero(t, f.stops.Load())
}

func TestBrowserOIDCExpiryClosesSocketDuringBlockedAuthCheck(t *testing.T) {
	f := newBrowserAuthTestFixture(t)
	headers, _ := f.session(t, browserAuthCheckInterval+time.Second)
	handle := f.open(t, headers)
	socket := f.dial(t, handle, headers)
	// Block the revalidation query past expiry. The independent expiry timer
	// must disconnect both sockets without waiting for the store timeout.
	f.server.authStore.db.SetMaxOpenConns(1)
	connection, err := f.server.authStore.db.Conn(t.Context())
	require.NoError(t, err)
	defer connection.Close()
	socket.assertClosed(t, browserAuthCheckInterval+2*time.Second)
	assert.Zero(t, f.stops.Load())
}

func TestBrowserOIDCRevocationAndRoleChangesCloseWebsocket(t *testing.T) {
	for _, change := range []string{"revoked", "role removed", "store unavailable"} {
		t.Run(change, func(t *testing.T) {
			f := newBrowserAuthTestFixture(t)
			headers, token := f.session(t, time.Hour)
			handle := f.open(t, headers)
			socket := f.dial(t, handle, headers)
			switch change {
			case "revoked":
				require.NoError(t, f.server.authStore.DeleteWebSession(t.Context(), token))
			case "role removed":
				_, err := f.server.authStore.db.ExecContext(t.Context(), `UPDATE web_auth_sessions SET roles_json = '["user"]' WHERE token_sha256 = ?`, authHash(token))
				require.NoError(t, err)
			case "store unavailable":
				require.NoError(t, f.server.authStore.Close())
			}
			socket.assertClosed(t, browserAuthCheckInterval+2*time.Second)
			assert.Zero(t, f.stops.Load())
		})
	}
}

func TestBrowserDeviceCredentialExpiryAndRevocation(t *testing.T) {
	for _, change := range []string{"expired", "revoked"} {
		t.Run(change, func(t *testing.T) {
			f := newBrowserAuthTestFixture(t)
			webAuth, webToken := f.session(t, time.Hour)
			session, found, err := f.server.authStore.LoadWebSession(t.Context(), webToken)
			require.NoError(t, err)
			require.True(t, found)
			duration := time.Hour
			if change == "expired" {
				duration = time.Second
			}
			started, _ := issueUserCredentialForHTTPTest(t, f.server.authStore, principalFromWebSession(session), duration)
			headers := http.Header{"Authorization": {"Bearer " + started.BearerToken}}
			handle := f.open(t, headers)
			socket := f.dial(t, handle, headers)
			if change == "revoked" {
				response := f.request(t, http.MethodDelete, userauth.CurrentCredentialPath, headers)
				require.Equal(t, http.StatusNoContent, response.StatusCode)
				socket.assertClosed(t, browserAuthCheckInterval+2*time.Second)
			} else {
				socket.assertClosed(t, 2*time.Second)
			}
			assert.Zero(t, f.stops.Load())
			assert.Equal(t, handle.SessionID, f.open(t, webAuth).SessionID)
		})
	}
}

func TestBrowserAuthRechecksAfterAttachmentRegistration(t *testing.T) {
	f := newBrowserAuthTestFixture(t)
	headers, token := f.session(t, time.Hour)
	session, found, err := f.server.authStore.LoadWebSession(t.Context(), token)
	require.NoError(t, err)
	require.True(t, found)
	handle := f.open(t, headers)
	r := browserRequest(t, http.MethodGet, &handle, "unused")
	r.Header = headers
	r = r.WithContext(contextWithPrincipal(t.Context(), principalFromWebSession(session)))
	// Simulate logout after middleware admission, but before attachment insertion.
	response := f.request(t, http.MethodGet, "/auth/logout", headers)
	require.Equal(t, http.StatusFound, response.StatusCode)
	f.server.browserMu.Lock()
	stored := f.server.browserHandles[handle.ID]
	f.server.browserMu.Unlock()
	a, err := f.server.newBrowserAttachment(r.Context(), stored)
	require.NoError(t, err)
	defer f.server.releaseBrowserAttachment(a)
	require.Error(t, f.server.bindBrowserAttachmentAuth(r, a))
}

func TestBrowserCompatibilityAuthHasNoSessionLifetime(t *testing.T) {
	for _, mode := range []WebAuthMode{WebAuthModeOIDC, WebAuthModeToken, WebAuthModeNone} {
		t.Run(string(mode), func(t *testing.T) {
			f := newBrowserAuthTestFixture(t)
			f.server.config.WebAuthMode = mode
			headers := http.Header{}
			if mode != WebAuthModeNone {
				f.server.config.AuthToken = "test-compatibility-token"
				headers.Set("Authorization", "Bearer test-compatibility-token")
			}
			handle := f.open(t, headers)
			socket := f.dial(t, handle, headers)
			unrelated, _ := f.session(t, time.Hour)
			response := f.request(t, http.MethodGet, "/auth/logout", unrelated)
			require.Equal(t, http.StatusFound, response.StatusCode)
			f.server.browserMu.Lock()
			for a := range f.server.browserHandles[handle.ID].attachments {
				assert.Empty(t, a.sessionID)
				assert.Empty(t, a.credentialID)
				assert.NoError(t, a.ctx.Err())
			}
			f.server.browserMu.Unlock()
			f.server.closeBrowserHandles("", 0)
			socket.assertClosed(t, time.Second)
		})
	}
}

// Opt-in: runs an explicitly selected Chrome executable against only temporary
// Kodelet state and ephemeral loopback HTTP listeners, never the user's daemon.
func TestBrowserRealChromeRelay(t *testing.T) {
	executable := os.Getenv("KODELET_BROWSER_TEST_EXECUTABLE")
	if executable == "" {
		t.Skip("set KODELET_BROWSER_TEST_EXECUTABLE to run the isolated Chrome relay smoke test")
	}
	config := embeddedRunnerTestConfig(t)
	config.BrowserEnabled = true
	config.EmbeddedRunner.ServiceOptions.Browser = browser.Config{Executable: executable}
	s, endpoint, _ := startEmbeddedRunnerTestServer(t, config, "127.0.0.1:0")
	require.Eventually(t, func() bool { return s.EmbeddedRunnerStatus().Ready }, 10*time.Second, 10*time.Millisecond)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Isolated browser test</title><h1 id='app'>Browser ready</h1>"))
	}))
	t.Cleanup(app.Close)
	client := &http.Client{Timeout: 30 * time.Second}
	request := func(method, path string) *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), method, endpoint+path, nil)
		require.NoError(t, err)
		r.Header.Set("Authorization", "Bearer "+config.AuthToken)
		response, err := client.Do(r)
		require.NoError(t, err)
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	open := func(conversationID string) browserHandle {
		t.Helper()
		response := request(http.MethodPost, "/api/browser/session?conversationId="+conversationID)
		var body struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			Error     string `json:"error"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		require.Equal(t, http.StatusOK, response.StatusCode, body.Error)
		return browserHandle{ID: body.ID, SessionID: body.SessionID, CWD: body.CWD}
	}
	dial := func(handle browserHandle) *websocket.Conn {
		t.Helper()
		conn, response, err := websocket.DefaultDialer.DialContext(t.Context(),
			"ws"+strings.TrimPrefix(endpoint, "http")+"/api/browser/"+handle.ID+"/ws",
			http.Header{"Authorization": {"Bearer " + config.AuthToken}, "Origin": {endpoint}})
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	first := open("conversation-1")
	require.Equal(t, config.EmbeddedRunner.Workspace, first.CWD)
	conn := dial(first)
	var commandID int
	var frames []json.RawMessage
	command := func(conn *websocket.Conn, method string, params any) json.RawMessage {
		t.Helper()
		commandID++
		require.NoError(t, conn.SetWriteDeadline(time.Now().Add(10*time.Second)))
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
		require.NoError(t, conn.WriteJSON(map[string]any{"id": commandID, "method": method, "params": params}))
		for {
			var message struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			require.NoError(t, conn.ReadJSON(&message), method)
			if message.Method == "Page.screencastFrame" {
				frames = append(frames, message.Params)
			}
			if message.ID == commandID {
				require.Empty(t, message.Error, method)
				return message.Result
			}
		}
	}
	command(conn, "Page.enable", map[string]any{})
	command(conn, "Page.navigate", map[string]any{"url": app.URL})
	evaluated := command(conn, "Runtime.evaluate", map[string]any{
		"expression":   `new Promise(resolve => { const poll = () => { const h = document.querySelector('#app'); if (h) { window.kodeletBrowserSmoke = 42; resolve(h.textContent); } else { setTimeout(poll, 10); } }; poll(); })`,
		"awaitPromise": true, "returnByValue": true,
	})
	assert.Contains(t, string(evaluated), "Browser ready")
	command(conn, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 50, "maxWidth": 640, "maxHeight": 480})
	for len(frames) == 0 {
		var event struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		require.NoError(t, conn.ReadJSON(&event))
		if event.Method == "Page.screencastFrame" {
			frames = append(frames, event.Params)
		}
	}
	var frame struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
	}
	require.NoError(t, json.Unmarshal(frames[0], &frame))
	assert.NotEmpty(t, frame.Data)
	command(conn, "Page.screencastFrameAck", map[string]any{"sessionId": frame.SessionID})
	command(conn, "Page.stopScreencast", map[string]any{})
	require.NoError(t, conn.Close())
	second := open("conversation-1")
	assert.Equal(t, first.SessionID, second.SessionID)
	conn = dial(second)
	evaluated = command(conn, "Runtime.evaluate", map[string]any{"expression": "window.kodeletBrowserSmoke", "returnByValue": true})
	assert.Contains(t, string(evaluated), `"value":42`)
	other := open("conversation-2")
	assert.Equal(t, first.CWD, other.CWD)
	assert.NotEqual(t, first.SessionID, other.SessionID)
	otherConn := dial(other)
	evaluated = command(otherConn, "Runtime.evaluate", map[string]any{
		"expression": `({url: location.href, marker: typeof window.kodeletBrowserSmoke})`, "returnByValue": true,
	})
	assert.Contains(t, string(evaluated), `"url":"about:blank"`)
	assert.Contains(t, string(evaluated), `"marker":"undefined"`)
	response := request(http.MethodDelete, "/api/browser/"+second.ID)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			assert.False(t, websocket.IsCloseError(err, websocket.CloseProtocolError))
			break
		}
	}
	evaluated = command(otherConn, "Runtime.evaluate", map[string]any{"expression": "6 * 7", "returnByValue": true})
	assert.Contains(t, string(evaluated), `"value":42`, "stopping one conversation leaves another usable")
	response = request(http.MethodDelete, "/api/browser/"+other.ID)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	s.browserMu.Lock()
	assert.Empty(t, s.browserHandles)
	s.browserMu.Unlock()
}
