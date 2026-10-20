package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type realtimeProgressWireFrame struct {
	Type string `json:"type"`
	Item struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		CallID  string `json:"call_id"`
		Output  string `json:"output"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
}

// This fixture replaces only the upstream service: the host negotiates a real
// provider session, receives delegation over its WebSocket, and runs a durable
// chat turn. No real credentials, external network, or running server are needed.
func TestRealtimeProgressEndToEnd(t *testing.T) {
	const (
		callID       = "rtc_progress_e2e"
		handoffID    = "handoff_progress_e2e"
		sdp          = "v=0\r\n"
		progressText = "Found the issue; checking callers."
		finalText    = "All checks passed."
	)
	sockets := make(chan *websocket.Conn, 1)
	outbound := make(chan realtimeProgressWireFrame, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/realtime/calls":
			w.Header().Set("Location", "/v1/realtime/calls/"+callID)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sdp))
		case "/v1/realtime":
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if !assert.NoError(t, err) {
				return
			}
			defer conn.Close()
			sockets <- conn
			for {
				var frame realtimeProgressWireFrame
				if conn.ReadJSON(&frame) != nil {
					return
				}
				select {
				case outbound <- frame:
				case <-t.Context().Done():
					return
				}
			}
		case "/v1/realtime/calls/" + callID + "/hangup":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("OPENAI_API_BASE", upstream.URL+"/v1")
	t.Setenv("OPENAI_API_KEY", "realtime-e2e-fake-key")
	provider, err := openai.NewRealtimeProvider(llmtypes.Config{
		Provider: "openai",
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}, "", "")
	require.NoError(t, err)
	s, _ := realtimeTestServer(t)
	s.realtime = provider
	watcher := newSubscriberEventSink()
	watcher.ctx = t.Context()
	watcher.ownerID = "owner"
	watcher.clientID = "browser"
	_, registered := s.registerChatSubscriber("saved", watcher)
	require.True(t, registered)

	started := make(chan chat.ChatRequest, 1)
	finish := make(chan struct{})
	release := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(release)
	s.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		started <- req
		for _, event := range []chat.ChatEvent{
			{Kind: "text-delta", Delta: "Found the issue; "},
			{Kind: "text-delta", Delta: "checking callers."},
			{Kind: "content-end"},
		} {
			event.ConversationID = req.ConversationID
			event.Role = "assistant"
			if err := sink.Send(event); err != nil {
				return req.ConversationID, err
			}
		}
		select {
		case <-finish:
		case <-ctx.Done():
			return req.ConversationID, ctx.Err()
		}
		result := finalText
		return req.ConversationID, sink.Send(chat.ChatEvent{Kind: "result", Result: &result})
	}}

	response := httptest.NewRecorder()
	s.handleRealtime(response, realtimeRequest(t, http.MethodPost, "/api/realtime",
		`{"offer":"v=0\r\n","chat":{"conversationId":"saved"}}`, "browser"))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created struct{ ID string }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	conn := <-sockets
	t.Cleanup(func() { _ = conn.Close() })
	send := func(frame any) {
		t.Helper()
		require.NoError(t, conn.SetWriteDeadline(time.Now().Add(3*time.Second)))
		require.NoError(t, conn.WriteJSON(frame))
	}
	receive := func() realtimeProgressWireFrame {
		t.Helper()
		select {
		case frame := <-outbound:
			return frame
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for realtime provider output")
			return realtimeProgressWireFrame{}
		}
	}
	send(map[string]any{
		"type": "response.function_call_arguments.done", "name": "delegate_to_agent",
		"call_id": handoffID, "arguments": `{"request":"Investigate the issue and run the checks."}`,
	})
	var run chat.ChatRequest
	select {
	case run = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("delegated runner did not start")
	}
	// Ensure failures release the mock runner before the temporary store closes.
	s.realtimeMu.Lock()
	call := s.realtimeCalls[created.ID]
	s.realtimeMu.Unlock()
	require.NotNil(t, call)
	t.Cleanup(func() {
		release()
		call.close()
		call.work.Wait()
	})

	progress := receive()
	require.Equal(t, "conversation.item.create", progress.Type)
	require.Equal(t, "message", progress.Item.Type, "progress must not resolve the function call")
	require.Len(t, progress.Item.Content, 1)
	assert.Contains(t, progress.Item.Content[0].Text, progressText)
	receipt, err := s.turns.get(t.Context(), "saved", run.TurnID)
	require.NoError(t, err)
	assert.Equal(t, "running", receipt.Status, "progress must arrive before work completes")
	require.Equal(t, "response.create", receive().Type)
	send(map[string]any{"type": "conversation.item.created", "item": progress.Item})
	send(map[string]any{"type": "response.created"})
	send(map[string]any{"type": "response.done"})

	release()
	completed := receive()
	require.Equal(t, "conversation.item.create", completed.Type)
	require.Equal(t, "function_call_output", completed.Item.Type)
	assert.Equal(t, handoffID, completed.Item.CallID)
	assert.Equal(t, finalText, completed.Item.Output)
	require.Equal(t, "response.create", receive().Type)
	call.work.Wait()
	receipt, err = s.turns.get(t.Context(), "saved", run.TurnID)
	require.NoError(t, err)
	assert.Equal(t, "succeeded", receipt.Status)
}
