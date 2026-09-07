package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionExtensionWebsocketAuthentication(t *testing.T) {
	server := newRunnerTestServer(t, "client-secret")
	server.router = mux.NewRouter()
	server.setupRoutes()
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	for _, test := range []struct {
		name, token, clientID, origin string
		subprotocols                  []string
		status                        int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "runner credential", token: "client-secret-runner", status: http.StatusUnauthorized},
		{name: "missing client identity", token: "client-secret", status: http.StatusBadRequest},
		{name: "missing subprotocol", token: "client-secret", clientID: "sdk", status: http.StatusBadRequest},
		{name: "cross origin", token: "client-secret", clientID: "sdk", origin: "https://untrusted.example", subprotocols: []string{protocol.SessionExtensionsSubprotocol}, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{}
			if test.token != "" {
				headers.Set("Authorization", "Bearer "+test.token)
			}
			headers.Set(chat.ClientIDHeader, test.clientID)
			headers.Set("Origin", test.origin)
			dialer := websocket.Dialer{Subprotocols: test.subprotocols}
			_, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(httpServer.URL, "http")+protocol.SessionExtensionsEndpoint, headers)
			require.Error(t, err)
			require.NotNil(t, response)
			assert.Equal(t, test.status, response.StatusCode)
			require.NoError(t, response.Body.Close())
		})
	}
}

func TestSessionExtensionRelayRoundTripOwnershipAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server := newRunnerTestServer(t, "client-secret")
	server.router = mux.NewRouter()
	server.setupRoutes()
	httpServer := httptest.NewServer(server.router)
	t.Cleanup(httpServer.Close)
	link := newRunnerAPITestLink()
	registration, err := server.runnerRegistry.Register(protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version}, Capabilities: protocol.RunnerCapabilities{SessionExtensions: true, ConcurrentRuns: true},
		Host: protocol.Host{InstanceID: "host"}, Workspace: protocol.Workspace{Path: "/workspace", Name: "workspace"},
	}, link)
	require.NoError(t, err)
	require.NoError(t, server.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation, protocol.HeartbeatParams{
		RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle,
	}))
	identity := runnerUIRequestIdentity(registration)
	client, err := chat.NewClient(httpServer.URL, "client-secret", registration.RunnerID)
	require.NoError(t, err)
	incoming := make(chan protocol.ExtensionFrame, 8)
	relay, err := client.AttachSessionExtensions(ctx, "conversation", registration.RunnerID, []string{"inline-1"}, func(_ context.Context, frame protocol.ExtensionFrame) error {
		incoming <- frame
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	descriptor := relay.Attachment()
	server.sessionExtensionsMu.Lock()
	attachment := server.sessionExtensions[descriptor.ID]
	server.sessionExtensionsMu.Unlock()
	require.NotNil(t, attachment)
	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	request = request.WithContext(contextWithPrincipal(ctx, Principal{ID: attachment.principalID}))
	request.Header.Set(chat.ClientIDHeader, attachment.clientID)
	chatRequest := chat.ChatRequest{ConversationID: "conversation", RunnerID: registration.RunnerID, SessionExtensionsID: descriptor.ID}
	owner, err := server.sessionExtensionsForRequest(request, chatRequest)
	require.NoError(t, err)
	assert.Same(t, attachment, owner)
	for _, mutate := range []func(*http.Request, *chat.ChatRequest){
		func(r *http.Request, _ *chat.ChatRequest) { r.Header.Set(chat.ClientIDHeader, "another-client") },
		func(r *http.Request, _ *chat.ChatRequest) {
			*r = *r.WithContext(contextWithPrincipal(ctx, Principal{ID: "other-user"}))
		},
		func(_ *http.Request, req *chat.ChatRequest) { req.ConversationID = "another-conversation" },
		func(_ *http.Request, req *chat.ChatRequest) { req.RunnerID = "another-runner" },
	} {
		otherRequest, otherChat := request.Clone(ctx), chatRequest
		otherRequest = otherRequest.WithContext(request.Context())
		mutate(otherRequest, &otherChat)
		_, err := server.sessionExtensionsForRequest(otherRequest, otherChat)
		assert.Error(t, err)
	}
	_, err = server.sessionExtensionEnvironmentOption(ctx, chatRequest, "conversation", registration.RunnerID)
	assert.ErrorContains(t, err, "original authenticated client connection", "raw attachment IDs cannot bypass HTTP admission")
	otherClient, err := chat.NewClient(httpServer.URL, "client-secret", registration.RunnerID)
	require.NoError(t, err)
	_, err = otherClient.AttachSessionExtensions(ctx, "conversation", registration.RunnerID, []string{"inline-1"}, func(context.Context, protocol.ExtensionFrame) error { return nil })
	assert.ErrorContains(t, err, "already has a live")

	frames := make(chan protocol.ExtensionFrame, 8)
	link.call = func(callCtx context.Context, method string, params any, result any) error {
		switch method {
		case protocol.MethodRunOpen:
			value := params.(protocol.RunOpenParams)
			frame := protocol.ExtensionFrame{AttachmentID: descriptor.ID, RunID: value.RunID, ExtensionID: "inline-1", Message: json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{}}`)}
			_, rpcErr := server.HandleRunnerExtensionFrame(callCtx, identity, mustRunnerJSON(t, frame))
			if rpcErr != nil {
				return rpcErr
			}
			manifest := runnerpayload.Manifest{
				ProtocolVersion: protocol.Version, RunnerID: registration.RunnerID, RunID: value.RunID,
				Generation: registration.Generation, WorkingDirectory: "/workspace", SessionExtensionIDs: []string{"inline-1"},
			}
			manifest.Digest, err = runnerpayload.ComputeManifestDigest(manifest)
			require.NoError(t, err)
			*result.(*runnerpayload.Manifest) = manifest
		case protocol.MethodSessionExtensionFrame:
			frames <- params.(protocol.ExtensionFrame)
		case protocol.MethodRunCancel, protocol.MethodRunClose:
		default:
			return errors.Errorf("unexpected method %s", method)
		}
		return nil
	}
	_, err = server.runnerRegistry.OpenRun(ctx, registration.RunnerID, protocol.RunOpenParams{
		RunID: "run", ConversationID: "conversation", SessionExtensions: &descriptor,
	})
	require.NoError(t, err)
	frame := <-incoming
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{}}`, string(frame.Message))
	frame.Message = json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	require.NoError(t, relay.SendFrame(ctx, frame))
	assert.Equal(t, frame, <-frames)
	frame.Message = json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tool.update","parentId":1,"params":{"content":"progress"}}`)
	require.NoError(t, relay.SendFrame(ctx, frame))
	assert.Equal(t, frame, <-frames, "inner parent IDs and updates survive unchanged")
	assert.Error(t, server.validateSessionExtensionRequirements("conversation", nil))
	require.NoError(t, server.validateSessionExtensionRequirements("conversation", []string{"inline-1"}))
	wrongIdentity := identity
	wrongIdentity.Generation++
	_, rpcErr := server.HandleRunnerExtensionFrame(ctx, wrongIdentity, mustRunnerJSON(t, frame))
	require.NotNil(t, rpcErr)

	require.NoError(t, relay.Close())
	select {
	case frame := <-frames:
		assert.True(t, frame.Close)
		assert.Equal(t, "run", frame.RunID)
	case <-ctx.Done():
		require.FailNow(t, "disconnected SDK did not close its runner channel")
	}
	assert.Error(t, attachment.ctx.Err())
	_, err = server.sessionExtensionsForRequest(request, chatRequest)
	assert.Error(t, err)
	_, rpcErr = server.HandleRunnerExtensionFrame(ctx, identity, mustRunnerJSON(t, frame))
	require.NotNil(t, rpcErr, "disconnected callback is not replayed")
	require.NoError(t, server.runnerRegistry.CloseRun(ctx, "run", protocol.RunStatusCanceled, nil))
}
