package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRealtimeUsesConversationOwnerAndCapabilities(t *testing.T) {
	var owner atomic.Value
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer client-token", r.Header.Get("Authorization"))
		assert.NotEmpty(t, r.Header.Get(ClientIDHeader))
		if r.URL.Path == "/prefix/api/conversations/saved/stream" {
			owner.Store(r.Header.Get(ClientIDHeader))
			assert.Equal(t, "interactive", r.Header.Get(UICapabilitiesHeader))
			return
		}
		assert.Equal(t, owner.Load(), r.Header.Get(ClientIDHeader))
		switch {
		case r.URL.Path == "/prefix/api/realtime" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"enabled":true,"available":true,"defaultVoice":"marin","voices":["marin","cedar"]}`)
		case r.URL.Path == "/prefix/api/realtime" && r.Method == http.MethodPost:
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var request struct {
				Offer string      `json:"offer"`
				Chat  ChatRequest `json:"chat"`
				Voice string      `json:"voice"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
				return
			}
			assert.Equal(t, "v=0\r\n", request.Offer)
			assert.Equal(t, "cedar", request.Voice)
			assert.Equal(t, "saved", request.Chat.ConversationID)
			assert.Equal(t, "runner", request.Chat.RunnerID)
			assert.Equal(t, "/workspace", request.Chat.CWD)
			assert.Equal(t, "coding", request.Chat.Profile)
			assert.Equal(t, &ChatClientCapabilities{InteractiveUI: true}, request.Chat.ClientCapabilities)
			assert.Equal(t, &llmtypes.ExecutionOptions{NoTools: new(true)}, request.Chat.Options)
			_, _ = io.WriteString(w, `{"id":"call/with?name","conversationId":"saved","answer":"answer"}`)
		case r.Method == http.MethodGet:
			assert.Equal(t, "/prefix/api/realtime/call%2Fwith%3Fname/events", r.URL.EscapedPath())
			assert.Equal(t, "application/x-ndjson", r.Header.Get("Accept"))
			_, _ = io.WriteString(w, `

{"kind":"transcript","id":"utterance","role":"user","text":"café","final":true,"conversationId":"saved","anchor":0}
{"kind":"agent-started","conversationId":"saved","turnId":"voice-turn"}
{"kind":"warning","text":"warning"}
{"kind":"error","text":"interrupted"}
{"kind":"closed","conversationId":"saved"}`)
		case r.Method == http.MethodDelete:
			assert.Equal(t, "/prefix/api/realtime/call%2Fwith%3Fname", r.URL.EscapedPath())
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/prefix", "client-token", "runner", WithHTTPClient(server.Client()))
	require.NoError(t, err)
	ctx := extensions.ContextWithUIInputBroker(t.Context(), &staticControlPlaneUIBroker{})
	require.NoError(t, client.StreamConversation(ctx, "saved", &collectingChatSink{}))
	capabilities, err := client.RealtimeCapabilities(ctx)
	require.NoError(t, err)
	assert.Equal(t, RealtimeCapabilities{Enabled: true, Available: true, DefaultVoice: "marin", Voices: []string{"marin", "cedar"}}, capabilities)
	request := ChatRequest{
		ConversationID: " saved ", RunnerID: "other", CWD: "/workspace", Profile: "coding",
		Options:            &llmtypes.ExecutionOptions{NoTools: new(true)},
		ClientCapabilities: &ChatClientCapabilities{PersistentSurfaces: true},
	}
	session, err := client.StartRealtime(ctx, "v=0\r\n", request, " cedar ")
	require.NoError(t, err)
	assert.Equal(t, RealtimeSession{ID: "call/with?name", ConversationID: "saved", Answer: "answer"}, session)
	assert.Equal(t, &ChatClientCapabilities{PersistentSurfaces: true}, request.ClientCapabilities, "do not mutate caller input")
	var events []RealtimeEvent
	require.NoError(t, client.StreamRealtime(ctx, session.ID, func(event RealtimeEvent) { events = append(events, event) }))
	require.Len(t, events, 5)
	assert.Equal(t, RealtimeEvent{Kind: "transcript", ID: "utterance", Role: "user", Text: "café", Final: true, ConversationID: "saved", Anchor: new(0)}, events[0])
	assert.Equal(t, "voice-turn", events[1].TurnID)
	assert.Nil(t, events[1].Anchor)
	require.NoError(t, client.EndRealtime(ctx, session.ID))
	assert.EqualValues(t, 5, calls.Load())
}

func TestClientStartRealtimeErrorsNeverRetry(t *testing.T) {
	for _, test := range []struct {
		name, body, wantError string
		status                int
	}{
		{"unavailable", `{"error":"voice unavailable"}`, "voice unavailable", http.StatusServiceUnavailable},
		{"redirect", ``, "HTTP 307", http.StatusTemporaryRedirect},
		{"invalid JSON", `{`, "invalid realtime response", http.StatusOK},
		{"multiple responses", `{} {}`, "invalid realtime response", http.StatusOK},
		{"oversized response", strings.Repeat(" ", maxRealtimeResponseBytes+1), "size limit", http.StatusOK},
		{"missing session", `null`, "invalid realtime session", http.StatusOK},
		{"missing call ID", `{"conversationId":"saved","answer":"answer"}`, "invalid realtime session", http.StatusOK},
		{"missing answer", `{"id":"call","conversationId":"saved"}`, "invalid realtime session", http.StatusOK},
		{"wrong conversation", `{"id":"call","conversationId":"other","answer":"answer"}`, "invalid realtime session", http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, "", "")
			require.NoError(t, err)
			session, err := client.StartRealtime(t.Context(), "offer", ChatRequest{ConversationID: "saved"}, "")
			assert.ErrorContains(t, err, test.wantError)
			assert.EqualValues(t, 1, calls.Load())
			if test.name == "wrong conversation" {
				assert.Equal(t, "call", session.ID, "retain the call ID for cleanup")
			}
			if test.status != http.StatusOK {
				var responseErr *ControlPlaneHTTPError
				require.ErrorAs(t, err, &responseErr)
				assert.Equal(t, test.status, responseErr.StatusCode)
			}
		})
	}
}

func TestClientRealtimeOwnerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"owner required"}`)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "", "")
	require.NoError(t, err)
	_, capabilitiesErr := client.RealtimeCapabilities(t.Context())
	streamErr := client.StreamRealtime(t.Context(), "call", func(RealtimeEvent) { t.Error("unexpected event") })
	endErr := client.EndRealtime(t.Context(), "call")
	for _, err := range []error{capabilitiesErr, streamErr, endErr} {
		var responseErr *ControlPlaneHTTPError
		require.ErrorAs(t, err, &responseErr)
		assert.Equal(t, http.StatusForbidden, responseErr.StatusCode)
		assert.Equal(t, "owner required", responseErr.Message)
	}
}

func TestClientStreamRealtimeRejectsBrokenStreams(t *testing.T) {
	for _, body := range []string{"{\n", "null\n", "{}\n", strings.Repeat("x", maxRealtimeResponseBytes+1), `{"kind":"state"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		client, err := NewClient(server.URL, "", "")
		require.NoError(t, err)
		err = client.StreamRealtime(t.Context(), "call", func(RealtimeEvent) {})
		if body == `{"kind":"state"}` {
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
		} else {
			var protocolErr *ControlPlaneStreamProtocolError
			require.ErrorAs(t, err, &protocolErr)
			assert.False(t, protocolErr.Retryable())
		}
		server.Close()
	}
}

func TestClientStreamRealtimeHasNoTotalSizeOrClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 20 {
			_ = json.NewEncoder(w).Encode(RealtimeEvent{Kind: "transcript", Text: strings.Repeat("x", 70*1024)})
		}
		_, _ = io.WriteString(w, "{\"kind\":\"closed\"}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // The client should finish on closed, not wait for EOF.
	}))
	t.Cleanup(server.Close)
	httpClient := &http.Client{Timeout: time.Nanosecond}
	client, err := NewClient(server.URL, "", "", WithHTTPClient(httpClient))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	count := 0
	require.NoError(t, client.StreamRealtime(ctx, "call", func(RealtimeEvent) { count++ }))
	assert.Equal(t, 21, count)
	assert.Equal(t, time.Nanosecond, httpClient.Timeout, "do not mutate the shared HTTP client")
}

func TestClientRealtimeCancellation(t *testing.T) {
	for _, method := range []string{"capabilities", "start", "stream", "end"} {
		t.Run(method, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if method == "stream" {
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, "", "")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch method {
				case "capabilities":
					_, err = client.RealtimeCapabilities(ctx)
				case "start":
					_, err = client.StartRealtime(ctx, "offer", ChatRequest{ConversationID: "saved"}, "")
				case "stream":
					err = client.StreamRealtime(ctx, "call", func(RealtimeEvent) { t.Error("unexpected event") })
				case "end":
					err = client.EndRealtime(ctx, "call")
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(3 * time.Second):
				t.Fatal("request did not cancel")
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("server request was not canceled")
			}
		})
	}
}
