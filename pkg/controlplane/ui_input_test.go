package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebUIInputOwnershipFencesObserversAndDismissedRequests(t *testing.T) {
	original := &recordingChatSink{}
	broker := newWebUIInputBroker("conversation", original)
	t.Cleanup(broker.close)
	detach := broker.setOwner(t.Context(), "original", original)
	t.Cleanup(detach)
	start := func(ctx context.Context) <-chan extensions.UIInputResponse {
		response := make(chan extensions.UIInputResponse, 1)
		go func() {
			result, _ := broker.Input(ctx, extensions.UIInputRequest{ID: "reused-extension-id"})
			response <- result
		}()
		return response
	}
	promptCtx, cancelPrompt := context.WithCancel(t.Context())
	defer cancelPrompt()
	first := start(promptCtx)
	require.Eventually(t, func() bool { return len(original.Events()) > 0 }, time.Second, time.Millisecond)
	firstID := original.Events()[0].UIInput.ID
	assert.NotEqual(t, "reused-extension-id", firstID)
	assert.False(t, broker.respondOwned("viewer", firstID, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}))
	cancelPrompt()
	select {
	case response := <-first:
		assert.Equal(t, extensions.UIInputStatusDismissed, response.Status)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not dismiss the pending prompt")
	}
	require.Len(t, original.Events(), 2)
	assert.Equal(t, "ui-request-end", original.Events()[1].Kind)
	assert.Equal(t, firstID, original.Events()[1].UIRequestID)
	second := start(t.Context())
	require.Eventually(t, func() bool { return len(original.Events()) == 3 }, time.Second, time.Millisecond)
	secondID := original.Events()[2].UIInput.ID
	assert.NotEqual(t, firstID, secondID)
	answer := extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "new"}
	assert.False(t, broker.respondOwned("viewer", secondID, answer))
	assert.False(t, broker.respondOwned("original", firstID, answer))
	require.True(t, broker.respondOwned("original", secondID, answer))
	assert.False(t, broker.respondOwned("original", secondID, answer), "a response capability is single-use")
	assert.Equal(t, answer, <-second)
	third := start(t.Context())
	require.Eventually(t, func() bool { return len(original.Events()) == 5 }, time.Second, time.Millisecond)
	thirdID := original.Events()[4].UIInput.ID
	detach()
	select {
	case response := <-third:
		assert.Equal(t, extensions.UIInputStatusDismissed, response.Status)
	case <-time.After(time.Second):
		t.Fatal("disconnect did not dismiss the pending prompt")
	}
	assert.False(t, broker.respondOwned("original", thirdID, answer))
	response, err := broker.Input(t.Context(), extensions.UIInputRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, response.Status)
	assert.NoError(t, t.Context().Err(), "UI detach must not cancel execution")
}

func TestChatObserverAttachmentDoesNotChangeUIOwnership(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conversation", sink)
	t.Cleanup(broker.close)
	detach := broker.setOwner(t.Context(), "original", sink)
	t.Cleanup(detach)
	server := &Server{activeChats: map[string]*activeChatRun{"conversation": {uiInput: broker}}, chatSubscribers: make(map[string]map[*subscriberEventSink]struct{})}
	subscriber := newSubscriberEventSink()
	t.Cleanup(subscriber.Close)
	_, registered := server.registerChatSubscriber("conversation", subscriber)
	require.True(t, registered)
	assert.Equal(t, "original", broker.owner.clientID, "attaching an observer must not take ownership")
	detach()
	assert.Nil(t, broker.owner, "owner disconnect must not select an attached observer")
	server.removeChatSubscriber("conversation", subscriber)
	_, registered = server.registerChatSubscriber("conversation", subscriber)
	require.True(t, registered)
	assert.Nil(t, broker.owner, "reconnecting must not claim an unattended execution")
	server.removeChatSubscriber("conversation", subscriber)
}

func TestChatHTTPPromptIsDeliveredOnlyToInitiatingClient(t *testing.T) {
	observer := newSubscriberEventSink()
	t.Cleanup(observer.Close)
	server := &Server{
		conversationService: &mockConversationService{},
		runCtx:              t.Context(),
		activeChats:         make(map[string]*activeChatRun),
		chatSubscribers:     map[string]map[*subscriberEventSink]struct{}{"conversation": {observer: {}}},
	}
	server.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, request ChatRequest, sink ChatEventSink) (string, error) {
		broker := server.uiInputBrokerForRun(request.ConversationID)
		response, err := broker.Input(ctx, extensions.UIInputRequest{ID: "extension-id", Title: "Only the owner"})
		assert.NoError(t, err)
		assert.Equal(t, "owner-answer", response.Value)
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", ConversationID: request.ConversationID, Delta: "Visible to observers"}))
		return request.ConversationID, nil
	}}
	router := mux.NewRouter()
	router.HandleFunc("/api/chat", server.handleChat).Methods(http.MethodPost)
	router.HandleFunc("/api/conversations/{id}/ui-input/{requestId}", server.handleRespondUIInput).Methods(http.MethodPost)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", strings.NewReader(`{"message":"hello","conversationId":"conversation","clientCapabilities":{"interactiveUI":true}}`))
	require.NoError(t, err)
	request.Header.Set(chat.ClientIDHeader, "initiator")
	stream, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer stream.Body.Close()
	require.Equal(t, http.StatusOK, stream.StatusCode)
	decoder := json.NewDecoder(stream.Body)
	var prompt chat.ChatEvent
	require.NoError(t, decoder.Decode(&prompt))
	require.NotNil(t, prompt.UIInput)
	for _, clientID := range []string{"observer", "initiator"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/conversations/conversation/ui-input/"+prompt.UIInput.ID, strings.NewReader(`{"status":"submitted","value":"owner-answer"}`))
		require.NoError(t, err)
		request.Header.Set(chat.ClientIDHeader, clientID)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		if clientID == "observer" {
			assert.Equal(t, http.StatusNotFound, response.StatusCode)
		} else {
			assert.Equal(t, http.StatusOK, response.StatusCode)
		}
	}
	var event chat.ChatEvent
	for {
		require.NoError(t, decoder.Decode(&event))
		if event.Kind == "done" {
			break
		}
	}
	var observerKinds []string
	for range 4 {
		select {
		case event := <-observer.ch:
			observerKinds = append(observerKinds, event.Kind)
		case <-ctx.Done():
			t.Fatal("observer did not receive normal model events")
		}
	}
	assert.Equal(t, []string{"conversation", "user-message", "text-delta", "done"}, observerKinds)
}

func TestWebUIInputBrokerSendsEventAndWaitsForResponse(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)

	resultCh := make(chan extensions.UIInputResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Input(context.Background(), extensions.UIInputRequest{
			ID:               "input-1",
			Title:            "Choose",
			HelpText:         "1. A\n2. B",
			SubmitButtonText: "Select",
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	require.Eventually(t, func() bool { return len(sink.Events()) == 1 }, time.Second, 10*time.Millisecond)
	events := sink.Events()
	assert.Equal(t, "ui-input-request", events[0].Kind)
	require.NotNil(t, events[0].UIInput)
	assert.Equal(t, "input-1", events[0].UIInput.ID)
	assert.Equal(t, "Choose", events[0].UIInput.Title)
	assert.Equal(t, "Select", events[0].UIInput.SubmitButtonText)

	assert.True(t, broker.Respond("input-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "2"}))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result := <-resultCh:
		assert.Equal(t, extensions.UIInputStatusSubmitted, result.Status)
		assert.Equal(t, "2", result.Value)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ui input response")
	}
}

func TestWebUIInputBrokerSendsSeparateConfirmSelectAndNotifyEvents(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)

	confirmCh := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Confirm(context.Background(), extensions.UIConfirmRequest{
			ID:                "confirm-1",
			Title:             "Allow bash?",
			Message:           "A tool call incoming",
			ConfirmButtonText: "Allow",
		})
		confirmCh <- result
	}()
	require.Eventually(t, func() bool { return len(sink.Events()) == 1 }, time.Second, 10*time.Millisecond)
	events := sink.Events()
	assert.Equal(t, "ui-confirm-request", events[0].Kind)
	require.NotNil(t, events[0].UIConfirm)
	assert.Equal(t, "confirm-1", events[0].UIConfirm.ID)
	assert.Equal(t, "Allow", events[0].UIConfirm.ConfirmButtonText)
	assert.True(t, broker.Respond("confirm-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}))
	assert.True(t, (<-confirmCh).Confirmed)

	selectCh := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Select(context.Background(), extensions.UISelectRequest{
			ID:      "select-1",
			Title:   "Pick food",
			Options: []string{"Pasta", "Pizza"},
		})
		selectCh <- result
	}()
	require.Eventually(t, func() bool { return len(sink.Events()) == 2 }, time.Second, 10*time.Millisecond)
	events = sink.Events()
	assert.Equal(t, "ui-select-request", events[1].Kind)
	require.NotNil(t, events[1].UISelect)
	assert.Equal(t, []string{"Pasta", "Pizza"}, events[1].UISelect.Options)
	assert.True(t, broker.Respond("select-1", extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "Pizza"}))
	assert.Equal(t, "Pizza", (<-selectCh).Value)

	result, err := broker.Notify(context.Background(), extensions.UINotifyRequest{Message: "Done"})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, result.Status)
	events = sink.Events()
	require.Len(t, events, 3)
	assert.Equal(t, "ui-notification", events[2].Kind)
	require.NotNil(t, events[2].UINotify)
	assert.Equal(t, "Done", events[2].UINotify.Message)
}

func TestWebUIInputBrokerDuplicateIDKeepsNewestPromptRegistered(t *testing.T) {
	sink := &recordingChatSink{}
	broker := newWebUIInputBroker("conv-123", sink)

	firstResult := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Input(context.Background(), extensions.UIInputRequest{ID: "shared", Title: "First"})
		firstResult <- result
	}()
	require.Eventually(t, func() bool { return len(sink.Events()) == 1 }, time.Second, 10*time.Millisecond)

	secondResult := make(chan extensions.UIInputResponse, 1)
	go func() {
		result, _ := broker.Input(context.Background(), extensions.UIInputRequest{ID: "shared", Title: "Second"})
		secondResult <- result
	}()
	require.Eventually(t, func() bool { return len(sink.Events()) == 2 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, extensions.UIInputStatusDismissed, (<-firstResult).Status)

	response := extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "newest"}
	require.True(t, broker.Respond("shared", response))
	assert.Equal(t, response, <-secondResult)
}
