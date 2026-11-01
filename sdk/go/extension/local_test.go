package extension_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLocalTool executes a tool on a runtime whose host requests go to local.
func runLocalTool(t *testing.T, local extension.LocalRequestFunc, execute func(context.Context, *extension.ToolContext) error) (*extension.Runtime, chan jsonrpc.Message) {
	t.Helper()
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "run", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		return extension.Text("done"), execute(ctx, tc)
	}})
	out := make(chan jsonrpc.Message, 16)
	rt := extension.NewRuntime(ext, extension.TransportFunc(func(_ context.Context, data []byte) error {
		var message jsonrpc.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return err
		}
		out <- message
		return nil
	}), &extension.RuntimeOptions{LogOutput: io.Discard, LocalRequest: local})
	t.Cleanup(func() { rt.Close(nil) })
	require.NoError(t, rt.Receive([]byte(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"run","input":{}}}`)))
	return rt, out
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func TestLocalRequestsHonorTheCallerContext(t *testing.T) {
	t.Run("handler sees the caller deadline", func(t *testing.T) {
		handlerErr := make(chan error, 1)
		inputErr := make(chan error, 1)
		runLocalTool(t, func(ctx context.Context, _ string, _ json.RawMessage) (any, bool, error) {
			<-ctx.Done()
			handlerErr <- ctx.Err()
			return nil, true, ctx.Err()
		}, func(ctx context.Context, tc *extension.ToolContext) error {
			short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, _, err := tc.UI.Input(short, extension.InputRequest{Title: "x"})
			inputErr <- err
			return nil
		})
		require.ErrorIs(t, receive(t, inputErr), context.DeadlineExceeded)
		require.ErrorIs(t, receive(t, handlerErr), context.DeadlineExceeded)
	})

	t.Run("caller returns even if the handler ignores cancellation", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		inputErr := make(chan error, 1)
		runLocalTool(t, func(context.Context, string, json.RawMessage) (any, bool, error) {
			<-release
			return nil, true, nil
		}, func(ctx context.Context, tc *extension.ToolContext) error {
			short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, err := tc.UI.Confirm(short, extension.ConfirmRequest{Title: "x"})
			inputErr <- err
			return nil
		})
		require.ErrorIs(t, receive(t, inputErr), context.DeadlineExceeded)
	})

	t.Run("handler stops when the originating request is cancelled", func(t *testing.T) {
		handlerErr := make(chan error, 1)
		started := make(chan struct{})
		rt, _ := runLocalTool(t, func(ctx context.Context, _ string, _ json.RawMessage) (any, bool, error) {
			close(started)
			<-ctx.Done()
			handlerErr <- ctx.Err()
			return nil, true, ctx.Err()
		}, func(_ context.Context, tc *extension.ToolContext) error {
			_, _, err := tc.UI.Select(context.Background(), extension.SelectRequest{Title: "x", Options: []string{"a"}})
			return err
		})
		receive(t, started)
		require.NoError(t, rt.Receive([]byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":1}}`)))
		require.ErrorIs(t, receive(t, handlerErr), context.Canceled)
	})

	t.Run("declined requests are forwarded to the host", func(t *testing.T) {
		rt, out := runLocalTool(t, func(context.Context, string, json.RawMessage) (any, bool, error) {
			return nil, false, nil
		}, func(ctx context.Context, tc *extension.ToolContext) error {
			return tc.UI.Notify(ctx, extension.NotifyRequest{Message: "hello"})
		})
		forwarded := receive(t, out)
		assert.Equal(t, "kodelet.ui.notify", forwarded.Method)
		assert.JSONEq(t, "1", string(forwarded.ParentID))
		require.NoError(t, rt.Receive([]byte(`{"jsonrpc":"2.0","id":`+string(forwarded.ID)+`,"result":{}}`)))
		result := receive(t, out)
		assert.JSONEq(t, "1", string(result.ID))
		assert.JSONEq(t, `{"content":"done"}`, string(result.Result))
	})

	t.Run("handler panics become errors", func(t *testing.T) {
		inputErr := make(chan error, 1)
		runLocalTool(t, func(context.Context, string, json.RawMessage) (any, bool, error) {
			panic("boom")
		}, func(ctx context.Context, tc *extension.ToolContext) error {
			_, _, err := tc.UI.Input(ctx, extension.InputRequest{Title: "x"})
			inputErr <- err
			return nil
		})
		require.ErrorContains(t, receive(t, inputErr), "local kodelet.ui.input handler panicked: boom")
	})
}
