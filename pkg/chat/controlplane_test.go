package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type collectingChatSink struct {
	events []ChatEvent
}

func (s *collectingChatSink) Send(event ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestControlPlaneChatRunnerStreamsSelectedRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/base/api/chat", request.URL.Path)
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		var payload ChatRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Equal(t, "runner-1", payload.RunnerID)
		assert.Empty(t, payload.CWD)
		require.NotNil(t, payload.ClientCapabilities)
		assert.False(t, payload.ClientCapabilities.InteractiveUI)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"text\",\"conversation_id\":\"conversation-1\",\"delta\":\"hello\"}\n"))
	}))
	defer server.Close()

	runner, err := NewControlPlaneChatRunner(server.URL+"/base", "secret", "runner-1")
	require.NoError(t, err)
	sink := &collectingChatSink{}
	conversationID, err := runner.Run(context.Background(), ChatRequest{Message: "hello", CWD: "/local/path"}, sink)

	require.NoError(t, err)
	assert.Equal(t, "conversation-1", conversationID)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "hello", sink.events[1].Delta)
}

type staticControlPlaneUIBroker struct {
	input extensions.UIInputRequest
}

func (b *staticControlPlaneUIBroker) Input(_ context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	b.input = request
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "approved"}, nil
}

func (*staticControlPlaneUIBroker) Confirm(context.Context, extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}, nil
}

func (*staticControlPlaneUIBroker) Select(context.Context, extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "one"}, nil
}

func (*staticControlPlaneUIBroker) Notify(context.Context, extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}, nil
}

func TestControlPlaneChatRunnerRoutesUIResponsesBackToServer(t *testing.T) {
	uiResponse := make(chan extensions.UIInputResponse, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/base/api/chat":
			var payload ChatRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			require.NotNil(t, payload.ClientCapabilities)
			assert.True(t, payload.ClientCapabilities.InteractiveUI)
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
			_, _ = w.Write([]byte("{\"kind\":\"ui-input-request\",\"conversation_id\":\"conversation-1\",\"ui_input\":{\"id\":\"input-1\",\"title\":\"Approve\",\"defaultValue\":\"draft\"}}\n"))
			w.(http.Flusher).Flush()
			select {
			case <-uiResponse:
				_, _ = w.Write([]byte("{\"kind\":\"done\",\"conversation_id\":\"conversation-1\"}\n"))
			case <-request.Context().Done():
			}
		case "/base/api/conversations/conversation-1/ui-input/input-1":
			var response extensions.UIInputResponse
			require.NoError(t, json.NewDecoder(request.Body).Decode(&response))
			uiResponse <- response
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner, err := NewControlPlaneChatRunner(server.URL+"/base", "secret", "runner-1")
	require.NoError(t, err)
	broker := &staticControlPlaneUIBroker{}
	ctx := extensions.ContextWithUIInputBroker(t.Context(), broker)
	sink := &collectingChatSink{}

	conversationID, err := runner.Run(ctx, ChatRequest{Message: "hello"}, sink)

	require.NoError(t, err)
	assert.Equal(t, "conversation-1", conversationID)
	assert.Equal(t, "input-1", broker.input.ID)
	assert.Equal(t, "Approve", broker.input.Title)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "conversation", sink.events[0].Kind)
	assert.Equal(t, "done", sink.events[1].Kind)
}

func TestControlPlaneChatRunnerReturnsStreamAndHTTPError(t *testing.T) {
	t.Run("stream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"kind\":\"error\",\"conversation_id\":\"conversation-1\",\"error\":\"runner is offline\"}\n"))
		}))
		defer server.Close()
		runner, err := NewControlPlaneChatRunner(server.URL, "", "runner-1")
		require.NoError(t, err)

		conversationID, err := runner.Run(t.Context(), ChatRequest{Message: "hello"}, &collectingChatSink{})

		assert.Equal(t, "conversation-1", conversationID)
		require.ErrorContains(t, err, "runner is offline")
	})

	t.Run("http error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"runner is busy"}`))
		}))
		defer server.Close()
		runner, err := NewControlPlaneChatRunner(server.URL, "", "runner-1")
		require.NoError(t, err)

		_, err = runner.Run(t.Context(), ChatRequest{Message: "hello"}, &collectingChatSink{})

		require.ErrorContains(t, err, "runner is busy")
	})
}

func TestControlPlaneChatURLRequiresTLSOffLoopback(t *testing.T) {
	_, err := NewControlPlaneChatRunner("http://example.com", "", "runner-1")
	require.ErrorContains(t, err, "require https")
}
