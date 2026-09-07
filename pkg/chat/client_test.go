package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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

type lifecycleCollectingChatSink struct {
	collectingChatSink
	connected []bool
}

func (s *lifecycleCollectingChatSink) ConversationStreamConnected(active bool) error {
	s.connected = append(s.connected, active)
	return nil
}

type failingControlPlaneChatSink struct {
	err error
}

func (s failingControlPlaneChatSink) Send(ChatEvent) error { return s.err }

func TestControlPlaneWorkspaceDiscoveryEncodesModelProfile(t *testing.T) {
	for _, endpoint := range []string{"slash-commands", "cwd-suggestions"} {
		for _, target := range []WorkspaceTarget{
			{RunnerID: "runner", CWD: "~/project with spaces", EnvironmentProfile: "environment"},
			{RunnerID: "runner", Profile: "default", EnvironmentProfile: "environment"},
			{RunnerID: "runner", Profile: "model/+profile", EnvironmentProfile: "environment"},
			{ConversationID: "saved"},
		} {
			t.Run(endpoint+"/"+target.Profile+target.ConversationID, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, "/base/api/chat/"+endpoint, r.URL.Path)
					assert.Equal(t, "Bearer client-token", r.Header.Get("Authorization"))
					query := r.URL.Query()
					assert.Equal(t, target.Profile, query.Get("profile"))
					assert.Equal(t, target.Profile != "", query.Has("profile"))
					assert.Equal(t, target.RunnerID, query.Get("runnerId"))
					assert.Equal(t, target.CWD, query.Get("cwd"))
					assert.Equal(t, target.EnvironmentProfile, query.Get("environmentProfile"))
					assert.Equal(t, target.ConversationID, query.Get("conversationId"))
					if endpoint == "cwd-suggestions" {
						assert.Equal(t, "../other", query.Get("q"))
						require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceCWDHintsResult{}))
					} else {
						require.NoError(t, json.NewEncoder(w).Encode(protocol.WorkspaceDiscoverResult{}))
					}
				}))
				t.Cleanup(server.Close)
				runner, err := NewClient(server.URL+"/base", "client-token", "")
				require.NoError(t, err)
				if endpoint == "cwd-suggestions" {
					_, err = runner.WorkspaceCWDSuggestions(t.Context(), target, "../other")
				} else {
					_, err = runner.DiscoverWorkspace(t.Context(), target)
				}
				require.NoError(t, err)
				assert.Equal(t, 1, calls)
			})
		}
	}
}

func TestClientStreamsSelectedRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/base/api/chat", request.URL.Path)
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		var payload ChatRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Equal(t, "runner-1", payload.RunnerID)
		assert.Equal(t, "runner-work", payload.EnvironmentProfile)
		assert.Equal(t, "/local/path", payload.CWD)
		require.NotNil(t, payload.ClientCapabilities)
		assert.False(t, payload.ClientCapabilities.InteractiveUI)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"text\",\"conversation_id\":\"conversation-1\",\"delta\":\"hello\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"done\",\"conversation_id\":\"conversation-1\"}\n"))
	}))
	defer server.Close()

	runner, err := NewClient(server.URL+"/base", "secret", "runner-1")
	require.NoError(t, err)
	sink := &collectingChatSink{}
	conversationID, err := runner.Run(context.Background(), ChatRequest{Message: "hello", CWD: "/local/path", EnvironmentProfile: "runner-work"}, sink)

	require.NoError(t, err)
	assert.Equal(t, "conversation-1", conversationID)
	require.Len(t, sink.events, 3)
	assert.Equal(t, "hello", sink.events[1].Delta)
}

func TestClientStreamsWithoutSelectingRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload ChatRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Empty(t, payload.RunnerID)
		assert.Equal(t, "conversation-bound", payload.ConversationID)
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-bound\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"done\",\"conversation_id\":\"conversation-bound\"}\n"))
	}))
	defer server.Close()

	runner, err := NewClient(server.URL, "", "")
	require.NoError(t, err)
	conversationID, err := runner.Run(t.Context(), ChatRequest{Message: "continue", ConversationID: "conversation-bound"}, &collectingChatSink{})

	require.NoError(t, err)
	assert.Equal(t, "conversation-bound", conversationID)
}

func TestClientFollowsConversationAcrossTurns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "/base/api/conversations/conversation-1/stream", request.URL.Path)
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set(ConversationStreamActiveHeader, "true")
		_, _ = w.Write([]byte("\n"))
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"text-delta\",\"conversation_id\":\"conversation-1\",\"delta\":\"first\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"done\",\"conversation_id\":\"conversation-1\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"text-delta\",\"conversation_id\":\"conversation-1\",\"delta\":\"second\"}\n"))
		_, _ = w.Write([]byte("{\"kind\":\"error\",\"conversation_id\":\"conversation-1\",\"error\":\"second turn failed\"}\n"))
	}))
	defer server.Close()

	runner, err := NewClient(server.URL+"/base", "secret", "runner-1")
	require.NoError(t, err)
	sink := &lifecycleCollectingChatSink{}

	err = runner.StreamConversation(t.Context(), "conversation-1", sink)

	require.NoError(t, err)
	assert.Equal(t, []bool{true}, sink.connected)
	require.Len(t, sink.events, 6)
	assert.Equal(t, []string{"conversation", "text-delta", "done", "conversation", "text-delta", "error"}, []string{
		sink.events[0].Kind,
		sink.events[1].Kind,
		sink.events[2].Kind,
		sink.events[3].Kind,
		sink.events[4].Kind,
		sink.events[5].Kind,
	})
	assert.Equal(t, "first", sink.events[1].Delta)
	assert.Equal(t, "second", sink.events[4].Delta)
}

func TestControlPlaneHTTPErrorRetryability(t *testing.T) {
	assert.False(t, (&ControlPlaneHTTPError{StatusCode: http.StatusUnauthorized}).Retryable())
	assert.False(t, (&ControlPlaneHTTPError{StatusCode: http.StatusNotFound}).Retryable())
	assert.True(t, (&ControlPlaneHTTPError{StatusCode: http.StatusRequestTimeout}).Retryable())
	assert.True(t, (&ControlPlaneHTTPError{StatusCode: http.StatusTooManyRequests}).Retryable())
	assert.True(t, (&ControlPlaneHTTPError{StatusCode: http.StatusBadGateway}).Retryable())

	err := controlPlaneResponseError(&http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(`{"error":"access denied"}`)),
	})
	var responseErr *ControlPlaneHTTPError
	require.ErrorAs(t, err, &responseErr)
	assert.Equal(t, http.StatusForbidden, responseErr.StatusCode)
	assert.Equal(t, "access denied", responseErr.Message)
	assert.Equal(t, "server returned HTTP 403: access denied", responseErr.Error())
}

func TestClientHandlesConversationStreamUIWithoutBlockingEvents(t *testing.T) {
	responses := make(chan extensions.UIInputResponse, 1)
	responded := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
			_, _ = w.Write([]byte("{\"kind\":\"ui-confirm-request\",\"conversation_id\":\"conversation-1\",\"ui_confirm\":{\"id\":\"confirm-1\",\"title\":\"Approve\"}}\n"))
			_, _ = w.Write([]byte("{\"kind\":\"done\",\"conversation_id\":\"conversation-1\"}\n"))
			w.(http.Flusher).Flush()
			select {
			case <-responded:
			case <-request.Context().Done():
			}
		case http.MethodPost:
			assert.Equal(t, "/api/conversations/conversation-1/ui-input/confirm-1", request.URL.Path)
			var response extensions.UIInputResponse
			require.NoError(t, json.NewDecoder(request.Body).Decode(&response))
			responses <- response
			_, _ = w.Write([]byte(`{"success":true}`))
			close(responded)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)
	broker := &recordingControlPlaneUIBroker{response: extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}}
	ctx := extensions.ContextWithUIInputBroker(t.Context(), broker)
	sink := &collectingChatSink{}

	err = runner.StreamConversation(ctx, "conversation-1", sink)

	require.NoError(t, err)
	require.Len(t, sink.events, 2)
	assert.Equal(t, "conversation", sink.events[0].Kind)
	assert.Equal(t, "done", sink.events[1].Kind)
	select {
	case response := <-responses:
		assert.Equal(t, extensions.UIInputStatusSubmitted, response.Status)
		assert.True(t, response.Confirmed)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conversation stream ui response")
	}
}

type blockingControlPlaneUIBroker struct {
	recordingControlPlaneUIBroker
	started chan struct{}
	stopped chan struct{}
}

func (b *blockingControlPlaneUIBroker) Input(ctx context.Context, _ extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	close(b.started)
	<-ctx.Done()
	close(b.stopped)
	return extensions.UIInputResponse{}, ctx.Err()
}

func TestClientDismissesPromptWithoutStoppingExecution(t *testing.T) {
	for _, endEvent := range []bool{false, true} {
		t.Run(fmt.Sprint("end-event=", endEvent), func(t *testing.T) {
			broker := &blockingControlPlaneUIBroker{started: make(chan struct{}), stopped: make(chan struct{})}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				assert.Equal(t, http.MethodGet, request.Method, "detach or dismissal must not send stop or stale UI replies")
				assert.NotEmpty(t, request.Header.Get(ClientIDHeader))
				assert.Contains(t, request.Header.Get(UICapabilitiesHeader), "interactive")
				_, _ = w.Write([]byte("{\"kind\":\"ui-input-request\",\"ui_input\":{\"id\":\"prompt-1\",\"title\":\"Pending\"}}\n"))
				w.(http.Flusher).Flush()
				select {
				case <-broker.started:
				case <-request.Context().Done():
					return
				}
				_, _ = w.Write([]byte("{\"kind\":\"text-delta\",\"delta\":\"Still running\"}\n"))
				if endEvent {
					_, _ = w.Write([]byte("{\"kind\":\"ui-request-end\",\"ui_request_id\":\"prompt-1\"}\n"))
					w.(http.Flusher).Flush()
					select {
					case <-broker.stopped:
					case <-request.Context().Done():
					}
				}
			}))
			defer server.Close()
			runner, err := NewClient(server.URL, "", "")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			ctx = extensions.ContextWithUIInputBroker(ctx, broker)
			sink := &collectingChatSink{}
			require.NoError(t, runner.StreamConversation(ctx, "conversation-1", sink))
			select {
			case <-broker.stopped:
			default:
				t.Fatal("stream did not cancel its local prompt")
			}
			require.Len(t, sink.events, 1)
			assert.Equal(t, "Still running", sink.events[0].Delta)
		})
	}
}

func TestClientRejectsMismatchedConversationStreamEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-other\"}\n"))
	}))
	defer server.Close()

	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)

	err = runner.StreamConversation(t.Context(), "conversation-1", &collectingChatSink{})

	require.ErrorContains(t, err, "streamed conversation conversation-other while watching conversation-1")
	var protocolErr *ControlPlaneStreamProtocolError
	require.ErrorAs(t, err, &protocolErr)
	assert.False(t, protocolErr.Retryable())
}

func TestClientClassifiesOversizedStreamEvents(t *testing.T) {
	runner := &Client{}
	_, err := runner.consumeChatStream(
		t.Context(),
		strings.NewReader(strings.Repeat("x", maxControlPlaneChatEventSize+1)),
		"conversation-1",
		&collectingChatSink{},
		false,
		true,
		"conversation-1",
	)

	var protocolErr *ControlPlaneStreamProtocolError
	require.ErrorAs(t, err, &protocolErr)
	require.ErrorIs(t, err, bufio.ErrTooLong)
	assert.False(t, protocolErr.Retryable())
}

func TestClientListsAndLoadsRunnerConversations(t *testing.T) {
	updatedAt := time.Date(2026, time.August, 9, 12, 35, 0, 0, time.UTC)
	structuredResult := tooltypes.StructuredToolResult{ToolName: "bash", Success: true, Timestamp: updatedAt}
	listedRunnerIDs := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/api/conversations":
			assert.Equal(t, "200", request.URL.Query().Get("limit"))
			assert.Equal(t, "updated", request.URL.Query().Get("sortBy"))
			assert.Equal(t, "desc", request.URL.Query().Get("sortOrder"))
			listedRunnerIDs <- request.URL.Query().Get("runnerId")
			require.NoError(t, json.NewEncoder(w).Encode(conversations.ListConversationsResponse{
				Conversations: []convtypes.ConversationSummary{
					{ID: "conversation-bound", Summary: "Bound conversation", UpdatedAt: updatedAt, Metadata: map[string]any{RunnerIDMetadataKey: "runner-1"}},
					{ID: "conversation-other", Summary: "Other runner", UpdatedAt: updatedAt.Add(-time.Minute), Metadata: map[string]any{RunnerIDMetadataKey: "runner-2"}},
					{ID: "conversation-local", Summary: "Local", UpdatedAt: updatedAt.Add(-2 * time.Minute)},
				},
			}))
		case "/api/conversations/conversation-bound":
			assert.Equal(t, "stream", request.URL.Query().Get("format"))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":                 "conversation-bound",
				"updatedAt":          updatedAt,
				"provider":           "OpenAI",
				"cwd":                "/Users/jingkaihe/Workspace/kodelet",
				"profile":            "deep",
				"reasoningEffort":    "max",
				"runnerId":           "runner-1",
				"environmentProfile": "workspace",
				"summary":            "Bound conversation",
				"usage":              map[string]any{"currentContextWindow": 42, "maxContextWindow": 100},
				"entries": []conversations.StreamableMessage{
					{Kind: "text", Role: "user", Content: "how many cores?"},
					{Kind: "thinking", Role: "assistant", Content: "Inspecting the host"},
					{Kind: "tool-use", Role: "assistant", ToolCallID: "call-1", ToolName: "bash", Input: `{"command":"sysctl -n hw.ncpu"}`},
					{Kind: "tool-result", Role: "assistant", ToolCallID: "call-1", ToolName: "bash", Content: "legacy result"},
					{Kind: "text", Role: "assistant", Content: "Eight cores."},
				},
				"messages": []map[string]any{
					{"role": "user", "content": "how many cores?"},
					{
						"role":          "assistant",
						"content":       "Eight cores.",
						"thinkingTexts": []string{"Inspecting the host"},
						"toolCalls": []map[string]any{{
							"id":       "call-1",
							"function": map[string]any{"name": "bash", "arguments": `{"command":"sysctl -n hw.ncpu"}`},
						}},
					},
				},
				"toolResults": map[string]tooltypes.StructuredToolResult{"call-1": structuredResult},
			}))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner, err := NewClient(server.URL, "secret", "runner-1")
	require.NoError(t, err)
	summaries, err := runner.ListConversations(t.Context(), 200)
	require.NoError(t, err)
	assert.Equal(t, "runner-1", <-listedRunnerIDs)
	require.Len(t, summaries, 1)
	assert.Equal(t, "conversation-bound", summaries[0].ID)
	serverOnlyRunner, err := NewClient(server.URL, "secret", "")
	require.NoError(t, err)
	allSummaries, err := serverOnlyRunner.ListConversations(t.Context(), 200)
	require.NoError(t, err)
	assert.Empty(t, <-listedRunnerIDs)
	assert.Len(t, allSummaries, 3)

	history, err := runner.LoadConversation(t.Context(), "conversation-bound")
	require.NoError(t, err)
	assert.Equal(t, "/Users/jingkaihe/Workspace/kodelet", history.CWD)
	assert.Equal(t, "deep", history.Profile)
	assert.Equal(t, "max", history.ReasoningEffort)
	assert.Equal(t, "runner-1", history.RunnerID)
	assert.Equal(t, "workspace", history.EnvironmentProfile)
	assert.Equal(t, 42, history.Usage.CurrentContextWindow)
	require.Len(t, history.Messages, 5)
	assert.Equal(t, "text", history.Messages[0].Kind)
	assert.Equal(t, "thinking", history.Messages[1].Kind)
	assert.Equal(t, "tool-use", history.Messages[2].Kind)
	assert.Equal(t, "tool-result", history.Messages[3].Kind)
	assert.JSONEq(t, string(mustStructuredToolResultJSON(t, structuredResult)), history.Messages[3].Content)
	assert.Equal(t, "legacy result", history.Messages[3].ToolOutput)
	assert.Equal(t, "text", history.Messages[4].Kind)
}

func mustStructuredToolResultJSON(t *testing.T, result tooltypes.StructuredToolResult) []byte {
	t.Helper()
	payload, err := result.MarshalJSON()
	require.NoError(t, err)
	return payload
}

func TestClientSettingsSteeringAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/base/api/chat/settings":
			assert.Equal(t, "work", request.URL.Query().Get("profile"))
			require.NoError(t, json.NewEncoder(w).Encode(ControlPlaneChatSettings{
				CurrentProfile:         "work",
				Profiles:               []ControlPlaneProfileOption{{Name: "default"}, {Name: "work"}},
				ReasoningEffort:        "high",
				ReasoningEffortOptions: []string{"low", "high"},
			}))
		case "/base/api/conversations/conversation-1/steer":
			var payload struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			assert.Equal(t, "focus", payload.Message)
			_, _ = w.Write([]byte(`{"success":true,"conversation_id":"conversation-1","queued":true}`))
		case "/base/api/conversations/conversation-1/stop":
			assert.Equal(t, "turn-1", request.URL.Query().Get("turnId"))
			_, _ = w.Write([]byte(`{"stopped":true}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runner, err := NewClient(server.URL+"/base", "secret", "runner-1")
	require.NoError(t, err)
	settings, err := runner.ChatSettings(t.Context(), "work")
	require.NoError(t, err)
	assert.Equal(t, "work", settings.CurrentProfile)
	assert.Equal(t, []string{"low", "high"}, settings.ReasoningEffortOptions)
	queued, err := runner.SteerConversation(t.Context(), "conversation-1", "focus", nil)
	require.NoError(t, err)
	assert.True(t, queued)
	require.NoError(t, runner.StopConversationTurn(t.Context(), "conversation-1", "turn-1"))
}

func TestClientTreatsStaleScopedStopAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		turnID := request.URL.Query().Get("turnId")
		assert.True(t, turnID == "" || turnID == "turn-old")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]bool{"stopped": false}))
	}))
	defer server.Close()
	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)

	require.NoError(t, runner.StopConversationTurn(t.Context(), "conversation-1", "turn-old"))
	require.ErrorContains(t, runner.StopConversation(t.Context(), "conversation-1"), "not active")
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

type recordingControlPlaneUIBroker struct {
	input        extensions.UIInputRequest
	confirmation extensions.UIConfirmRequest
	selection    extensions.UISelectRequest
	notification extensions.UINotifyRequest
	response     extensions.UIInputResponse
	err          error
}

func (b *recordingControlPlaneUIBroker) Input(_ context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	b.input = request
	return b.response, b.err
}

func (b *recordingControlPlaneUIBroker) Confirm(_ context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	b.confirmation = request
	return b.response, b.err
}

func (b *recordingControlPlaneUIBroker) Select(_ context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	b.selection = request
	return b.response, b.err
}

func (b *recordingControlPlaneUIBroker) Notify(_ context.Context, request extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	b.notification = request
	return b.response, b.err
}

func TestClientRoutesUIResponsesBackToServer(t *testing.T) {
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

	runner, err := NewClient(server.URL+"/base", "secret", "runner-1")
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

func TestClientDoesNotRespondToUIAfterContextCancellation(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)
	broker := &recordingControlPlaneUIBroker{response: extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = extensions.ContextWithUIInputBroker(ctx, broker)
	cancel()

	handled, err := runner.handleUIEvent(ctx, "conversation-1", ChatEvent{
		Kind:    "ui-input-request",
		UIInput: &UIInputEvent{ID: "input-1", Title: "Question"},
	})

	assert.True(t, handled)
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-requests:
		t.Fatal("cancelled UI context submitted a response")
	default:
	}
}

func TestClientReturnsStreamAndHTTPError(t *testing.T) {
	t.Run("stream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"kind\":\"error\",\"conversation_id\":\"conversation-1\",\"error\":\"runner is offline\"}\n"))
		}))
		defer server.Close()
		runner, err := NewClient(server.URL, "", "runner-1")
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
		runner, err := NewClient(server.URL, "", "runner-1")
		require.NoError(t, err)

		_, err = runner.Run(t.Context(), ChatRequest{Message: "hello"}, &collectingChatSink{})

		require.ErrorContains(t, err, "runner is busy")
	})

	t.Run("incomplete stream", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"kind\":\"conversation\",\"conversation_id\":\"conversation-1\"}\n"))
		}))
		defer server.Close()
		runner, err := NewClient(server.URL, "", "runner-1")
		require.NoError(t, err)

		conversationID, err := runner.Run(t.Context(), ChatRequest{Message: "hello"}, &collectingChatSink{})

		assert.Equal(t, "conversation-1", conversationID)
		require.ErrorContains(t, err, "ended before the response was complete")
	})
}

func TestClientHandlesInteractiveEventVariants(t *testing.T) {
	responses := make(chan struct {
		path     string
		response extensions.UIInputResponse
	}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var response extensions.UIInputResponse
		require.NoError(t, json.NewDecoder(request.Body).Decode(&response))
		responses <- struct {
			path     string
			response extensions.UIInputResponse
		}{path: request.URL.Path, response: response}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)
	broker := &recordingControlPlaneUIBroker{response: extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: "one", Confirmed: true}}
	ctx := extensions.ContextWithUIInputBroker(t.Context(), broker)

	tests := []struct {
		name      string
		event     ChatEvent
		requestID string
	}{
		{name: "input", event: ChatEvent{Kind: "ui-input", UIInput: &UIInputEvent{ID: "input-1", Title: "Input", HelpText: "help", Message: "message", Placeholder: "value", DefaultValue: "draft", SubmitButtonText: "Send", CancelButtonText: "Cancel", Required: true, Secret: true}}, requestID: "input-1"},
		{name: "confirm", event: ChatEvent{Kind: "ui-confirm-request", UIConfirm: &UIConfirmEvent{ID: "confirm-1", Title: "Confirm", Message: "continue?", ConfirmButtonText: "Yes", CancelButtonText: "No"}}, requestID: "confirm-1"},
		{name: "select", event: ChatEvent{Kind: "ui-select", UISelect: &UISelectEvent{ID: "select-1", Title: "Select", Message: "choose", Options: []string{"one", "two"}, SubmitButtonText: "Pick", CancelButtonText: "Cancel"}}, requestID: "select-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handled, handleErr := runner.handleUIEvent(ctx, "conversation-1", test.event)
			require.NoError(t, handleErr)
			assert.True(t, handled)
			posted := <-responses
			assert.Equal(t, "/api/conversations/conversation-1/ui-input/"+test.requestID, posted.path)
			assert.Equal(t, extensions.UIInputStatusSubmitted, posted.response.Status)
		})
	}
	assert.Equal(t, "Input", broker.input.Title)
	assert.True(t, broker.input.Secret)
	assert.Equal(t, "Confirm", broker.confirmation.Title)
	assert.Equal(t, []string{"one", "two"}, broker.selection.Options)

	handled, err := runner.handleUIEvent(ctx, "conversation-1", ChatEvent{Kind: "ui-notification", UINotify: &UINotifyEvent{Title: "Done", Message: "finished"}})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, "Done", broker.notification.Title)
	handled, err = runner.handleUIEvent(ctx, "conversation-1", ChatEvent{Kind: "text"})
	require.NoError(t, err)
	assert.False(t, handled)
}

func TestClientUIFallbacksAndValidation(t *testing.T) {
	var unavailable extensions.UIInputResponse
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.NoError(t, json.NewDecoder(request.Body).Decode(&unavailable))
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)

	handled, err := runner.handleUIEvent(t.Context(), "conversation-1", ChatEvent{Kind: "ui-input-request", UIInput: &UIInputEvent{ID: "input-1"}})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, extensions.UIInputStatusUnavailable, unavailable.Status)
	assert.Contains(t, unavailable.Reason, "not available")

	for _, event := range []ChatEvent{{Kind: "ui-input"}, {Kind: "ui-confirm"}, {Kind: "ui-select"}, {Kind: "ui-notify"}} {
		handled, err = runner.handleUIEvent(t.Context(), "conversation-1", event)
		assert.True(t, handled)
		require.Error(t, err)
	}
	handled, err = runner.handleUIEvent(t.Context(), "conversation-1", ChatEvent{Kind: "ui-notification", UINotify: &UINotifyEvent{Message: "visible event"}})
	require.NoError(t, err)
	assert.False(t, handled)
	require.ErrorContains(t, runner.respondToUIInput(t.Context(), "", "input", extensions.UIInputResponse{}), "missing conversation or request id")

	brokerErr := errors.New("terminal unavailable")
	broker := &recordingControlPlaneUIBroker{err: brokerErr}
	ctx := extensions.ContextWithUIInputBroker(t.Context(), broker)
	_, err = runner.handleUIEvent(ctx, "conversation-1", ChatEvent{Kind: "ui-confirm", UIConfirm: &UIConfirmEvent{ID: "confirm-1"}})
	require.ErrorIs(t, err, brokerErr)
}

func TestClientValidationAndMalformedResponses(t *testing.T) {
	serverOnly, err := NewClient("https://example.com", "", " ")
	require.NoError(t, err)
	assert.Empty(t, serverOnly.runnerID)
	var nilRunner *Client
	_, err = nilRunner.Run(t.Context(), ChatRequest{}, &collectingChatSink{})
	require.ErrorContains(t, err, "not initialized")
	_, err = nilRunner.ListConversations(t.Context(), 10)
	require.ErrorContains(t, err, "not initialized")
	_, err = nilRunner.LoadConversation(t.Context(), "conversation")
	require.ErrorContains(t, err, "not initialized")
	require.ErrorContains(t, nilRunner.StreamConversation(t.Context(), "conversation", &collectingChatSink{}), "not initialized")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/chat":
			_, _ = w.Write([]byte("\nnot-json\n"))
		case "/api/chat/settings", "/api/conversations/conversation/steer":
			_, _ = w.Write([]byte("not-json"))
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()
	runner, err := NewClient(server.URL, "", "runner-1")
	require.NoError(t, err)
	_, err = runner.Run(t.Context(), ChatRequest{Message: "hello"}, nil)
	require.ErrorContains(t, err, "chat event sink is required")
	require.ErrorContains(t, runner.StreamConversation(t.Context(), " ", &collectingChatSink{}), "conversation ID is required")
	require.ErrorContains(t, runner.StreamConversation(t.Context(), "conversation", nil), "chat event sink is required")
	_, err = runner.Run(t.Context(), ChatRequest{Message: "hello"}, &collectingChatSink{})
	require.ErrorContains(t, err, "failed to decode chat event")
	var protocolErr *ControlPlaneStreamProtocolError
	require.ErrorAs(t, err, &protocolErr)
	_, err = runner.ChatSettings(t.Context(), "")
	require.ErrorContains(t, err, "failed to decode chat settings")
	_, err = runner.SteerConversation(t.Context(), "conversation", "message", nil)
	require.ErrorContains(t, err, "failed to decode steering response")
	require.ErrorContains(t, runner.StopConversation(t.Context(), " "), "conversation ID is required")
	_, err = runner.SteerConversation(t.Context(), " ", "message", nil)
	require.ErrorContains(t, err, "conversation ID is required")
	_, err = runner.SteerConversation(t.Context(), "conversation", " ", nil)
	require.ErrorContains(t, err, "steering message is required")

	sinkErr := errors.New("sink closed")
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kind":"text","content":"hello"}` + "\n"))
	}))
	defer streamServer.Close()
	runner, err = NewClient(streamServer.URL, "", "runner-1")
	require.NoError(t, err)
	_, err = runner.Run(t.Context(), ChatRequest{Message: "hello"}, failingControlPlaneChatSink{err: sinkErr})
	require.ErrorIs(t, err, sinkErr)

	invalidSteeringServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer invalidSteeringServer.Close()
	runner, err = NewClient(invalidSteeringServer.URL, "", "runner-1")
	require.NoError(t, err)
	_, err = runner.SteerConversation(t.Context(), "conversation", "message", nil)
	require.ErrorContains(t, err, "invalid steering response")

	assert.Equal(t, "first", firstNonEmptyString(" ", "first", "second"))
	assert.Empty(t, firstNonEmptyString(" ", "\t"))
	assert.False(t, strings.Contains(controlPlaneResponseError(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody}).Error(), "not-json"))
}

func TestControlPlaneChatURLRequiresTLSOffLoopback(t *testing.T) {
	_, err := NewClient("http://example.com", "", "runner-1")
	require.ErrorContains(t, err, "require https")
}
