package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/browser"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserToolCallLeases(t *testing.T) {
	var released atomic.Int32
	connection := browser.Connection{SessionID: "session", CDPURL: "ws://127.0.0.1:9222/devtools/browser/test", PageTargetID: "page"}
	call := newBrowserToolCall(t.Context(), func(context.Context) (browser.Connection, func(), error) {
		var once sync.Once
		return connection, func() { once.Do(func() { released.Add(1) }) }, nil
	})
	t.Cleanup(call.close)
	value, rpcErr := call.request(BrowserAcquireMethod, json.RawMessage(`{}`))
	require.Nil(t, rpcErr)
	acquired := value.(browserAcquireResult)
	require.NotEmpty(t, acquired.LeaseID)
	assert.Equal(t, connection, acquired.Connection)
	params, err := json.Marshal(map[string]string{"leaseId": acquired.LeaseID})
	require.NoError(t, err)
	for range 2 {
		_, rpcErr = call.request(BrowserReleaseMethod, params)
		require.Nil(t, rpcErr)
	}
	assert.EqualValues(t, 1, released.Load())
	_, rpcErr = call.request(BrowserReleaseMethod, json.RawMessage(`{"leaseId":"another-invocation"}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, -32602, rpcErr.Code)
	for _, input := range []string{`[]`, `42`, `{"conversationId":"forged"}`, `{"cwd":"/other"}`} {
		_, rpcErr = call.request(BrowserAcquireMethod, json.RawMessage(input))
		require.NotNil(t, rpcErr, input)
		assert.Equal(t, -32602, rpcErr.Code)
	}
	_, rpcErr = call.request(BrowserReleaseMethod, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	for i := 1; i < maxBrowserToolLeases; i++ {
		_, rpcErr = call.request(BrowserAcquireMethod, nil)
		require.Nil(t, rpcErr)
	}
	_, rpcErr = call.request(BrowserAcquireMethod, nil)
	require.NotNil(t, rpcErr)
	call.close()
	call.close()
	assert.EqualValues(t, maxBrowserToolLeases, released.Load())
	_, rpcErr = call.request(BrowserAcquireMethod, nil)
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "ended")
}

func TestBrowserToolCallFailsClosed(t *testing.T) {
	_, rpcErr := (toolExecutionHostHandler{}).HandleRPCRequest(t.Context(), BrowserAcquireMethod, nil)
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "authorized")
	assert.Nil(t, BrowserAcquirerFromContext(t.Context()))
	call := newBrowserToolCall(t.Context(), func(context.Context) (browser.Connection, func(), error) {
		return browser.Connection{}, nil, errors.New("browser disabled")
	})
	defer call.close()
	_, rpcErr = call.request(BrowserAcquireMethod, nil)
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "browser disabled")
}

func TestBrowserToolCallCloseCancelsAcquisition(t *testing.T) {
	started := make(chan struct{})
	call := newBrowserToolCall(t.Context(), func(ctx context.Context) (browser.Connection, func(), error) {
		close(started)
		<-ctx.Done()
		return browser.Connection{}, nil, ctx.Err()
	})
	done := make(chan *rpcError, 1)
	go func() {
		_, err := call.request(BrowserAcquireMethod, nil)
		done <- err
	}()
	<-started
	call.close()
	require.NotNil(t, <-done)
}

func TestBrowserRPCRequiresExplicitActiveParent(t *testing.T) {
	for _, method := range []string{BrowserAcquireMethod, BrowserReleaseMethod} {
		for _, parent := range []string{"", "null", `"bad"`, "8"} {
			var output bytes.Buffer
			client := newRPCClient(strings.NewReader(""), &output)
			client.pending[7] = &rpcPendingCall{ctx: t.Context(), handler: contextHostRequestHandler{}}
			client.dispatchIncomingRequest(rpcIncomingMessage{
				ID: json.RawMessage(`42`), ParentID: json.RawMessage(parent), Method: method,
			})
			frames := readAllTestFrames(t, output.Bytes())
			require.Len(t, frames, 1)
			var response rpcResponse
			require.NoError(t, json.Unmarshal(frames[0], &response))
			require.NotNil(t, response.Error)
			assert.Equal(t, -32602, response.Error.Code)
		}
	}
}

func TestProcessBrowserLeasesEndWithInvocation(t *testing.T) {
	for _, ending := range []string{"success", "error", "cancel", "disconnect"} {
		t.Run(ending, func(t *testing.T) {
			local, extension := net.Pipe()
			t.Cleanup(func() { _ = local.Close(); _ = extension.Close() })
			require.NoError(t, extension.SetDeadline(time.Now().Add(5*time.Second)))
			// Model an installed subprocess transport without launching a fixture executable.
			process := &Process{Extension: Extension{ID: "browser-test"}, runtimeCtx: t.Context()}
			process.bindRPC(local, local)
			t.Cleanup(func() { _ = process.Close() })
			var released atomic.Bool
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = ContextWithBrowserAcquirer(ctx, func(context.Context) (browser.Connection, func(), error) {
				return browser.Connection{SessionID: "session", CDPURL: "ws://127.0.0.1:9222/devtools/browser/test", PageTargetID: "page"}, func() { released.Store(true) }, nil
			})
			done := make(chan error, 1)
			go func() {
				_, err := process.ExecuteTool(ctx, "browse", json.RawMessage(`{}`), ExtensionCallContext{})
				done <- err
			}()
			reader := bufio.NewReader(extension)
			request, err := readIncomingMessage(reader)
			require.NoError(t, err)
			payload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 10, "parentId": request.ID, "method": BrowserAcquireMethod, "params": map[string]any{},
			})
			require.NoError(t, err)
			require.NoError(t, writeFrame(extension, payload))
			response, err := readIncomingMessage(reader)
			require.NoError(t, err)
			require.Nil(t, response.Error)
			assert.Contains(t, string(response.Result), "cdpUrl")
			assert.False(t, released.Load())
			switch ending {
			case "success", "error":
				response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": ToolExecutionResult{Content: "done"}}
				if ending == "error" {
					delete(response, "result")
					response["error"] = rpcError{Code: -32000, Message: "failed"}
				}
				payload, err := json.Marshal(response)
				require.NoError(t, err)
				require.NoError(t, writeFrame(extension, payload))
			case "cancel":
				cancel()
				_, err := readIncomingMessage(reader)
				require.NoError(t, err)
			case "disconnect":
				require.NoError(t, extension.Close())
			}
			select {
			case err := <-done:
				if ending == "success" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("extension invocation did not finish")
			}
			assert.True(t, released.Load(), "tool completion must release every browser lease")
		})
	}
}
