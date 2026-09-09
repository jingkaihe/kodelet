package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

const (
	maxSessionExtensionMessageBytes = 4 * 1024 * 1024
	sessionExtensionQueueSize       = 64
	sessionExtensionDeliveryTimeout = 10 * time.Second
)

// sessionExtensionTransport adapts the existing Content-Length byte stream to
// reliable, delivery-acknowledged raw JSON-RPC messages. It never reconnects.
type sessionExtensionTransport struct {
	net.Conn
	relay     net.Conn
	peer      Peer
	identity  protocol.ExtensionFrame
	ctx       context.Context
	cancel    context.CancelFunc
	incoming  chan []byte
	mu        sync.Mutex
	queued    int
	closed    bool
	closeOnce sync.Once
	closeDone chan struct{}
}

func newSessionExtensionTransport(ctx context.Context, peer Peer, identity protocol.ExtensionFrame) *sessionExtensionTransport {
	local, relay := net.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	t := &sessionExtensionTransport{
		Conn: local, relay: relay, peer: peer, identity: identity,
		ctx: ctx, cancel: cancel, incoming: make(chan []byte, sessionExtensionQueueSize),
		closeDone: make(chan struct{}),
	}
	go t.sendLoop()
	go t.receiveLoop()
	context.AfterFunc(ctx, func() { t.shutdown(true) })
	return t
}

func (t *sessionExtensionTransport) sendLoop() {
	defer t.shutdown(true)
	reader := bufio.NewReader(t.relay)
	for {
		headers, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err != nil {
			return
		}
		length, err := strconv.Atoi(headers.Get("Content-Length"))
		if err != nil || length < 0 || length > maxSessionExtensionMessageBytes {
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		frame := t.identity
		frame.Message = payload
		ctx, cancel := context.WithTimeout(t.ctx, sessionExtensionDeliveryTimeout)
		err = t.peer.Call(ctx, protocol.MethodSessionExtensionFrame, frame, new(struct{}))
		cancel()
		if err != nil {
			return
		}
	}
}

func (t *sessionExtensionTransport) receiveLoop() {
	defer t.shutdown(true)
	for {
		select {
		case <-t.ctx.Done():
			return
		case payload := <-t.incoming:
			_, err := fmt.Fprintf(t.relay, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
			t.mu.Lock()
			t.queued -= len(payload)
			t.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// deliver never waits for the extension reader or callback execution. The
// bounded queue prevents backpressure from occupying the peer's control slots.
func (t *sessionExtensionTransport) deliver(payload json.RawMessage) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("session extension channel is closed; explicit reattachment is required")
	}
	if t.queued+len(payload) <= maxSessionExtensionMessageBytes {
		select {
		case t.incoming <- append([]byte(nil), payload...):
			t.queued += len(payload)
			t.mu.Unlock()
			return nil
		default:
		}
	}
	t.mu.Unlock()
	t.shutdown(true)
	return errors.New("session extension delivery queue is full; channel closed")
}

func (t *sessionExtensionTransport) Close() error {
	t.shutdown(true)
	return nil
}

func (t *sessionExtensionTransport) shutdown(notify bool) {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.cancel()
		_ = t.Conn.Close()
		_ = t.relay.Close()
		if notify {
			// Process.Close may hold its lifecycle lock. Never wait here for a
			// remote peer or a reverse handler that needs that same lock.
			go func() {
				defer close(t.closeDone)
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), time.Second)
				defer cancel()
				frame := t.identity
				frame.Close = true
				_ = t.peer.Call(ctx, protocol.MethodSessionExtensionFrame, frame, new(struct{}))
			}()
		} else {
			close(t.closeDone)
		}
	})
}

func (s *Service) attachSessionExtensions(run *activeRun, descriptor protocol.SessionExtensions) ([]extensions.Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peer == nil || s.runs[run.id] != run || run.closing || run.ctx.Err() != nil {
		return nil, errors.New("session extensions require an active authenticated runner connection")
	}
	run.attachments = make(map[string]*sessionExtensionTransport, len(descriptor.ExtensionIDs))
	attachments := make([]extensions.Attachment, 0, len(descriptor.ExtensionIDs))
	for _, id := range descriptor.ExtensionIDs {
		transport := newSessionExtensionTransport(s.ctx, s.peer, protocol.ExtensionFrame{
			AttachmentID: descriptor.ID, RunID: run.id, ExtensionID: id,
		})
		run.attachments[id] = transport
		attachments = append(attachments, extensions.Attachment{ID: id, Transport: transport})
	}
	return attachments, nil
}

func (s *Service) receiveExtensionFrame(frame protocol.ExtensionFrame) error {
	if err := frame.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	run := s.runs[frame.RunID]
	var transport *sessionExtensionTransport
	if run != nil {
		transport = run.attachments[frame.ExtensionID]
	}
	s.mu.Unlock()
	// Opening runs and bounded closing replies must pass: initialization and
	// session.end are themselves bidirectional extension RPC exchanges.
	if transport == nil || transport.identity.AttachmentID != frame.AttachmentID {
		return errors.New("session extension frame does not match an attached run channel")
	}
	if frame.Close {
		transport.shutdown(false)
		return nil
	}
	return transport.deliver(frame.Message)
}

func (s *Service) closeRunAttachments(run *activeRun) {
	s.mu.Lock()
	attachments := run.attachments
	run.attachments = nil
	s.mu.Unlock()
	for _, transport := range attachments {
		_ = transport.Close()
	}
	// Keep run.close pending until the bounded close deliveries finish. A
	// fire-and-forget close could arrive after the daemon revokes the run route.
	for _, transport := range attachments {
		<-transport.closeDone
	}
}
