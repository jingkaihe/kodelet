package extension_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/require"
)

type toolFunc = func(context.Context, *extension.ToolContext) (extension.ToolResult, error)

// addTool registers a tool that takes no input.
func addTool(ext *extension.Extension, name string, fn toolFunc) {
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: name, Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		return fn(ctx, tc)
	}})
}

func newHarness(t *testing.T, ext *extension.Extension, opts *extensiontest.Options) *extensiontest.Harness {
	t.Helper()
	h := extensiontest.New(ext, opts)
	t.Cleanup(h.Close)
	return h
}

// toolHarness serves an extension whose only tool, "run", calls fn. A nil
// host uses the harness default.
func toolHarness(t *testing.T, host extensiontest.HostFunc, fn toolFunc) *extensiontest.Harness {
	t.Helper()
	ext := extension.New(extension.Metadata{})
	addTool(ext, "run", fn)
	return newHarness(t, ext, &extensiontest.Options{Host: host})
}

func runTool(t *testing.T, h *extensiontest.Harness, opts ...extensiontest.CallOption) (extension.ToolResult, error) {
	t.Helper()
	return h.ExecuteTool(t.Context(), "run", map[string]any{}, opts...)
}

// initWith reinitializes h with the default parameters as changed by configure.
func initWith(t *testing.T, h *extensiontest.Harness, configure func(*extension.InitializeParams)) {
	t.Helper()
	params := extensiontest.DefaultInitializeParams()
	configure(&params)
	_, err := h.Initialize(t.Context(), &params)
	require.NoError(t, err)
}

// reply computes a host response from the request.
type reply = func(extensiontest.HostCall) (any, error)

// replies answers host requests by method with a fixed value or a reply
// func. Other methods fail as not found.
func replies(byMethod map[string]any) extensiontest.HostFunc {
	return func(_ context.Context, call extensiontest.HostCall) (any, error) {
		value, ok := byMethod[call.Method]
		if !ok {
			return nil, &extension.RPCError{Code: jsonrpc.CodeMethodNotFound, Message: "method not found: " + call.Method}
		}
		if compute, ok := value.(reply); ok {
			return compute(call)
		}
		return value, nil
	}
}

// methods lists the host requests h has received, in order.
func methods(h *extensiontest.Harness) []string {
	var names []string
	for _, call := range h.HostCalls() {
		names = append(names, call.Method)
	}
	return names
}

// firstCall returns the first host request for method.
func firstCall(t *testing.T, h *extensiontest.Harness, method string) extensiontest.HostCall {
	t.Helper()
	for _, call := range h.HostCalls() {
		if call.Method == method {
			return call
		}
	}
	t.Fatalf("no %s host request", method)
	return extensiontest.HostCall{}
}

// noop is an event handler that changes nothing.
func noop[P any](context.Context, P, *extension.EventContext) (extension.EventResult, error) {
	return extension.EventResult{}, nil
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

// wire drives an extension with raw JSON-RPC messages and records what it
// sends back.
type wire struct {
	t        *testing.T
	write    func([]byte) error
	messages chan jsonrpc.Message
	// closeInput and served are set for stdio wires: closing the input ends
	// Serve, whose result arrives on served.
	closeInput func() error
	served     chan error
}

func (w *wire) send(message string) {
	w.t.Helper()
	require.NoError(w.t, w.write([]byte(message)))
}

func (w *wire) next() jsonrpc.Message {
	w.t.Helper()
	return receive(w.t, w.messages)
}

// serveWire runs ext.Serve over in-memory pipes with Content-Length framing.
func serveWire(t *testing.T, ext *extension.Extension) *wire {
	t.Helper()
	hostToExtension, extensionInput := io.Pipe()
	extensionOutput, extensionToHost := io.Pipe()
	frames := jsonrpc.NewFrameWriter(extensionInput)
	w := &wire{t: t, write: frames.Write, messages: make(chan jsonrpc.Message, 32), closeInput: extensionInput.Close, served: make(chan error, 1)}
	go func() {
		w.served <- ext.Serve(context.Background(), hostToExtension, extensionToHost)
		_ = extensionToHost.Close()
	}()
	go func() {
		reader := jsonrpc.NewFrameReader(extensionOutput)
		for {
			payload, err := reader.Read()
			if err != nil {
				return
			}
			var message jsonrpc.Message
			if json.Unmarshal(payload, &message) == nil {
				w.messages <- message
			}
		}
	}()
	t.Cleanup(func() { _ = extensionInput.Close() })
	return w
}

// runtimeWire drives a Runtime directly. A non-nil intercept sees each
// outgoing message first and consumes it by returning true; it may block to
// simulate a slow host.
func runtimeWire(t *testing.T, ext *extension.Extension, opts extension.RuntimeOptions, intercept func(*wire, jsonrpc.Message) bool) *wire {
	t.Helper()
	w := &wire{t: t, messages: make(chan jsonrpc.Message, 32)}
	opts.LogOutput = io.Discard
	runtime := extension.NewRuntime(ext, extension.TransportFunc(func(_ context.Context, data []byte) error {
		var message jsonrpc.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return err
		}
		if intercept == nil || !intercept(w, message) {
			w.messages <- message
		}
		return nil
	}), &opts)
	w.write = runtime.Receive
	t.Cleanup(func() { runtime.Close(nil) })
	return w
}
