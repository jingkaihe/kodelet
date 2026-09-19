package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

func TestPeerQueuesRequestsWithoutStarvingControlCalls(t *testing.T) {
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

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- clientPeer.Call(t.Context(), "second", nil, nil)
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("queued request completed before capacity was released: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	require.NoError(t, clientPeer.Call(t.Context(), MethodRunClose, RunCloseParams{RunID: "run-one"}, nil))
	frameCtx, cancelFrame := context.WithTimeout(t.Context(), time.Second)
	defer cancelFrame()
	require.NoError(t, clientPeer.Call(frameCtx, MethodSessionExtensionFrame, ExtensionFrame{
		AttachmentID: "attachment", RunID: "run-one", ExtensionID: "inline-1", Message: json.RawMessage(`{"id":1,"result":{}}`),
	}, nil), "callback replies must make progress while ordinary request slots are occupied")
	close(releaseSlow)
	require.NoError(t, <-slowDone)
	require.NoError(t, <-secondDone)
}

func TestPeerQueuesControlCallsInsteadOfRejectingThem(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{
			MaxConcurrentControlRequests: 1,
			Handler: RequestHandlerFunc(func(_ context.Context, method string, _ json.RawMessage) (any, *RPCError) {
				require.Equal(t, MethodRunClose, method)
				if calls.Add(1) == 1 {
					close(firstStarted)
					<-releaseFirst
				}
				return map[string]bool{"closed": true}, nil
			}),
		},
		PeerConfig{},
	)

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		firstDone <- clientPeer.Call(t.Context(), MethodRunClose, RunCloseParams{RunID: "run-one"}, nil)
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first control request did not start")
	}
	go func() {
		secondDone <- clientPeer.Call(t.Context(), MethodRunClose, RunCloseParams{RunID: "run-two"}, nil)
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("queued control request completed before capacity was released: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, int32(2), calls.Load())
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
	for _, test := range []struct {
		name           string
		captureContent bool
	}{
		{name: "metadata only by default"},
		{name: "content capture enabled", captureContent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := recordPeerSpans(t)
			// Keep tracing disabled here: only configure content policy, without a live exporter.
			_, err := telemetry.InitTracer(t.Context(), telemetry.Config{CaptureContent: test.captureContent})
			require.NoError(t, err)
			const errorMessage = "runner is busy: private tool output"
			_, clientPeer := newTestPeerPair(t,
				PeerConfig{
					Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
						return nil, &RPCError{Code: ErrorCodeBusy, Message: errorMessage}
					}),
				},
				PeerConfig{},
			)

			err = clientPeer.Call(t.Context(), "run.open", map[string]string{"runId": "run-one"}, nil)
			var rpcErr *RPCError
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, ErrorCodeBusy, rpcErr.Code)
			assert.Equal(t, errorMessage, rpcErr.Message, "telemetry policy must not change the RPC error")
			require.Eventually(t, func() bool { return len(recorder.Ended()) == 2 }, time.Second, time.Millisecond)
			for _, span := range recorder.Ended() {
				assert.Equal(t, codes.Error, span.Status().Code)
				assert.Contains(t, span.Attributes(), attribute.Int("rpc.jsonrpc.error_code", ErrorCodeBusy))
				assert.Contains(t, span.Attributes(), attribute.String("error.type", "rpc_error"))
				if test.captureContent {
					assert.Equal(t, rpcErr.Error(), span.Status().Description)
					require.Len(t, span.Events(), 1)
					assert.Equal(t, "exception", span.Events()[0].Name)
					assert.Contains(t, span.Events()[0].Attributes, attribute.String("exception.message", rpcErr.Error()))
				} else {
					assert.Equal(t, "rpc_error", span.Status().Description)
					assert.Empty(t, span.Events(), "raw exception messages must not bypass content policy")
					attrs, err := json.Marshal(span.Attributes())
					require.NoError(t, err)
					assert.NotContains(t, string(attrs), errorMessage)
				}
			}
		})
	}
}

func TestPeerCancellationCancelsRemoteRequest(t *testing.T) {
	recorder := recordPeerSpans(t)
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
	require.Eventually(t, func() bool { return len(recorder.Ended()) == 2 }, time.Second, time.Millisecond)
	for _, span := range recorder.Ended() {
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Contains(t, span.Attributes(), attribute.String("error.type", "cancelled"))
		assert.Equal(t, "cancelled", span.Status().Description)
		assert.Empty(t, span.Events())
		assert.Equal(t, "runner.rpc _OTHER", span.Name())
		assert.Contains(t, span.Attributes(), attribute.String("rpc.method", "_OTHER"))
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
		assert.Equal(t, ErrorReasonResultTooLarge, rpcErr.Reason())
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

func TestPeerShutdownBoundsHandlerDrainWithoutCallerDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	serverPeer, clientPeer := newTestPeerPair(t, PeerConfig{
		ShutdownWait: 20 * time.Millisecond,
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
	err := serverPeer.Shutdown(context.Background(), websocket.CloseNormalClosure, "done")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	select {
	case <-serverPeer.Done():
	case <-time.After(time.Second):
		t.Fatal("peer did not finish after blocked handler returned")
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

func TestPeerTracingParentsPerCall(t *testing.T) {
	recorder := recordPeerSpans(t)
	tracer := otel.Tracer("test")
	connectionCtx, connectionSpan := tracer.Start(t.Context(), "websocket")
	defer connectionSpan.End()
	handler := RequestHandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
		_, span := tracer.Start(ctx, "tool.execution")
		defer span.End()
		return "private result", nil
	})
	serverPeer, clientPeer := newTestPeerPair(t,
		PeerConfig{Handler: handler}, PeerConfig{Handler: handler}, connectionCtx,
	)

	state, err := trace.ParseTraceState("vendor=value")
	require.NoError(t, err)
	parents := make([]trace.SpanContext, 0, 2)
	results := make(chan error, 2)
	methods := []string{MethodToolExecute, "model.helper.execute"}
	for i, method := range methods {
		ctx, span := tracer.Start(t.Context(), "invocation")
		defer span.End()
		parent := span.SpanContext().WithTraceState(state)
		parents = append(parents, parent)
		ctx = trace.ContextWithSpanContext(ctx, parent)
		peer := clientPeer
		if i == 1 {
			peer = serverPeer
		}
		go func() {
			results <- peer.Call(ctx, method, map[string]string{"input": "private input"}, nil)
		}()
	}
	for range 2 {
		require.NoError(t, <-results)
	}
	require.Eventually(t, func() bool { return len(recorder.Ended()) == 6 }, time.Second, time.Millisecond)
	assert.NotEqual(t, parents[0].TraceID(), parents[1].TraceID())
	for i, parent := range parents {
		var clientSpan, serverSpan, toolSpan sdktrace.ReadOnlySpan
		for _, span := range recorder.Ended() {
			if span.SpanContext().TraceID() != parent.TraceID() {
				continue
			}
			switch span.SpanKind() {
			case trace.SpanKindClient:
				clientSpan = span
			case trace.SpanKindServer:
				serverSpan = span
			default:
				toolSpan = span
			}
		}
		require.NotNil(t, clientSpan)
		require.NotNil(t, serverSpan)
		require.NotNil(t, toolSpan)
		assert.Equal(t, parent.SpanID(), clientSpan.Parent().SpanID())
		assert.Equal(t, clientSpan.SpanContext().SpanID(), serverSpan.Parent().SpanID())
		assert.Equal(t, serverSpan.SpanContext().SpanID(), toolSpan.Parent().SpanID())
		assert.True(t, serverSpan.Parent().IsRemote())
		assert.Equal(t, state, serverSpan.Parent().TraceState())
		assert.NotEqual(t, connectionSpan.SpanContext().TraceID(), serverSpan.SpanContext().TraceID())
		for _, span := range []sdktrace.ReadOnlySpan{clientSpan, serverSpan} {
			assert.Equal(t, "runner.rpc "+methods[i], span.Name())
			assert.Equal(t, codes.Unset, span.Status().Code)
			assert.ElementsMatch(t, []attribute.KeyValue{
				attribute.String("rpc.system", "jsonrpc"),
				attribute.String("rpc.service", "kodelet.runner"),
				attribute.String("rpc.method", methods[i]),
			}, span.Attributes(), "RPC spans must not capture params or results")
			assert.Empty(t, span.Events())
		}
	}
}

func TestPeerTracingWithoutValidCarrier(t *testing.T) {
	recorder := recordPeerSpans(t)
	connectionCtx, connectionSpan := otel.Tracer("test").Start(t.Context(), "websocket")
	defer connectionSpan.End()
	_, clientPeer := newTestPeerPair(t,
		PeerConfig{Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
			return nil, nil
		})},
		PeerConfig{}, connectionCtx,
	)

	carriers := []map[string]string{
		nil,
		{"traceparent": "invalid", "tracestate": "vendor=value"},
		{"tracestate": "vendor=value"},
	}
	for i, carrier := range carriers {
		id := "legacy-" + strings.Repeat("x", i+1)
		payload, err := json.Marshal(Message{
			JSONRPC:      JSONRPCVersion,
			ID:           &id,
			Method:       MethodToolExecute,
			TraceContext: carrier,
		})
		require.NoError(t, err)
		// Send an old-style/custom request without the instrumented Call wrapper.
		require.NoError(t, clientPeer.enqueueControl(t.Context(), websocket.TextMessage, payload, nil))
	}
	require.Eventually(t, func() bool { return len(recorder.Ended()) == len(carriers) }, time.Second, time.Millisecond)
	traceIDs := make(map[trace.TraceID]bool)
	for _, span := range recorder.Ended() {
		assert.Equal(t, trace.SpanKindServer, span.SpanKind())
		assert.False(t, span.Parent().IsValid())
		assert.Empty(t, span.SpanContext().TraceState().String())
		assert.NotEqual(t, connectionSpan.SpanContext().TraceID(), span.SpanContext().TraceID())
		assert.False(t, traceIDs[span.SpanContext().TraceID()], "independent calls must start independent traces")
		traceIDs[span.SpanContext().TraceID()] = true
	}
}

func TestPeerTracingLocalFailure(t *testing.T) {
	recorder := recordPeerSpans(t)
	_, clientPeer := newTestPeerPair(t, PeerConfig{}, PeerConfig{})
	err := clientPeer.Call(t.Context(), MethodToolExecute, make(chan struct{}), nil)
	require.ErrorContains(t, err, "failed to encode runner rpc params")
	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, trace.SpanKindClient, spans[0].SpanKind())
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.NotContains(t, spans[0].Status().Description, "failed to encode runner rpc params")
	assert.Empty(t, spans[0].Events())
}

func TestPeerInternalRPCTracing(t *testing.T) {
	for _, method := range []string{MethodLifecycleDispatch, MethodUIExtensionCleanup} {
		for _, detailed := range []bool{false, true} {
			name := method + "/default"
			if detailed {
				name = method + "/detailed"
			}
			t.Run(name, func(t *testing.T) {
				recorder := recordPeerSpans(t)
				_, err := telemetry.InitTracer(t.Context(), telemetry.Config{InternalRPCSpans: detailed})
				require.NoError(t, err)
				tracer := otel.Tracer("test")
				connectionCtx, connectionSpan := tracer.Start(t.Context(), "websocket")
				defer connectionSpan.End()
				handler := RequestHandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, *RPCError) {
					_, span := tracer.Start(ctx, "extension handler")
					span.End()
					return nil, nil
				})
				_, clientPeer := newTestPeerPair(t, PeerConfig{Handler: handler}, PeerConfig{}, connectionCtx)
				ctx, parent := tracer.Start(t.Context(), "invoke_agent kodelet")
				state, err := trace.ParseTraceState("vendor=value")
				require.NoError(t, err)
				// Propagate tracestate without changing the caller span's lifetime.
				callCtx := trace.ContextWithSpanContext(ctx, parent.SpanContext().WithTraceState(state))
				require.NoError(t, clientPeer.Call(callCtx, method, nil, nil))
				assert.True(t, parent.IsRecording(), "suppressing a transport span must not end its parent")
				parent.End()
				count := 2
				if detailed {
					count = 4
				}
				require.Eventually(t, func() bool { return len(recorder.Ended()) == count }, time.Second, time.Millisecond)
				var handlerSpan, clientSpan, serverSpan sdktrace.ReadOnlySpan
				for _, span := range recorder.Ended() {
					assert.Equal(t, parent.SpanContext().TraceID(), span.SpanContext().TraceID())
					switch {
					case span.Name() == "extension handler":
						handlerSpan = span
					case span.SpanKind() == trace.SpanKindClient:
						clientSpan = span
					case span.SpanKind() == trace.SpanKindServer:
						serverSpan = span
					}
				}
				require.NotNil(t, handlerSpan)
				assert.Equal(t, state, handlerSpan.Parent().TraceState())
				if detailed {
					require.NotNil(t, clientSpan)
					require.NotNil(t, serverSpan)
					assert.Equal(t, parent.SpanContext().SpanID(), clientSpan.Parent().SpanID())
					assert.Equal(t, clientSpan.SpanContext().SpanID(), serverSpan.Parent().SpanID())
					assert.Equal(t, serverSpan.SpanContext().SpanID(), handlerSpan.Parent().SpanID())
				} else {
					assert.Nil(t, clientSpan)
					assert.Nil(t, serverSpan)
					assert.Equal(t, parent.SpanContext().SpanID(), handlerSpan.Parent().SpanID())
					assert.True(t, handlerSpan.Parent().IsRemote())
				}
			})
		}
	}
}

func TestPeerInternalRPCNoOpDoesNotCreateTraces(t *testing.T) {
	recorder := recordPeerSpans(t)
	serverPeer, clientPeer := newTestPeerPair(t, PeerConfig{
		Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
			return nil, nil
		}),
	}, PeerConfig{})
	for _, method := range []string{MethodLifecycleDispatch, MethodUIExtensionCleanup} {
		require.NoError(t, clientPeer.Call(t.Context(), method, nil, nil))
	}
	// Wait for trace finalizers before cleanup cancels the connection context.
	require.Eventually(t, func() bool {
		serverPeer.inboundMu.Lock()
		defer serverPeer.inboundMu.Unlock()
		return len(serverPeer.inbound) == 0
	}, time.Second, time.Millisecond)
	assert.Empty(t, recorder.Ended())
}

func TestPeerInternalRPCFailureDiagnostics(t *testing.T) {
	for _, content := range []bool{false, true} {
		name := "metadata"
		if content {
			name = "content"
		}
		t.Run(name, func(t *testing.T) {
			recorder := recordPeerSpans(t)
			_, err := telemetry.InitTracer(t.Context(), telemetry.Config{CaptureContent: content})
			require.NoError(t, err)
			const secret = "private extension failure"
			_, clientPeer := newTestPeerPair(t, PeerConfig{
				Handler: RequestHandlerFunc(func(context.Context, string, json.RawMessage) (any, *RPCError) {
					return nil, &RPCError{Code: ErrorCodeInternal, Message: secret}
				}),
			}, PeerConfig{})
			ctx, parent := otel.Tracer("test").Start(t.Context(), "invoke_agent kodelet")
			err = clientPeer.Call(ctx, MethodLifecycleDispatch, nil, nil)
			require.ErrorContains(t, err, secret)
			assert.True(t, parent.IsRecording())
			parent.End()
			require.Eventually(t, func() bool { return len(recorder.Ended()) == 2 }, time.Second, time.Millisecond)
			for _, span := range recorder.Ended() {
				if span.SpanKind() == trace.SpanKindServer {
					assert.Equal(t, codes.Error, span.Status().Code)
					assert.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())
				}
				require.NotEmpty(t, span.Events())
				event := span.Events()[0]
				assert.Equal(t, "runner.rpc.error", event.Name)
				assert.Contains(t, event.Attributes, attribute.String("rpc.method", MethodLifecycleDispatch))
				assert.Contains(t, event.Attributes, attribute.String("error.type", "rpc_error"))
				assert.Contains(t, event.Attributes, attribute.Int("rpc.jsonrpc.error_code", ErrorCodeInternal))
				if content {
					assert.Contains(t, event.Attributes, attribute.String("exception.message", err.Error()))
				} else {
					assert.NotContains(t, span.Status().Description, secret)
					assert.NotContains(t, span.Attributes(), attribute.String("exception.message", err.Error()))
					require.Len(t, span.Events(), 1)
					assert.NotContains(t, event.Attributes, attribute.String("exception.message", err.Error()))
				}
			}
		})
	}
}

func TestInternalRPCDiagnosticsWithoutParentAndOnCancellation(t *testing.T) {
	recorder := recordPeerSpans(t)
	ctx, finish := startRPCTrace(t.Context(), MethodUIExtensionCleanup, trace.SpanKindClient)
	assert.Equal(t, t.Context(), ctx)
	finish(context.DeadlineExceeded)
	require.Len(t, recorder.Ended(), 1)
	span := recorder.Ended()[0]
	assert.False(t, span.Parent().IsValid())
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Contains(t, span.Attributes(), attribute.String("error.type", "timeout"))
	assert.Equal(t, "runner.rpc "+MethodUIExtensionCleanup, span.Name())

	ctx, parent := otel.Tracer("test").Start(t.Context(), "invocation")
	_, finish = startRPCTrace(ctx, MethodLifecycleDispatch, trace.SpanKindClient)
	finish(context.Canceled)
	parent.End()
	require.Len(t, recorder.Ended(), 2)
	span = recorder.Ended()[1]
	require.Len(t, span.Events(), 1)
	assert.Contains(t, span.Events()[0].Attributes, attribute.String("error.type", "cancelled"))
}

func recordPeerSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder, _ := telemetrytest.NewRecorder(t, false)
	// RPC propagation must work even without global propagator initialization.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	return recorder
}

func newTestPeerPair(t *testing.T, serverConfig, clientConfig PeerConfig, parent ...context.Context) (*Peer, *Peer) {
	t.Helper()
	connectionCtx := t.Context()
	if len(parent) != 0 {
		connectionCtx = parent[0]
	}
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
		if err := peer.Start(connectionCtx); err != nil {
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
	require.NoError(t, clientPeer.Start(connectionCtx))

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
