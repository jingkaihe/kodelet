package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPCClientCallWritesCancelNotificationOnContextCancel(t *testing.T) {
	reader, writer := io.Pipe()
	var outbound bytes.Buffer
	client := newRPCClient(reader, &outbound, time.Second)
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

		client := newRPCClient(&inbound, &outbound, time.Second)
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

		client := newRPCClient(&inbound, &outbound, time.Second)
		err = client.call(context.Background(), "extension.test", nil, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected rpc response id")
	})
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
