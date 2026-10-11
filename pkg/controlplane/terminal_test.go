package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalHandlersRejectInvalidTabIDs(t *testing.T) {
	server := &Server{}
	// The protocol validator owns the character/length matrix; both handlers
	// must invoke it before resolving a workspace or upgrading the connection.
	for _, handler := range []http.HandlerFunc{server.handleTerminalWebsocket, server.handleCloseTerminal} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, "/api/terminal?terminalId=../tab", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "invalid terminal ID")
	}
}

func TestCloseTerminalAuthorizesAndForwardsOnlyItsScope(t *testing.T) {
	server := newRunnerTestServer(t, "")
	// Closing must not require the saved directory to still exist.
	savedCWD := filepath.Join(t.TempDir(), "removed-worktree")
	server.conversationService = &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
		if id == "saved" {
			return &conversations.GetConversationResponse{ID: id, CWD: savedCWD}, nil
		}
		return nil, convtypes.ErrConversationNotFound
	}}
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities: protocol.RunnerCapabilities{
			WorkspaceTerminal: true, WorkspaceCWD: true,
		},
		Host:      protocol.Host{InstanceID: "terminal-close", Hostname: "worker", OS: "linux", Arch: "amd64"},
		Workspace: protocol.Workspace{Path: "/runner/project", Name: "project"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
	}))
	for _, id := range []string{"saved", "reserved"} {
		require.NoError(t, server.runnerRegistry.BindConversation(t.Context(), id, registration.RunnerID))
	}
	for _, test := range []struct {
		name, conversationID, runnerID, cwd, terminalID string
		status                                          int
		wantCWD                                         string
	}{
		{"saved", "saved", "", savedCWD, "tab-A_1", http.StatusNoContent, savedCWD},
		{"default tab", "saved", "", "", "", http.StatusNoContent, savedCWD},
		{"draft", "draft", registration.RunnerID, "../selected", "tab-2", http.StatusNoContent, "../selected"},
		{"reserved", "reserved", "", "", "tab-2", http.StatusNoContent, "/runner/project"},
		{"missing conversation", "", registration.RunnerID, "", "tab-1", http.StatusBadRequest, ""},
		{"wrong saved directory", "saved", "", "/other", "tab-1", http.StatusBadRequest, ""},
		{"wrong saved runner", "saved", "other-runner", "", "tab-1", http.StatusBadRequest, ""},
		{"wrong reserved runner", "reserved", "other-runner", "", "tab-1", http.StatusBadRequest, ""},
		{"cleanup error", "saved", "", "", "failed", http.StatusBadGateway, savedCWD},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			sessionID := ""
			if test.name != "default tab" {
				sessionID = "terminal-session-123"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			link.call = func(callCtx context.Context, method string, params, _ any) error {
				called = true
				assert.Equal(t, protocol.MethodWorkspaceTerminalClose, method)
				assert.Equal(t, protocol.WorkspaceTerminalCloseParams{
					ConversationID: test.conversationID, CWD: test.wantCWD, TerminalID: test.terminalID,
					SessionID: sessionID,
				}, params)
				cancel()
				assert.NoError(t, callCtx.Err(), "cleanup outlives the HTTP observer")
				if test.terminalID == "failed" {
					return errors.New("cleanup failed")
				}
				return nil
			}
			query := url.Values{
				"conversationId": {test.conversationID}, "runnerId": {test.runnerID},
				"cwd": {test.cwd}, "terminalId": {test.terminalID},
				"sessionId": {sessionID},
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, "/api/terminal?"+query.Encode(), nil).WithContext(ctx)
			server.handleCloseTerminal(w, r)
			assert.Equal(t, test.status, w.Code, w.Body.String())
			assert.Equal(t, test.wantCWD != "", called)
		})
	}
}

func TestCloseTerminalRouteRequiresTerminalRole(t *testing.T) {
	store, _ := newAuthStoreTest(t)
	server := newRunnerTestServer(t, "")
	server.router = mux.NewRouter()
	server.config.WebAuthMode = WebAuthModeOIDC
	server.authStore = store
	server.setupRoutes()
	for _, role := range []Role{RoleUser, RoleTerminal} {
		token, csrfToken, err := store.CreateWebSession(t.Context(), "issuer", string(role), "User", "user@example.com", []string{string(role)}, time.Hour)
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodDelete, "/api/terminal", nil)
		r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: token})
		r.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: csrfToken})
		r.Header.Set(webCSRFHeaderName, csrfToken)
		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, r)
		if role == RoleUser {
			assert.Equal(t, http.StatusForbidden, w.Code)
		} else {
			assert.Equal(t, http.StatusBadRequest, w.Code, "terminal role reaches workspace validation")
		}
	}
}

func TestTerminalOpenFailureSignalsExplicitRetry(t *testing.T) {
	server := newRunnerTestServer(t, "")
	for _, test := range []struct {
		name string
		err  error
		info string
		code int
	}{
		{
			name: "capacity",
			err:  &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "runner terminal session limit reached (8); exit an existing terminal to release its slot"},
			info: "runner terminal session limit reached (8); exit an existing terminal to release its slot",
			code: websocket.CloseTryAgainLater,
		},
		{
			name: "discarded",
			err:  &protocol.RPCError{Code: protocol.ErrorCodeStale, Data: protocol.RPCErrorData{Reason: protocol.ErrorReasonWorkspaceSessionDiscarded}},
			info: "Failed to open runner terminal.",
			code: websocket.ClosePolicyViolation,
		},
		{
			name: "closed",
			err:  &protocol.RPCError{Code: protocol.ErrorCodeStale, Data: protocol.RPCErrorData{Reason: protocol.ErrorReasonWorkspaceSessionClosed}},
			info: "Failed to open runner terminal.",
			code: websocket.ClosePolicyViolation,
		},
		{
			name: "invalid directory",
			err:  &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "invalid working directory"},
			info: "invalid working directory",
			code: websocket.CloseTryAgainLater,
		},
		{
			name: "transport",
			err:  errors.New("connection unavailable"),
			info: "Failed to open runner terminal.",
			code: websocket.CloseAbnormalClosure,
		},
		{
			name: "control plane timeout",
			err:  context.DeadlineExceeded,
			info: "Failed to open runner terminal.",
			code: websocket.CloseAbnormalClosure,
		},
		{
			name: "runner unavailable",
			err:  &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "runner service is closed"},
			info: "runner service is closed",
			code: websocket.CloseAbnormalClosure,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			link := newRunnerAPITestLink()
			link.call = func(_ context.Context, method string, _, _ any) error {
				assert.Equal(t, protocol.MethodWorkspaceTerminalOpen, method)
				return test.err
			}
			registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version},
				Capabilities:     protocol.RunnerCapabilities{WorkspaceTerminal: true},
				Host:             protocol.Host{InstanceID: "terminal-failure-" + test.name, Hostname: "worker", OS: "linux", Arch: "amd64"},
				Workspace:        protocol.Workspace{Path: "/workspace", Name: "workspace"},
			}, link)
			require.NoError(t, err)
			require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
				RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
			}))
			runner, found := server.runnerRegistry.Runner(registration.RunnerID)
			require.True(t, found)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.handleRemoteTerminalWebsocket(w, r, &workspaceRunnerTarget{Runner: runner, CWD: "/workspace"}, false)
			}))
			defer httpServer.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
			var info terminalMessage
			require.NoError(t, conn.ReadJSON(&info))
			assert.Equal(t, terminalMessage{Type: "info", Text: test.info}, info)
			_, _, err = conn.ReadMessage()
			assert.True(t, websocket.IsCloseError(err, test.code), "unexpected close: %v", err)
		})
	}
}

func TestWebsocketWriterWritesMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		writer := &websocketWriter{conn: conn}
		require.NoError(t, writer.writeJSON(terminalMessage{Type: "info", Text: "hello"}))
		require.NoError(t, writer.Write(websocket.BinaryMessage, []byte("bytes")))
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()

	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, messageType)
	var message terminalMessage
	require.NoError(t, json.Unmarshal(payload, &message))
	assert.Equal(t, terminalMessage{Type: "info", Text: "hello"}, message)

	messageType, payload, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, messageType)
	assert.Equal(t, []byte("bytes"), payload)
}

func TestWebsocketWriterRejectsNilConnection(t *testing.T) {
	var writer *websocketWriter
	require.ErrorContains(t, writer.Write(websocket.TextMessage, nil), "websocket writer is not initialized")

	writer = &websocketWriter{}
	require.ErrorContains(t, writer.Write(websocket.TextMessage, nil), "websocket writer is not initialized")
}

func TestTerminalExitMessageIncludesZeroCode(t *testing.T) {
	payload, err := json.Marshal(terminalMessage{Type: "exit", Code: terminalExitCode(0)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"exit","code":0}`, string(payload))
}

func TestReadTerminalWebsocketForwardsMessagesAndReadErrors(t *testing.T) {
	serverReadCh := make(chan terminalSocketRead, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		readTerminalWebsocket(r.Context(), conn, serverReadCh)
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"input","data":"echo hi\n"}`)))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("raw")))
	require.NoError(t, conn.Close())

	first := receiveTerminalSocketRead(t, serverReadCh)
	assert.Equal(t, websocket.TextMessage, first.MessageType)
	assert.Equal(t, []byte(`{"type":"input","data":"echo hi\n"}`), first.Payload)
	require.NoError(t, first.Err)

	second := receiveTerminalSocketRead(t, serverReadCh)
	assert.Equal(t, websocket.BinaryMessage, second.MessageType)
	assert.Equal(t, []byte("raw"), second.Payload)
	require.NoError(t, second.Err)

	third := receiveTerminalSocketRead(t, serverReadCh)
	require.Error(t, third.Err)
}

func TestParseTerminalDimension(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty", raw: "", want: 0},
		{name: "whitespace", raw: "   ", want: 0},
		{name: "invalid", raw: "wide", want: 0},
		{name: "valid", raw: " 120 ", want: 120},
		{name: "negative", raw: "-3", want: -3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseTerminalDimension(tt.raw))
		})
	}
}

func TestValidateRemoteTerminalRead(t *testing.T) {
	for _, test := range []struct {
		name    string
		cursor  uint64
		result  protocol.WorkspaceTerminalReadResult
		wantErr string
	}{
		{name: "contiguous output", cursor: 5, result: protocol.WorkspaceTerminalReadResult{Data: []byte("next"), NextCursor: 9}},
		{name: "empty poll", cursor: 5, result: protocol.WorkspaceTerminalReadResult{NextCursor: 5}},
		{name: "truncated output advances start", cursor: 5, result: protocol.WorkspaceTerminalReadResult{Data: []byte("tail"), NextCursor: 14, Truncated: true}},
		{name: "cursor moves backwards", cursor: 5, result: protocol.WorkspaceTerminalReadResult{NextCursor: 4}, wantErr: "moved backwards"},
		{name: "data exceeds cursor", cursor: 0, result: protocol.WorkspaceTerminalReadResult{Data: []byte("bad"), NextCursor: 2}, wantErr: "invalid output cursor"},
		{name: "chunk exceeds requested size", cursor: 0, result: protocol.WorkspaceTerminalReadResult{Data: make([]byte, remoteTerminalReadBytes+1), NextCursor: remoteTerminalReadBytes + 1}, wantErr: "exceeds the requested chunk size"},
		{name: "non-contiguous output", cursor: 5, result: protocol.WorkspaceTerminalReadResult{Data: []byte("tail"), NextCursor: 14}, wantErr: "not contiguous"},
		{name: "invalid truncated output", cursor: 5, result: protocol.WorkspaceTerminalReadResult{Data: []byte("tail"), NextCursor: 8, Truncated: true}, wantErr: "before the requested cursor"},
		{name: "truncation must advance", cursor: 5, result: protocol.WorkspaceTerminalReadResult{NextCursor: 5, Truncated: true}, wantErr: "without advancing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateRemoteTerminalRead(test.cursor, test.result)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestRemoteTerminalAttachmentLimit(t *testing.T) {
	server := &Server{}
	for i := 0; i < maxRemoteTerminalAttachments; i++ {
		assert.True(t, server.acquireRemoteTerminalAttachment("runner-1"))
	}
	assert.False(t, server.acquireRemoteTerminalAttachment("runner-1"))
	assert.True(t, server.acquireRemoteTerminalAttachment("runner-2"))

	server.releaseRemoteTerminalAttachment("runner-1")
	assert.True(t, server.acquireRemoteTerminalAttachment("runner-1"))
	server.releaseRemoteTerminalAttachment("runner-2")
	assert.NotContains(t, server.remoteTerminals, "runner-2")
}

func TestTerminalOriginAllowedBranches(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:8080")
	assert.True(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:3000")
	assert.False(t, terminalOriginAllowed(req))
	assert.True(t, (&Server{config: &ServerConfig{}}).terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "https://app.example.com")
	assert.True(t, (&Server{config: &ServerConfig{CORSOrigins: []string{"https://app.example.com"}}}).terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "://bad-url")
	assert.False(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = ""
	req.Header.Set("Origin", "http://localhost:3000")
	assert.False(t, terminalOriginAllowed(req))
}

func TestNormalizedHostPort(t *testing.T) {
	assert.Equal(t, "", normalizedHostPort("   "))
	assert.Equal(t, "example.com:8080", normalizedHostPort("Example.COM:8080"))
	assert.Equal(t, "[::1]:8080", normalizedHostPort("[::1]:8080"))
	assert.Equal(t, "example.com", normalizedHostPort("Example.COM"))
	assert.Equal(t, "::1", normalizedHostPort("[::1"))
}

func receiveTerminalSocketRead(t *testing.T, ch <-chan terminalSocketRead) terminalSocketRead {
	t.Helper()

	select {
	case read := <-ch:
		return read
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal websocket read")
		return terminalSocketRead{}
	}
}

func TestReadTerminalWebsocketStopsWhenContextIsCanceledBeforeSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readCh := make(chan terminalSocketRead)
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		readTerminalWebsocket(ctx, conn, readCh)
		close(done)
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("blocked")))
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal websocket reader to stop after context cancellation")
	}
}
