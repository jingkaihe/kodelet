package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chatEventTestMessage struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	ConnectionID   string          `json:"connectionId"`
	ConversationID string          `json:"conversationId"`
	Active         bool            `json:"active"`
	TurnID         string          `json:"turnId"`
	Reason         string          `json:"reason"`
	Error          string          `json:"error"`
	Event          json.RawMessage `json:"event"`
}

type chatEventTestClient struct {
	t            *testing.T
	conn         *websocket.Conn
	connectionID string
}

func chatEventTestServer(t *testing.T, run func(context.Context, ChatRequest, ChatEventSink) (string, error)) (*Server, *httptest.Server) {
	t.Helper()
	server := &Server{
		conversationService: &mockConversationService{getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			if id == "missing" {
				return nil, convtypes.ErrConversationNotFound
			}
			return &conversations.GetConversationResponse{ID: id}, nil
		}},
		runCtx:     t.Context(),
		router:     mux.NewRouter(),
		turns:      openTurnTestStore(t, filepath.Join(t.TempDir(), "events.db")),
		chatRunner: &mockChatRunner{runFunc: run},
	}
	server.setupRoutes()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

// dialChatEvents connects as the page whose client ID is "page".
func dialChatEvents(t *testing.T, baseURL string) *chatEventTestClient {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(baseURL, "http")+"/api/chat/events?clientId=page", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := &chatEventTestClient{t: t, conn: conn}
	hello := client.read()
	require.Equal(t, "hello", hello.Type)
	require.NotEmpty(t, hello.ConnectionID)
	client.connectionID = hello.ConnectionID
	return client
}

func (c *chatEventTestClient) write(message map[string]string) {
	c.t.Helper()
	require.NoError(c.t, c.conn.WriteJSON(message))
}

func (c *chatEventTestClient) read() chatEventTestMessage {
	c.t.Helper()
	require.NoError(c.t, c.conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	var message chatEventTestMessage
	require.NoError(c.t, c.conn.ReadJSON(&message))
	return message
}

func (c *chatEventTestClient) readEvent(id string) chat.ChatEvent {
	c.t.Helper()
	message := c.read()
	require.Equal(c.t, "event", message.Type, "message: %+v", message)
	require.Equal(c.t, id, message.ID)
	var event chat.ChatEvent
	require.NoError(c.t, json.Unmarshal(message.Event, &event))
	return event
}

// readUntilClosed returns a subscription's event kinds and its closing message.
func (c *chatEventTestClient) readUntilClosed(id string) ([]chat.ChatEvent, chatEventTestMessage) {
	c.t.Helper()
	var events []chat.ChatEvent
	for {
		message := c.read()
		require.Equal(c.t, id, message.ID, "message: %+v", message)
		if message.Type == "closed" {
			return events, message
		}
		require.Equal(c.t, "event", message.Type)
		var event chat.ChatEvent
		require.NoError(c.t, json.Unmarshal(message.Event, &event))
		events = append(events, event)
	}
}

func eventKinds(events []chat.ChatEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func submitOnEventSocket(t *testing.T, baseURL, body string, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/chat", strings.NewReader(body))
	require.NoError(t, err)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// waitForChatSubscribers waits for the test conversation's subscriber count.
func waitForChatSubscribers(t *testing.T, server *Server, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		server.chatSubscribersMu.Lock()
		defer server.chatSubscribersMu.Unlock()
		return len(server.chatSubscribers["conversation"]) == count
	}, 3*time.Second, 5*time.Millisecond)
}

func TestChatEventSocketSubscriptionLifecycle(t *testing.T) {
	server, httpServer := chatEventTestServer(t, nil)
	client := dialChatEvents(t, httpServer.URL)

	client.write(map[string]string{"type": "subscribe", "id": "missing-1", "conversationId": "missing"})
	rejected := client.read()
	assert.Equal(t, "closed", rejected.Type)
	assert.Equal(t, "missing-1", rejected.ID)
	assert.Equal(t, chatEventEndRejected, rejected.Reason)
	assert.Equal(t, "conversation not found", rejected.Error)

	client.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})
	subscribed := client.read()
	assert.Equal(t, "subscribed", subscribed.Type)
	assert.Equal(t, "watch-1", subscribed.ID)
	assert.Equal(t, "conversation", subscribed.ConversationID)
	assert.False(t, subscribed.Active)

	waitForChatSubscribers(t, server, 1)
	server.broadcastChatEvent("conversation", chat.ChatEvent{Kind: "text-delta", ConversationID: "conversation", Delta: "hello"})
	assert.Equal(t, "hello", client.readEvent("watch-1").Delta)

	// Deleting a conversation ends its subscribers.
	server.closeChatSubscribers("conversation")
	closed := client.read()
	assert.Equal(t, "closed", closed.Type)
	assert.Equal(t, "watch-1", closed.ID)
	assert.Equal(t, chatEventEndEnded, closed.Reason)

	// A page that unsubscribes gets no reply and leaves no subscriber behind.
	client.write(map[string]string{"type": "subscribe", "id": "watch-2", "conversationId": "conversation"})
	assert.Equal(t, "subscribed", client.read().Type)
	waitForChatSubscribers(t, server, 1)
	client.write(map[string]string{"type": "unsubscribe", "id": "watch-2"})
	waitForChatSubscribers(t, server, 0)
}

func TestChatEventSocketDescribesActiveRunOnSubscribe(t *testing.T) {
	server, httpServer := chatEventTestServer(t, nil)
	run := newActiveChatRun(func() {})
	run.turnID = "turn-1"
	server.activeChats = map[string]*activeChatRun{"conversation": run}
	client := dialChatEvents(t, httpServer.URL)

	client.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})
	subscribed := client.read()
	assert.Equal(t, "subscribed", subscribed.Type)
	assert.True(t, subscribed.Active)
	assert.Equal(t, "turn-1", subscribed.TurnID)
	event := client.readEvent("watch-1")
	assert.Equal(t, "conversation", event.Kind)
	assert.Equal(t, "turn-1", event.TurnID)
}

func TestChatEventSocketLifecycleLevelSkipsTranscriptDetail(t *testing.T) {
	server, httpServer := chatEventTestServer(t, nil)
	client := dialChatEvents(t, httpServer.URL)
	client.write(map[string]string{"type": "subscribe", "id": "background", "conversationId": "conversation", "level": "lifecycle"})
	require.Equal(t, "subscribed", client.read().Type)
	waitForChatSubscribers(t, server, 1)

	for _, event := range []chat.ChatEvent{
		{Kind: "conversation", TurnID: "turn-1"},
		{Kind: "text-delta", Delta: "skipped"},
		{Kind: "tool-use", ToolName: "bash"},
		{Kind: "ui-notification", UINotify: &chat.UINotifyEvent{Message: "kept"}},
		{Kind: "resources-reloaded"},
		{Kind: "done", TurnID: "turn-1"},
	} {
		event.ConversationID = "conversation"
		server.broadcastChatEvent("conversation", event)
	}
	server.closeChatSubscribers("conversation")
	events, closed := client.readUntilClosed("background")
	assert.Equal(t, []string{"conversation", "ui-notification", "resources-reloaded", "done"}, eventKinds(events))
	assert.Equal(t, chatEventEndEnded, closed.Reason)
}

func TestChatEventSocketRejectsReusedSubscriptionIDs(t *testing.T) {
	_, httpServer := chatEventTestServer(t, nil)
	client := dialChatEvents(t, httpServer.URL)
	client.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})
	require.Equal(t, "subscribed", client.read().Type)
	client.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})

	require.NoError(t, client.conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err := client.conn.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	assert.Equal(t, websocket.ClosePolicyViolation, closeErr.Code)
	assert.Equal(t, errDuplicateChatEventSubscription.Error(), closeErr.Text)
}

func TestChatEventSocketRejectsCrossOriginUpgrade(t *testing.T) {
	_, httpServer := chatEventTestServer(t, nil)
	header := http.Header{"Origin": {"https://attacker.example"}}
	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/chat/events?clientId=page", header)
	require.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
}

func TestChatEventSocketSubmissionReturnsOnceTurnStartsAndOwnsPrompts(t *testing.T) {
	var server *Server
	answered := make(chan string, 1)
	server, httpServer := chatEventTestServer(t, func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		if err := sink.Send(chat.ChatEvent{Kind: "text-delta", ConversationID: req.ConversationID, Delta: "Working"}); err != nil {
			return req.ConversationID, err
		}
		response, err := server.uiInputBrokerForRun(req.ConversationID).Input(ctx, extensions.UIInputRequest{ID: "extension-id", Title: "Name?"})
		if err != nil {
			return req.ConversationID, err
		}
		answered <- response.Value
		return req.ConversationID, nil
	})
	client := dialChatEvents(t, httpServer.URL)
	// Another page follows the same conversation.
	watcher := dialChatEvents(t, httpServer.URL)
	watcher.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})
	require.Equal(t, "subscribed", watcher.read().Type)
	waitForChatSubscribers(t, server, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	response := submitOnEventSocket(t, httpServer.URL,
		`{"message":"hello","conversationId":"conversation","turnId":"turn-1","clientCapabilities":{"interactiveUI":true}}`,
		map[string]string{
			chat.ClientIDHeader:         "page",
			chatEventConnectionHeader:   client.connectionID,
			chatEventSubscriptionHeader: "send-1",
		})
	// The run is waiting for the prompt answer, so this response cannot have
	// waited for the run to finish.
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var accepted chatEventSubmissionResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&accepted))
	assert.Equal(t, chatEventSubmissionResponse{ConversationID: "conversation", TurnID: "turn-1", Accepted: true}, accepted)

	// Like a streamed response, the submitter's subscription carries the run's
	// events but not the message it submitted, which the page already shows.
	var prompt chat.ChatEvent
	var kinds []string
	for prompt.UIInput == nil {
		prompt = client.readEvent("send-1")
		kinds = append(kinds, prompt.Kind)
	}
	assert.Equal(t, []string{"text-delta", "ui-input-request"}, kinds)

	for clientID, status := range map[string]int{"another-page": http.StatusNotFound, "page": http.StatusOK} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			httpServer.URL+"/api/conversations/conversation/ui-input/"+prompt.UIInput.ID,
			strings.NewReader(`{"status":"submitted","value":"Ada"}`))
		require.NoError(t, err)
		request.Header.Set(chat.ClientIDHeader, clientID)
		answer, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.NoError(t, answer.Body.Close())
		assert.Equal(t, status, answer.StatusCode, clientID)
	}
	assert.Equal(t, "Ada", <-answered)

	events, closed := client.readUntilClosed("send-1")
	assert.Equal(t, []string{"ui-request-end", "done"}, eventKinds(events))
	assert.Equal(t, "turn-1", events[1].TurnID)
	assert.Equal(t, chatEventEndCompleted, closed.Reason)

	// Other pages see the run start with the submitted message, but not its prompts.
	var watched []chat.ChatEvent
	for len(watched) == 0 || watched[len(watched)-1].Kind != "done" {
		watched = append(watched, watcher.readEvent("watch-1"))
	}
	assert.Equal(t, []string{"conversation", "user-message", "text-delta", "done"}, eventKinds(watched))
	assert.Equal(t, "turn-1", watched[0].TurnID)
	assert.Equal(t, "hello", watched[1].Content)
	// The submitter was never a broadcast subscriber; only the other page is.
	waitForChatSubscribers(t, server, 1)
	receipt, err := server.turns.get(t.Context(), "conversation", "turn-1")
	require.NoError(t, err)
	assert.Equal(t, "succeeded", receipt.Status)
}

func TestChatEventSocketSubmissionValidatesItsConnection(t *testing.T) {
	var runs atomic.Int32
	server, httpServer := chatEventTestServer(t, func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
		runs.Add(1)
		return req.ConversationID, nil
	})
	client := dialChatEvents(t, httpServer.URL)
	client.write(map[string]string{"type": "subscribe", "id": "watch-1", "conversationId": "conversation"})
	require.Equal(t, "subscribed", client.read().Type)

	for _, test := range []struct {
		name                               string
		clientID, connection, subscription string
		status                             int
	}{
		{"unknown connection", "page", "unknown", "send-1", http.StatusPreconditionFailed},
		{"another page's connection", "another-page", client.connectionID, "send-1", http.StatusPreconditionFailed},
		{"missing subscription", "page", client.connectionID, "", http.StatusBadRequest},
		{"subscription in use", "page", client.connectionID, "watch-1", http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := submitOnEventSocket(t, httpServer.URL, `{"message":"hello","conversationId":"conversation"}`, map[string]string{
				chat.ClientIDHeader:         test.clientID,
				chatEventConnectionHeader:   test.connection,
				chatEventSubscriptionHeader: test.subscription,
			})
			assert.Equal(t, test.status, response.StatusCode)
		})
	}
	assert.Zero(t, runs.Load(), "a rejected binding must not start work")

	// A failed submission releases its reservation, so the ID can be reused.
	response := submitOnEventSocket(t, httpServer.URL, `{"message":"","conversationId":"conversation"}`, map[string]string{
		chat.ClientIDHeader:         "page",
		chatEventConnectionHeader:   client.connectionID,
		chatEventSubscriptionHeader: "send-2",
	})
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Eventually(t, func() bool {
		connection := server.lookupChatEventConnection(client.connectionID)
		connection.mu.Lock()
		defer connection.mu.Unlock()
		_, reserved := connection.subscriptions["send-2"]
		return !reserved
	}, time.Second, 5*time.Millisecond)
}

func TestChatEventSocketDisconnectKeepsRunAndDismissesPrompt(t *testing.T) {
	var server *Server
	prompted := make(chan struct{})
	promptStatus := make(chan string, 1)
	release := make(chan struct{})
	server, httpServer := chatEventTestServer(t, func(ctx context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
		close(prompted)
		response, err := server.uiInputBrokerForRun(req.ConversationID).Confirm(ctx, extensions.UIConfirmRequest{ID: "approval", Title: "Allow?"})
		if err != nil {
			return req.ConversationID, err
		}
		promptStatus <- response.Status
		select {
		case <-release:
			return req.ConversationID, nil
		case <-ctx.Done():
			return req.ConversationID, ctx.Err()
		}
	})
	client := dialChatEvents(t, httpServer.URL)
	response := submitOnEventSocket(t, httpServer.URL,
		`{"message":"hello","conversationId":"conversation","turnId":"turn-1","clientCapabilities":{"interactiveUI":true}}`,
		map[string]string{
			chat.ClientIDHeader:         "page",
			chatEventConnectionHeader:   client.connectionID,
			chatEventSubscriptionHeader: "send-1",
		})
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	<-prompted
	for client.readEvent("send-1").Kind != "ui-confirm-request" {
	}

	require.NoError(t, client.conn.Close())
	assert.Equal(t, extensions.UIInputStatusDismissed, <-promptStatus)
	assert.True(t, server.isActiveChat("conversation"), "losing the page must not cancel accepted work")
	close(release)
	require.Eventually(t, func() bool {
		receipt, err := server.turns.get(t.Context(), "conversation", "turn-1")
		return err == nil && receipt.Status == "succeeded"
	}, 3*time.Second, 5*time.Millisecond)
}

func TestChatEventSubscriptionReportsOverflow(t *testing.T) {
	server := &Server{conversationService: &mockConversationService{}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connection := &chatEventConnection{
		server:        server,
		ctx:           ctx,
		cancel:        cancel,
		out:           make(chan []byte, 8),
		subscriptions: make(map[string]*chatEventSubscription),
	}
	subscription, err := connection.reserve("watch-1")
	require.NoError(t, err)
	sink := newSizedSubscriberEventSink(1)
	sink.ctx = subscription.ctx
	_, registered := server.registerChatSubscriber("conversation", sink)
	require.True(t, registered)
	require.True(t, connection.attach(subscription, "conversation", sink))

	// The second event finds the buffer full, so the subscriber is dropped.
	server.broadcastChatEvent("conversation", chat.ChatEvent{Kind: "text-delta", Delta: "kept"})
	server.broadcastChatEvent("conversation", chat.ChatEvent{Kind: "text-delta", Delta: "lost"})
	connection.forward(subscription, false, false)

	var messages []chatEventTestMessage
	for len(connection.out) > 0 {
		var message chatEventTestMessage
		require.NoError(t, json.Unmarshal(<-connection.out, &message))
		messages = append(messages, message)
	}
	require.Len(t, messages, 2)
	assert.Equal(t, "event", messages[0].Type)
	assert.Equal(t, chatEventTestMessage{Type: "closed", ID: "watch-1", Reason: chatEventEndOverflow}, messages[1])
	assert.Error(t, subscription.ctx.Err(), "an overflowed subscription no longer owns interactive UI")
}

func TestChatEventSubmitterThatFallsBehindIsToldToFollowAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connection := &chatEventConnection{
		server:        &Server{},
		ctx:           ctx,
		cancel:        cancel,
		out:           make(chan []byte, 8),
		subscriptions: make(map[string]*chatEventSubscription),
	}
	subscription, err := connection.reserve("send-1")
	require.NoError(t, err)
	sink := newSizedSubscriberEventSink(1)
	require.True(t, connection.attach(subscription, "conversation", sink))
	submitter := submitterEventSink{sink}

	require.NoError(t, submitter.Send(chat.ChatEvent{Kind: "text-delta", Delta: "kept"}))
	require.Error(t, submitter.Send(chat.ChatEvent{Kind: "text-delta", Delta: "lost"}))
	connection.forward(subscription, false, false)

	var messages []chatEventTestMessage
	for len(connection.out) > 0 {
		var message chatEventTestMessage
		require.NoError(t, json.Unmarshal(<-connection.out, &message))
		messages = append(messages, message)
	}
	require.Len(t, messages, 2)
	assert.Equal(t, "event", messages[0].Type)
	assert.Equal(t, chatEventTestMessage{Type: "closed", ID: "send-1", Reason: chatEventEndOverflow}, messages[1])
}

func TestCoalesceChatDeltasMergesAdjacentDeltasOnly(t *testing.T) {
	merged := coalesceChatDeltas([]chat.ChatEvent{
		{Kind: "text-delta", ConversationID: "c", Delta: "Hel"},
		{Kind: "text-delta", ConversationID: "c", Delta: "lo"},
		{Kind: "thinking-delta", ConversationID: "c", Delta: "hmm"},
		{Kind: "tool-use", ConversationID: "c"},
		{Kind: "text-delta", ConversationID: "c", Delta: " again"},
		{Kind: "text-delta", ConversationID: "c", ExtensionID: "ext", Delta: "!"},
	})
	assert.Equal(t, []chat.ChatEvent{
		{Kind: "text-delta", ConversationID: "c", Delta: "Hello"},
		{Kind: "thinking-delta", ConversationID: "c", Delta: "hmm"},
		{Kind: "tool-use", ConversationID: "c"},
		{Kind: "text-delta", ConversationID: "c", Delta: " again"},
		{Kind: "text-delta", ConversationID: "c", ExtensionID: "ext", Delta: "!"},
	}, merged)
}

func TestChatEventSocketCarriesLiveVoiceEvents(t *testing.T) {
	server, session := realtimeTestServer(t)
	server.router = mux.NewRouter()
	server.setupRoutes()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	newCall := func(clientID string) *realtimeCall {
		ctx, cancel := context.WithCancel(t.Context())
		call := &realtimeCall{
			// Without web authentication, every request is the anonymous administrator.
			server: server, id: convtypes.GenerateID(), ownerID: administrativePrincipal("anonymous").ID, clientID: clientID,
			chat: chat.ChatRequest{ConversationID: "saved"}, ctx: ctx, cancel: cancel, session: session,
			events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}),
		}
		server.realtimeMu.Lock()
		if server.realtimeCalls == nil {
			server.realtimeCalls = make(map[string]*realtimeCall)
		}
		server.realtimeCalls[call.id] = call
		server.realtimeMu.Unlock()
		return call
	}
	client := dialChatEvents(t, httpServer.URL)

	other := newCall("another-page")
	client.write(map[string]string{"type": "realtime", "id": "call-0", "callId": other.id})
	rejected := client.read()
	assert.Equal(t, chatEventTestMessage{Type: "closed", ID: "call-0", Reason: chatEventEndRejected, Error: "live voice call not found"}, rejected)

	call := newCall("page")
	call.emit(realtimeEvent{Kind: "state", Text: "connected"})
	client.write(map[string]string{"type": "realtime", "id": "call-1", "callId": call.id})
	message := client.read()
	assert.Equal(t, "realtime", message.Type)
	assert.JSONEq(t, `{"kind":"state","text":"connected","conversationId":"saved"}`, string(message.Event))
	call.close()
	message = client.read()
	assert.Equal(t, "realtime", message.Type)
	assert.JSONEq(t, `{"kind":"closed","conversationId":"saved"}`, string(message.Event))
	assert.Equal(t, chatEventTestMessage{Type: "closed", ID: "call-1", Reason: chatEventEndCompleted}, client.read())

	// Like closing its NDJSON stream, unsubscribing hangs up the call.
	hangUp := newCall("page")
	client.write(map[string]string{"type": "realtime", "id": "call-2", "callId": hangUp.id})
	require.Eventually(t, func() bool {
		hangUp.mu.Lock()
		defer hangUp.mu.Unlock()
		return hangUp.attached
	}, time.Second, 5*time.Millisecond)
	client.write(map[string]string{"type": "unsubscribe", "id": "call-2"})
	require.Eventually(t, func() bool {
		hangUp.mu.Lock()
		defer hangUp.mu.Unlock()
		return hangUp.closed
	}, time.Second, 5*time.Millisecond)
}
