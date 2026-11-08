package controlplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

// The chat event socket carries every conversation a Web UI page follows over
// one WebSocket. Browsers allow about six HTTP/1.1 connections per origin, and
// each streaming response holds one until it ends; established WebSockets do
// not use that pool. Commands remain ordinary HTTP requests: the socket only
// subscribes and unsubscribes, so its cookie authentication cannot be used to
// act for the user.

const (
	chatEventConnectionHeader   = "X-Kodelet-Event-Connection"
	chatEventSubscriptionHeader = "X-Kodelet-Event-Subscription"

	chatEventSubscriptionBuffer = 256
	maxChatEventSubscriptions   = 256
	maxChatEventMessageBytes    = 64 << 10
	chatEventOutboundQueue      = 64
	chatEventWriteTimeout       = 10 * time.Second
	chatEventPingInterval       = 25 * time.Second
	chatEventReadTimeout        = 60 * time.Second
	maxCoalescedChatEvents      = 64
)

// Subscription levels. A lifecycle subscription receives what a page needs
// about a conversation it is not showing; see lifecycleChatEvent.
const (
	chatEventLevelFull      = "full"
	chatEventLevelLifecycle = "lifecycle"
)

// Reasons the server reports when it ends a subscription.
const (
	// chatEventEndCompleted means a submitted turn finished after its final event.
	chatEventEndCompleted = "completed"
	// chatEventEndOverflow means the page fell behind; it should subscribe
	// again and reconcile saved state.
	chatEventEndOverflow = "overflow"
	// chatEventEndEnded means the server stopped following the conversation,
	// for example because it was deleted.
	chatEventEndEnded = "ended"
	// chatEventEndRejected means the subscription was never established.
	chatEventEndRejected = "rejected"
)

var (
	errChatEventConnectionClosed      = errors.New("chat event connection is closed")
	errInvalidChatEventSubscription   = errors.New("invalid subscription ID")
	errDuplicateChatEventSubscription = errors.New("subscription ID is already in use")
	errTooManyChatEventSubscriptions  = errors.New("too many subscriptions on this connection")
)

type chatEventClientMessage struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	ConversationID string `json:"conversationId,omitempty"`
	Level          string `json:"level,omitempty"`
	Capabilities   string `json:"capabilities,omitempty"`
	CallID         string `json:"callId,omitempty"`
}

type chatEventServerMessage struct {
	Type           string `json:"type"`
	ID             string `json:"id,omitempty"`
	ConnectionID   string `json:"connectionId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	Active         bool   `json:"active,omitempty"`
	TurnID         string `json:"turnId,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
	Event          any    `json:"event,omitempty"`
}

// chatEventSubmissionResponse answers a chat request that delivers its events
// on the page's event socket.
type chatEventSubmissionResponse struct {
	ConversationID string `json:"conversation_id"`
	TurnID         string `json:"turn_id"`
	// Accepted is false when the turn was already submitted; Receipt then
	// reports its saved status.
	Accepted bool              `json:"accepted"`
	Receipt  *chat.TurnReceipt `json:"receipt,omitempty"`
}

type chatEventConnection struct {
	server      *Server
	id          string
	principalID string
	clientID    string
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	out         chan []byte

	mu            sync.Mutex
	subscriptions map[string]*chatEventSubscription
	closed        bool
}

type chatEventSubscription struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	// The remaining fields are guarded by the connection's mutex.
	conversationID string
	sink           *subscriberEventSink
	// unsubscribed is set when the page ends the subscription, which needs no reply.
	unsubscribed bool
	// endReason, when the server ends the subscription, is the reason reported.
	endReason string
}

func (s *Server) handleChatEventsWebsocket(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.URL.Query().Get("clientId"))
	if !validUIClientID(clientID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "a valid clientId query parameter is required", nil)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 16 << 10,
		CheckOrigin:     s.terminalOriginAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.G(r.Context()).WithError(err).Debug("failed to upgrade chat event websocket")
		return
	}
	principal, _ := principalFromContext(r.Context())
	// Server shutdown ends the connection; the upgrade request's context does not.
	ctx, cancel := context.WithCancel(s.chatExecutionContext(r.Context()))
	connection := &chatEventConnection{
		server:        s,
		id:            rand.Text(),
		principalID:   principal.ID,
		clientID:      clientID,
		conn:          conn,
		ctx:           ctx,
		cancel:        cancel,
		out:           make(chan []byte, chatEventOutboundQueue),
		subscriptions: make(map[string]*chatEventSubscription),
	}
	s.registerChatEventConnection(connection)
	defer connection.close()
	go connection.writeLoop()
	if !connection.send(chatEventServerMessage{Type: "hello", ConnectionID: connection.id}) {
		return
	}
	connection.readLoop()
}

func (s *Server) registerChatEventConnection(connection *chatEventConnection) {
	s.chatEventConnectionsMu.Lock()
	defer s.chatEventConnectionsMu.Unlock()
	if s.chatEventConnections == nil {
		s.chatEventConnections = make(map[string]*chatEventConnection)
	}
	s.chatEventConnections[connection.id] = connection
}

func (s *Server) unregisterChatEventConnection(connection *chatEventConnection) {
	s.chatEventConnectionsMu.Lock()
	defer s.chatEventConnectionsMu.Unlock()
	if s.chatEventConnections[connection.id] == connection {
		delete(s.chatEventConnections, connection.id)
	}
}

func (s *Server) lookupChatEventConnection(id string) *chatEventConnection {
	s.chatEventConnectionsMu.Lock()
	defer s.chatEventConnectionsMu.Unlock()
	return s.chatEventConnections[id]
}

// writeLoop is the connection's only data writer. A page that stops reading
// fails the write deadline, which closes the connection rather than letting
// queued events grow without bound.
func (c *chatEventConnection) writeLoop() {
	ping := time.NewTicker(chatEventPingInterval)
	defer ping.Stop()
	// Closing the socket also ends the read loop.
	defer func() { _ = c.conn.Close() }()
	for {
		select {
		case <-c.ctx.Done():
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, ""),
				time.Now().Add(time.Second),
			)
			return
		case payload := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(chatEventWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(chatEventWriteTimeout)); err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *chatEventConnection) readLoop() {
	c.conn.SetReadLimit(maxChatEventMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(chatEventReadTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(chatEventReadTimeout))
	})
	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(chatEventReadTimeout))
		var message chatEventClientMessage
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if messageType != websocket.TextMessage || decoder.Decode(&message) != nil {
			c.closeWithPolicyViolation("chat event messages must be JSON objects")
			return
		}
		switch message.Type {
		case "subscribe":
			err = c.subscribe(message)
		case "unsubscribe":
			c.unsubscribe(strings.TrimSpace(message.ID))
		case "realtime":
			err = c.attachRealtime(message)
		default:
			err = errors.New("unsupported chat event message type")
		}
		if err != nil {
			c.closeWithPolicyViolation(err.Error())
			return
		}
	}
}

func (c *chatEventConnection) closeWithPolicyViolation(reason string) {
	_ = c.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
		time.Now().Add(time.Second),
	)
}

// send queues a message, waiting while the writer catches up. It reports
// false once the connection has closed.
func (c *chatEventConnection) send(message chatEventServerMessage) bool {
	payload, err := json.Marshal(message)
	if err != nil {
		logger.G(c.ctx).WithError(err).Warn("failed to encode chat event message")
		return true
	}
	select {
	case c.out <- payload:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *chatEventConnection) close() {
	c.cancel()
	c.mu.Lock()
	c.closed = true
	subscriptions := c.subscriptions
	c.subscriptions = nil
	c.mu.Unlock()
	for _, subscription := range subscriptions {
		c.release(subscription, subscription.conversationID, subscription.sink)
	}
	c.server.unregisterChatEventConnection(c)
}

// reserve claims a subscription ID before its subscription is established.
func (c *chatEventConnection) reserve(id string) (*chatEventSubscription, error) {
	if !validUIClientID(id) {
		return nil, errInvalidChatEventSubscription
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.ctx.Err() != nil {
		return nil, errChatEventConnectionClosed
	}
	if _, exists := c.subscriptions[id]; exists {
		return nil, errDuplicateChatEventSubscription
	}
	if len(c.subscriptions) >= maxChatEventSubscriptions {
		return nil, errTooManyChatEventSubscriptions
	}
	ctx, cancel := context.WithCancel(c.ctx)
	subscription := &chatEventSubscription{id: id, ctx: ctx, cancel: cancel}
	c.subscriptions[id] = subscription
	return subscription, nil
}

// reject ends a reserved subscription that could not be established.
func (c *chatEventConnection) reject(subscription *chatEventSubscription, reason string) {
	c.mu.Lock()
	if c.subscriptions[subscription.id] == subscription {
		delete(c.subscriptions, subscription.id)
	}
	c.mu.Unlock()
	subscription.cancel()
	c.send(chatEventServerMessage{
		Type:   "closed",
		ID:     subscription.id,
		Reason: chatEventEndRejected,
		Error:  reason,
	})
}

// reserveFromPage reserves a subscription the page asked for. An invalid or
// reused ID is a protocol error: a rejection naming a live subscription's ID
// would be ambiguous.
func (c *chatEventConnection) reserveFromPage(id string) (*chatEventSubscription, error) {
	subscription, err := c.reserve(id)
	if errors.Is(err, errTooManyChatEventSubscriptions) {
		c.send(chatEventServerMessage{Type: "closed", ID: id, Reason: chatEventEndRejected, Error: err.Error()})
		return nil, nil //nolint:nilnil // The page was told; the connection stays open.
	}
	return subscription, err
}

func (c *chatEventConnection) subscribe(message chatEventClientMessage) error {
	conversationID := strings.TrimSpace(message.ConversationID)
	level := strings.TrimSpace(message.Level)
	subscription, err := c.reserveFromPage(strings.TrimSpace(message.ID))
	if subscription == nil {
		return err
	}
	switch {
	case !validReceiptID(conversationID):
		c.reject(subscription, "invalid conversation ID")
		return nil
	case level != "" && level != chatEventLevelFull && level != chatEventLevelLifecycle:
		c.reject(subscription, "unsupported subscription level")
		return nil
	case c.server.conversationService == nil:
		c.reject(subscription, "conversation service is unavailable")
		return nil
	}
	if _, err := c.server.conversationService.GetConversation(subscription.ctx, conversationID); err != nil {
		c.reject(subscription, "conversation not found")
		return nil
	}
	sink := c.newSink(subscription, parseClientUICapabilities(message.Capabilities), level)
	active, registered := c.server.registerChatSubscriber(conversationID, sink)
	if !registered {
		sink.Close()
		c.reject(subscription, "conversation is unavailable")
		return nil
	}
	if !c.attach(subscription, conversationID, sink) {
		c.release(subscription, conversationID, sink)
		return nil
	}
	go c.forward(subscription, true, active)
	return nil
}

func (c *chatEventConnection) newSink(subscription *chatEventSubscription, capabilities chat.ChatClientCapabilities, level string) *subscriberEventSink {
	sink := newSizedSubscriberEventSink(chatEventSubscriptionBuffer)
	sink.ctx = subscription.ctx
	sink.ownerID = c.principalID
	sink.clientID = c.clientID
	sink.capabilities = capabilities
	if level == chatEventLevelLifecycle {
		sink.accept = lifecycleChatEvent
	}
	return sink
}

// attach records a registered sink, unless the subscription ended meanwhile.
func (c *chatEventConnection) attach(subscription *chatEventSubscription, conversationID string, sink *subscriberEventSink) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.subscriptions[subscription.id] != subscription {
		return false
	}
	subscription.conversationID = conversationID
	subscription.sink = sink
	return true
}

func (c *chatEventConnection) unsubscribe(id string) {
	c.mu.Lock()
	subscription := c.subscriptions[id]
	if subscription == nil {
		c.mu.Unlock()
		return
	}
	subscription.unsubscribed = true
	delete(c.subscriptions, id)
	conversationID, sink := subscription.conversationID, subscription.sink
	c.mu.Unlock()
	c.release(subscription, conversationID, sink)
}

// release stops a subscription's delivery and frees its server resources.
// Cancelling its context also detaches any interactive UI it owns.
func (c *chatEventConnection) release(subscription *chatEventSubscription, conversationID string, sink *subscriberEventSink) {
	subscription.cancel()
	if sink != nil {
		c.server.removeChatSubscriber(conversationID, sink)
		sink.Close()
	}
}

// end stops a subscription from the server side. Its forwarder reports reason
// after delivering every event already queued.
func (c *chatEventConnection) end(subscription *chatEventSubscription, reason string) {
	c.mu.Lock()
	if c.subscriptions[subscription.id] == subscription && !subscription.unsubscribed {
		subscription.endReason = reason
	}
	conversationID, sink := subscription.conversationID, subscription.sink
	c.mu.Unlock()
	if sink != nil {
		c.server.removeChatSubscriber(conversationID, sink)
		sink.Close()
	}
}

// finish forgets a subscription whose delivery has ended and returns the reason
// to report, or "" when the page ended it or the connection has closed.
func (c *chatEventConnection) finish(subscription *chatEventSubscription) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions[subscription.id] == subscription {
		delete(c.subscriptions, subscription.id)
	}
	switch {
	case subscription.unsubscribed || c.closed:
		return ""
	case subscription.endReason != "":
		return subscription.endReason
	case subscription.sink != nil && subscription.sink.overflowed.Load():
		return chatEventEndOverflow
	default:
		return chatEventEndEnded
	}
}

// forward relays a subscription's events in order, then reports why it ended.
// An acknowledged subscription first describes the conversation's current
// state, after registration, so no event between the two can be missed.
func (c *chatEventConnection) forward(subscription *chatEventSubscription, acknowledge, active bool) {
	defer subscription.cancel()
	if acknowledge {
		preamble := c.server.conversationStreamPreamble(subscription.conversationID, active)
		acknowledgement := chatEventServerMessage{
			Type:           "subscribed",
			ID:             subscription.id,
			ConversationID: subscription.conversationID,
			Active:         active,
		}
		if active && len(preamble) > 0 {
			acknowledgement.TurnID = preamble[0].TurnID
		}
		if !c.send(acknowledgement) {
			return
		}
		for _, event := range preamble {
			if !c.sendEvent(subscription.id, event) {
				return
			}
		}
	}
	events := subscription.sink.ch
	batch := make([]chat.ChatEvent, 0, maxCoalescedChatEvents)
	for open := true; open; {
		event, ok := <-events
		if !ok {
			break
		}
		batch = append(batch[:0], event)
	drain:
		for len(batch) < maxCoalescedChatEvents {
			select {
			case next, ok := <-events:
				if !ok {
					open = false
					break drain
				}
				batch = append(batch, next)
			default:
				break drain
			}
		}
		for _, event := range coalesceChatDeltas(batch) {
			if !c.sendEvent(subscription.id, event) {
				return
			}
		}
	}
	if reason := c.finish(subscription); reason != "" {
		c.send(chatEventServerMessage{Type: "closed", ID: subscription.id, Reason: reason})
	}
}

func (c *chatEventConnection) sendEvent(id string, event chat.ChatEvent) bool {
	return c.send(chatEventServerMessage{Type: "event", ID: id, Event: event})
}

// attachRealtime delivers a live voice call's events in place of its NDJSON
// stream. As there, the call ends when its observer does.
func (c *chatEventConnection) attachRealtime(message chatEventClientMessage) error {
	subscription, err := c.reserveFromPage(strings.TrimSpace(message.ID))
	if subscription == nil {
		return err
	}
	c.server.realtimeMu.Lock()
	call := c.server.realtimeCalls[strings.TrimSpace(message.CallID)]
	c.server.realtimeMu.Unlock()
	if call == nil || call.ownerID != c.principalID || call.clientID != c.clientID {
		c.reject(subscription, "live voice call not found")
		return nil
	}
	if !call.attach() {
		c.reject(subscription, "live voice event stream is already attached or closed")
		return nil
	}
	go func() {
		defer subscription.cancel()
		call.pump(subscription.ctx, chatEventPingInterval, func(event *realtimeEvent) error {
			// WebSocket pings keep the connection alive.
			if event == nil {
				return nil
			}
			if !c.send(chatEventServerMessage{Type: "realtime", ID: subscription.id, Event: event}) {
				return errChatEventConnectionClosed
			}
			return nil
		})
		c.mu.Lock()
		if !subscription.unsubscribed {
			subscription.endReason = chatEventEndCompleted
		}
		c.mu.Unlock()
		if reason := c.finish(subscription); reason != "" {
			c.send(chatEventServerMessage{Type: "closed", ID: subscription.id, Reason: reason})
		}
	}()
	return nil
}

// lifecycleChatEvent keeps what a page needs about a conversation it is not
// showing: runs starting and ending, resource reloads, and interactive UI.
func lifecycleChatEvent(event chat.ChatEvent) bool {
	switch event.Kind {
	case "conversation", "done", "error", "resources-reloaded":
		return true
	}
	return strings.HasPrefix(event.Kind, "ui-")
}

// coalesceChatDeltas merges adjacent text or thinking deltas, so a page that
// fell behind catches up in fewer frames. Like the Web UI's own coalescing, a
// merged event keeps the later event's other fields.
func coalesceChatDeltas(events []chat.ChatEvent) []chat.ChatEvent {
	merged := make([]chat.ChatEvent, 0, len(events))
	for _, event := range events {
		last := len(merged) - 1
		if last >= 0 && mergeableChatDeltas(merged[last], event) {
			event.Delta = merged[last].Delta + event.Delta
			merged[last] = event
			continue
		}
		merged = append(merged, event)
	}
	return merged
}

func mergeableChatDeltas(previous, next chat.ChatEvent) bool {
	return (next.Kind == "text-delta" || next.Kind == "thinking-delta") &&
		previous.Kind == next.Kind &&
		previous.ConversationID == next.ConversationID &&
		previous.ExtensionID == next.ExtensionID &&
		previous.Role == next.Role
}

type chatEventBindingError struct {
	status  int
	message string
}

func (e *chatEventBindingError) Error() string {
	return e.message
}

// chatEventBinding is the event socket subscription that a chat submission
// reserves before admission and activates once the turn is admitted.
type chatEventBinding struct {
	connection   *chatEventConnection
	subscription *chatEventSubscription
	activated    bool
}

// reserveChatEventSubmission validates the event socket that a chat request
// names. It returns nil when the request streams its events in the response,
// as CLI and SDK clients do.
func (s *Server) reserveChatEventSubmission(r *http.Request, clientID string) (*chatEventBinding, error) {
	connectionID := strings.TrimSpace(r.Header.Get(chatEventConnectionHeader))
	if connectionID == "" {
		return nil, nil //nolint:nilnil // No event socket means an ordinary streamed response.
	}
	if !validUIClientID(clientID) {
		return nil, &chatEventBindingError{
			status:  http.StatusBadRequest,
			message: "event socket submissions require a valid X-Kodelet-Client-ID header",
		}
	}
	principal, _ := principalFromContext(r.Context())
	connection := s.lookupChatEventConnection(connectionID)
	if connection == nil || connection.principalID != principal.ID || connection.clientID != clientID || connection.ctx.Err() != nil {
		return nil, &chatEventBindingError{
			status:  http.StatusPreconditionFailed,
			message: "the page's event connection is unavailable; reconnect and try again",
		}
	}
	subscription, err := connection.reserve(strings.TrimSpace(r.Header.Get(chatEventSubscriptionHeader)))
	switch {
	case errors.Is(err, errChatEventConnectionClosed):
		return nil, &chatEventBindingError{
			status:  http.StatusPreconditionFailed,
			message: "the page's event connection is unavailable; reconnect and try again",
		}
	case errors.Is(err, errInvalidChatEventSubscription):
		return nil, &chatEventBindingError{
			status:  http.StatusBadRequest,
			message: "a valid " + chatEventSubscriptionHeader + " header is required",
		}
	case err != nil:
		return nil, &chatEventBindingError{status: http.StatusConflict, message: err.Error()}
	}
	return &chatEventBinding{connection: connection, subscription: subscription}, nil
}

func (s *Server) writeChatEventBindingError(w http.ResponseWriter, err error) {
	var bindingErr *chatEventBindingError
	if errors.As(err, &bindingErr) {
		s.writeErrorResponse(w, bindingErr.status, bindingErr.message, nil)
		return
	}
	s.writeErrorResponse(w, http.StatusInternalServerError, "failed to bind the event connection", err)
}

// release frees a reservation whose turn never started.
func (b *chatEventBinding) release() {
	if b.activated {
		return
	}
	b.connection.reject(b.subscription, "the turn did not start")
}

// runChatForEventSubscription runs an admitted turn whose events reach the
// submitting page on its event socket. The request returns once the turn has
// started; the run continues if the page disconnects, as with streamed turns.
//
// The subscription is the run's direct observer, not a broadcast subscriber,
// so the page receives exactly what a streamed response would. In particular,
// it does not receive the user message it submitted, which it already shows.
func (s *Server) runChatForEventSubscription(w http.ResponseWriter, r *http.Request, req chat.ChatRequest, clientID string, attachment *sessionExtensionAttachment, receipt chat.TurnReceipt, binding *chatEventBinding) {
	connection, subscription := binding.connection, binding.subscription
	capabilities := chat.ChatClientCapabilities{}
	if req.ClientCapabilities != nil {
		capabilities = *req.ClientCapabilities
	}
	sink := connection.newSink(subscription, capabilities, chatEventLevelFull)
	observer := chatRunObserver{ownerCtx: subscription.ctx}
	// Attach before the run starts, so the page receives its first event.
	if connection.attach(subscription, req.ConversationID, sink) {
		binding.activated = true
		observer.sink = submitterEventSink{sink}
		observer.ownerSink = observer.sink
		go connection.forward(subscription, false, false)
	} else {
		// The run proceeds like a turn whose page has left: it has no
		// interactive owner, and the page reconciles from saved state.
		sink.Close()
	}

	started := make(chan struct{})
	var startOnce sync.Once
	type runResult struct {
		replay *chat.TurnReceipt
		err    error
	}
	finished := make(chan runResult, 1)
	activated := binding.activated
	// The request's values, such as its principal and trace, apply to the
	// run; its cancellation does not.
	requestCtx := context.WithoutCancel(r.Context())
	observer.started = func() { startOnce.Do(func() { close(started) }) }
	go func() {
		replay, err := s.runObservedChat(requestCtx, req, observer, clientID, attachment, receipt)
		if activated {
			connection.end(subscription, chatEventEndCompleted)
		}
		finished <- runResult{replay: replay, err: err}
	}()

	select {
	case <-started:
		s.replyChatSubmission(w, req, nil)
	case result := <-finished:
		switch {
		case result.err != nil:
			code := http.StatusInternalServerError
			if errors.Is(result.err, errConversationBusy) {
				code = http.StatusConflict
			}
			s.writeErrorResponse(w, code, result.err.Error(), nil)
		case result.replay != nil:
			s.replyChatSubmission(w, req, result.replay)
		default:
			s.replyChatSubmission(w, req, nil)
		}
	}
}

// submitterEventSink delivers a run's events to the page that submitted it.
// A page that falls behind loses the subscription, as an unread streamed
// response would, and is told to follow the conversation again.
type submitterEventSink struct {
	*subscriberEventSink
}

func (s submitterEventSink) Send(event chat.ChatEvent) error {
	err := s.subscriberEventSink.Send(event)
	if err != nil {
		s.Close()
	}
	return err
}

// replyChatSubmission reports an accepted turn, or with a receipt, one that
// was already submitted.
func (s *Server) replyChatSubmission(w http.ResponseWriter, req chat.ChatRequest, receipt *chat.TurnReceipt) {
	w.Header().Set("X-Kodelet-Conversation-ID", req.ConversationID)
	w.Header().Set("X-Kodelet-Turn-ID", req.TurnID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusAccepted
	if receipt != nil {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(chatEventSubmissionResponse{
		ConversationID: req.ConversationID,
		TurnID:         req.TurnID,
		Accepted:       receipt == nil,
		Receipt:        receipt,
	})
}
