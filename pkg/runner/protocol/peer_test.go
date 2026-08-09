package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerSupportsSymmetricCallsAndNotifications(t *testing.T) {
	notifications := make(chan string, 1)
	requestIDs := make(chan string, 2)
	serverPeer, clientPeer := newTestPeerPair(t,
		PeerConfig{
			RequestPrefix: "server",
			Handler: RequestHandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
				requestIDs <- RequestIDFromContext(ctx)
				if method != "echo" {
					return nil, &RPCError{Code: ErrorCodeMethodNotFound, Message: "unknown"}
				}
				var input struct {
					Value string `json:"value"`
				}
				if err := json.Unmarshal(params, &input); err != nil {
					return nil, &RPCError{Code: ErrorCodeInvalidParams, Message: err.Error()}
				}
				return input, nil
			}),
			Notifications: NotificationHandlerFunc(func(_ context.Context, method string, _ json.RawMessage) {
				notifications <- method
			}),
		},
		PeerConfig{
			RequestPrefix: "runner",
			Handler: RequestHandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
				requestIDs <- RequestIDFromContext(ctx)
				if method != "double" {
					return nil, &RPCError{Code: ErrorCodeMethodNotFound, Message: "unknown"}
				}
				var input struct {
					Value int `json:"value"`
				}
				if err := json.Unmarshal(params, &input); err != nil {
					return nil, &RPCError{Code: ErrorCodeInvalidParams, Message: err.Error()}
				}
				return map[string]int{"value": input.Value * 2}, nil
			}),
		},
	)

	var echo struct {
		Value string `json:"value"`
	}
	require.NoError(t, clientPeer.Call(t.Context(), "echo", map[string]string{"value": "hello"}, &echo))
	assert.Equal(t, "hello", echo.Value)
	assert.Equal(t, "runner:1", <-requestIDs)

	var doubled struct {
		Value int `json:"value"`
	}
	require.NoError(t, serverPeer.Call(t.Context(), "double", map[string]int{"value": 21}, &doubled))
	assert.Equal(t, 42, doubled.Value)
	assert.Equal(t, "server:1", <-requestIDs)

	require.NoError(t, clientPeer.Notify(t.Context(), "runner.heartbeat", map[string]string{"state": "idle"}))
	select {
	case method := <-notifications:
		assert.Equal(t, "runner.heartbeat", method)
	case <-time.After(time.Second):
		t.Fatal("notification was not delivered")
	}
}

func TestPeerCallTrackedExposesInboundWireRequestID(t *testing.T) {
	remoteID := make(chan string, 1)
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{Handler: RequestHandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
			remoteID <- RequestIDFromContext(ctx)
			return map[string]bool{"ok": true}, nil
		})},
		PeerConfig{RequestPrefix: "tracked"},
	)

	var trackedID string
	require.NoError(t, clientPeer.CallTracked(t.Context(), "tracked.call", nil, nil, func(requestID string) {
		trackedID = requestID
	}))
	assert.Equal(t, "tracked:1", trackedID)
	assert.Equal(t, trackedID, <-remoteID)
}

func TestPeerBoundsRequestsWithoutStarvingControlCalls(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	serverPeer, clientPeer := newTestPeerPair(t,
		PeerConfig{
			MaxConcurrentRequests:        1,
			MaxConcurrentControlRequests: 1,
			Handler: RequestHandlerFunc(func(_ context.Context, method string, _ json.RawMessage) (any, *RPCError) {
				switch method {
				case "slow":
					close(slowStarted)
					<-releaseSlow
					return map[string]bool{"ok": true}, nil
				case MethodRunClose:
					return map[string]bool{"closed": true}, nil
				default:
					return map[string]bool{"ok": true}, nil
				}
			}),
		},
		PeerConfig{},
	)
	_ = serverPeer

	slowDone := make(chan error, 1)
	go func() {
		slowDone <- clientPeer.Call(t.Context(), "slow", nil, nil)
	}()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow request did not start")
	}

	err := clientPeer.Call(t.Context(), "second", nil, nil)
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ErrorCodeBusy, rpcErr.Code)

	require.NoError(t, clientPeer.Call(t.Context(), MethodRunClose, RunCloseParams{RunID: "run-one"}, nil))
	close(releaseSlow)
	require.NoError(t, <-slowDone)
}

func TestPeerOperationCancelBypassesNotificationLimit(t *testing.T) {
	notificationStarted := make(chan struct{})
	releaseNotification := make(chan struct{})
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{
			MaxConcurrentNotifications: 1,
			Handler: RequestHandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
				close(requestStarted)
				<-ctx.Done()
				close(requestCanceled)
				return nil, &RPCError{Code: ErrorCodeUnavailable, Message: ctx.Err().Error()}
			}),
			Notifications: NotificationHandlerFunc(func(context.Context, string, json.RawMessage) {
				close(notificationStarted)
				<-releaseNotification
			}),
		},
		PeerConfig{},
	)
	require.NoError(t, clientPeer.Notify(t.Context(), "notification.block", nil))
	select {
	case <-notificationStarted:
	case <-time.After(time.Second):
		t.Fatal("notification did not start")
	}

	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		callDone <- clientPeer.Call(ctx, "cancel-me", nil, nil)
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	require.ErrorIs(t, <-callDone, context.Canceled)
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("operation.cancel was blocked by notification handling")
	}
	close(releaseNotification)
}

func TestPeerPropagatesRPCErrors(t *testing.T) {
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{
			Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
				return nil, &RPCError{Code: ErrorCodeBusy, Message: "runner is busy"}
			}),
		},
		PeerConfig{},
	)

	err := clientPeer.Call(t.Context(), "run.open", map[string]string{"runId": "run-one"}, nil)
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ErrorCodeBusy, rpcErr.Code)
	assert.Equal(t, "runner is busy", rpcErr.Message)
}

func TestPeerCancellationCancelsRemoteRequest(t *testing.T) {
	remoteStarted := make(chan struct{})
	remoteCanceled := make(chan struct{})
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{
			Handler: RequestHandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
				close(remoteStarted)
				<-ctx.Done()
				close(remoteCanceled)
				return nil, &RPCError{Code: ErrorCodeUnavailable, Message: ctx.Err().Error()}
			}),
		},
		PeerConfig{},
	)

	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		callDone <- clientPeer.Call(ctx, "slow", nil, nil)
	}()
	select {
	case <-remoteStarted:
	case <-time.After(time.Second):
		t.Fatal("remote request was not started")
	}
	cancel()
	require.ErrorIs(t, <-callDone, context.Canceled)

	select {
	case <-remoteCanceled:
	case <-time.After(time.Second):
		t.Fatal("remote request was not canceled")
	}
}

func TestPeerValidationUpdatesAndShutdown(t *testing.T) {
	_, err := NewPeer(nil, PeerConfig{})
	require.ErrorContains(t, err, "connection is required")
	var nilPeer *Peer
	select {
	case <-nilPeer.Done():
	default:
		t.Fatal("nil peer Done channel should be closed")
	}
	assert.ErrorIs(t, nilPeer.Err(), ErrPeerClosed)
	require.NoError(t, nilPeer.Close())
	require.NoError(t, nilPeer.Shutdown(t.Context(), 0, "ignored"))
	assert.ErrorIs(t, nilPeer.Call(t.Context(), "method", nil, nil), ErrPeerClosed)

	notifications := make(chan string, 2)
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{Notifications: NotificationHandlerFunc(func(_ context.Context, method string, _ json.RawMessage) {
			notifications <- method
		})},
		PeerConfig{},
	)
	require.ErrorContains(t, clientPeer.Start(t.Context()), "already started")
	require.ErrorContains(t, clientPeer.Call(t.Context(), "", nil, nil), "method is required")
	require.ErrorContains(t, clientPeer.Notify(t.Context(), "", nil), "method is required")
	require.Error(t, clientPeer.Call(t.Context(), "method", func() {}, nil))
	require.Error(t, clientPeer.Notify(t.Context(), "method", func() {}))
	require.NoError(t, clientPeer.NotifyUpdate("tool.update", map[string]string{"value": "one"}))
	select {
	case method := <-notifications:
		assert.Equal(t, "tool.update", method)
	case <-time.After(time.Second):
		t.Fatal("update notification was not delivered")
	}

	require.NoError(t, clientPeer.Shutdown(t.Context(), websocket.CloseNormalClosure, "done"))
	select {
	case <-clientPeer.Done():
	case <-time.After(time.Second):
		t.Fatal("peer did not terminate after shutdown")
	}
	assert.Error(t, clientPeer.Err())
	assert.Error(t, clientPeer.NotifyUpdate("tool.update", nil))
}

func TestPeerReturnsHandlerAndDecodeFailures(t *testing.T) {
	t.Run("missing handler", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{}, PeerConfig{})
		err := clientPeer.Call(t.Context(), "missing", nil, nil)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, ErrorCodeMethodNotFound, rpcErr.Code)
	})

	t.Run("handler panic", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{
			Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
				panic("boom")
			}),
		}, PeerConfig{})
		err := clientPeer.Call(t.Context(), "panic", nil, nil)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, ErrorCodeInternal, rpcErr.Code)
		assert.Contains(t, rpcErr.Message, "panic")
	})

	t.Run("result decode", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{
			Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
				return map[string]string{"value": "not-an-int"}, nil
			}),
		}, PeerConfig{})
		var result struct {
			Value int `json:"value"`
		}
		err := clientPeer.Call(t.Context(), "decode", nil, &result)
		require.ErrorContains(t, err, "decode runner rpc result")
	})

	t.Run("result encode", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{
			Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
				return func() {}, nil
			}),
		}, PeerConfig{})
		err := clientPeer.Call(t.Context(), "encode", nil, nil)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, ErrorCodeInternal, rpcErr.Code)
	})
}

func TestPeerRejectsOversizedMessagesWithoutClosingConnection(t *testing.T) {
	t.Run("result", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{
			WriteLimit: 512,
			Handler: RequestHandlerFunc(func(_ context.Context, method string, _ json.RawMessage) (any, *RPCError) {
				if method == "large" {
					return map[string]string{"value": strings.Repeat("x", 2048)}, nil
				}
				return map[string]string{"value": "ok"}, nil
			}),
		}, PeerConfig{})

		err := clientPeer.Call(t.Context(), "large", nil, nil)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, ErrorCodeUnavailable, rpcErr.Code)
		assert.Contains(t, rpcErr.Message, "message-size limit")

		var result map[string]string
		require.NoError(t, clientPeer.Call(t.Context(), "small", nil, &result))
		assert.Equal(t, "ok", result["value"])
	})

	t.Run("request", func(t *testing.T) {
		_, clientPeer := newTestPeerPair(t, PeerConfig{
			Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
				return map[string]bool{"ok": true}, nil
			}),
		}, PeerConfig{WriteLimit: 256})

		err := clientPeer.Call(t.Context(), "large", map[string]string{"value": strings.Repeat("x", 1024)}, nil)
		require.ErrorContains(t, err, "outbound limit")
		require.NoError(t, clientPeer.Call(t.Context(), "small", nil, nil))
	})
}

func TestPeerDoneWaitsForInboundHandlers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	serverPeer, clientPeer := newTestPeerPair(t, PeerConfig{
		Notifications: NotificationHandlerFunc(func(context.Context, string, json.RawMessage) {
			close(started)
			<-release
		}),
	}, PeerConfig{})

	require.NoError(t, clientPeer.Notify(t.Context(), "block", nil))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("notification handler did not start")
	}
	require.NoError(t, serverPeer.Close())
	select {
	case <-serverPeer.TransportDone():
	case <-time.After(time.Second):
		t.Fatal("transport did not terminate")
	}
	select {
	case <-serverPeer.Done():
		t.Fatal("peer reported handler completion before the handler returned")
	default:
	}
	close(release)
	select {
	case <-serverPeer.Done():
	case <-time.After(time.Second):
		t.Fatal("peer did not finish after its handler returned")
	}
}

func TestPeerDefaultsAndMarshalRPCValue(t *testing.T) {
	config := withPeerDefaults(PeerConfig{PongWait: 3 * time.Second, PingPeriod: 4 * time.Second})
	assert.Equal(t, "rpc", config.RequestPrefix)
	assert.Positive(t, config.ControlQueueSize)
	assert.Positive(t, config.UpdateQueueSize)
	assert.Equal(t, 2*time.Second, config.PingPeriod)
	assert.Positive(t, config.ReadLimit)
	assert.Equal(t, config.ReadLimit, config.WriteLimit)
	assert.Positive(t, config.MaxConcurrentRequests)
	assert.Positive(t, config.MaxConcurrentControlRequests)
	assert.Positive(t, config.MaxConcurrentNotifications)

	payload, err := marshalRPCValue(json.RawMessage(`{"value":1}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":1}`, string(payload))
	payload, err = marshalRPCValue(json.RawMessage(nil))
	require.NoError(t, err)
	assert.Equal(t, "null", string(payload))
}

func newTestPeerPair(t *testing.T, serverConfig, clientConfig PeerConfig) (*Peer, *Peer) {
	t.Helper()
	serverPeerCh := make(chan *Peer, 1)
	serverErrCh := make(chan error, 1)
	upgrader := websocket.Upgrader{
		Subprotocols: []string{Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		peer, err := NewPeer(conn, serverConfig)
		if err != nil {
			serverErrCh <- err
			return
		}
		if err := peer.Start(t.Context()); err != nil {
			serverErrCh <- err
			return
		}
		serverPeerCh <- peer
		<-peer.Done()
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{Subprotocols: []string{Subprotocol}}
	conn, response, err := dialer.DialContext(t.Context(), wsURL, nil)
	if response != nil && response.Body != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	require.NoError(t, err)
	require.Equal(t, Subprotocol, conn.Subprotocol())

	clientPeer, err := NewPeer(conn, clientConfig)
	require.NoError(t, err)
	require.NoError(t, clientPeer.Start(t.Context()))

	var serverPeer *Peer
	select {
	case serverPeer = <-serverPeerCh:
	case err := <-serverErrCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("server peer was not created")
	}

	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = serverPeer.Close()
	})
	return serverPeer, clientPeer
}
