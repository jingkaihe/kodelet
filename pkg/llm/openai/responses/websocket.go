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
	Stream(context.Context, responses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[responses.ResponseStreamEventUnion], error)
	Close() error
}

type responsesWebSocketTransport struct {
	baseURL string
	mu      sync.Mutex
	conn    *websocket.Conn
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
	return &responsesWebSocketTransport{baseURL: baseURL}
}

func (t *responsesWebSocketTransport) Stream(
	ctx context.Context,
	params responses.ResponseNewParams,
	requestHeaders []string,
	authorizer auth.HTTPAuthorizer,
) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
	if t == nil {
		return nil, errors.New("responses websocket transport is not initialized")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	conn, err := t.connectionLocked(ctx, requestHeaders, authorizer)
	if err != nil {
		return nil, err
	}

	request := responseCreateWebSocketRequest{Params: params}

	if err := writeWebSocketJSON(ctx, conn, request); err != nil {
		t.closeLocked()
		return nil, err
	}

	return ssestream.NewStream[responses.ResponseStreamEventUnion](newWebSocketStreamDecoder(ctx, conn, t, responsesWebSocketIdleTimeout), nil), nil
}

func (t *responsesWebSocketTransport) Close() error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeLocked()
}

func (t *responsesWebSocketTransport) connectionLocked(ctx context.Context, requestHeaders []string, authorizer auth.HTTPAuthorizer) (*websocket.Conn, error) {
	if t.conn != nil {
		_ = t.closeLocked()
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
	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, websocketHandshakeError(err, resp)
	}

	t.conn = conn
	logger.G(ctx).WithField("url", wsURL).Debug("connected to Responses API websocket")
	return t.conn, nil
}

func (t *responsesWebSocketTransport) closeLocked() error {
	if t.conn == nil {
		return nil
	}
	err := t.conn.Close()
	t.conn = nil
	return err
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
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	message := fmt.Sprintf("failed to connect Responses API websocket: HTTP %d", resp.StatusCode)
	if statusText := http.StatusText(resp.StatusCode); statusText != "" {
		message += " " + statusText
	}
	if err == nil {
		return errors.New(message)
	}
	return errors.Wrap(err, message)
}

func writeWebSocketJSON(ctx context.Context, conn *websocket.Conn, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "failed to encode Responses API websocket request")
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketIdleTimeout))
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = conn.SetWriteDeadline(time.Time{})
		return errors.Wrap(err, "failed to send Responses API websocket request")
	}
	_ = conn.SetWriteDeadline(time.Time{})
	return nil
}

type webSocketStreamDecoder struct {
	ctx         context.Context
	conn        *websocket.Conn
	transport   *responsesWebSocketTransport
	idleTimeout time.Duration
	event       ssestream.Event
	err         error
	closed      bool
	done        bool
	stopCancel  chan struct{}
	stopOnce    sync.Once
}

func newWebSocketStreamDecoder(ctx context.Context, conn *websocket.Conn, transport *responsesWebSocketTransport, idleTimeout time.Duration) ssestream.Decoder {
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
				decoder.closeTransport()
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

	for {
		if d.ctx == nil {
			if d.idleTimeout > 0 {
				_ = d.conn.SetReadDeadline(time.Now().Add(d.idleTimeout))
			}
		} else if deadline, ok := d.ctx.Deadline(); ok {
			_ = d.conn.SetReadDeadline(deadline)
		} else if d.idleTimeout > 0 {
			_ = d.conn.SetReadDeadline(time.Now().Add(d.idleTimeout))
		}

		messageType, data, err := d.conn.ReadMessage()
		if err != nil {
			if d.ctx != nil && d.ctx.Err() != nil {
				d.err = d.ctx.Err()
			} else {
				d.err = errors.Wrap(err, "failed to read Responses API websocket event")
			}
			d.closeTransport()
			return false
		}

		switch messageType {
		case websocket.TextMessage:
			d.event = ssestream.Event{Data: bytes.TrimSpace(data)}
			d.done = isTerminalWebSocketResponseEvent(d.event.Data)
			if d.done {
				d.closeTransport()
			}
			return true
		case websocket.BinaryMessage:
			d.err = errors.New("unexpected binary Responses API websocket event")
			d.closeTransport()
			return false
		case websocket.CloseMessage:
			d.err = errors.New("Responses API websocket closed before response.completed")
			d.closeTransport()
			return false
		case websocket.PingMessage:
			_ = d.conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second))
		}
	}
}

func isTerminalWebSocketResponseEvent(data []byte) bool {
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false
	}
	switch payload.Type {
	case "response.completed", "response.incomplete", "response.failed", "error":
		return true
	default:
		return false
	}
}

func (d *webSocketStreamDecoder) Event() ssestream.Event {
	return d.event
}

func (d *webSocketStreamDecoder) Close() error {
	d.closed = true
	if d.done {
		d.stopCancelWatch()
		return nil
	}
	d.closeTransport()
	return nil
}

func (d *webSocketStreamDecoder) Err() error {
	return d.err
}

func (d *webSocketStreamDecoder) closeTransport() {
	d.stopCancelWatch()
	if d.transport == nil {
		return
	}
	_ = d.transport.Close()
}

func (d *webSocketStreamDecoder) stopCancelWatch() {
	d.stopOnce.Do(func() {
		if d.stopCancel != nil {
			close(d.stopCancel)
		}
	})
}
