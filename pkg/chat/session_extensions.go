package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

// SessionExtensionRelay is a single authenticated, session-scoped callback connection.
// A closed relay cannot reconnect or replay callbacks; resuming requires a new attachment.
type SessionExtensionRelay interface {
	Attachment() protocol.SessionExtensions
	SendFrame(context.Context, protocol.ExtensionFrame) error
	Close() error
	Done() <-chan struct{}
	Err() error
}

type sessionExtensionRelay struct {
	peer       *protocol.Peer
	cancel     context.CancelFunc
	mu         sync.RWMutex
	attachment protocol.SessionExtensions
	err        error
	frames     chan protocol.ExtensionFrame
}

// AttachSessionExtensions opens a callback relay using this client's daemon credential
// and client identity. The handler receives accepted frames independently of their RPC
// acknowledgements, so a callback may make nested host requests without deadlocking.
func (r *Client) AttachSessionExtensions(ctx context.Context, conversationID, runnerID string, extensionIDs []string, handler func(context.Context, protocol.ExtensionFrame) error) (SessionExtensionRelay, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(runnerID) == "" || len(extensionIDs) == 0 || handler == nil {
		return nil, errors.New("session extensions require a conversation, runner, extension IDs and frame handler")
	}
	if err := (protocol.SessionExtensions{ID: conversationID, ExtensionIDs: extensionIDs}).Validate(); err != nil {
		return nil, err
	}
	endpoint, err := controlplaneurl.WebSocketEndpoint(r.baseURL, strings.Split(strings.Trim(protocol.SessionExtensionsEndpoint, "/"), "/")...)
	if err != nil {
		return nil, err
	}
	request := &http.Request{Header: make(http.Header)}
	r.authorize(request)
	request.Header.Set(ClientIDHeader, r.clientID)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{protocol.SessionExtensionsSubprotocol}
	connectCtx, cancelConnect := context.WithTimeout(ctx, 15*time.Second)
	defer cancelConnect()
	conn, response, err := dialer.DialContext(connectCtx, endpoint, request.Header)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to attach session extensions; check the server connection and session-extension support")
	}
	if conn.Subprotocol() != protocol.SessionExtensionsSubprotocol {
		_ = conn.Close()
		return nil, errors.New("server does not support the session-extension relay protocol")
	}
	relayCtx, cancel := context.WithCancel(ctx)
	relay := &sessionExtensionRelay{cancel: cancel, frames: make(chan protocol.ExtensionFrame, 128)}
	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{RequestPrefix: "sdk", Handler: relay})
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	relay.peer = peer
	if err := peer.Start(relayCtx); err != nil {
		_ = relay.Close()
		return nil, err
	}
	var attachment protocol.SessionExtensions
	err = peer.Call(connectCtx, protocol.MethodSessionExtensionsAttach, struct {
		ConversationID string   `json:"conversationId"`
		RunnerID       string   `json:"runnerId"`
		ExtensionIDs   []string `json:"extensionIds"`
	}{conversationID, runnerID, extensionIDs}, &attachment)
	if err == nil && (attachment.Validate() != nil || !slices.Equal(attachment.ExtensionIDs, extensionIDs)) {
		err = errors.New("server returned an invalid session-extension attachment")
	}
	if err != nil {
		_ = relay.Close()
		return nil, errors.Wrap(err, "failed to attach session extensions")
	}
	relay.mu.Lock()
	relay.attachment = attachment
	relay.mu.Unlock()
	go func() {
		select {
		case <-peer.TransportDone():
			cancel()
		case <-relayCtx.Done():
		}
	}()
	go relay.deliverFrames(relayCtx, handler)
	return relay, nil
}

func (r *sessionExtensionRelay) Attachment() protocol.SessionExtensions {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return protocol.SessionExtensions{ID: r.attachment.ID, ExtensionIDs: slices.Clone(r.attachment.ExtensionIDs)}
}

func (r *sessionExtensionRelay) validateFrame(frame protocol.ExtensionFrame) error {
	attachment := r.Attachment()
	if attachment.ID == "" || frame.AttachmentID != attachment.ID || !slices.Contains(attachment.ExtensionIDs, frame.ExtensionID) {
		return errors.New("extension frame does not belong to this attachment")
	}
	return frame.Validate()
}

func (r *sessionExtensionRelay) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, *protocol.RPCError) {
	if method != protocol.MethodSessionExtensionFrame {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeMethodNotFound, Message: "session extension method not found"}
	}
	var frame protocol.ExtensionFrame
	if err := json.Unmarshal(params, &frame); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	if err := r.validateFrame(frame); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	select {
	case r.frames <- frame:
		return struct{}{}, nil
	case <-ctx.Done():
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: ctx.Err().Error()}
	case <-r.peer.TransportDone():
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "session extension relay is closed"}
	}
}

func (r *sessionExtensionRelay) deliverFrames(ctx context.Context, handler func(context.Context, protocol.ExtensionFrame) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-r.frames:
			if ctx.Err() != nil {
				return
			}
			if err := handler(ctx, frame); err != nil {
				r.mu.Lock()
				r.err = errors.Wrap(err, "session extension frame delivery failed")
				r.mu.Unlock()
				_ = r.Close()
				return
			}
		}
	}
}

func (r *sessionExtensionRelay) SendFrame(ctx context.Context, frame protocol.ExtensionFrame) error {
	if err := r.validateFrame(frame); err != nil {
		return err
	}
	return r.peer.Call(ctx, protocol.MethodSessionExtensionFrame, frame, nil)
}

func (r *sessionExtensionRelay) Close() error {
	r.cancel()
	return r.peer.Close()
}

func (r *sessionExtensionRelay) Done() <-chan struct{} { return r.peer.TransportDone() }

func (r *sessionExtensionRelay) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.err != nil {
		return r.err
	}
	return r.peer.Err()
}
