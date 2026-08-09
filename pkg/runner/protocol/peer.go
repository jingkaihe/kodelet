package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

const (
	defaultControlQueueSize = 128
	defaultUpdateQueueSize  = 64
	defaultWriteWait        = 10 * time.Second
	defaultShutdownWait     = 10 * time.Second
	defaultPongWait         = 45 * time.Second
	defaultPingPeriod       = 30 * time.Second
	defaultReadLimit        = 4 * 1024 * 1024
	defaultMaxRequests      = 64
	defaultMaxControlCalls  = 8
	defaultMaxNotifications = 64
)

var (
	// ErrPeerClosed is returned when an RPC operation targets a closed peer.
	ErrPeerClosed = errors.New("runner rpc peer is closed")
	// ErrPeerNotStarted is returned when an operation precedes Start.
	ErrPeerNotStarted = errors.New("runner rpc peer is not started")
)

// RequestHandler handles requests received from the remote peer.
type RequestHandler interface {
	HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *RPCError)
}

// RequestHandlerFunc adapts a function to RequestHandler.
type RequestHandlerFunc func(context.Context, string, json.RawMessage) (any, *RPCError)

func (f RequestHandlerFunc) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
	return f(ctx, method, params)
}

// NotificationHandler handles notifications received from the remote peer.
type NotificationHandler interface {
	HandleNotification(ctx context.Context, method string, params json.RawMessage)
}

// NotificationHandlerFunc adapts a function to NotificationHandler.
type NotificationHandlerFunc func(context.Context, string, json.RawMessage)

func (f NotificationHandlerFunc) HandleNotification(ctx context.Context, method string, params json.RawMessage) {
	f(ctx, method, params)
}

// PeerConfig configures one symmetric JSON-RPC WebSocket peer.
type PeerConfig struct {
	RequestPrefix                string
	Handler                      RequestHandler
	Notifications                NotificationHandler
	ControlQueueSize             int
	UpdateQueueSize              int
	WriteWait                    time.Duration
	ShutdownWait                 time.Duration
	PongWait                     time.Duration
	PingPeriod                   time.Duration
	ReadLimit                    int64
	WriteLimit                   int64
	MaxConcurrentRequests        int
	MaxConcurrentControlRequests int
	MaxConcurrentNotifications   int
}

type outboundFrame struct {
	messageType int
	payload     []byte
	done        chan error
}

type callResult struct {
	message Message
	err     error
}

type inboundCall struct {
	cancel context.CancelFunc
}

type requestIDContextKey struct{}

// RequestIDFromContext returns the wire request ID for an inbound RPC handler.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// Peer owns one WebSocket reader, one writer, bounded outbound queues, and symmetric RPC correlation.
type Peer struct {
	conn                *websocket.Conn
	config              PeerConfig
	control             chan outboundFrame
	updates             chan outboundFrame
	requestSeq          atomic.Uint64
	started             atomic.Bool
	ctx                 context.Context
	cancel              context.CancelFunc
	transportDone       chan struct{}
	done                chan struct{}
	failOnce            sync.Once
	errMu               sync.RWMutex
	terminalErr         error
	pendingMu           sync.Mutex
	pending             map[string]chan callResult
	inboundMu           sync.Mutex
	inbound             map[string]*inboundCall
	requestSlots        chan struct{}
	controlRequestSlots chan struct{}
	notificationSlots   chan struct{}
	workerMu            sync.Mutex
	workerWG            sync.WaitGroup
}

// NewPeer creates a dormant peer. Start must be called after handlers have been fully wired.
func NewPeer(conn *websocket.Conn, config PeerConfig) (*Peer, error) {
	if conn == nil {
		return nil, errors.New("runner websocket connection is required")
	}
	config = withPeerDefaults(config)
	return &Peer{
		conn:                conn,
		config:              config,
		control:             make(chan outboundFrame, config.ControlQueueSize),
		updates:             make(chan outboundFrame, config.UpdateQueueSize),
		transportDone:       make(chan struct{}),
		done:                make(chan struct{}),
		pending:             make(map[string]chan callResult),
		inbound:             make(map[string]*inboundCall),
		requestSlots:        make(chan struct{}, config.MaxConcurrentRequests),
		controlRequestSlots: make(chan struct{}, config.MaxConcurrentControlRequests),
		notificationSlots:   make(chan struct{}, config.MaxConcurrentNotifications),
	}, nil
}

func withPeerDefaults(config PeerConfig) PeerConfig {
	config.RequestPrefix = strings.TrimSpace(config.RequestPrefix)
	if config.RequestPrefix == "" {
		config.RequestPrefix = "rpc"
	}
	if config.ControlQueueSize <= 0 {
		config.ControlQueueSize = defaultControlQueueSize
	}
	if config.UpdateQueueSize <= 0 {
		config.UpdateQueueSize = defaultUpdateQueueSize
	}
	if config.WriteWait <= 0 {
		config.WriteWait = defaultWriteWait
	}
	if config.ShutdownWait <= 0 {
		config.ShutdownWait = defaultShutdownWait
	}
	if config.PongWait <= 0 {
		config.PongWait = defaultPongWait
	}
	if config.PingPeriod <= 0 {
		config.PingPeriod = defaultPingPeriod
	}
	if config.PingPeriod >= config.PongWait {
		config.PingPeriod = config.PongWait * 2 / 3
		if config.PingPeriod <= 0 {
			config.PingPeriod = time.Nanosecond
		}
	}
	if config.ReadLimit <= 0 {
		config.ReadLimit = defaultReadLimit
	}
	if config.WriteLimit <= 0 {
		config.WriteLimit = config.ReadLimit
	}
	if config.MaxConcurrentRequests <= 0 {
		config.MaxConcurrentRequests = defaultMaxRequests
	}
	if config.MaxConcurrentControlRequests <= 0 {
		config.MaxConcurrentControlRequests = defaultMaxControlCalls
	}
	if config.MaxConcurrentNotifications <= 0 {
		config.MaxConcurrentNotifications = defaultMaxNotifications
	}
	return config
}

// Start launches the peer's reader, writer, and ping loops.
func (p *Peer) Start(parent context.Context) error {
	if p == nil {
		return errors.New("runner rpc peer is required")
	}
	if !p.started.CompareAndSwap(false, true) {
		return errors.New("runner rpc peer is already started")
	}
	if parent == nil {
		parent = context.Background()
	}
	p.ctx, p.cancel = context.WithCancel(parent)
	p.conn.SetReadLimit(p.config.ReadLimit)
	if err := p.conn.SetReadDeadline(time.Now().Add(p.config.PongWait)); err != nil {
		p.fail(err)
		return err
	}
	p.conn.SetPongHandler(func(string) error {
		return p.conn.SetReadDeadline(time.Now().Add(p.config.PongWait))
	})

	go p.writeLoop()
	go p.readLoop()
	go p.pingLoop()
	go func() {
		<-p.ctx.Done()
		p.fail(p.ctx.Err())
	}()
	return nil
}

// TransportDone closes as soon as the WebSocket transport terminates.
func (p *Peer) TransportDone() <-chan struct{} {
	if p == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.transportDone
}

// Done closes after the transport and all in-flight handlers terminate.
func (p *Peer) Done() <-chan struct{} {
	if p == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.done
}

// Err returns the terminal connection error after Done closes.
func (p *Peer) Err() error {
	if p == nil {
		return ErrPeerClosed
	}
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.terminalErr
}

// Call sends a request and waits for its correlated response.
func (p *Peer) Call(ctx context.Context, method string, params any, result any) error {
	return p.call(ctx, method, params, result, nil)
}

// CallTracked sends a request and synchronously exposes its wire ID before enqueueing it.
func (p *Peer) CallTracked(ctx context.Context, method string, params any, result any, onRequestID func(string)) error {
	return p.call(ctx, method, params, result, onRequestID)
}

func (p *Peer) call(ctx context.Context, method string, params any, result any, onRequestID func(string)) error {
	if err := p.ready(); err != nil {
		return err
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return errors.New("runner rpc method is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	id := fmt.Sprintf("%s:%d", p.config.RequestPrefix, p.requestSeq.Add(1))
	paramsPayload, err := marshalRPCValue(params)
	if err != nil {
		return errors.Wrap(err, "failed to encode runner rpc params")
	}
	payload, err := json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: paramsPayload})
	if err != nil {
		return errors.Wrap(err, "failed to encode runner rpc request")
	}
	if err := p.validateOutboundFrame(websocket.TextMessage, payload); err != nil {
		return err
	}

	responseCh := make(chan callResult, 1)
	p.pendingMu.Lock()
	p.pending[id] = responseCh
	p.pendingMu.Unlock()
	if onRequestID != nil {
		onRequestID(id)
	}
	if err := p.enqueueControl(ctx, websocket.TextMessage, payload, nil); err != nil {
		p.removePending(id, responseCh)
		return err
	}

	select {
	case response := <-responseCh:
		if response.err != nil {
			return response.err
		}
		if response.message.Error != nil {
			return response.message.Error
		}
		if result == nil || len(response.message.Result) == 0 || string(response.message.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.message.Result, result); err != nil {
			return errors.Wrap(err, "failed to decode runner rpc result")
		}
		return nil
	case <-ctx.Done():
		if p.removePending(id, responseCh) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), p.config.WriteWait)
			_ = p.Notify(cancelCtx, MethodOperationCancel, OperationCancelParams{RequestID: id})
			cancel()
		}
		return ctx.Err()
	case <-p.transportDone:
		return p.closedError()
	}
}

// Notify sends a normal-priority notification.
func (p *Peer) Notify(ctx context.Context, method string, params any) error {
	payload, err := notificationPayload(method, params)
	if err != nil {
		return err
	}
	return p.enqueueControl(ctx, websocket.TextMessage, payload, nil)
}

// NotifyUpdate sends a replaceable low-priority notification. When the bounded
// queue is full, the oldest pending update is discarded in favor of this one.
func (p *Peer) NotifyUpdate(method string, params any) error {
	if err := p.ready(); err != nil {
		return err
	}
	payload, err := notificationPayload(method, params)
	if err != nil {
		return err
	}
	if err := p.validateOutboundFrame(websocket.TextMessage, payload); err != nil {
		return err
	}
	frame := outboundFrame{messageType: websocket.TextMessage, payload: payload}
	select {
	case p.updates <- frame:
		return nil
	default:
	}
	select {
	case <-p.updates:
	default:
	}
	select {
	case p.updates <- frame:
		return nil
	case <-p.transportDone:
		return p.closedError()
	default:
		return nil
	}
}

func notificationPayload(method string, params any) ([]byte, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("runner rpc method is required")
	}
	paramsPayload, err := marshalRPCValue(params)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode runner rpc notification params")
	}
	payload, err := json.Marshal(Message{JSONRPC: JSONRPCVersion, Method: method, Params: paramsPayload})
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode runner rpc notification")
	}
	return payload, nil
}

// Shutdown sends a WebSocket close frame, then terminates the peer.
func (p *Peer) Shutdown(ctx context.Context, code int, reason string) error {
	if p == nil || !p.started.Load() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.config.ShutdownWait)
		defer cancel()
	}
	if code == 0 {
		code = websocket.CloseNormalClosure
	}
	done := make(chan error, 1)
	payload := websocket.FormatCloseMessage(code, reason)
	if err := p.enqueueControl(ctx, websocket.CloseMessage, payload, done); err != nil {
		p.fail(err)
		return err
	}
	select {
	case err := <-done:
		p.fail(io.EOF)
		if err != nil {
			return err
		}
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		p.fail(ctx.Err())
		return ctx.Err()
	case <-p.transportDone:
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close immediately terminates the peer and unblocks all pending operations.
func (p *Peer) Close() error {
	if p == nil {
		return nil
	}
	p.fail(ErrPeerClosed)
	return nil
}

func (p *Peer) ready() error {
	if p == nil {
		return ErrPeerClosed
	}
	if !p.started.Load() {
		return ErrPeerNotStarted
	}
	select {
	case <-p.transportDone:
		return p.closedError()
	default:
		return nil
	}
}

func (p *Peer) closedError() error {
	if err := p.Err(); err != nil {
		return err
	}
	return ErrPeerClosed
}

func (p *Peer) enqueueControl(ctx context.Context, messageType int, payload []byte, done chan error) error {
	if err := p.ready(); err != nil {
		return err
	}
	if err := p.validateOutboundFrame(messageType, payload); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	frame := outboundFrame{messageType: messageType, payload: payload, done: done}
	select {
	case p.control <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.transportDone:
		return p.closedError()
	}
}

func (p *Peer) validateOutboundFrame(messageType int, payload []byte) error {
	if messageType == websocket.TextMessage && int64(len(payload)) > p.config.WriteLimit {
		return errors.Errorf("runner rpc message is %d bytes, exceeding the %d-byte outbound limit", len(payload), p.config.WriteLimit)
	}
	return nil
}

func (p *Peer) writeLoop() {
	for {
		frame, ok := p.nextOutbound()
		if !ok {
			return
		}
		err := p.writeFrame(frame)
		if frame.done != nil {
			frame.done <- err
		}
		if err != nil {
			p.fail(err)
			return
		}
		if frame.messageType == websocket.CloseMessage {
			return
		}
	}
}

func (p *Peer) nextOutbound() (outboundFrame, bool) {
	select {
	case frame := <-p.control:
		return frame, true
	default:
	}
	select {
	case frame := <-p.control:
		return frame, true
	case frame := <-p.updates:
		return frame, true
	case <-p.ctx.Done():
		return outboundFrame{}, false
	}
}

func (p *Peer) writeFrame(frame outboundFrame) error {
	if err := p.validateOutboundFrame(frame.messageType, frame.payload); err != nil {
		return err
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(p.config.WriteWait)); err != nil {
		return err
	}
	return p.conn.WriteMessage(frame.messageType, frame.payload)
}

func (p *Peer) pingLoop() {
	ticker := time.NewTicker(p.config.PingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(p.ctx, p.config.WriteWait)
			err := p.enqueueControl(ctx, websocket.PingMessage, nil, nil)
			cancel()
			if err != nil {
				if p.ctx.Err() != nil {
					return
				}
				continue
			}
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Peer) readLoop() {
	for {
		messageType, payload, err := p.conn.ReadMessage()
		if err != nil {
			p.fail(err)
			return
		}
		if messageType != websocket.TextMessage {
			p.fail(errors.New("runner rpc accepts only WebSocket text messages"))
			return
		}
		message, err := DecodeMessage(payload)
		if err != nil {
			p.fail(err)
			return
		}
		if message.Method == "" {
			p.dispatchResponse(message)
			continue
		}
		if message.ID == nil {
			p.dispatchNotification(message)
			continue
		}
		p.dispatchRequest(message)
	}
}

func (p *Peer) dispatchResponse(message Message) {
	id := *message.ID
	p.pendingMu.Lock()
	responseCh := p.pending[id]
	delete(p.pending, id)
	p.pendingMu.Unlock()
	if responseCh == nil {
		return
	}
	responseCh <- callResult{message: message}
}

func (p *Peer) dispatchNotification(message Message) {
	if message.Method == MethodOperationCancel {
		var params OperationCancelParams
		if json.Unmarshal(message.Params, &params) == nil {
			p.cancelInbound(params.RequestID)
		}
		return
	}
	if p.config.Notifications == nil {
		return
	}
	if !tryAcquire(p.notificationSlots) {
		p.fail(errors.New("runner rpc notification concurrency limit exceeded"))
		return
	}
	if !p.startWorker(func() {
		defer releaseSlot(p.notificationSlots)
		p.config.Notifications.HandleNotification(p.ctx, message.Method, message.Params)
	}) {
		releaseSlot(p.notificationSlots)
	}
}

func (p *Peer) dispatchRequest(message Message) {
	id := *message.ID
	requestCtx, cancel := context.WithCancel(p.ctx)
	requestCtx = context.WithValue(requestCtx, requestIDContextKey{}, id)
	call := &inboundCall{cancel: cancel}
	slots := p.requestSlots
	if isControlRequest(message.Method) {
		slots = p.controlRequestSlots
	}
	p.inboundMu.Lock()
	if _, exists := p.inbound[id]; exists {
		p.inboundMu.Unlock()
		cancel()
		p.trySendErrorResponse(id, &RPCError{Code: ErrorCodeInvalidRequest, Message: "duplicate rpc request id"})
		return
	}
	if !tryAcquire(slots) {
		p.inboundMu.Unlock()
		cancel()
		p.trySendErrorResponse(id, &RPCError{Code: ErrorCodeBusy, Message: "runner rpc request concurrency limit exceeded"})
		return
	}
	p.inbound[id] = call
	p.inboundMu.Unlock()

	if !p.startWorker(func() {
		defer releaseSlot(slots)
		defer cancel()
		defer p.removeInbound(id, call)

		result, rpcErr := p.handleRequestSafely(requestCtx, message.Method, message.Params)
		if rpcErr != nil {
			p.sendErrorResponse(id, rpcErr)
			return
		}
		resultPayload, err := marshalRPCValue(result)
		if err != nil {
			p.sendErrorResponse(id, &RPCError{Code: ErrorCodeInternal, Message: err.Error()})
			return
		}
		payload, err := json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: &id, Result: resultPayload})
		if err != nil {
			p.sendErrorResponse(id, &RPCError{Code: ErrorCodeInternal, Message: err.Error()})
			return
		}
		if err := p.validateOutboundFrame(websocket.TextMessage, payload); err != nil {
			p.sendErrorResponse(id, &RPCError{
				Code:    ErrorCodeUnavailable,
				Message: "runner rpc result exceeds the connection message-size limit; return a smaller result or use an artifact channel",
				Data:    RPCErrorData{Reason: ErrorReasonResultTooLarge},
			})
			return
		}
		ctx, cancelWrite := context.WithTimeout(p.ctx, p.config.WriteWait)
		defer cancelWrite()
		_ = p.enqueueControl(ctx, websocket.TextMessage, payload, nil)
	}) {
		p.removeInbound(id, call)
		cancel()
		releaseSlot(slots)
	}
}

func (p *Peer) startWorker(worker func()) bool {
	p.workerMu.Lock()
	defer p.workerMu.Unlock()
	select {
	case <-p.transportDone:
		return false
	default:
	}
	p.workerWG.Add(1)
	go func() {
		defer p.workerWG.Done()
		worker()
	}()
	return true
}

func isControlRequest(method string) bool {
	switch strings.TrimSpace(method) {
	case MethodRunCancel, MethodRunClose:
		return true
	default:
		return false
	}
}

func tryAcquire(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseSlot(slots chan struct{}) {
	<-slots
}

func (p *Peer) handleRequestSafely(ctx context.Context, method string, params json.RawMessage) (result any, rpcErr *RPCError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			rpcErr = &RPCError{
				Code:    ErrorCodeInternal,
				Message: fmt.Sprintf("runner rpc handler panic: %v", recovered),
			}
		}
	}()
	if p.config.Handler == nil {
		return nil, &RPCError{Code: ErrorCodeMethodNotFound, Message: "rpc method not found"}
	}
	return p.config.Handler.HandleRequest(ctx, method, params)
}

func (p *Peer) sendErrorResponse(id string, rpcErr *RPCError) {
	payload, err := p.errorResponsePayload(id, rpcErr)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, p.config.WriteWait)
	defer cancel()
	_ = p.enqueueControl(ctx, websocket.TextMessage, payload, nil)
}

func (p *Peer) trySendErrorResponse(id string, rpcErr *RPCError) {
	payload, err := p.errorResponsePayload(id, rpcErr)
	if err != nil {
		p.fail(err)
		return
	}
	frame := outboundFrame{messageType: websocket.TextMessage, payload: payload}
	select {
	case p.control <- frame:
	case <-p.transportDone:
	default:
		p.fail(errors.New("runner rpc control queue is full while rejecting inbound request"))
	}
}

func (p *Peer) errorResponsePayload(id string, rpcErr *RPCError) ([]byte, error) {
	payload, err := json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: &id, Error: rpcErr})
	if err != nil {
		return nil, err
	}
	if p.validateOutboundFrame(websocket.TextMessage, payload) == nil {
		return payload, nil
	}
	fallback := &RPCError{Code: ErrorCodeUnavailable, Message: "runner rpc error exceeds the connection message-size limit"}
	payload, err = json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: &id, Error: fallback})
	if err != nil {
		return nil, err
	}
	if err := p.validateOutboundFrame(websocket.TextMessage, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (p *Peer) removePending(id string, expected chan callResult) bool {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	current := p.pending[id]
	if current != expected {
		return false
	}
	delete(p.pending, id)
	return true
}

func (p *Peer) removeInbound(id string, expected *inboundCall) {
	p.inboundMu.Lock()
	defer p.inboundMu.Unlock()
	if p.inbound[id] == expected {
		delete(p.inbound, id)
	}
}

func (p *Peer) cancelInbound(id string) {
	p.inboundMu.Lock()
	call := p.inbound[strings.TrimSpace(id)]
	p.inboundMu.Unlock()
	if call != nil {
		call.cancel()
	}
}

func (p *Peer) fail(err error) {
	p.failOnce.Do(func() {
		if err == nil {
			err = ErrPeerClosed
		}
		p.errMu.Lock()
		p.terminalErr = err
		p.errMu.Unlock()
		if p.cancel != nil {
			p.cancel()
		}
		_ = p.conn.Close()

		p.pendingMu.Lock()
		pending := p.pending
		p.pending = make(map[string]chan callResult)
		p.pendingMu.Unlock()
		for _, responseCh := range pending {
			responseCh <- callResult{err: err}
		}

		p.inboundMu.Lock()
		inbound := p.inbound
		p.inbound = make(map[string]*inboundCall)
		p.inboundMu.Unlock()
		for _, call := range inbound {
			call.cancel()
		}

		p.workerMu.Lock()
		close(p.transportDone)
		p.workerMu.Unlock()
		go func() {
			p.workerWG.Wait()
			close(p.done)
		}()
	})
}

func marshalRPCValue(value any) (json.RawMessage, error) {
	if raw, ok := value.(json.RawMessage); ok {
		if raw == nil {
			return json.RawMessage("null"), nil
		}
		return append(json.RawMessage(nil), raw...), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}
