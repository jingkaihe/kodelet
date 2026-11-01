package extension_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeSpeaksFramedJSONRPCAndCleansUpOnDisconnect(t *testing.T) {
	ext := extension.New(extension.Metadata{Name: "echo"})
	type echoInput struct {
		Text string `json:"text"`
	}
	extension.RegisterTool(ext, extension.Tool[echoInput]{Name: "echo", Description: "Echo", Execute: func(_ context.Context, in echoInput, _ *extension.ToolContext) (extension.ToolResult, error) {
		return extension.Text(in.Text), nil
	}})
	var sessionEnded atomic.Int32
	extension.On(ext, extension.EventSessionEnd, func(context.Context, extension.SessionEndEvent, *extension.EventContext) (extension.EventResult, error) {
		sessionEnded.Add(1)
		return extension.EventResult{}, nil
	})
	w := serveWire(t, ext)

	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"protocolVersion":"2026-05-30","extension":{"id":"echo"},"capabilities":{}}}`)
	initialized := w.next()
	assert.JSONEq(t, "1", string(initialized.ID))
	assert.Contains(t, string(initialized.Result), `"name":"echo"`)

	w.send(`{"jsonrpc":"2.0","id":"call-1","method":"extension.tool.execute","params":{"name":"echo","input":{"text":"hi"},"context":{}}}`)
	result := w.next()
	assert.JSONEq(t, `"call-1"`, string(result.ID))
	assert.JSONEq(t, `{"content":"hi"}`, string(result.Result))

	w.send(`not json`)
	parseError := w.next()
	require.NotNil(t, parseError.Error)
	assert.Equal(t, jsonrpc.CodeParseError, parseError.Error.Code)
	assert.Equal(t, "null", string(parseError.ID))

	w.send(`{"jsonrpc":"2.0","id":2,"method":"extension.unknown"}`)
	unknown := w.next()
	require.NotNil(t, unknown.Error)
	assert.Equal(t, jsonrpc.CodeMethodNotFound, unknown.Error.Code)

	w.send(`{"jsonrpc":"2.0","id":3,"method":"extension.event.handle","params":{"id":"e1","event":"session.end"}}`)
	assert.JSONEq(t, "{}", string(w.next().Result))

	require.NoError(t, w.closeInput())
	require.NoError(t, receive(t, w.served), "Serve returns nil after EOF")
	assert.Equal(t, int32(1), sessionEnded.Load(), "session.end runs once across the event and disconnect cleanup")
}

func TestCancellationAbortsHandlersAndBlocksLateHostRPC(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	started := make(chan struct{})
	lateErr := make(chan error, 1)
	addTool(ext, "wait", func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		close(started)
		<-ctx.Done()
		lateErr <- tc.UI.Notify(context.Background(), extension.NotifyRequest{Message: "too late"})
		return extension.Text("late result"), nil
	})
	w := serveWire(t, ext)
	w.send(`{"jsonrpc":"2.0","id":7,"method":"extension.tool.execute","params":{"name":"wait","input":{}}}`)
	<-started
	w.send(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":7}}`)
	require.ErrorIs(t, receive(t, lateErr), extension.ErrRequestInactive)

	w.send(`{"jsonrpc":"2.0","id":8,"method":"extension.unknown"}`)
	assert.JSONEq(t, "8", string(w.next().ID), "the cancelled request gets no response")
}

func TestReverseResponsesCannotCompleteForwardRequestsWithTheSameID(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	addTool(ext, "ask", func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		value, ok, err := tc.UI.Input(ctx, extension.InputRequest{Title: "Name"})
		if err != nil || !ok {
			return extension.ToolResult{}, errors.Join(err, errors.New("no input"))
		}
		return extension.Text(value), nil
	})
	w := serveWire(t, ext)
	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"protocolVersion":"2026-05-30","extension":{"id":"x"}}}`)
	w.next()
	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"ask","input":{}}}`)
	reverse := w.next()
	assert.Equal(t, "kodelet.ui.input", reverse.Method)
	assert.JSONEq(t, "1", string(reverse.ID))
	assert.JSONEq(t, "1", string(reverse.ParentID))
	w.send(`{"jsonrpc":"2.0","id":1,"result":{"status":"submitted","value":"Ada"}}`)
	result := w.next()
	assert.JSONEq(t, "1", string(result.ID))
	assert.JSONEq(t, `{"content":"Ada"}`, string(result.Result))
}

func TestEventHandlingPreservesLargeIntegers(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var first, second, output string
	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		first = string(event.Tool.Input)
		return extension.EventResult{Input: json.RawMessage(`{"id":9007199254740995}`)}, nil
	}, extension.WithPriority(1))
	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		second = string(event.Tool.Input)
		return extension.EventResult{}, nil
	})
	extension.On(ext, extension.EventToolResult, func(_ context.Context, event extension.ToolResultEvent, _ *extension.EventContext) (extension.EventResult, error) {
		output = string(event.Tool.Output)
		return extension.EventResult{Output: json.RawMessage(`{"content":"redacted","count":9007199254740997}`)}, nil
	})
	w := serveWire(t, ext)

	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.event.handle","params":{"id":"e1","event":"tool.call","payload":{"tool":{"name":"lookup","input":{"id":9007199254740993}}}}}`)
	call := w.next()
	// JSONEq compares through float64, so compare the exact bytes.
	assert.Equal(t, `{"id":9007199254740993}`, first)
	assert.Equal(t, `{"id":9007199254740995}`, second)
	assert.Contains(t, string(call.Result), `"input":{"id":9007199254740995}`)

	w.send(`{"jsonrpc":"2.0","id":2,"method":"extension.event.handle","params":{"id":"e2","event":"tool.result","payload":{"tool":{"name":"lookup","input":{},"output":{"content":"x","count":9007199254740999}}}}}`)
	result := w.next()
	assert.Equal(t, `{"content":"x","count":9007199254740999}`, output)
	assert.Contains(t, string(result.Result), `"count":9007199254740997`)
}

func TestHandlerPanicsAndRPCErrorsBecomeErrorResponses(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	addTool(ext, "panic", func(context.Context, *extension.ToolContext) (extension.ToolResult, error) { panic("boom") })
	addTool(ext, "coded", func(context.Context, *extension.ToolContext) (extension.ToolResult, error) {
		return extension.ToolResult{}, &extension.RPCError{Code: -32050, Message: "custom failure"}
	})
	w := serveWire(t, ext)
	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"panic","input":{}}}`)
	panicked := w.next()
	require.NotNil(t, panicked.Error)
	assert.Equal(t, jsonrpc.CodeServerError, panicked.Error.Code)
	assert.True(t, strings.HasPrefix(panicked.Error.Message, "extension handler panicked: boom"))
	w.send(`{"jsonrpc":"2.0","id":2,"method":"extension.tool.execute","params":{"name":"coded","input":{}}}`)
	coded := w.next()
	require.NotNil(t, coded.Error)
	assert.Equal(t, -32050, coded.Error.Code)
	assert.Equal(t, "custom failure", coded.Error.Message)
}

func TestRuntimeCloseRunsBoundedSessionCleanup(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.On(ext, extension.EventSessionEnd, func(ctx context.Context, _ extension.SessionEndEvent, _ *extension.EventContext) (extension.EventResult, error) {
		<-ctx.Done()
		return extension.EventResult{}, nil
	})
	runtime := extension.NewRuntime(ext, extension.TransportFunc(func(context.Context, []byte) error { return nil }), &extension.RuntimeOptions{LogOutput: io.Discard})
	started := time.Now()
	runtime.Close(nil)
	runtime.Close(nil)
	assert.Less(t, time.Since(started), 3*time.Second)
	require.ErrorIs(t, runtime.Receive([]byte(`{}`)), extension.ErrRuntimeClosed)
}
