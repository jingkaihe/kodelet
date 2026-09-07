package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type extensionACPOutput chan json.RawMessage

func (o extensionACPOutput) Write(p []byte) (int, error) {
	o <- append(json.RawMessage(nil), p...)
	return len(p), nil
}

func (o extensionACPOutput) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	select {
	case data := <-o:
		var message map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &message))
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ACP output")
		return nil
	}
}

type fakeSessionExtensionRelay struct {
	attachment protocol.SessionExtensions
	ctx        context.Context
	cancel     context.CancelFunc
	handler    func(context.Context, protocol.ExtensionFrame) error
	frames     chan protocol.ExtensionFrame
}

func (r *fakeSessionExtensionRelay) Attachment() protocol.SessionExtensions { return r.attachment }
func (r *fakeSessionExtensionRelay) SendFrame(_ context.Context, frame protocol.ExtensionFrame) error {
	if err := frame.Validate(); err != nil {
		return err
	}
	r.frames <- frame
	return nil
}
func (r *fakeSessionExtensionRelay) Close() error          { r.cancel(); return nil }
func (r *fakeSessionExtensionRelay) Done() <-chan struct{} { return r.ctx.Done() }
func (r *fakeSessionExtensionRelay) Err() error            { return r.ctx.Err() }

type fakeSessionExtensionsClient struct {
	fakeRemoteChatClient
	attach func(context.Context) error
	relays chan *fakeSessionExtensionRelay
}

func (c *fakeSessionExtensionsClient) AttachSessionExtensions(ctx context.Context, conversationID, runnerID string, extensionIDs []string, handler func(context.Context, protocol.ExtensionFrame) error) (chat.SessionExtensionRelay, error) {
	if c.attach != nil {
		if err := c.attach(ctx); err != nil {
			return nil, err
		}
	}
	relayCtx, cancel := context.WithCancel(ctx)
	relay := &fakeSessionExtensionRelay{attachment: protocol.SessionExtensions{ID: "attachment-" + conversationID, ExtensionIDs: extensionIDs}, ctx: relayCtx, cancel: cancel, handler: handler, frames: make(chan protocol.ExtensionFrame, 8)}
	c.relays <- relay
	return relay, nil
}

func newExtensionACPServer(t *testing.T, client RemoteChatClient) (*Server, extensionACPOutput) {
	t.Helper()
	output := make(extensionACPOutput, 32)
	server := NewServer(WithInput(bytes.NewReader(nil)), WithOutput(output), WithContext(t.Context()), WithRemoteSessions(RemoteSessionConfig{Provider: staticRemoteChatProvider{client: client, runnerID: "runner-1"}}))
	t.Cleanup(server.Shutdown)
	require.NoError(t, server.handleInitialize(&acptypes.Request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"protocolVersion":1,"clientCapabilities":{"_meta":{"sessionExtensions":{"version":1}}}}`)}))
	var response acptypes.InitializeResponse
	require.NoError(t, json.Unmarshal(output.next(t)["result"], &response))
	assert.Equal(t, map[string]any{"version": float64(1)}, response.Meta["sessionExtensions"])
	return server, output
}

func extensionSessionMeta(ids ...string) map[string]any {
	return map[string]any{"sessionExtensions": map[string]any{"version": 1, "extensionIds": ids}}
}

func TestACPSessionExtensionNegotiation(t *testing.T) {
	server, output := newExtensionACPServer(t, &fakeRemoteChatClient{})
	for _, value := range []any{nil, "invalid", map[string]any{"version": 2, "extensionIds": []string{"inline-1"}}, map[string]any{"version": 1, "extensionIds": []string{"same", "same"}}, map[string]any{"version": 1, "extensionIds": []string{"../bad"}}} {
		_, err := server.requestedSessionExtensions(map[string]any{"sessionExtensions": value})
		require.Error(t, err)
	}
	ids, err := server.requestedSessionExtensions(extensionSessionMeta("inline-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"inline-1"}, ids)
	server.clientCapsMu.Lock()
	server.clientCaps.Meta = nil
	server.clientCapsMu.Unlock()
	_, err = server.requestedSessionExtensions(extensionSessionMeta("inline-1"))
	require.ErrorContains(t, err, "clientCapabilities")
	require.NoError(t, server.handleSessionNew(&acptypes.Request{ID: json.RawMessage(`2`), Params: mustJSONRawMessage(t, acptypes.NewSessionRequest{Meta: extensionSessionMeta("inline-1")})}))
	assert.Contains(t, string(output.next(t)["error"]), "clientCapabilities")
	assert.Empty(t, server.remoteSessions.sessions)
}

func TestACPSessionExtensionAttachmentAndIsolation(t *testing.T) {
	client := &fakeSessionExtensionsClient{relays: make(chan *fakeSessionExtensionRelay, 2)}
	server, output := newExtensionACPServer(t, client)
	firstID, err := server.remoteSessions.newSession(t.Context(), acptypes.NewSessionRequest{CWD: "/workspace", Meta: extensionSessionMeta("inline-1")})
	require.NoError(t, err)
	first := <-client.relays
	secondID, err := server.remoteSessions.newSession(t.Context(), acptypes.NewSessionRequest{CWD: "/workspace", Meta: extensionSessionMeta("inline-1")})
	require.NoError(t, err)
	second := <-client.relays
	_, target := server.remoteSessions.promptTarget(firstID)
	assert.Equal(t, first.attachment.ID, target.SessionExtensionsID)
	_, target = server.remoteSessions.promptTarget(secondID)
	assert.Equal(t, second.attachment.ID, target.SessionExtensionsID)
	assert.NotEqual(t, first.attachment.ID, second.attachment.ID)
	// The relay lifetime is independent of the session's temporary setup context.
	require.NoError(t, first.ctx.Err())

	frame := protocol.ExtensionFrame{AttachmentID: first.attachment.ID, RunID: "run-first", ExtensionID: "inline-1", Message: json.RawMessage(`{"jsonrpc":"2.0","id":7,"method":"initialize"}`)}
	delivered := make(chan error, 1)
	go func() { delivered <- first.handler(first.ctx, frame) }()
	reverse := output.next(t)
	assert.JSONEq(t, `"kodelet/extensionFrame"`, string(reverse["method"]))
	var sdkFrame sessionExtensionFrame
	require.NoError(t, json.Unmarshal(reverse["params"], &sdkFrame))
	assert.Equal(t, firstID, sdkFrame.SessionID)
	assert.Equal(t, frame.Message, sdkFrame.Message)
	// Nested host replies can flow before the SDK acknowledges frame receipt.
	sdkFrame.Message = json.RawMessage(`{"jsonrpc":"2.0","id":7,"result":{}}`)
	require.NoError(t, server.handleSessionExtensionFrame(&acptypes.Request{ID: json.RawMessage(`10`), Params: mustJSONRawMessage(t, sdkFrame)}))
	assert.JSONEq(t, `{}`, string(output.next(t)["result"]))
	assert.Equal(t, sdkFrame.Message, (<-first.frames).Message)
	for _, foreign := range []sessionExtensionFrame{
		{SessionID: "unknown", RunID: frame.RunID, ExtensionID: frame.ExtensionID, Message: sdkFrame.Message},
		{SessionID: secondID, RunID: frame.RunID, ExtensionID: frame.ExtensionID, Message: sdkFrame.Message},
		{SessionID: firstID, RunID: "unknown", ExtensionID: frame.ExtensionID, Message: sdkFrame.Message},
		{SessionID: firstID, RunID: frame.RunID, ExtensionID: "unknown", Message: sdkFrame.Message},
	} {
		require.NoError(t, server.handleSessionExtensionFrame(&acptypes.Request{ID: json.RawMessage(`11`), Params: mustJSONRawMessage(t, foreign)}))
		assert.NotEmpty(t, output.next(t)["error"])
	}
	assert.Empty(t, second.frames)
	require.NoError(t, server.handleResponse(reverse["id"], json.RawMessage(`{}`), nil))
	require.NoError(t, <-delivered)
	// No live callback path or discovery target is persisted in the chat request.
	data, err := json.Marshal(target)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "inline-1")
}

func TestACPSessionExtensionAttachFailureAndResume(t *testing.T) {
	client := &fakeSessionExtensionsClient{relays: make(chan *fakeSessionExtensionRelay, 4), attach: func(context.Context) error { return errors.New("daemon denied attachment") }}
	server, output := newExtensionACPServer(t, client)
	require.NoError(t, server.handleSessionNew(&acptypes.Request{ID: json.RawMessage(`2`), Params: mustJSONRawMessage(t, acptypes.NewSessionRequest{Meta: extensionSessionMeta("inline-1")})}))
	assert.Contains(t, string(output.next(t)["error"]), "daemon denied attachment")
	assert.Empty(t, server.remoteSessions.sessions)
	client.attach = nil
	client.history = chat.ConversationHistory{ID: "saved-session", RunnerID: "runner-1", CWD: "/workspace"}
	setupCtx, cancel := context.WithCancel(t.Context())
	_, err := server.remoteSessions.loadSession(setupCtx, acptypes.LoadSessionRequest{SessionID: "saved-session", Meta: extensionSessionMeta("inline-1")})
	require.NoError(t, err)
	relay := <-client.relays
	cancel()
	require.NoError(t, relay.ctx.Err())
	_, request := server.remoteSessions.promptTarget("saved-session")
	assert.Equal(t, relay.attachment.ID, request.SessionExtensionsID)
	_, err = server.remoteSessions.loadSession(t.Context(), acptypes.LoadSessionRequest{SessionID: "saved-session", Meta: extensionSessionMeta("inline-1")})
	require.NoError(t, err)
	assert.ErrorIs(t, relay.ctx.Err(), context.Canceled)
	assert.NotSame(t, relay, <-client.relays)
}

func TestACPSessionExtensionEOFAndDisconnect(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(map[bool]string{false: "relay disconnect", true: "stdio EOF"}[eof], func(t *testing.T) {
			client := &fakeSessionExtensionsClient{relays: make(chan *fakeSessionExtensionRelay, 1)}
			server, output := newExtensionACPServer(t, client)
			id, err := server.remoteSessions.newSession(t.Context(), acptypes.NewSessionRequest{Meta: extensionSessionMeta("inline-1")})
			require.NoError(t, err)
			relay := <-client.relays
			_, err = server.remoteSessions.beginPrompt(id)
			require.NoError(t, err)
			promptCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stopped := make(chan struct{})
			var once sync.Once
			client.stopTurn = func(conversation, turn string) {
				assert.Equal(t, string(id), conversation)
				assert.Equal(t, "turn-1", turn)
				once.Do(func() { close(stopped) })
			}
			server.activePromptsMu.Lock()
			server.activePrompts[id] = &activePrompt{ctx: promptCtx, cancel: cancel, remoteClient: client, remoteStarted: true, turnID: "turn-1"}
			server.activePromptsMu.Unlock()
			pending := make(chan error, 1)
			go func() { _, err := server.CallClient(relay.ctx, "client/pending", nil); pending <- err }()
			output.next(t)
			if eof {
				require.NoError(t, server.Run())
			} else {
				require.NoError(t, relay.Close())
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("callback-dependent prompt was not stopped")
			}
			assert.ErrorIs(t, promptCtx.Err(), context.Canceled)
			require.Error(t, <-pending)
			server.pendingMu.Lock()
			assert.Empty(t, server.pendingRequests)
			server.pendingMu.Unlock()
			server.remoteSessions.finishPrompt(id, false)
			_, err = server.remoteSessions.beginPrompt(id)
			require.ErrorContains(t, err, "disconnected")
		})
	}
}

func TestACPSessionExtensionClientErrorDetails(t *testing.T) {
	server, output := newExtensionACPServer(t, &fakeRemoteChatClient{})
	result := make(chan error, 1)
	go func() { _, err := server.CallClient(t.Context(), sessionExtensionFrameMethod, nil); result <- err }()
	request := output.next(t)
	require.NoError(t, server.handleResponse(request["id"], nil, &acptypes.RPCError{Code: -32001, Message: "unknown inline extension", Data: json.RawMessage(`{"extensionId":"inline-9"}`)}))
	err := <-result
	require.ErrorContains(t, err, "unknown inline extension")
	var rpcErr *protocol.RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, -32001, rpcErr.Code)
	assert.Equal(t, json.RawMessage(`{"extensionId":"inline-9"}`), rpcErr.Data)
}

func TestACPSessionExtensionPromptDisconnectIsNotCancellation(t *testing.T) {
	for _, test := range []struct {
		name       string
		disconnect bool
		done       bool
	}{
		{name: "relay lost while running", disconnect: true},
		{name: "relay lost with cancelled terminal event", disconnect: true, done: true},
		{name: "explicit cancellation with live relay", done: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeSessionExtensionsClient{relays: make(chan *fakeSessionExtensionRelay, 1)}
			server, output := newExtensionACPServer(t, client)
			id, err := server.remoteSessions.newSession(t.Context(), acptypes.NewSessionRequest{Meta: extensionSessionMeta("inline-1")})
			require.NoError(t, err)
			relay := <-client.relays
			client.run = func(ctx context.Context, request chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
				if test.disconnect {
					require.NoError(t, relay.Close())
				} else {
					server.cancelRemotePrompt(id)
				}
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("prompt was not cancelled")
				}
				if test.done {
					require.NoError(t, sink.Send(chat.ChatEvent{Kind: "done", Cancelled: true}))
				}
				return request.ConversationID, ctx.Err()
			}
			require.NoError(t, server.handleSessionPrompt(&acptypes.Request{ID: json.RawMessage(`2`), Params: mustJSONRawMessage(t, acptypes.PromptRequest{SessionID: id, Prompt: []acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "call the inline tool"}}})}))
			response := output.next(t)
			if test.disconnect {
				assert.Empty(t, response["result"])
				assert.Contains(t, string(response["error"]), "inline extension relay lost")
				assert.Contains(t, string(response["error"]), "callback side effects may be uncertain")
				assert.Contains(t, string(response["error"]), "do not retry or reattach automatically")
			} else {
				assert.Empty(t, response["error"])
				assert.JSONEq(t, `{"stopReason":"cancelled"}`, string(response["result"]))
				require.NoError(t, relay.ctx.Err())
			}
		})
	}
}

type disconnectOnAttachmentRelay struct {
	*fakeSessionExtensionRelay
	disconnect func()
}

func (r *disconnectOnAttachmentRelay) Attachment() protocol.SessionExtensions {
	r.disconnect()
	return r.fakeSessionExtensionRelay.Attachment()
}

func TestACPSessionExtensionPromptDisconnectBeforeSubmission(t *testing.T) {
	client := &fakeSessionExtensionsClient{relays: make(chan *fakeSessionExtensionRelay, 1)}
	server, output := newExtensionACPServer(t, client)
	id, err := server.remoteSessions.newSession(t.Context(), acptypes.NewSessionRequest{Meta: extensionSessionMeta("inline-1")})
	require.NoError(t, err)
	relay := <-client.relays
	server.remoteSessions.mu.Lock()
	server.remoteSessions.sessions[id].extensionRelay = &disconnectOnAttachmentRelay{fakeSessionExtensionRelay: relay, disconnect: func() {
		require.NoError(t, relay.Close())
		// Deterministically lose the attachment after beginPrompt, before submission.
		server.prepareRemotePromptCancellation(id)
	}}
	server.remoteSessions.mu.Unlock()
	require.NoError(t, server.handleSessionPrompt(&acptypes.Request{ID: json.RawMessage(`2`), Params: mustJSONRawMessage(t, acptypes.PromptRequest{SessionID: id})}))
	response := output.next(t)
	assert.Empty(t, response["result"])
	assert.Contains(t, string(response["error"]), "inline extension relay lost")
	assert.Empty(t, client.recordedRequests())
}
