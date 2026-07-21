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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPCClientCallWritesCancelNotificationOnContextCancel(t *testing.T) {
	reader, writer := io.Pipe()
	var outbound bytes.Buffer
	client := newRPCClient(reader, &outbound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.call(ctx, "extension.test", map[string]any{"ok": true}, nil)
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
		err = client.call(context.Background(), "extension.test", nil, nil)

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
		err = client.call(context.Background(), "extension.test", nil, nil)

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
	if method != "kodelet.ui.input" {
		return nil, &rpcError{Code: -32601, Message: "not found"}
	}
	value, _ := ctx.Value(rpcCallContextKey{}).(string)
	return UIInputResponse{Status: UIInputStatusSubmitted, Value: value}, nil
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
