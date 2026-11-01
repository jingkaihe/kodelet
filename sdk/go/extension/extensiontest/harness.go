// Package extensiontest drives an extension through its real JSON-RPC
// dispatcher with an in-memory host, for unit tests.
//
//	h := extensiontest.New(ext, nil)
//	defer h.Close()
//	result, err := h.ExecuteTool(ctx, "get_weather", map[string]any{"location": "London"})
package extensiontest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// HostCall is a request or notification the extension sent to the host.
type HostCall struct {
	Method string
	Params json.RawMessage
	// ParentID is the originating host request ID for scoped requests.
	ParentID json.RawMessage
}

// DecodeParams decodes the call parameters into v.
func (c HostCall) DecodeParams(v any) error {
	return json.Unmarshal(c.Params, v)
}

// HostFunc answers extension-initiated host requests. Return an
// *extension.RPCError to choose the error code.
type HostFunc func(ctx context.Context, call HostCall) (any, error)

// Options configures a Harness.
type Options struct {
	// Host answers host requests. The default reports interactive UI as
	// unavailable, accepts notifications and tool updates, and rejects other
	// methods as not found.
	Host HostFunc
	// LogOutput receives handler logs; it defaults to io.Discard.
	LogOutput io.Writer
}

// DefaultInitializeParams returns the initialize parameters used when a test
// does not call Initialize explicitly.
func DefaultInitializeParams() extension.InitializeParams {
	cwd, _ := os.Getwd()
	return extension.InitializeParams{
		ProtocolVersion: extension.ProtocolVersion,
		Kodelet:         map[string]any{"version": "test"},
		Extension: extension.ExtensionInfo{
			ID:     "test-extension",
			CWD:    cwd,
			Config: map[string]any{},
		},
		Capabilities: map[string]any{
			"toolUpdates": true,
			"shortcuts":   map[string]any{"submit": true},
		},
	}
}

// Harness is an in-memory Kodelet host for one extension runtime.
type Harness struct {
	runtime *extension.Runtime
	host    HostFunc

	mu            sync.Mutex
	nextID        int64
	nextEvent     int
	pending       map[string]chan jsonrpc.Message
	calls         []HostCall
	notifications []HostCall
	initialized   bool
}

// New creates a harness for ext.
func New(ext *extension.Extension, opts *Options) *Harness {
	if opts == nil {
		opts = &Options{}
	}
	h := &Harness{host: opts.Host, pending: map[string]chan jsonrpc.Message{}}
	if h.host == nil {
		h.host = defaultHost
	}
	output := opts.LogOutput
	if output == nil {
		output = io.Discard
	}
	h.runtime = extension.NewRuntime(ext, extension.TransportFunc(h.receive), &extension.RuntimeOptions{LogOutput: output})
	return h
}

func defaultHost(_ context.Context, call HostCall) (any, error) {
	switch call.Method {
	case "kodelet.ui.input", "kodelet.ui.confirm", "kodelet.ui.select":
		return map[string]any{"status": "unavailable"}, nil
	case "kodelet.ui.notify", "kodelet.tool.update":
		return map[string]any{}, nil
	default:
		return nil, &extension.RPCError{Code: jsonrpc.CodeMethodNotFound, Message: "method not found: " + call.Method}
	}
}

// Close closes the runtime, running session.end handlers.
func (h *Harness) Close() {
	h.runtime.Close(nil)
}

// Runtime returns the underlying runtime.
func (h *Harness) Runtime() *extension.Runtime {
	return h.runtime
}

// HostCalls returns the host requests issued so far.
func (h *Harness) HostCalls() []HostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostCall(nil), h.calls...)
}

// Notifications returns the notifications sent to the host so far.
func (h *Harness) Notifications() []HostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostCall(nil), h.notifications...)
}

// Notify delivers a host notification, such as kodelet.ui.capabilities or a
// surface event, to the extension.
func (h *Harness) Notify(method string, params any) error {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil {
		return err
	}
	data, err := json.Marshal(jsonrpc.NewNotification(method, encoded))
	if err != nil {
		return err
	}
	return h.runtime.Receive(data)
}

// Initialize sends extension.initialize. A nil params uses DefaultInitializeParams.
func (h *Harness) Initialize(ctx context.Context, params *extension.InitializeParams) (extension.InitializeResult, error) {
	if params == nil {
		defaults := DefaultInitializeParams()
		params = &defaults
	}
	var result extension.InitializeResult
	err := h.call(ctx, "extension.initialize", params, &result)
	if err == nil {
		h.mu.Lock()
		h.initialized = true
		h.mu.Unlock()
	}
	return result, err
}

func (h *Harness) ensureInitialized(ctx context.Context) error {
	h.mu.Lock()
	initialized := h.initialized
	h.mu.Unlock()
	if initialized {
		return nil
	}
	_, err := h.Initialize(ctx, nil)
	return err
}

// CallOption sets the call context Kodelet sends with a request.
type CallOption func(map[string]any)

// WithCall sets the call context for a request. RunnerID comes from initialization.
func WithCall(info extension.CallInfo) CallOption {
	return func(context map[string]any) {
		set := func(key, value string) {
			if value != "" {
				context[key] = value
			}
		}
		set("sessionId", info.SessionID)
		set("conversationId", info.ConversationID)
		set("uiScopeId", info.UIScopeID)
		set("cwd", info.CWD)
		set("provider", info.Provider)
		set("model", info.Model)
		set("profile", info.Profile)
		set("recipeName", info.RecipeName)
		set("invokedBy", info.InvokedBy)
	}
}

func callContext(opts []CallOption) map[string]any {
	context := map[string]any{}
	for _, opt := range opts {
		opt(context)
	}
	return context
}

// ExecuteTool calls a tool with input encoded as JSON.
func (h *Harness) ExecuteTool(ctx context.Context, name string, input any, opts ...CallOption) (extension.ToolResult, error) {
	var result extension.ToolResult
	if err := h.ensureInitialized(ctx); err != nil {
		return result, err
	}
	err := h.call(ctx, "extension.tool.execute", map[string]any{"name": name, "input": input, "context": callContext(opts)}, &result)
	return result, err
}

// ExecuteCommand calls a command with structured input and its invocation.
func (h *Harness) ExecuteCommand(ctx context.Context, name string, input any, invocation extension.CommandInvocation, opts ...CallOption) (extension.CommandResult, error) {
	var result extension.CommandResult
	if err := h.ensureInitialized(ctx); err != nil {
		return result, err
	}
	params := map[string]any{"name": name, "invocation": invocation, "context": callContext(opts)}
	if input != nil {
		params["input"] = input
	}
	err := h.call(ctx, "extension.command.execute", params, &result)
	return result, err
}

// ExecuteShortcut runs a shortcut. A zero result means no follow-up.
func (h *Harness) ExecuteShortcut(ctx context.Context, key string, opts ...CallOption) (extension.ShortcutResult, error) {
	var result extension.ShortcutResult
	if err := h.ensureInitialized(ctx); err != nil {
		return result, err
	}
	err := h.call(ctx, "extension.shortcut.execute", map[string]any{"key": key, "context": callContext(opts)}, &result)
	return result, err
}

// HandleEvent delivers an event with payload encoded as JSON.
func (h *Harness) HandleEvent(ctx context.Context, event string, payload any, opts ...CallOption) (extension.EventResult, error) {
	var result extension.EventResult
	if err := h.ensureInitialized(ctx); err != nil {
		return result, err
	}
	h.mu.Lock()
	h.nextEvent++
	id := "event-" + strconv.Itoa(h.nextEvent)
	h.mu.Unlock()
	params := map[string]any{"id": id, "event": event, "context": callContext(opts)}
	if payload != nil {
		params["payload"] = payload
	}
	err := h.call(ctx, "extension.event.handle", params, &result)
	return result, err
}

// call sends a request and waits for its response. If ctx ends first, the
// harness sends $/cancelRequest like Kodelet does.
func (h *Harness) call(ctx context.Context, method string, params any, result any) error {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.nextID++
	id := jsonrpc.IntRaw(h.nextID)
	key, _ := jsonrpc.IDKey(id)
	response := make(chan jsonrpc.Message, 1)
	h.pending[key] = response
	h.mu.Unlock()
	data, err := json.Marshal(jsonrpc.NewRequest(id, method, encoded))
	if err != nil {
		return err
	}
	if err := h.runtime.Receive(data); err != nil {
		return err
	}
	select {
	case message := <-response:
		if message.Error != nil {
			return message.Error
		}
		if result != nil && jsonrpc.HasValue(message.Result) {
			return json.Unmarshal(message.Result, result)
		}
		return nil
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pending, key)
		h.mu.Unlock()
		_ = h.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}

func (h *Harness) receive(_ context.Context, data []byte) error {
	var message jsonrpc.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return fmt.Errorf("extension sent invalid JSON: %w", err)
	}
	if message.Method == "" {
		key, ok := jsonrpc.IDKey(message.ID)
		if !ok {
			return nil
		}
		h.mu.Lock()
		response := h.pending[key]
		delete(h.pending, key)
		h.mu.Unlock()
		if response != nil {
			response <- message
		}
		return nil
	}
	call := HostCall{Method: message.Method, Params: message.Params, ParentID: message.ParentID}
	h.mu.Lock()
	if !message.HasID() {
		h.notifications = append(h.notifications, call)
		h.mu.Unlock()
		return nil
	}
	h.calls = append(h.calls, call)
	h.mu.Unlock()
	go h.answer(message.ID, call)
	return nil
}

func (h *Harness) answer(id json.RawMessage, call HostCall) {
	value, err := h.host(context.Background(), call)
	var reply jsonrpc.Message
	if err != nil {
		code := jsonrpc.CodeServerError
		var rpcErr *extension.RPCError
		if errors.As(err, &rpcErr) {
			code = rpcErr.Code
		}
		reply = jsonrpc.NewError(id, code, err.Error())
	} else {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			reply = jsonrpc.NewError(id, jsonrpc.CodeServerError, marshalErr.Error())
		} else {
			reply = jsonrpc.NewResult(id, encoded)
		}
	}
	data, _ := json.Marshal(reply)
	_ = h.runtime.Receive(data)
}
