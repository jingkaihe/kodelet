package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/pkg/errors"
)

const (
	responsesWebSocketIdleTimeout     = 10 * time.Minute
	responsesWebSocketBetaHeaderValue = "responses_websockets=2026-02-06"
)

type responsesWebSocketStreamer interface {
	Stream(context.Context, responsesWebSocketParamsFactory, []string, auth.HTTPAuthorizer) (*ssestream.Stream[responses.ResponseStreamEventUnion], uint64, error)
	Close() error
}

type responsesWebSocketParamsFactory func(connectionGeneration uint64) responses.ResponseNewParams

type responsesWebSocketTransport struct {
	baseURL        string
	mu             sync.Mutex
	conn           *responsesWebSocketConnection
	nextGeneration uint64
	closed         bool
	requestPermit  chan struct{}
}

type responsesWebSocketConnection struct {
	generation uint64
	conn       *websocket.Conn
	messages   chan responsesWebSocketMessage
	stop       chan struct{}
	done       chan struct{}
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

type responsesWebSocketMessage struct {
	messageType int
	data        []byte
	err         error
}

type websocketHandshakeStatusError struct {
	message    string
	statusCode int
	body       string
	err        error
}

func (e *websocketHandshakeStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.err == nil {
		return e.message
	}
	return e.message + ": " + e.err.Error()
}

func (e *websocketHandshakeStatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type responseCreateWebSocketRequest struct {
	Params responses.ResponseNewParams
}

// MarshalJSON sends a WebSocket `response.create` event whose body mirrors the
// SDK's Responses create request. The SDK owns omitzero/union encoding, so keep
// that behavior rather than re-declaring every field with stdlib JSON tags.
func (r responseCreateWebSocketRequest) MarshalJSON() ([]byte, error) {
	body, err := r.Params.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["type"] = json.RawMessage(`"response.create"`)
	delete(payload, "background")
	delete(payload, "stream")
	delete(payload, "stream_options")
	return json.Marshal(payload)
}

func newResponsesWebSocketTransport(baseURL string) *responsesWebSocketTransport {
	requestPermit := make(chan struct{}, 1)
	requestPermit <- struct{}{}
	return &responsesWebSocketTransport{
		baseURL:       baseURL,
		requestPermit: requestPermit,
	}
}

func (t *responsesWebSocketTransport) Stream(
	ctx context.Context,
	paramsFactory responsesWebSocketParamsFactory,
	requestHeaders []string,
	authorizer auth.HTTPAuthorizer,
) (*ssestream.Stream[responses.ResponseStreamEventUnion], uint64, error) {
	if t == nil {
		return nil, 0, errors.New("responses websocket transport is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if paramsFactory == nil {
		return nil, 0, errors.New("responses websocket request factory is not initialized")
	}

	if err := t.acquireRequest(ctx); err != nil {
		return nil, 0, err
	}
	releaseRequest := true
	defer func() {
		if releaseRequest {
			t.releaseRequest()
		}
	}()

	t.mu.Lock()
	conn, err := t.connectionLocked(ctx, requestHeaders, authorizer)
	t.mu.Unlock()
	if err != nil {
		return nil, 0, err
	}

	request := responseCreateWebSocketRequest{Params: paramsFactory(conn.generation)}

	if err := writeWebSocketJSON(ctx, conn, request); err != nil {
		t.invalidateConnection(conn)
		return nil, 0, err
	}

	releaseRequest = false
	decoder := newWebSocketStreamDecoder(ctx, conn, t, responsesWebSocketIdleTimeout)
	return ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil), conn.generation, nil
}

func (t *responsesWebSocketTransport) Close() error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	t.closed = true
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.close()
}

func (t *responsesWebSocketTransport) acquireRequest(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.requestPermit:
		return nil
	}
}

func (t *responsesWebSocketTransport) releaseRequest() {
	t.requestPermit <- struct{}{}
}

func (t *responsesWebSocketTransport) connectionLocked(ctx context.Context, requestHeaders []string, authorizer auth.HTTPAuthorizer) (*responsesWebSocketConnection, error) {
	if t.closed {
		return nil, errors.New("responses websocket transport is closed")
	}
	if t.conn != nil {
		select {
		case <-t.conn.done:
			t.conn = nil
		default:
			return t.conn, nil
		}
	}

	wsURL, err := responsesWebSocketURL(t.baseURL)
	if err != nil {
		return nil, err
	}

	headers := http.Header{}
	for _, header := range requestHeaders {
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			continue
		}
		headers.Set(name, value)
	}
	headers.Set("OpenAI-Beta", responsesWebSocketBetaHeaderValue)

	if authorizer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, wsURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header = headers.Clone()
		if err := authorizer.Authorize(req); err != nil {
			return nil, err
		}
		headers = req.Header.Clone()
	}
	headers.Set("OpenAI-Beta", responsesWebSocketBetaHeaderValue)

	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	socket, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, websocketHandshakeError(err, resp)
	}

	t.nextGeneration++
	conn := &responsesWebSocketConnection{
		generation: t.nextGeneration,
		conn:       socket,
		messages:   make(chan responsesWebSocketMessage, 1600),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	t.conn = conn
	t.startReader(conn)
	logger.G(ctx).
		WithField("url", wsURL).
		WithField("generation", conn.generation).
		Debug("connected to Responses API websocket")
	return t.conn, nil
}

func (t *responsesWebSocketTransport) startReader(conn *responsesWebSocketConnection) {
	go func() {
		defer func() {
			close(conn.messages)
			close(conn.done)
			t.mu.Lock()
			if t.conn == conn {
				t.conn = nil
			}
			t.mu.Unlock()
		}()

		for {
			messageType, data, err := conn.conn.ReadMessage()
			message := responsesWebSocketMessage{messageType: messageType, data: data, err: err}
			select {
			case <-conn.stop:
				return
			case conn.messages <- message:
			}
			if err != nil || messageType == websocket.CloseMessage {
				return
			}
		}
	}()
}

func (t *responsesWebSocketTransport) invalidateConnection(conn *responsesWebSocketConnection) {
	if t == nil || conn == nil {
		return
	}
	t.mu.Lock()
	if t.conn == conn {
		t.conn = nil
	}
	t.mu.Unlock()
	_ = conn.close()
}

func (c *responsesWebSocketConnection) close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		close(c.stop)
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}

func responsesWebSocketURL(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", errors.Errorf("unsupported websocket base URL scheme %q", parsed.Scheme)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/responses"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func websocketHandshakeError(err error, resp *http.Response) error {
	if resp == nil {
		return errors.Wrap(err, "failed to connect Responses API websocket")
	}
	body := ""
	if resp.Body != nil {
		if bodyBytes, readErr := io.ReadAll(resp.Body); readErr == nil {
			body = string(bodyBytes)
		}
		_ = resp.Body.Close()
	}
	message := fmt.Sprintf("failed to connect Responses API websocket: HTTP %d", resp.StatusCode)
	if statusText := http.StatusText(resp.StatusCode); statusText != "" {
		message += " " + statusText
	}
	if err == nil {
		return &websocketHandshakeStatusError{message: message, statusCode: resp.StatusCode, body: body}
	}
	return &websocketHandshakeStatusError{message: message, statusCode: resp.StatusCode, body: body, err: err}
}

func writeWebSocketJSON(ctx context.Context, conn *responsesWebSocketConnection, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "failed to encode Responses API websocket request")
	}

	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.conn.SetWriteDeadline(time.Now().Add(responsesWebSocketIdleTimeout))
	}
	if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = conn.conn.SetWriteDeadline(time.Time{})
		return errors.Wrap(err, "failed to send Responses API websocket request")
	}
	_ = conn.conn.SetWriteDeadline(time.Time{})
	return nil
}

type webSocketStreamDecoder struct {
	ctx         context.Context
	conn        *responsesWebSocketConnection
	transport   *responsesWebSocketTransport
	idleTimeout time.Duration
	event       ssestream.Event
	err         error
	closed      bool
	done        bool
	stopCancel  chan struct{}
	finishOnce  sync.Once
}

func newWebSocketStreamDecoder(ctx context.Context, conn *responsesWebSocketConnection, transport *responsesWebSocketTransport, idleTimeout time.Duration) ssestream.Decoder {
	decoder := &webSocketStreamDecoder{
		ctx:         ctx,
		conn:        conn,
		transport:   transport,
		idleTimeout: idleTimeout,
		stopCancel:  make(chan struct{}),
	}
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				decoder.finish(true)
			case <-decoder.stopCancel:
			}
		}()
	}
	return decoder
}

func (d *webSocketStreamDecoder) Next() bool {
	if d.err != nil || d.closed || d.done {
		return false
	}

	message, ok := d.nextMessage()
	if !ok {
		return false
	}
	if message.err != nil {
		if d.ctx != nil && d.ctx.Err() != nil {
			d.err = d.ctx.Err()
		} else {
			d.err = errors.Wrap(message.err, "failed to read Responses API websocket event")
		}
		d.finish(true)
		return false
	}

	switch message.messageType {
	case websocket.TextMessage:
		d.event = ssestream.Event{Data: bytes.TrimSpace(message.data)}
		disposition := classifyWebSocketResponseEvent(d.event.Data)
		switch disposition {
		case webSocketResponseCompleted:
			d.done = true
			d.finish(false)
		case webSocketResponseFailed:
			d.done = true
			d.finish(true)
			if eventErr := parseResponsesWebSocketEventError(d.event.Data); eventErr != nil {
				d.err = eventErr
				return false
			}
		}
		return true
	case websocket.BinaryMessage:
		d.err = errors.New("unexpected binary Responses API websocket event")
		d.finish(true)
		return false
	case websocket.CloseMessage:
		d.err = errors.New("Responses API websocket closed before response.completed")
		d.finish(true)
		return false
	default:
		d.err = errors.Errorf("unexpected Responses API websocket message type %d", message.messageType)
		d.finish(true)
		return false
	}
}

func (d *webSocketStreamDecoder) nextMessage() (responsesWebSocketMessage, bool) {
	var timeout <-chan time.Time
	var timer *time.Timer
	if d.idleTimeout > 0 {
		timer = time.NewTimer(d.idleTimeout)
		timeout = timer.C
		defer timer.Stop()
	}

	if d.ctx == nil {
		select {
		case message, ok := <-d.conn.messages:
			if !ok {
				d.err = errors.New("Responses API websocket closed before response.completed")
				d.finish(true)
			}
			return message, ok
		case <-timeout:
			d.err = errors.New("idle timeout waiting for Responses API websocket event")
			d.finish(true)
			return responsesWebSocketMessage{}, false
		}
	}

	select {
	case message, ok := <-d.conn.messages:
		if !ok {
			if d.ctx.Err() != nil {
				d.err = d.ctx.Err()
			} else {
				d.err = errors.New("Responses API websocket closed before response.completed")
			}
			d.finish(true)
		}
		return message, ok
	case <-d.ctx.Done():
		d.err = d.ctx.Err()
		d.finish(true)
		return responsesWebSocketMessage{}, false
	case <-timeout:
		d.err = errors.New("idle timeout waiting for Responses API websocket event")
		d.finish(true)
		return responsesWebSocketMessage{}, false
	}
}

type webSocketResponseDisposition int

const (
	webSocketResponseInProgress webSocketResponseDisposition = iota
	webSocketResponseCompleted
	webSocketResponseFailed
)

func classifyWebSocketResponseEvent(data []byte) webSocketResponseDisposition {
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return webSocketResponseInProgress
	}
	switch payload.Type {
	case "response.completed":
		return webSocketResponseCompleted
	case "response.incomplete", "response.failed", "error":
		return webSocketResponseFailed
	default:
		return webSocketResponseInProgress
	}
}

type responsesWebSocketEventError struct {
	statusCode int
	code       string
	message    string
	body       string
}

func (e *responsesWebSocketEventError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	if strings.TrimSpace(e.code) != "" {
		return e.code
	}
	return "Responses API websocket error"
}

func parseResponsesWebSocketEventError(data []byte) error {
	var payload struct {
		Type       string `json:"type"`
		Status     int    `json:"status"`
		StatusCode int    `json:"status_code"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		Error      struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Type != "error" {
		return nil
	}
	statusCode := payload.Status
	if statusCode == 0 {
		statusCode = payload.StatusCode
	}
	return &responsesWebSocketEventError{
		statusCode: statusCode,
		code:       firstNonEmpty(payload.Code, payload.Error.Code),
		message:    firstNonEmpty(payload.Message, payload.Error.Message),
		body:       string(data),
	}
}

func (d *webSocketStreamDecoder) Event() ssestream.Event {
	return d.event
}

func (d *webSocketStreamDecoder) Close() error {
	d.closed = true
	d.finish(!d.done)
	return nil
}

func (d *webSocketStreamDecoder) Err() error {
	return d.err
}

func (d *webSocketStreamDecoder) finish(invalidate bool) {
	d.finishOnce.Do(func() {
		if d.stopCancel != nil {
			close(d.stopCancel)
		}
		if invalidate && d.transport != nil {
			d.transport.invalidateConnection(d.conn)
		}
		if d.transport != nil {
			d.transport.releaseRequest()
		}
	})
}
