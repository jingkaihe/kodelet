package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func extensionRelayServer(t *testing.T, handler protocol.RequestHandler) (*Client, <-chan *protocol.Peer) {
	t.Helper()
	peers := make(chan *protocol.Peer, 1)
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/prefix"+protocol.SessionExtensionsEndpoint, r.URL.Path)
		headers <- r.Header.Clone()
		upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SessionExtensionsSubprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		peer, err := protocol.NewPeer(conn, protocol.PeerConfig{Handler: handler})
		if !assert.NoError(t, err) {
			_ = conn.Close()
			return
		}
		assert.NoError(t, peer.Start(t.Context()))
		t.Cleanup(func() { _ = peer.Close() })
		peers <- peer
		<-peer.TransportDone()
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/prefix", "client-secret", "runner-1")
	require.NoError(t, err)
	t.Cleanup(func() {
		select {
		case header := <-headers:
			assert.Equal(t, "Bearer client-secret", header.Get("Authorization"))
			assert.Equal(t, client.clientID, header.Get(ClientIDHeader))
		default:
			t.Error("relay did not connect")
		}
	})
	return client, peers
}

func TestSessionExtensionRelayDuplex(t *testing.T) {
	attachment := protocol.SessionExtensions{ID: "attachment-1", ExtensionIDs: []string{"inline-1"}}
	frames := make(chan protocol.ExtensionFrame, 4)
	client, peers := extensionRelayServer(t, protocol.RequestHandlerFunc(func(_ context.Context, method string, params json.RawMessage) (any, *protocol.RPCError) {
		if method == protocol.MethodSessionExtensionsAttach {
			assert.JSONEq(t, `{"conversationId":"conversation-1","runnerId":"runner-1","extensionIds":["inline-1"]}`, string(params))
			return attachment, nil
		}
		assert.Equal(t, protocol.MethodSessionExtensionFrame, method)
		var frame protocol.ExtensionFrame
		assert.NoError(t, json.Unmarshal(params, &frame))
		frames <- frame
		return struct{}{}, nil
	}))
	started := make(chan protocol.ExtensionFrame, 1)
	release := make(chan struct{})
	relay, err := client.AttachSessionExtensions(t.Context(), "conversation-1", "runner-1", attachment.ExtensionIDs, func(ctx context.Context, frame protocol.ExtensionFrame) error {
		started <- frame
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	assert.Equal(t, attachment, relay.Attachment())
	copy := relay.Attachment()
	copy.ExtensionIDs[0] = "changed"
	assert.Equal(t, attachment, relay.Attachment())
	peer := <-peers
	frame := protocol.ExtensionFrame{AttachmentID: attachment.ID, RunID: "run-1", ExtensionID: "inline-1", Message: json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	// The acknowledgement must not wait for the callback to finish.
	require.NoError(t, peer.Call(ctx, protocol.MethodSessionExtensionFrame, frame, nil))
	assert.Equal(t, frame, <-started)
	response := frame
	response.Message = json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	require.NoError(t, relay.SendFrame(ctx, response))
	assert.Equal(t, response, <-frames)
	foreign := frame
	foreign.AttachmentID = "another-session"
	require.ErrorContains(t, relay.SendFrame(ctx, foreign), "does not belong")
	require.ErrorContains(t, peer.Call(ctx, protocol.MethodSessionExtensionFrame, foreign, nil), "does not belong")
	require.ErrorContains(t, peer.Call(ctx, "unknown", frame, nil), "not found")
	close(release)
	require.NoError(t, peer.Close())
	select {
	case <-relay.Done():
	case <-ctx.Done():
		t.Fatal("relay did not close on server disconnect")
	}
	require.Error(t, relay.SendFrame(ctx, response))
}

func TestSessionExtensionRelayHandshakeErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		result any
		rpcErr *protocol.RPCError
		want   string
	}{
		{name: "invalid attachment", result: protocol.SessionExtensions{ID: "a", ExtensionIDs: []string{"different"}}, want: "invalid session-extension attachment"},
		{name: "server rejection", rpcErr: &protocol.RPCError{Code: protocol.ErrorCodeConflict, Message: "runner policy denied inline tools", Data: map[string]any{"reason": "policy"}}, want: "runner policy denied inline tools"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, peers := extensionRelayServer(t, protocol.RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *protocol.RPCError) {
				return test.result, test.rpcErr
			}))
			relay, err := client.AttachSessionExtensions(t.Context(), "conversation-1", "runner-1", []string{"inline-1"}, func(context.Context, protocol.ExtensionFrame) error { return nil })
			require.Nil(t, relay)
			require.ErrorContains(t, err, test.want)
			if test.rpcErr != nil {
				var rpcErr *protocol.RPCError
				require.ErrorAs(t, err, &rpcErr)
				assert.Equal(t, test.rpcErr, rpcErr)
			}
			select {
			case <-(<-peers).TransportDone():
			case <-time.After(time.Second):
				t.Fatal("failed attachment left websocket open")
			}
		})
	}
}

func TestSessionExtensionRelayCallbackFailureAndCancellation(t *testing.T) {
	for _, callbackError := range []bool{false, true} {
		t.Run(map[bool]string{false: "context cancellation", true: "callback failure"}[callbackError], func(t *testing.T) {
			client, peers := extensionRelayServer(t, protocol.RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *protocol.RPCError) {
				return protocol.SessionExtensions{ID: "attachment-1", ExtensionIDs: []string{"inline-1"}}, nil
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			relay, err := client.AttachSessionExtensions(ctx, "conversation-1", "runner-1", []string{"inline-1"}, func(context.Context, protocol.ExtensionFrame) error { return errors.New("SDK connection failed") })
			require.NoError(t, err)
			t.Cleanup(func() { _ = relay.Close() })
			peer := <-peers
			if callbackError {
				// The queued frame is accepted; subsequent delivery failure is terminal.
				_ = peer.Call(ctx, protocol.MethodSessionExtensionFrame, protocol.ExtensionFrame{AttachmentID: "attachment-1", RunID: "run-1", ExtensionID: "inline-1", Close: true}, nil)
			} else {
				cancel()
			}
			select {
			case <-relay.Done():
			case <-time.After(time.Second):
				t.Fatal("relay stayed open")
			}
			if callbackError {
				assert.ErrorContains(t, relay.Err(), "SDK connection failed")
			}
		})
	}
}
