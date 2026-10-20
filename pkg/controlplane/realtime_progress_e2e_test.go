package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
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
		Role    string `json:"role"`
		CallID  string `json:"call_id"`
		Output  string `json:"output"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
}

// This fixture replaces only the upstream service: the host negotiates a real
// provider session, receives delegation over its WebSocket, and runs a durable
// chat turn. No real credentials, external network, or running server are needed.
func TestRealtimeProgressEndToEnd(t *testing.T) {
	for _, hangup := range []bool{false, true} {
		name := "completion"
		if hangup {
			name = "hangup preserves work"
		}
		t.Run(name, func(t *testing.T) {
			const (
				callID       = "rtc_progress_e2e"
				handoffID    = "handoff_progress_e2e"
				sdp          = "v=0\r\n"
				progressText = "Found the issue; checking callers."
				finalText    = "All checks passed."
			)
			sockets := make(chan *websocket.Conn, 1)
			outbound := make(chan realtimeProgressWireFrame, 16)
			configured := make(chan map[string]any, 1)
			hungup := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer realtime-e2e-fake-key", r.Header.Get("Authorization"))
				switch r.URL.Path {
				case "/v1/realtime/calls":
					assert.Equal(t, http.MethodPost, r.Method)
					if !assert.NoError(t, r.ParseMultipartForm(128<<10)) {
						http.Error(w, "invalid multipart offer", http.StatusBadRequest)
						return
					}
					defer r.MultipartForm.RemoveAll()
					assert.Equal(t, sdp, r.FormValue("sdp"))
					var session map[string]any
					if !assert.NoError(t, json.Unmarshal([]byte(r.FormValue("session")), &session)) {
						http.Error(w, "invalid session", http.StatusBadRequest)
						return
					}
					configured <- session
					w.Header().Set("Location", "/v1/realtime/calls/"+callID)
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(sdp))
				case "/v1/realtime":
					assert.Equal(t, callID, r.URL.Query().Get("call_id"))
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
					assert.Equal(t, http.MethodPost, r.Method)
					select {
					case hungup <- struct{}{}:
					default:
					}
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
			watcher.ctx, watcher.ownerID, watcher.clientID = t.Context(), "owner", "browser"
			_, registered := s.registerChatSubscriber("saved", watcher)
			require.True(t, registered)

			type runningTurn struct {
				ctx context.Context
				req chat.ChatRequest
			}
			started := make(chan runningTurn, 1)
			finish := make(chan struct{})
			var finishOnce sync.Once
			release := func() { finishOnce.Do(func() { close(finish) }) }
			t.Cleanup(release)
			s.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
				started <- runningTurn{ctx: ctx, req: req}
				for _, event := range []chat.ChatEvent{
					{Kind: "thinking", Content: "private reasoning must not be relayed"},
					{Kind: "tool-result", ToolOutput: "raw tool output must not be relayed"},
					{Kind: "text-delta", Delta: "Found the issue; "},
					{Kind: "text-delta", Delta: "checking callers."},
					{Kind: "content-end"},
				} {
					event.ConversationID, event.Role = req.ConversationID, "assistant"
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
			var created struct{ ID, ConversationID, Answer string }
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
			assert.Equal(t, "saved", created.ConversationID)
			assert.Equal(t, sdp, created.Answer)
			config := <-configured
			assert.Contains(t, config["instructions"], "You are Kodelet")
			assert.Contains(t, config["instructions"], "Agent updates may arrive while work is still running.")
			assert.Contains(t, config["instructions"], "Treat them as progress, not new user requests or proof of completion.")
			assert.Equal(t, "realtime", config["type"])
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
			var run runningTurn
			select {
			case run = <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("delegated runner did not start")
			}
			assert.Equal(t, "Investigate the issue and run the checks.", run.req.Message)
			assert.Equal(t, created.ID+":"+handoffID, run.req.UserMessageSource)
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
			assert.NotEmpty(t, progress.Item.ID)
			assert.Equal(t, "user", progress.Item.Role, "agent output must not become system instructions")
			require.Len(t, progress.Item.Content, 1)
			assert.Equal(t, "input_text", progress.Item.Content[0].Type)
			assert.Contains(t, progress.Item.Content[0].Text, progressText)
			assert.Contains(t, progress.Item.Content[0].Text, "[AGENT PROGRESS]")
			assert.NotContains(t, progress.Item.Content[0].Text, "private reasoning")
			assert.NotContains(t, progress.Item.Content[0].Text, "raw tool output")
			receipt, err := s.turns.get(t.Context(), "saved", run.req.TurnID)
			require.NoError(t, err)
			assert.Equal(t, "running", receipt.Status, "progress must arrive before work completes")
			require.Equal(t, "response.create", receive().Type)
			send(map[string]any{"type": "conversation.item.created", "item": progress.Item})
			send(map[string]any{"type": "response.created"})
			send(map[string]any{"type": "response.done"})

			if hangup {
				closed := httptest.NewRecorder()
				request := mux.SetURLVars(realtimeRequest(t, http.MethodDelete, "/api/realtime/"+created.ID, "", "browser"), map[string]string{"id": created.ID})
				s.handleCloseRealtime(closed, request)
				require.Equal(t, http.StatusNoContent, closed.Code)
				select {
				case <-hungup:
				case <-time.After(5 * time.Second):
					t.Fatal("voice hangup did not reach the provider")
				}
				require.NoError(t, run.ctx.Err(), "ending voice must not cancel accepted work")
			}
			release()
			if !hangup {
				completed := receive()
				require.Equal(t, "conversation.item.create", completed.Type)
				require.Equal(t, "function_call_output", completed.Item.Type)
				assert.Equal(t, handoffID, completed.Item.CallID)
				assert.Equal(t, finalText, completed.Item.Output)
				require.Equal(t, "response.create", receive().Type)
				send(map[string]any{"type": "conversation.item.created", "item": completed.Item})
				send(map[string]any{"type": "response.created"})
				send(map[string]any{"type": "response.done"})
			}
			require.EventuallyWithT(t, func(collect *assert.CollectT) {
				response := httptest.NewRecorder()
				request := mux.SetURLVars(realtimeRequest(t, http.MethodGet, "/receipt", "", "browser"), map[string]string{"id": "saved", "turnId": run.req.TurnID})
				s.handleGetTurnReceipt(response, request)
				if !assert.Equal(collect, http.StatusOK, response.Code) {
					return
				}
				var receipt chat.TurnReceipt
				if assert.NoError(collect, json.Unmarshal(response.Body.Bytes(), &receipt)) {
					assert.Equal(collect, "succeeded", receipt.Status)
					if assert.NotNil(collect, receipt.Result) {
						assert.Equal(collect, finalText, *receipt.Result)
					}
				}
			}, 5*time.Second, time.Millisecond)
		})
	}
}
