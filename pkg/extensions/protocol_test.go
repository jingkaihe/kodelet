package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPCClientCallWritesCancelNotificationOnContextCancel(t *testing.T) {
	reader, writer := io.Pipe()
	var outbound bytes.Buffer
	client := newRPCClient(reader, &outbound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.callWithHostHandler(ctx, "extension.test", map[string]any{"ok": true}, nil, nil)
	_ = writer.Close()

	require.ErrorIs(t, err, context.Canceled)
	frames := readAllTestFrames(t, outbound.Bytes())
	require.Len(t, frames, 2)
	var request rpcRequest
	require.NoError(t, json.Unmarshal(frames[0], &request))
	assert.Equal(t, "extension.test", request.Method)
	var cancelNotification rpcNotification
	require.NoError(t, json.Unmarshal(frames[1], &cancelNotification))
	assert.Equal(t, "$/cancelRequest", cancelNotification.Method)
}

func TestRPCClientCallHandlesErrorResponseAndUnexpectedID(t *testing.T) {
	t.Run("rpc error response", func(t *testing.T) {
		var outbound bytes.Buffer
		response := rpcResponse{JSONRPC: "2.0", ID: 1, Error: &rpcError{Code: -32000, Message: "boom"}}
		payload, err := json.Marshal(response)
		require.NoError(t, err)
		var inbound bytes.Buffer
		require.NoError(t, writeFrame(&inbound, payload))

		client := newRPCClient(&inbound, &outbound)
		err = client.callWithHostHandler(context.Background(), "extension.test", nil, nil, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension rpc error -32000: boom")
	})

	t.Run("unexpected response id", func(t *testing.T) {
		var outbound bytes.Buffer
		response := rpcResponse{JSONRPC: "2.0", ID: 99}
		payload, err := json.Marshal(response)
		require.NoError(t, err)
		var inbound bytes.Buffer
		require.NoError(t, writeFrame(&inbound, payload))

		client := newRPCClient(&inbound, &outbound)
		err = client.callWithHostHandler(context.Background(), "extension.test", nil, nil, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected rpc response id")
	})
}

func TestRPCClientCallHandlesHostRequestBeforeResponse(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})
	client := newRPCClient(clientReader, clientWriter)
	var result ToolExecutionResult
	callDone := make(chan error, 1)
	go func() {
		callDone <- client.callWithHostHandler(context.Background(), "extension.tool.execute", nil, &result, testHostRequestHandler{})
	}()

	outbound := bufio.NewReader(serverReader)
	requestPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var request rpcRequest
	require.NoError(t, json.Unmarshal(requestPayload, &request))
	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":7,"method":"kodelet.ui.input","params":{"title":"Choose"}}`)))

	hostResponsePayload, err := readFrame(outbound)
	require.NoError(t, err)
	var hostResponse rpcResponse
	require.NoError(t, json.Unmarshal(hostResponsePayload, &hostResponse))
	assert.Equal(t, int64(7), hostResponse.ID)
	assert.JSONEq(t, `{"status":"submitted","value":"2"}`, string(hostResponse.Result))

	responsePayload, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"content":"done"}`)})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, responsePayload))

	require.NoError(t, <-callDone)
	assert.Equal(t, "done", result.Content)
}

func TestRPCClientRoutesLegacyPersistentUIToSinglePendingCall(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})

	client := newRPCClient(clientReader, clientWriter)
	client.setHostRequestHandler(contextHostRequestHandler{})
	callDone := make(chan error, 1)
	go func() {
		ctx := context.WithValue(context.Background(), rpcCallContextKey{}, "conversation-one")
		callDone <- client.callWithHostHandler(ctx, "extension.command.execute", nil, nil, contextHostRequestHandler{})
	}()

	outbound := bufio.NewReader(serverReader)
	requestPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var request rpcRequest
	require.NoError(t, json.Unmarshal(requestPayload, &request))

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":7,"method":"kodelet.ui.widget.set","params":{"id":"status","placement":"aboveComposer","frame":{"sequence":1,"lines":["working"]}}}`)))
	legacyPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var legacyResponse rpcResponse
	require.NoError(t, json.Unmarshal(legacyPayload, &legacyResponse))
	assert.Equal(t, int64(7), legacyResponse.ID)
	assert.JSONEq(t, `{"accepted":true,"conversation":"conversation-one"}`, string(legacyResponse.Result))

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":8,"method":"kodelet.ui.widget.set","params":{"scopeId":"","id":"global-status","placement":"aboveComposer","frame":{"sequence":1,"lines":["global"]}}}`)))
	globalPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var globalResponse rpcResponse
	require.NoError(t, json.Unmarshal(globalPayload, &globalResponse))
	assert.Equal(t, int64(8), globalResponse.ID)
	assert.JSONEq(t, `{"accepted":true,"conversation":""}`, string(globalResponse.Result))

	responsePayload, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, responsePayload))
	require.NoError(t, <-callDone)
}

func TestRPCClientRoutesParentlessNotificationsToPersistentHostHandler(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})

	notifications := make(chan rpcIncomingNotification, 1)
	client := newRPCClient(clientReader, clientWriter)
	client.setHostRequestHandler(recordingRPCNotificationHandler{notifications: notifications})
	callDone := make(chan error, 1)
	go func() {
		callDone <- client.callWithHostHandler(context.Background(), "extension.initialize", nil, nil, nil)
	}()

	outbound := bufio.NewReader(serverReader)
	requestPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var request rpcRequest
	require.NoError(t, json.Unmarshal(requestPayload, &request))

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","method":"kodelet.ui.surface.frame","params":{"id":"doom","frame":{"sequence":2,"lines":["latest"]}}}`)))
	notification := <-notifications
	assert.Equal(t, UISurfaceFrameMethod, notification.method)
	assert.JSONEq(t, `{"id":"doom","frame":{"sequence":2,"lines":["latest"]}}`, string(notification.params))

	responsePayload, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.NoError(t, writeFrame(serverWriter, responsePayload))
	require.NoError(t, <-callDone)
}

func TestRPCClientUsesPersistentHostContextForParentlessRequests(t *testing.T) {
	client := newRPCClient(strings.NewReader(""), io.Discard)
	hostCtx := context.WithValue(context.Background(), rpcCallContextKey{}, "conversation-host")
	handler := persistentContextHostRequestHandler{ctx: hostCtx}
	client.setHostRequestHandler(handler)

	ctx, selected, parentMatched, ambiguous := client.hostRequestTarget(nil)
	assert.False(t, parentMatched)
	assert.False(t, ambiguous)
	assert.Equal(t, "conversation-host", ctx.Value(rpcCallContextKey{}))
	result, rpcErr := selected.HandleRPCRequest(ctx, "kodelet.ui.widget.set", nil)
	require.Nil(t, rpcErr)
	assert.Equal(t, map[string]any{"accepted": true, "conversation": "conversation-host"}, result)
}

func TestRPCClientRunsPostResponseHookAfterWriteAttempt(t *testing.T) {
	t.Run("successful write", func(t *testing.T) {
		var outbound bytes.Buffer
		writtenBeforeCallback := false
		client := newRPCClient(strings.NewReader(""), &outbound)
		handler := fixedRPCResponseHandler{result: BackgroundTaskReleaseResponse{
			Released: true,
			AfterResponse: func() {
				writtenBeforeCallback = outbound.Len() > 0
			},
		}}

		err := client.handleIncomingRequest(context.Background(), rpcIncomingMessage{
			ID:     json.RawMessage(`7`),
			Method: BackgroundTaskReleaseMethod,
		}, handler)

		require.NoError(t, err)
		assert.True(t, writtenBeforeCallback)
		frames := readAllTestFrames(t, outbound.Bytes())
		require.Len(t, frames, 1)
		var response rpcResponse
		require.NoError(t, json.Unmarshal(frames[0], &response))
		assert.JSONEq(t, `{"released":true}`, string(response.Result))
	})

	t.Run("failed write", func(t *testing.T) {
		wantErr := errors.New("writer disconnected")
		callbackCalled := false
		client := newRPCClient(strings.NewReader(""), failingRPCWriter{err: wantErr})
		handler := fixedRPCResponseHandler{result: BackgroundTaskReleaseResponse{
			Released:      true,
			AfterResponse: func() { callbackCalled = true },
		}}

		err := client.handleIncomingRequest(context.Background(), rpcIncomingMessage{
			ID:     json.RawMessage(`7`),
			Method: BackgroundTaskReleaseMethod,
		}, handler)

		require.ErrorIs(t, err, wantErr)
		assert.True(t, callbackCalled)
	})
}

func TestRPCClientNotificationWriteFailureTerminatesClient(t *testing.T) {
	wantErr := errors.New("writer disconnected")
	failed := make(chan error, 1)
	client := newRPCClient(strings.NewReader(""), failingRPCWriter{err: wantErr})
	client.setTerminalHandler(func(err error) { failed <- err })

	err := client.notify(UISurfaceInputMethod, map[string]any{"id": "game"})

	require.ErrorIs(t, err, wantErr)
	select {
	case terminalErr := <-failed:
		require.ErrorIs(t, terminalErr, wantErr)
	case <-time.After(time.Second):
		t.Fatal("terminal handler was not called")
	}
}

func TestRPCClientCallsRunConcurrentlyAndRouteHostRequests(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientReader.Close()
		_ = serverWriter.Close()
		_ = serverReader.Close()
		_ = clientWriter.Close()
	})
	client := newRPCClient(clientReader, clientWriter)
	client.setHostRequestHandler(contextHostRequestHandler{})
	type callResult struct {
		label   string
		content string
		err     error
	}
	results := make(chan callResult, 2)
	for _, label := range []string{"first", "second"} {
		go func() {
			ctx := context.WithValue(context.Background(), rpcCallContextKey{}, label)
			var result ToolExecutionResult
			err := client.callWithHostHandler(
				ctx,
				"extension.tool.execute",
				map[string]string{"label": label},
				&result,
				contextHostRequestHandler{},
			)
			results <- callResult{label: label, content: result.Content, err: err}
		}()
	}

	outbound := bufio.NewReader(serverReader)
	requestIDs := make(map[string]int64, 2)
	for range 2 {
		payload, err := readFrame(outbound)
		require.NoError(t, err)
		var request struct {
			ID     int64 `json:"id"`
			Params struct {
				Label string `json:"label"`
			} `json:"params"`
		}
		require.NoError(t, json.Unmarshal(payload, &request))
		requestIDs[request.Params.Label] = request.ID
	}
	require.NotZero(t, requestIDs["first"])
	require.NotZero(t, requestIDs["second"])

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":76,"method":"kodelet.tool.update","params":{"content":"ambiguous"}}`)))
	invalidUpdatePayload, err := readFrame(outbound)
	require.NoError(t, err)
	var invalidUpdateResponse rpcResponse
	require.NoError(t, json.Unmarshal(invalidUpdatePayload, &invalidUpdateResponse))
	assert.Equal(t, int64(76), invalidUpdateResponse.ID)
	require.NotNil(t, invalidUpdateResponse.Error)
	assert.Equal(t, -32602, invalidUpdateResponse.Error.Code)

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":75,"method":"kodelet.conversation.fork"}`)))
	invalidForkPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var invalidForkResponse rpcResponse
	require.NoError(t, json.Unmarshal(invalidForkPayload, &invalidForkResponse))
	assert.Equal(t, int64(75), invalidForkResponse.ID)
	require.NotNil(t, invalidForkResponse.Error)
	assert.Equal(t, -32602, invalidForkResponse.Error.Code)

	require.NoError(t, writeFrame(serverWriter, []byte(`{"jsonrpc":"2.0","id":78,"method":"kodelet.ui.widget.set","params":{"id":"status","placement":"aboveComposer","frame":{"sequence":1,"lines":["working"]}}}`)))
	ambiguousUIPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var parentlessUIResponse rpcResponse
	require.NoError(t, json.Unmarshal(ambiguousUIPayload, &parentlessUIResponse))
	assert.Equal(t, int64(78), parentlessUIResponse.ID)
	assert.Nil(t, parentlessUIResponse.Error)
	assert.JSONEq(t, `{"accepted":true,"conversation":""}`, string(parentlessUIResponse.Result))

	parentedWidgetRequest := []byte(`{"jsonrpc":"2.0","id":79,"parentId":` + strconv.FormatInt(requestIDs["first"], 10) + `,"method":"kodelet.ui.widget.set","params":{"id":"status","placement":"aboveComposer","frame":{"sequence":1,"lines":["working"]}}}`)
	require.NoError(t, writeFrame(serverWriter, parentedWidgetRequest))
	parentedWidgetPayload, err := readFrame(outbound)
	require.NoError(t, err)
	var parentedWidgetResponse rpcResponse
	require.NoError(t, json.Unmarshal(parentedWidgetPayload, &parentedWidgetResponse))
	assert.Equal(t, int64(79), parentedWidgetResponse.ID)
	assert.Nil(t, parentedWidgetResponse.Error)
	assert.JSONEq(t, `{"accepted":true,"conversation":"first"}`, string(parentedWidgetResponse.Result))

	hostRequest := []byte(`{"jsonrpc":"2.0","id":77,"parentId":` + strconv.FormatInt(requestIDs["second"], 10) + `,"method":"kodelet.ui.input","params":{"title":"Choose"}}`)
	require.NoError(t, writeFrame(serverWriter, hostRequest))
	hostResponsePayload, err := readFrame(outbound)
	require.NoError(t, err)
	var hostResponse rpcResponse
	require.NoError(t, json.Unmarshal(hostResponsePayload, &hostResponse))
	assert.Equal(t, int64(77), hostResponse.ID)
	assert.JSONEq(t, `{"status":"submitted","value":"second"}`, string(hostResponse.Result))

	for _, label := range []string{"second", "first"} {
		responsePayload, err := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			ID:      requestIDs[label],
			Result:  json.RawMessage(`{"content":` + strconv.Quote(label+" done") + `}`),
		})
		require.NoError(t, err)
		require.NoError(t, writeFrame(serverWriter, responsePayload))
	}

	got := make(map[string]callResult, 2)
	for range 2 {
		result := <-results
		got[result.label] = result
	}
	require.NoError(t, got["first"].err)
	require.NoError(t, got["second"].err)
	assert.Equal(t, "first done", got["first"].content)
	assert.Equal(t, "second done", got["second"].content)
}

type testHostRequestHandler struct{}

type fixedRPCResponseHandler struct {
	result any
}

func (h fixedRPCResponseHandler) HandleRPCRequest(context.Context, string, json.RawMessage) (any, *rpcError) {
	return h.result, nil
}

func (testHostRequestHandler) HandleRPCRequest(_ context.Context, method string, params json.RawMessage) (any, *rpcError) {
	if method != "kodelet.ui.input" {
		return nil, &rpcError{Code: -32601, Message: "not found"}
	}
	var request UIInputRequest
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	if request.Title != "Choose" {
		return nil, &rpcError{Code: -32602, Message: "bad title"}
	}
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: "2"}, nil
}

type rpcCallContextKey struct{}

type contextHostRequestHandler struct{}

func (contextHostRequestHandler) HandleRPCRequest(ctx context.Context, method string, _ json.RawMessage) (any, *rpcError) {
	value, _ := ctx.Value(rpcCallContextKey{}).(string)
	switch method {
	case "kodelet.ui.input":
		return UIInputResponse{Status: UIInputStatusSubmitted, Value: value}, nil
	case "kodelet.ui.widget.set":
		return map[string]any{"accepted": true, "conversation": value}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "not found"}
	}
}

type persistentContextHostRequestHandler struct {
	contextHostRequestHandler
	ctx context.Context
}

func (h persistentContextHostRequestHandler) hostContext() context.Context {
	return h.ctx
}

type rpcIncomingNotification struct {
	method string
	params json.RawMessage
}

type recordingRPCNotificationHandler struct {
	notifications chan<- rpcIncomingNotification
}

type failingRPCWriter struct {
	err error
}

func (w failingRPCWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (h recordingRPCNotificationHandler) HandleRPCRequest(context.Context, string, json.RawMessage) (any, *rpcError) {
	return nil, &rpcError{Code: -32601, Message: "not found"}
}

func (h recordingRPCNotificationHandler) HandleRPCNotification(method string, params json.RawMessage) {
	h.notifications <- rpcIncomingNotification{method: method, params: params}
}

func TestReadFrameValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "missing content length", input: "Header: value\r\n\r\n{}", wantErr: "missing Content-Length"},
		{name: "invalid content length", input: "Content-Length: nope\r\n\r\n{}", wantErr: "invalid Content-Length"},
		{name: "short payload", input: "Content-Length: 5\r\n\r\n{}", wantErr: "failed to read rpc payload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readFrame(bufio.NewReader(strings.NewReader(tt.input)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestReadResponseRejectsInvalidJSON(t *testing.T) {
	var inbound bytes.Buffer
	require.NoError(t, writeFrame(&inbound, []byte("not-json")))

	_, err := readResponse(bufio.NewReader(&inbound))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal rpc response")
}

func readAllTestFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(data))
	var frames [][]byte
	for {
		_, err := reader.Peek(1)
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		frame, err := readFrame(reader)
		require.NoError(t, err)
		frames = append(frames, frame)
	}
	return frames
}
