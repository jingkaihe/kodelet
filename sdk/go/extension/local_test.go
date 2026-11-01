package extension_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLocalTool starts a "run" tool on a runtime whose host requests go to local first.
func runLocalTool(t *testing.T, local extension.LocalRequestFunc, execute func(context.Context, *extension.ToolContext) error) *wire {
	t.Helper()
	ext := extension.New(extension.Metadata{})
	addTool(ext, "run", func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		return extension.Text("done"), execute(ctx, tc)
	})
	w := runtimeWire(t, ext, extension.RuntimeOptions{LocalRequest: local}, nil)
	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"run","input":{}}}`)
	return w
}

// blockUntilCancelled is a local handler that reports its context error.
func blockUntilCancelled(started chan<- struct{}, handlerErr chan<- error) extension.LocalRequestFunc {
	return func(ctx context.Context, _ string, _ json.RawMessage) (any, bool, error) {
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		handlerErr <- ctx.Err()
		return nil, true, ctx.Err()
	}
}

// withDeadline calls request with a 50 ms deadline and reports its error.
func withDeadline(errs chan<- error, request func(context.Context, *extension.ToolContext) error) func(context.Context, *extension.ToolContext) error {
	return func(ctx context.Context, tc *extension.ToolContext) error {
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		errs <- request(short, tc)
		return nil
	}
}

func TestLocalRequestsHonorTheCallerContext(t *testing.T) {
	t.Run("handler sees the caller deadline", func(t *testing.T) {
		handlerErr, inputErr := make(chan error, 1), make(chan error, 1)
		runLocalTool(t, blockUntilCancelled(nil, handlerErr), withDeadline(inputErr, func(ctx context.Context, tc *extension.ToolContext) error {
			_, _, err := tc.UI.Input(ctx, extension.InputRequest{Title: "x"})
			return err
		}))
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
		}, withDeadline(inputErr, func(ctx context.Context, tc *extension.ToolContext) error {
			_, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Title: "x"})
			return err
		}))
		require.ErrorIs(t, receive(t, inputErr), context.DeadlineExceeded)
	})

	t.Run("handler stops when the originating request is cancelled", func(t *testing.T) {
		started, handlerErr := make(chan struct{}), make(chan error, 1)
		w := runLocalTool(t, blockUntilCancelled(started, handlerErr), func(_ context.Context, tc *extension.ToolContext) error {
			_, _, err := tc.UI.Select(context.Background(), extension.SelectRequest{Title: "x", Options: []string{"a"}})
			return err
		})
		receive(t, started)
		w.send(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":1}}`)
		require.ErrorIs(t, receive(t, handlerErr), context.Canceled)
	})

	t.Run("declined requests are forwarded to the host", func(t *testing.T) {
		w := runLocalTool(t, func(context.Context, string, json.RawMessage) (any, bool, error) {
			return nil, false, nil
		}, func(ctx context.Context, tc *extension.ToolContext) error {
			return tc.UI.Notify(ctx, extension.NotifyRequest{Message: "hello"})
		})
		forwarded := w.next()
		assert.Equal(t, "kodelet.ui.notify", forwarded.Method)
		assert.JSONEq(t, "1", string(forwarded.ParentID))
		w.send(`{"jsonrpc":"2.0","id":` + string(forwarded.ID) + `,"result":{}}`)
		result := w.next()
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
