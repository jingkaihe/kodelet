package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/jingkaihe/kodelet/sdk/go/internal/queue"
)

const (
	acpProtocolVersion   = 1
	acpMessageLimit      = 64 * 1024 * 1024
	extensionFrameMethod = "kodelet/extensionFrame"
	processExitTimeout   = time.Second
	stdoutExitGrace      = 2 * time.Second
)

type rpcResult struct {
	result json.RawMessage
	err    error
}

// acpConn is a JSON-RPC connection to one kodelet acp process.
type acpConn struct {
	proc  acpProcess
	relay *relay
	// writer serializes stdin writes off the read loop, so acknowledging a
	// frame never blocks on a peer that is itself blocked writing to us.
	writer *queue.Worker

	mu                sync.Mutex
	closed            bool
	closeErr          error
	nextID            int64
	pending           map[int64]chan rpcResult
	handlers          map[int]func(method string, params json.RawMessage)
	nextHandler       int
	steering          bool
	sessionExtensions bool
	hierarchy         bool
	sessionID         string

	closeOnce   sync.Once
	closeResult error
}

func newACPConn(proc acpProcess, extensions []*extension.Extension, ui *UIHandlers) *acpConn {
	conn := &acpConn{
		proc:     proc,
		writer:   queue.NewWorker(),
		pending:  map[int64]chan rpcResult{},
		handlers: map[int]func(string, json.RawMessage){},
	}
	if len(extensions) > 0 {
		conn.relay = newRelay(conn, extensions, ui)
	}
	go conn.readLoop()
	go func() {
		<-proc.Done()
		conn.fail(proc.Exit().runError())
		conn.writer.Stop()
	}()
	return conn
}

func (c *acpConn) readLoop() {
	reader := jsonrpc.NewLineReader(c.proc.Stdout(), acpMessageLimit)
	for {
		line, err := reader.Read()
		if err != nil {
			if !c.isClosed() {
				select {
				case <-c.proc.Done():
					c.fail(c.proc.Exit().runError())
				case <-time.After(stdoutExitGrace):
					c.fail(errStdoutEnded)
				}
			}
			return
		}
		c.handleLine(line)
	}
}

func (c *acpConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// fail closes the connection after a transport failure or process exit and
// kills the process, which may be blocked on a broken pipe.
func (c *acpConn) fail(err error) {
	if c.markClosed(err) {
		_ = c.proc.Kill()
	}
}

// markClosed rejects pending requests and starts closing inline extension
// runtimes. It reports whether this call closed the connection.
func (c *acpConn) markClosed(err error) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = map[int64]chan rpcResult{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: err}
	}
	if c.relay != nil {
		go c.relay.closeAll(err)
	}
	return true
}

func (c *acpConn) handleLine(line []byte) {
	if len(line) == 0 {
		return
	}
	var message jsonrpc.Message
	if err := json.Unmarshal(line, &message); err != nil {
		params, _ := json.Marshal(map[string]string{"line": string(line)})
		c.dispatchNotification("$/stdout", params)
		return
	}
	switch {
	case message.IsRequest():
		c.handleServerRequest(message)
	case message.Method != "":
		c.dispatchNotification(message.Method, message.Params)
	default:
		id, ok := message.IntID()
		if !ok {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if message.Error != nil {
			ch <- rpcResult{err: message.Error}
			return
		}
		ch <- rpcResult{result: message.Result}
	}
}

func (c *acpConn) dispatchNotification(method string, params json.RawMessage) {
	c.mu.Lock()
	handlers := make([]func(string, json.RawMessage), 0, len(c.handlers))
	for id := 0; id < c.nextHandler; id++ {
		if handler, ok := c.handlers[id]; ok {
			handlers = append(handlers, handler)
		}
	}
	c.mu.Unlock()
	for _, handler := range handlers {
		handler(method, params)
	}
}

// onNotification registers a handler called on the read goroutine; it must
// not block.
func (c *acpConn) onNotification(handler func(method string, params json.RawMessage)) func() {
	c.mu.Lock()
	id := c.nextHandler
	c.nextHandler++
	c.handlers[id] = handler
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.handlers, id)
		c.mu.Unlock()
	}
}

func (c *acpConn) handleServerRequest(message jsonrpc.Message) {
	if message.Method != extensionFrameMethod {
		c.write(jsonrpc.NewError(message.ID, jsonrpc.CodeMethodNotFound, "unsupported client RPC method: "+message.Method))
		return
	}
	if c.relay == nil {
		c.write(jsonrpc.NewError(message.ID, jsonrpc.CodeInvalidParams, "invalid or unsupported inline extension frame"))
		return
	}
	dispatch, err := c.relay.accept(message.Params)
	if err != nil {
		c.write(jsonrpc.NewError(message.ID, jsonrpc.CodeInvalidParams, err.Error()))
		return
	}
	// Acknowledge acceptance before invoking callbacks: nested host RPC uses
	// this same duplex pipe.
	c.write(jsonrpc.NewResult(message.ID, json.RawMessage("{}")))
	dispatch()
}

func (c *acpConn) write(message jsonrpc.Message) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	data = append(data, '\n')
	c.writer.Submit(func() {
		if _, err := c.proc.Write(data); err != nil {
			c.fail(fmt.Errorf("ACP stdin failed: %w", err))
		}
	})
}

func (c *acpConn) closedError() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return errConnectionClosed
}

// start registers a request and queues it for writing. Writes are ordered,
// so any message queued after start returns reaches kodelet acp after it.
func (c *acpConn) start(method string, params any) (int64, <-chan rpcResult, error) {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil {
		return 0, nil, err
	}
	c.mu.Lock()
	if c.closed {
		err := c.closedError()
		c.mu.Unlock()
		return 0, nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	c.write(jsonrpc.NewRequest(jsonrpc.IntRaw(id), method, encoded))
	return id, ch, nil
}

func (c *acpConn) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch, err := c.start(method, params)
	if err != nil {
		return nil, err
	}
	select {
	case result := <-ch:
		return result.result, result.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *acpConn) notify(method string, params any) {
	encoded, err := jsonrpc.Marshal(params)
	if err != nil || c.isClosed() {
		return
	}
	c.write(jsonrpc.NewNotification(method, encoded))
}

func (c *acpConn) initialize(ctx context.Context) error {
	raw, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": acpProtocolVersion,
		"clientCapabilities": map[string]any{
			"terminal": true,
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"_meta":    map[string]any{"sessionExtensions": map[string]any{"version": 1}},
		},
		"clientInfo": map[string]any{"name": "kodelet-sdk-go", "title": "Kodelet Go SDK"},
	})
	if err != nil {
		return err
	}
	var result struct {
		Meta struct {
			Steering struct {
				Supported bool `json:"supported"`
			} `json:"steering"`
			SessionExtensions struct {
				Version int `json:"version"`
			} `json:"sessionExtensions"`
			ConversationHierarchy struct {
				Version int `json:"version"`
			} `json:"conversationHierarchy"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(raw, &result)
	c.mu.Lock()
	c.steering = result.Meta.Steering.Supported
	c.sessionExtensions = result.Meta.SessionExtensions.Version == 1
	c.hierarchy = result.Meta.ConversationHierarchy.Version == 1
	sessionExtensions := c.sessionExtensions
	c.mu.Unlock()
	if c.relay != nil && !sessionExtensions {
		return errors.New("kodelet acp does not support inline session extensions (sessionExtensions version 1); update Kodelet and the selected runner")
	}
	return nil
}

func (c *acpConn) sessionMetadata(parentConversationID string) map[string]any {
	meta := map[string]any{}
	if c.relay != nil {
		meta["sessionExtensions"] = map[string]any{"version": 1, "extensionIds": c.relay.ids}
	}
	if parentConversationID != "" {
		meta["conversationHierarchy"] = map[string]any{"version": 1, "parentConversationId": parentConversationID}
	}
	return meta
}

func (c *acpConn) createSession(ctx context.Context, cwd, parentConversationID string) (string, error) {
	c.mu.Lock()
	hierarchy := c.hierarchy
	c.mu.Unlock()
	if parentConversationID != "" && !hierarchy {
		return "", errors.New("child conversations require kodelet acp conversationHierarchy version 1 support; update Kodelet and the daemon")
	}
	params := map[string]any{"cwd": cwd}
	if meta := c.sessionMetadata(parentConversationID); len(meta) > 0 {
		params["_meta"] = meta
	}
	raw, err := c.request(ctx, "session/new", params)
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID *string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.SessionID == nil {
		return "", errors.New("invalid session/new response from kodelet acp")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != "" && c.sessionID != *result.SessionID {
		return "", errors.New("inline extension frame belongs to a different ACP session")
	}
	c.sessionID = *result.SessionID
	return c.sessionID, nil
}

func (c *acpConn) loadSession(ctx context.Context, sessionID, cwd string) error {
	c.mu.Lock()
	c.sessionID = sessionID
	c.mu.Unlock()
	params := map[string]any{"sessionId": sessionID, "cwd": cwd}
	if meta := c.sessionMetadata(""); len(meta) > 0 {
		params["_meta"] = meta
	}
	_, err := c.request(ctx, "session/load", params)
	return err
}

// startPrompt queues session/prompt and returns its pending result.
func (c *acpConn) startPrompt(sessionID string, prompt []map[string]any) (<-chan rpcResult, error) {
	_, result, err := c.start("session/prompt", map[string]any{"sessionId": sessionID, "prompt": prompt})
	return result, err
}

func stopReason(raw json.RawMessage) string {
	var result struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &result)
	return result.StopReason
}

func (c *acpConn) steer(ctx context.Context, sessionID, message string) (SteerResult, error) {
	c.mu.Lock()
	supported := c.steering
	c.mu.Unlock()
	if !supported {
		return SteerResult{}, errors.New("kodelet acp does not advertise session steering support")
	}
	raw, err := c.request(ctx, "_session/steering", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": message}},
		"_meta":     map[string]any{"steering": map[string]any{"idleBehavior": "promptRequired"}},
	})
	if err != nil {
		return SteerResult{}, err
	}
	var result struct {
		Outcome SteeringOutcome `json:"outcome"`
		Reason  *string         `json:"reason"`
	}
	invalid := errors.New("invalid _session/steering response from kodelet acp")
	if json.Unmarshal(raw, &result) != nil {
		return SteerResult{}, invalid
	}
	switch result.Outcome {
	case SteeringInjected, SteeringStartedNewTurn, SteeringPromptRequired, SteeringFailed:
	default:
		return SteerResult{}, invalid
	}
	reason := ""
	if result.Reason != nil {
		reason = *result.Reason
	}
	if result.Outcome == SteeringPromptRequired && reason != "noRunningTurn" {
		return SteerResult{}, invalid
	}
	return SteerResult{Outcome: result.Outcome, Reason: reason}, nil
}

func (c *acpConn) cancel(sessionID string) {
	c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// close stops the process with SIGTERM, then SIGKILL, and closes inline
// extension runtimes. It is safe to call more than once.
func (c *acpConn) close() error {
	c.closeOnce.Do(func() {
		c.markClosed(errConnectionClosed)
		var extensionsClosed chan struct{}
		if c.relay != nil {
			extensionsClosed = make(chan struct{})
			go func() {
				c.relay.closeAll(errConnectionClosed)
				close(extensionsClosed)
			}()
		}
		c.closeResult = c.stopProcess()
		if extensionsClosed != nil {
			<-extensionsClosed
		}
	})
	return c.closeResult
}

func (c *acpConn) stopProcess() error {
	select {
	case <-c.proc.Done():
		return nil
	default:
	}
	_ = c.proc.Terminate()
	select {
	case <-c.proc.Done():
		return nil
	case <-time.After(processExitTimeout):
	}
	_ = c.proc.Kill()
	select {
	case <-c.proc.Done():
		return nil
	case <-time.After(processExitTimeout):
		return errors.New("kodelet acp process did not close after SIGKILL; cleanup is incomplete")
	}
}
