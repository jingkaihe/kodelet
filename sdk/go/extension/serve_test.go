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

// stdioHost plays the Kodelet side of a framed stdio connection.
type stdioHost struct {
	t        *testing.T
	writer   *io.PipeWriter
	frames   *jsonrpc.FrameWriter
	messages chan jsonrpc.Message
	done     chan error
}

func serveExtension(t *testing.T, ext *extension.Extension) *stdioHost {
	t.Helper()
	hostToExtension, extensionInput := io.Pipe()
	extensionOutput, extensionToHost := io.Pipe()
	host := &stdioHost{
		t:        t,
		writer:   extensionInput,
		frames:   jsonrpc.NewFrameWriter(extensionInput),
		messages: make(chan jsonrpc.Message, 32),
		done:     make(chan error, 1),
	}
	go func() {
		host.done <- ext.Serve(context.Background(), hostToExtension, extensionToHost)
		_ = extensionToHost.Close()
	}()
	go func() {
		reader := jsonrpc.NewFrameReader(extensionOutput)
		for {
			payload, err := reader.Read()
			if err != nil {
				close(host.messages)
				return
			}
			var message jsonrpc.Message
			if json.Unmarshal(payload, &message) == nil {
				host.messages <- message
			}
		}
	}()
	t.Cleanup(func() { _ = extensionInput.Close() })
	return host
}

func (h *stdioHost) send(message string) {
	h.t.Helper()
	require.NoError(h.t, h.frames.Write([]byte(message)))
}

func (h *stdioHost) next() jsonrpc.Message {
	h.t.Helper()
	select {
	case message, ok := <-h.messages:
		require.True(h.t, ok, "extension closed its output")
		return message
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for an extension message")
		return jsonrpc.Message{}
	}
}

func TestServeSpeaksFramedJSONRPCAndCleansUpOnDisconnect(t *testing.T) {
	ext := extension.New(extension.Metadata{Name: "echo"})
	extension.RegisterTool(ext, extension.Tool[struct {
		Text string `json:"text"`
	}]{Name: "echo", Description: "Echo", Execute: func(_ context.Context, in struct {
		Text string `json:"text"`
	}, _ *extension.ToolContext,
	) (extension.ToolResult, error) {
		return extension.Text(in.Text), nil
	}})
	var sessionEnded atomic.Int32
	extension.On(ext, extension.EventSessionEnd, func(context.Context, extension.SessionEndEvent, *extension.EventContext) (extension.EventResult, error) {
		sessionEnded.Add(1)
		return extension.EventResult{}, nil
	})
	host := serveExtension(t, ext)

	host.send(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"protocolVersion":"2026-05-30","extension":{"id":"echo"},"capabilities":{}}}`)
	initialized := host.next()
	assert.JSONEq(t, "1", string(initialized.ID))
	assert.Contains(t, string(initialized.Result), `"name":"echo"`)

	host.send(`{"jsonrpc":"2.0","id":"call-1","method":"extension.tool.execute","params":{"name":"echo","input":{"text":"hi"},"context":{}}}`)
	result := host.next()
	assert.JSONEq(t, `"call-1"`, string(result.ID))
	assert.JSONEq(t, `{"content":"hi"}`, string(result.Result))

	host.send(`not json`)
	parseError := host.next()
	require.NotNil(t, parseError.Error)
	assert.Equal(t, jsonrpc.CodeParseError, parseError.Error.Code)
	assert.Equal(t, "null", string(parseError.ID))

	host.send(`{"jsonrpc":"2.0","id":2,"method":"extension.unknown"}`)
	unknown := host.next()
	require.NotNil(t, unknown.Error)
	assert.Equal(t, jsonrpc.CodeMethodNotFound, unknown.Error.Code)

	host.send(`{"jsonrpc":"2.0","id":3,"method":"extension.event.handle","params":{"id":"e1","event":"session.end"}}`)
	assert.JSONEq(t, "{}", string(host.next().Result))

	require.NoError(t, host.writer.Close())
	select {
	case err := <-host.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after EOF")
	}
	assert.Equal(t, int32(1), sessionEnded.Load(), "session.end runs once across the event and disconnect cleanup")
}

func TestCancellationAbortsHandlersAndBlocksLateHostRPC(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	started := make(chan struct{})
	lateErr := make(chan error, 1)
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "wait", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		close(started)
		<-ctx.Done()
		lateErr <- tc.UI.Notify(context.Background(), extension.NotifyRequest{Message: "too late"})
		return extension.Text("late result"), nil
	}})
	host := serveExtension(t, ext)
	host.send(`{"jsonrpc":"2.0","id":7,"method":"extension.tool.execute","params":{"name":"wait","input":{}}}`)
	<-started
	host.send(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":7}}`)
	require.ErrorIs(t, <-lateErr, extension.ErrRequestInactive)

	host.send(`{"jsonrpc":"2.0","id":8,"method":"extension.unknown"}`)
	next := host.next()
	assert.JSONEq(t, "8", string(next.ID), "the cancelled request gets no response")
}

func TestReverseResponsesCannotCompleteForwardRequestsWithTheSameID(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "ask", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		value, ok, err := tc.UI.Input(ctx, extension.InputRequest{Title: "Name"})
		if err != nil || !ok {
			return extension.ToolResult{}, errors.Join(err, errors.New("no input"))
		}
		return extension.Text(value), nil
	}})
	host := serveExtension(t, ext)
	host.send(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"protocolVersion":"2026-05-30","extension":{"id":"x"}}}`)
	host.next()
	host.send(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"ask","input":{}}}`)
	reverse := host.next()
	assert.Equal(t, "kodelet.ui.input", reverse.Method)
	assert.JSONEq(t, "1", string(reverse.ID))
	assert.JSONEq(t, "1", string(reverse.ParentID))
	host.send(`{"jsonrpc":"2.0","id":1,"result":{"status":"submitted","value":"Ada"}}`)
	result := host.next()
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
	host := serveExtension(t, ext)

	host.send(`{"jsonrpc":"2.0","id":1,"method":"extension.event.handle","params":{"id":"e1","event":"tool.call","payload":{"tool":{"name":"lookup","input":{"id":9007199254740993}}}}}`)
	call := host.next()
	// JSONEq compares through float64, so compare the exact bytes.
	assert.Equal(t, `{"id":9007199254740993}`, first)
	assert.Equal(t, `{"id":9007199254740995}`, second)
	assert.Contains(t, string(call.Result), `"input":{"id":9007199254740995}`)

	host.send(`{"jsonrpc":"2.0","id":2,"method":"extension.event.handle","params":{"id":"e2","event":"tool.result","payload":{"tool":{"name":"lookup","input":{},"output":{"content":"x","count":9007199254740999}}}}}`)
	result := host.next()
	assert.Equal(t, `{"content":"x","count":9007199254740999}`, output)
	assert.Contains(t, string(result.Result), `"count":9007199254740997`)
}

func TestHandlerPanicsAndRPCErrorsBecomeErrorResponses(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "panic", Execute: func(context.Context, struct{}, *extension.ToolContext) (extension.ToolResult, error) {
		panic("boom")
	}})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "coded", Execute: func(context.Context, struct{}, *extension.ToolContext) (extension.ToolResult, error) {
		return extension.ToolResult{}, &extension.RPCError{Code: -32050, Message: "custom failure"}
	}})
	host := serveExtension(t, ext)
	host.send(`{"jsonrpc":"2.0","id":1,"method":"extension.tool.execute","params":{"name":"panic","input":{}}}`)
	panicked := host.next()
	require.NotNil(t, panicked.Error)
	assert.Equal(t, jsonrpc.CodeServerError, panicked.Error.Code)
	assert.True(t, strings.HasPrefix(panicked.Error.Message, "extension handler panicked: boom"))
	host.send(`{"jsonrpc":"2.0","id":2,"method":"extension.tool.execute","params":{"name":"coded","input":{}}}`)
	coded := host.next()
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
