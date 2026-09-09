package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nativeUITestResult struct {
	value any
	err   *protocol.RPCError
}

func nativeUITestPost(t *testing.T, server *Server, action, clientID string, value any) int {
	t.Helper()
	request := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(mustRunnerJSON(t, value))), map[string]string{"id": "conversation-ui"})
	request.Header.Set(chat.ClientIDHeader, clientID)
	response := httptest.NewRecorder()
	if action == "ack" {
		server.handlePersistentUIAck(response, request)
	} else {
		server.handlePersistentUIInput(response, request)
	}
	return response.Code
}

func nativeUITestStart(ctx context.Context, t *testing.T, server *Server, identity runnerregistry.UIRequestIdentity, sink *runnerUIEventSink, method string, value any) (*chat.UIPersistentEvent, <-chan nativeUITestResult) {
	t.Helper()
	data := mustRunnerJSON(t, value)
	result := make(chan nativeUITestResult, 1)
	go func() {
		value, err := server.HandleRunnerUIRequest(ctx, identity, method, data)
		result <- nativeUITestResult{value, err}
	}()
	for {
		select {
		case event := <-sink.events:
			if event.UIPersistent != nil && event.UIPersistent.RequestID != "" {
				return event.UIPersistent, result
			}
		case early := <-result:
			require.FailNow(t, "native request returned without delivering UI", "%+v", early)
		case <-time.After(time.Second):
			require.FailNow(t, "native request was not delivered")
		}
	}
}

func nativeUITestResultWithin(t *testing.T, result <-chan nativeUITestResult) any {
	t.Helper()
	select {
	case response := <-result:
		require.Nil(t, response.err)
		return response.value
	case <-time.After(time.Second):
		require.FailNow(t, "native request did not finish promptly")
		return nil
	}
}

func nativeUITestOpen() runnerpayload.UISurfaceOpenParams {
	return runnerpayload.UISurfaceOpenParams{RunID: "run-ui", Owner: runnerpayload.ExtensionOwner{ExtensionID: "native", Generation: 1}, Lifecycle: 7, Request: extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 1}}}
}

func TestNativeRunnerUISurfacesTranscriptAndInput(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, sink, broker := openRunnerUIRun(t, server)
	t.Cleanup(broker.close)
	broker.setOwner(withNativeCapabilities(t.Context(), &chat.ChatClientCapabilities{PersistentSurfaces: true}), "native", sink)
	identity := runnerUIRequestIdentity(registration)
	open := nativeUITestOpen()
	event, result := nativeUITestStart(t.Context(), t, server, identity, sink, protocol.MethodUISurfaceOpen, open)
	require.NotEmpty(t, event.RouteID)
	require.NotEmpty(t, event.Epoch)
	var request extensions.UISurfaceOpenRequest
	require.NoError(t, json.Unmarshal(event.Request, &request))
	assert.Equal(t, "conversation-ui", request.ScopeID)
	ack := chat.UIPersistentAck{RequestID: event.RequestID, Response: extensions.UIFrameResponse{Accepted: true, LatestSequence: 1}}
	assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "ack", "observer", ack))
	assert.Equal(t, http.StatusOK, nativeUITestPost(t, server, "ack", "native", ack))
	assert.True(t, nativeUITestResultWithin(t, result).(extensions.UIFrameResponse).Accepted)
	assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "ack", "native", ack), "acknowledgements are single-use")
	input := chat.UIPersistentInput{RouteID: event.RouteID, Method: protocol.MethodUISurfaceInput, Request: mustRunnerJSON(t, extensions.UISurfaceInputNotification{ScopeID: "conversation-ui", ID: "canvas", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"})}
	assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "input", "observer", input))
	assert.Equal(t, http.StatusOK, nativeUITestPost(t, server, "input", "native", input))
	input.Method = protocol.MethodUISurfaceResize
	input.Request = mustRunnerJSON(t, extensions.UISurfaceResizeNotification{ScopeID: "conversation-ui", ID: "canvas", Sequence: 2, Width: 100, Height: 40})
	assert.Equal(t, http.StatusOK, nativeUITestPost(t, server, "input", "native", input))
	input.Request = mustRunnerJSON(t, extensions.UISurfaceResizeNotification{ScopeID: "other", ID: "canvas", Sequence: 3, Width: 100, Height: 40})
	assert.Equal(t, http.StatusBadRequest, nativeUITestPost(t, server, "input", "native", input))
	stale, rpcErr := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUISurfaceOpen, mustRunnerJSON(t, open))
	require.Nil(t, rpcErr)
	assert.False(t, stale.(extensions.UIFrameResponse).Accepted)
	for _, operation := range []struct {
		method string
		params any
	}{
		{protocol.MethodUISurfaceFrame, runnerpayload.UISurfaceFrameParams{RunID: open.RunID, Owner: open.Owner, Request: extensions.UISurfaceFrameRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 2}}}},
		{protocol.MethodUITranscriptAppend, runnerpayload.UITranscriptAppendParams{RunID: open.RunID, Owner: open.Owner, Request: extensions.UITranscriptAppendRequest{Message: "saved canvas"}}},
		{protocol.MethodUISurfaceClose, runnerpayload.UISurfaceCloseParams{RunID: open.RunID, Owner: open.Owner, Request: extensions.UISurfaceCloseRequest{ID: "canvas", Sequence: 3}}},
	} {
		event, result := nativeUITestStart(t.Context(), t, server, identity, sink, operation.method, operation.params)
		assert.Equal(t, http.StatusOK, nativeUITestPost(t, server, "ack", "native", chat.UIPersistentAck{RequestID: event.RequestID, Response: extensions.UIFrameResponse{Accepted: true}}))
		value := nativeUITestResultWithin(t, result)
		if operation.method == protocol.MethodUITranscriptAppend {
			assert.True(t, value.(extensions.UITranscriptAppendResponse).Accepted)
		} else {
			assert.True(t, value.(extensions.UIFrameResponse).Accepted)
		}
	}
	assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "input", "native", input))
}

func TestNativeRunnerUILifecycleInvalidatesPendingAndLateInput(t *testing.T) {
	for _, finish := range []string{"disconnect", "completion", "process", "runner", "deadline"} {
		t.Run(finish, func(t *testing.T) {
			server := newRunnerTestServer(t, "")
			registration, sink, broker := openRunnerUIRun(t, server)
			t.Cleanup(broker.close)
			ownerCtx, detach := context.WithCancel(withNativeCapabilities(t.Context(), &chat.ChatClientCapabilities{PersistentSurfaces: true}))
			defer detach()
			broker.setOwner(ownerCtx, "native", sink)
			identity := runnerUIRequestIdentity(registration)
			open := nativeUITestOpen()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			event, result := nativeUITestStart(ctx, t, server, identity, sink, protocol.MethodUISurfaceOpen, open)
			switch finish {
			case "disconnect":
				detach()
			case "completion":
				broker.close()
			case "process":
				_, err := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIExtensionCleanup, mustRunnerJSON(t, runnerpayload.UIExtensionCleanupParams{Owner: open.Owner}))
				require.Nil(t, err)
			case "runner":
				server.runnerRegistry.Detach(identity.RunnerID, identity.ConnectionID, identity.Generation, nil)
				server.RunnerUIDetached(identity)
			case "deadline":
				cancel()
			}
			response := nativeUITestResultWithin(t, result).(extensions.UIFrameResponse)
			assert.False(t, response.Accepted)
			assert.NotEmpty(t, response.Reason)
			assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "ack", "native", chat.UIPersistentAck{RequestID: event.RequestID, Response: extensions.UIFrameResponse{Accepted: true}}))
			assert.Equal(t, http.StatusConflict, nativeUITestPost(t, server, "input", "native", chat.UIPersistentInput{RouteID: event.RouteID}))
			broker.mu.Lock()
			assert.Empty(t, broker.native.pending)
			assert.Empty(t, broker.native.routes)
			broker.mu.Unlock()
			if finish == "process" {
				value, err := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUISurfaceOpen, mustRunnerJSON(t, open))
				require.Nil(t, err)
				assert.Contains(t, value.(extensions.UIFrameResponse).Reason, "process is closed")
			}
		})
	}
}

func TestNativeRunnerUIUnavailableAndBackgroundWidgets(t *testing.T) {
	server := newRunnerTestServer(t, "")
	registration, sink, broker := openRunnerUIRun(t, server)
	t.Cleanup(broker.close)
	identity := runnerUIRequestIdentity(registration)
	open := nativeUITestOpen()
	for _, capabilities := range []*chat.ChatClientCapabilities{nil, {InteractiveUI: true, PersistentWidgets: true}} {
		broker.setOwner(withNativeCapabilities(t.Context(), capabilities), "browser", sink)
		value, err := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUISurfaceOpen, mustRunnerJSON(t, open))
		require.Nil(t, err)
		assert.Contains(t, value.(extensions.UIFrameResponse).Reason, "does not support")
	}
	_, err := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUITranscriptAppend, json.RawMessage(`{"runId":"run-ui","owner":{"extensionId":"native","generation":1},"request":null}`))
	require.NotNil(t, err)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, err.Code)
	value, err := server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUIWidgetSet, mustRunnerJSON(t, runnerpayload.UIWidgetSetParams{RunID: open.RunID, Owner: open.Owner, Request: extensions.UIWidgetSetRequest{ID: "status", Frame: extensions.UIFrame{Sequence: 1}}}))
	require.Nil(t, err)
	assert.True(t, value.(extensions.UIFrameResponse).Accepted)
	broker.close()
	require.NoError(t, server.runnerRegistry.CloseRun(t.Context(), "run-ui", runnerregistry.RunStatusSucceeded, nil))
	value, err = server.HandleRunnerUIRequest(t.Context(), identity, protocol.MethodUISurfaceOpen, mustRunnerJSON(t, open))
	require.Nil(t, err)
	assert.Contains(t, value.(extensions.UIFrameResponse).Reason, "active execution")
	_, widgets := server.extensionUI.Snapshot("conversation-ui")
	assert.Len(t, widgets, 1, "ending interactive ownership must not discard background widgets")
	server.cleanupRunnerPersistentUI(identity, open.Owner)
	_, widgets = server.extensionUI.Snapshot("conversation-ui")
	assert.Empty(t, widgets)
}
