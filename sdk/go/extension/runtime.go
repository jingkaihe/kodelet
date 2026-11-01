package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/jingkaihe/kodelet/sdk/go/internal/queue"
)

const shutdownTimeout = time.Second

var (
	errHostDisconnected          = errors.New("extension host disconnected")
	errRequestCompleted          = errors.New("extension request completed")
	errRequestCancelled          = errors.New("extension request cancelled")
	errRequestIDReused           = errors.New("extension request id was reused")
	errShutdownTimeout           = errors.New("extension shutdown timed out after host disconnect")
	errSurfaceClosedWhileOpening = errors.New("the host closed the interactive surface while it was opening")
)

// Transport delivers outgoing extension JSON-RPC messages to the host.
// Send is called from a single goroutine, in message order.
type Transport interface {
	Send(ctx context.Context, message []byte) error
}

// TransportFunc adapts a function to Transport.
type TransportFunc func(ctx context.Context, message []byte) error

// Send calls f.
func (f TransportFunc) Send(ctx context.Context, message []byte) error {
	return f(ctx, message)
}

// LocalRequestFunc serves a host RPC request in-process. Return handled=false
// to forward the request to the host. It runs on its own goroutine with a
// context that ends with the caller's context or the originating request.
type LocalRequestFunc func(ctx context.Context, method string, params json.RawMessage) (result any, handled bool, err error)

// RuntimeOptions configures NewRuntime.
type RuntimeOptions struct {
	// LocalRequest optionally serves host requests in-process.
	LocalRequest LocalRequestFunc
	// LogOutput receives handler logs; it defaults to os.Stderr.
	LogOutput io.Writer
}

// Runtime dispatches extension JSON-RPC for one host connection. Run and
// Serve use it over stdio; SDK sessions use it over the ACP relay. Most
// extensions do not need it directly.
type Runtime struct {
	ext        *Extension
	transport  Transport
	local      LocalRequestFunc
	logOutput  io.Writer
	baseCtx    context.Context
	cancelBase context.CancelCauseFunc
	outbox     *queue.Worker
	// inbox runs UI callbacks in order, off the read loop.
	inbox *queue.Worker

	mu        sync.Mutex
	closed    bool
	closeDone chan struct{}
	init      *InitializeParams
	active    map[string]*activeRequest
	pending   map[int64]*pendingCall
	nextID    int64
	loggers   map[string]*slog.Logger
	uiCaps    map[string]any
	sequences map[string]int
	surfaces  map[string]*Surface

	sessionEndOnce   sync.Once
	sessionEndDone   chan struct{}
	sessionEndResult *EventResult
	sessionEndErr    error
}

type activeRequest struct {
	id     json.RawMessage
	key    string
	ctx    context.Context
	cancel context.CancelCauseFunc
	active bool
}

type pendingCall struct {
	owner  *activeRequest
	result chan callResult
}

type callResult struct {
	result json.RawMessage
	err    error
}

// NewRuntime creates a runtime for ext that sends messages with transport.
func NewRuntime(ext *Extension, transport Transport, opts *RuntimeOptions) *Runtime {
	if opts == nil {
		opts = &RuntimeOptions{}
	}
	output := opts.LogOutput
	if output == nil {
		output = os.Stderr
	}
	baseCtx, cancel := context.WithCancelCause(context.Background())
	return &Runtime{
		ext:            ext,
		transport:      transport,
		local:          opts.LocalRequest,
		logOutput:      &lockedWriter{writer: output},
		baseCtx:        baseCtx,
		cancelBase:     cancel,
		outbox:         queue.NewWorker(),
		inbox:          queue.NewWorker(),
		closeDone:      make(chan struct{}),
		active:         map[string]*activeRequest{},
		pending:        map[int64]*pendingCall{},
		loggers:        map[string]*slog.Logger{},
		sequences:      map[string]int{},
		surfaces:       map[string]*Surface{},
		sessionEndDone: make(chan struct{}),
	}
}

// Receive handles one incoming JSON-RPC message without blocking on handlers.
func (r *Runtime) Receive(message []byte) error {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return ErrRuntimeClosed
	}
	var msg jsonrpc.Message
	if err := json.Unmarshal(message, &msg); err != nil {
		r.send(jsonrpc.NewError(nil, jsonrpc.CodeParseError, err.Error()))
		return nil
	}
	switch {
	case msg.Method == "":
		// Duplex IDs are independent: even unmatched responses are not requests.
		r.handleResponse(msg)
	case msg.Method == "$/cancelRequest" && !msg.HasID():
		r.cancelRequest(msg.Params)
	case !msg.HasID():
		if callbacks := r.handleNotification(msg.Method, msg.Params); callbacks != nil {
			r.inbox.Submit(callbacks)
		}
	default:
		r.startRequest(msg)
	}
	return nil
}

// Close aborts active handlers, rejects pending host requests, and runs
// session.end handlers once with a bounded timeout. It is safe to call more
// than once; later calls wait for the first to finish.
func (r *Runtime) Close(cause error) {
	if cause == nil {
		cause = errHostDisconnected
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.closeDone
		return
	}
	r.closed = true
	pending := r.pending
	r.pending = map[int64]*pendingCall{}
	active := r.active
	r.active = map[string]*activeRequest{}
	for _, request := range active {
		r.deactivateLocked(request, cause)
	}
	r.mu.Unlock()
	for _, call := range pending {
		call.result <- callResult{err: cause}
	}

	ctx, cancel := context.WithTimeoutCause(context.Background(), shutdownTimeout, errShutdownTimeout)
	done := make(chan struct{})
	go func() {
		defer close(done)
		env := r.env(&requestClient{rt: r})
		_, _ = r.sessionEnd(ctx, env, handleEventParams{ID: "host-disconnected", Event: EventSessionEnd.name})
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	cancel()
	r.cancelBase(cause)
	r.outbox.Stop()
	r.inbox.Stop()
	close(r.closeDone)
}

func (r *Runtime) send(message jsonrpc.Message) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	r.outbox.Submit(func() {
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return
		}
		if err := r.transport.Send(r.baseCtx, data); err != nil {
			go r.Close(err)
		}
	})
}

func (r *Runtime) handleResponse(msg jsonrpc.Message) {
	id, ok := msg.IntID()
	if !ok {
		return
	}
	r.mu.Lock()
	call := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if call == nil {
		return
	}
	if msg.Error != nil {
		call.result <- callResult{err: msg.Error}
		return
	}
	call.result <- callResult{result: msg.Result}
}

func (r *Runtime) cancelRequest(params json.RawMessage) {
	var cancel struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(params, &cancel) != nil {
		return
	}
	key, ok := jsonrpc.IDKey(cancel.ID)
	if !ok {
		return
	}
	r.mu.Lock()
	request := r.active[key]
	if request != nil {
		r.finishLocked(request, errRequestCancelled)
	}
	r.mu.Unlock()
}

func (r *Runtime) startRequest(msg jsonrpc.Message) {
	key, ok := jsonrpc.IDKey(msg.ID)
	if !ok {
		r.send(jsonrpc.NewError(msg.ID, jsonrpc.CodeInvalidParams, "invalid request id"))
		return
	}
	ctx, cancel := context.WithCancelCause(r.baseCtx)
	request := &activeRequest{id: msg.ID, key: key, ctx: ctx, cancel: cancel, active: true}
	r.mu.Lock()
	if previous := r.active[key]; previous != nil {
		r.finishLocked(previous, errRequestIDReused)
	}
	r.active[key] = request
	init := r.init
	r.mu.Unlock()

	go func() {
		result, err := r.dispatch(ctx, request, init, msg)
		r.mu.Lock()
		respond := request.active
		r.finishLocked(request, errRequestCompleted)
		r.mu.Unlock()
		if !respond {
			return
		}
		if err != nil {
			code := jsonrpc.CodeServerError
			var rpcErr *RPCError
			if errors.As(err, &rpcErr) {
				code = rpcErr.Code
			}
			r.send(jsonrpc.NewError(request.id, code, err.Error()))
			return
		}
		r.send(jsonrpc.NewResult(request.id, result))
	}()
}

func (r *Runtime) dispatch(ctx context.Context, request *activeRequest, init *InitializeParams, msg jsonrpc.Message) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("extension handler panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	env := &callEnv{rt: r, client: &requestClient{rt: r, owner: request}, init: init}
	var value any
	switch msg.Method {
	case "extension.initialize":
		var params InitializeParams
		if err := decodeParams(msg.Params, &params); err != nil {
			return nil, err
		}
		initialized, err := r.ext.initialize(params)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.init = &params
		r.mu.Unlock()
		value = initialized
	case "extension.tool.execute":
		var params executeToolParams
		if err := decodeParams(msg.Params, &params); err != nil {
			return nil, err
		}
		value, err = r.ext.executeTool(ctx, env, params)
	case "extension.command.execute":
		var params executeCommandParams
		if err := decodeParams(msg.Params, &params); err != nil {
			return nil, err
		}
		value, err = r.ext.executeCommand(ctx, env, params)
	case "extension.shortcut.execute":
		var params executeShortcutParams
		if err := decodeParams(msg.Params, &params); err != nil {
			return nil, err
		}
		var shortcut ShortcutResult
		shortcut, err = r.ext.executeShortcut(ctx, env, params)
		if shortcut.Action != "" {
			value = shortcut
		}
	case "extension.event.handle":
		var params handleEventParams
		if err := decodeParams(msg.Params, &params); err != nil {
			return nil, err
		}
		if params.Event == EventSessionEnd.name {
			value, err = r.sessionEnd(ctx, env, params)
		} else {
			value, err = r.ext.handleEvent(ctx, env, params)
		}
	default:
		return nil, &RPCError{Code: jsonrpc.CodeMethodNotFound, Message: "unknown JSON-RPC method: " + msg.Method}
	}
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func decodeParams(raw json.RawMessage, target any) error {
	if !jsonrpc.HasValue(raw) {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &RPCError{Code: jsonrpc.CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return nil
}

// sessionEnd runs session.end handlers at most once per runtime, whether the
// host sends the event or the connection closes first.
func (r *Runtime) sessionEnd(ctx context.Context, env *callEnv, params handleEventParams) (*EventResult, error) {
	r.sessionEndOnce.Do(func() {
		go func() {
			defer close(r.sessionEndDone)
			defer func() {
				if recovered := recover(); recovered != nil {
					r.sessionEndErr = fmt.Errorf("session.end handler panicked: %v", recovered)
				}
			}()
			r.sessionEndResult, r.sessionEndErr = r.ext.handleEvent(ctx, env, params)
		}()
	})
	select {
	case <-r.sessionEndDone:
		return r.sessionEndResult, r.sessionEndErr
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (r *Runtime) env(client *requestClient) *callEnv {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &callEnv{rt: r, client: client, init: r.init}
}

func (r *Runtime) deactivateLocked(request *activeRequest, cause error) {
	if request.active {
		request.active = false
		request.cancel(cause)
	}
}

// finishLocked ends a request: it cancels its context, rejects host requests
// it issued, and forgets it. Callers hold r.mu.
func (r *Runtime) finishLocked(request *activeRequest, cause error) {
	r.deactivateLocked(request, cause)
	for id, call := range r.pending {
		if call.owner == request {
			delete(r.pending, id)
			call.result <- callResult{err: cause}
		}
	}
	if r.active[request.key] == request {
		delete(r.active, request.key)
	}
}

func (r *Runtime) isActive(request *activeRequest) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return request.active
}

// call sends a host RPC request. A non-nil owner scopes it to that incoming
// request with parentId; the call fails when the owner finishes.
func (r *Runtime) call(ctx context.Context, owner *activeRequest, method string, params any) (json.RawMessage, error) {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrRuntimeClosed
	}
	if owner != nil && !owner.active {
		r.mu.Unlock()
		return nil, ErrRequestInactive
	}
	r.nextID++
	id := r.nextID
	call := &pendingCall{owner: owner, result: make(chan callResult, 1)}
	r.pending[id] = call
	r.mu.Unlock()

	message := jsonrpc.NewRequest(jsonrpc.IntRaw(id), method, encoded)
	if owner != nil {
		message.ParentID = owner.id
	}
	if r.local != nil {
		// Off the caller's goroutine, so the caller can give up at its deadline
		// even if the local handler ignores cancellation.
		go r.serveLocal(ctx, owner, id, call, message)
	} else {
		r.send(message)
	}
	select {
	case outcome := <-call.result:
		return outcome.result, outcome.err
	case <-ctx.Done():
		r.mu.Lock()
		if r.pending[id] == call {
			delete(r.pending, id)
		}
		r.mu.Unlock()
		return nil, ctx.Err()
	}
}

// serveLocal answers a host request in-process, or forwards it to the host
// when the local handler declines. The handler's context ends with either the
// caller's context or the originating request.
func (r *Runtime) serveLocal(ctx context.Context, owner *activeRequest, id int64, call *pendingCall, message jsonrpc.Message) {
	scope := r.baseCtx
	if owner != nil {
		scope = owner.ctx
	}
	localCtx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(scope, func() { cancel(context.Cause(scope)) })
	defer func() {
		stop()
		cancel(nil)
	}()
	value, handled, err := r.runLocal(localCtx, message)
	r.mu.Lock()
	waiting := r.pending[id] == call
	if handled && waiting {
		delete(r.pending, id)
	}
	r.mu.Unlock()
	switch {
	case !waiting:
	case !handled:
		r.send(message)
	default:
		var result json.RawMessage
		if err == nil {
			result, err = json.Marshal(value)
		}
		call.result <- callResult{result: result, err: err}
	}
}

func (r *Runtime) runLocal(ctx context.Context, message jsonrpc.Message) (value any, handled bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value, handled, err = nil, true, fmt.Errorf("local %s handler panicked: %v", message.Method, recovered)
		}
	}()
	return r.local(ctx, message.Method, message.Params)
}

func (r *Runtime) notify(method string, params any) error {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil {
		return err
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return ErrRuntimeClosed
	}
	r.send(jsonrpc.NewNotification(method, encoded))
	return nil
}

// handleNotification applies host notifications in arrival order and returns
// any user callbacks to run on the notification goroutine.
func (r *Runtime) handleNotification(method string, params json.RawMessage) func() {
	if method == "kodelet.ui.capabilities" {
		var capabilities map[string]any
		if json.Unmarshal(params, &capabilities) == nil && capabilities != nil {
			r.mu.Lock()
			r.uiCaps = capabilities
			r.mu.Unlock()
		}
		return nil
	}
	var notification surfaceNotification
	if json.Unmarshal(params, &notification) != nil || notification.ID == "" {
		return nil
	}
	r.mu.Lock()
	surface := r.surfaces[uiObjectKey(normalizedUIScope(notification.ScopeID), notification.ID)]
	r.mu.Unlock()
	if surface == nil {
		return nil
	}
	return surface.handleNotification(method, notification)
}

func (r *Runtime) logger(extensionID string) *slog.Logger {
	r.mu.Lock()
	defer r.mu.Unlock()
	logger := r.loggers[extensionID]
	if logger == nil {
		logger = newLogger(r.logOutput, extensionID)
		r.loggers[extensionID] = logger
	}
	return logger
}

func (r *Runtime) uiCapabilities() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uiCaps
}

func (r *Runtime) nextSequence(kind, key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequences[kind+"\x00"+key]++
	return r.sequences[kind+"\x00"+key]
}

func (r *Runtime) registerSurface(key string, surface *Surface) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.surfaces[key] != nil {
		return false
	}
	r.surfaces[key] = surface
	return true
}

func (r *Runtime) unregisterSurface(key string, surface *Surface) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.surfaces[key] == surface {
		delete(r.surfaces, key)
	}
}

// requestClient issues host requests on behalf of one call.
type requestClient struct {
	rt    *Runtime
	owner *activeRequest
}

func (c *requestClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.rt.call(ctx, c.owner, method, params)
}

// requestPersistent scopes the request to the originating call while it is
// active and sends it unscoped afterwards, for UI and leases that outlive it.
func (c *requestClient) requestPersistent(ctx context.Context, method string, params any) (json.RawMessage, error) {
	owner := c.owner
	if owner != nil && !c.rt.isActive(owner) {
		owner = nil
	}
	return c.rt.call(ctx, owner, method, params)
}
