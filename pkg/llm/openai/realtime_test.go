package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/auth"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealtimeProviderConfig(t *testing.T) {
	config := transcriptionConfig(t, "openai")
	config.OpenAI.BaseURL = "https://ignored.example/v1"
	config.OpenAI.APIKeyEnvVar = "REALTIME_TEST_KEY"
	t.Setenv("OPENAI_API_BASE", "https://proxy.example/v1")
	t.Setenv("REALTIME_TEST_KEY", "")
	p, err := NewRealtimeProvider(config, "", "")
	require.NoError(t, err)
	assert.Equal(t, "https://proxy.example/v1", p.baseURL)
	assert.Equal(t, "gpt-realtime", p.model) // Never config.Model.
	assert.Equal(t, "marin", p.voice)
	require.ErrorContains(t, p.CheckAvailability(), "REALTIME_TEST_KEY")
	t.Setenv("REALTIME_TEST_KEY", "configured-later")
	require.NoError(t, p.CheckAvailability())
	header, err := p.headers(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Bearer configured-later", header.Get("Authorization"))
	config.OpenAI.Platform = "codex"
	p, err = NewRealtimeProvider(config, "", "")
	require.NoError(t, err)
	assert.Equal(t, "https://chatgpt.com/backend-api/codex", p.baseURL)
	assert.Equal(t, "https://api.openai.com/v1", p.sidebandBaseURL)
	assert.Equal(t, "gpt-live-1-codex", p.model)
	assert.Equal(t, "cove", p.voice)
	p.readCodex = func() (*auth.CodexCredentials, error) { return &auth.CodexCredentials{}, nil }
	require.ErrorContains(t, p.CheckAvailability(), "kodelet codex login")
	config.OpenAI.Platform = "openai"
	t.Setenv("OPENAI_API_BASE", "https://user:secret@example.com/v1")
	_, err = NewRealtimeProvider(config, "", "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
}

func TestRealtimeVoiceSelection(t *testing.T) {
	for _, platform := range []string{"openai", "codex"} {
		t.Run(platform, func(t *testing.T) {
			p, err := NewRealtimeProvider(transcriptionConfig(t, platform), "", "custom-voice")
			require.NoError(t, err)
			defaultVoice, voices := p.VoiceOptions()
			assert.Equal(t, "custom-voice", defaultVoice)
			assert.Contains(t, voices, defaultVoice)
			for _, selected := range []string{voices[0], ""} {
				config := p.sessionConfig(llmtypes.RealtimeOffer{Voice: selected})
				if selected == "" {
					selected = defaultVoice
				}
				assert.Equal(t, map[string]any{"voice": selected}, config["audio"].(map[string]any)["output"])
			}
			assert.Equal(t, defaultVoice, p.voice, "per-call selection must not mutate the shared provider")
		})
	}
}

type realtimeFixture struct {
	session llmtypes.RealtimeSession
	socket  *websocket.Conn
	writes  chan map[string]any
	hangups atomic.Int32
	cancel  context.CancelFunc
}

func openRealtimeFixture(t *testing.T, platform string) *realtimeFixture {
	t.Helper()
	f := &realtimeFixture{writes: make(chan map[string]any, 32)}
	p, err := NewRealtimeProvider(transcriptionConfig(t, platform), "", "")
	require.NoError(t, err)
	t.Setenv("OPENAI_API_KEY", "test-token")
	p.requestCodex = func(context.Context) (*auth.CodexCredentials, error) {
		return &auth.CodexCredentials{AccessToken: "test-token", AccountID: "test-account"}, nil
	}
	sockets := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		if platform == "codex" {
			assert.Equal(t, "quicksilver=v2", r.Header.Get("OpenAI-Alpha"))
			assert.Equal(t, "test-account", r.Header.Get("ChatGPT-Account-ID"))
			assert.Equal(t, auth.CodexOriginator, r.Header.Get("originator"))
		}
		switch r.URL.Path {
		case "/v1/realtime/calls":
			assert.Equal(t, http.MethodPost, r.Method)
			var session map[string]any
			if platform == "codex" {
				var offer struct {
					SDP     string
					Session map[string]any
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&offer))
				assert.Equal(t, "quicksilver", r.URL.Query().Get("intent"))
				assert.Equal(t, "avas", r.URL.Query().Get("architecture"))
				assert.Equal(t, "v=0\r\n", offer.SDP)
				session = offer.Session
				assert.Equal(t, map[string]any{"type": "client", "ack_filler": true}, session["delegation"])
				assert.NotContains(t, session, "tools")
				assert.NotContains(t, session, "type")
			} else {
				assert.NoError(t, r.ParseMultipartForm(realtimeMaxSDP))
				defer r.MultipartForm.RemoveAll()
				assert.Equal(t, "v=0\r\n", r.FormValue("sdp"))
				assert.NoError(t, json.Unmarshal([]byte(r.FormValue("session")), &session))
				assert.Equal(t, "realtime", session["type"])
				tools, ok := session["tools"].([]any)
				if assert.True(t, ok) && assert.Len(t, tools, 1) {
					assert.Equal(t, realtimeDelegateTool, tools[0].(map[string]any)["name"])
				}
			}
			assert.Equal(t, p.model, session["model"])
			assert.Equal(t, "Work with the existing coding conversation.", session["instructions"])
			// This authority must never be used for sideband or hangup.
			w.Header().Set("Location", "https://untrusted.invalid/v1/realtime/calls/rtc_test")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("v=0\r\n"))
		case "/v1/realtime/calls/rtc_test/hangup":
			assert.Equal(t, "openai", platform)
			assert.Equal(t, http.MethodPost, r.Method)
			f.hangups.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			if platform == "codex" {
				assert.Equal(t, "/v1/live/rtc_test", r.URL.Path)
			} else {
				assert.Equal(t, "/v1/realtime", r.URL.Path)
				assert.Equal(t, "rtc_test", r.URL.Query().Get("call_id"))
			}
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if !assert.NoError(t, err) {
				return
			}
			sockets <- conn
			defer conn.Close()
			for {
				var message map[string]any
				if conn.ReadJSON(&message) != nil {
					return
				}
				if message["type"] == "session.close" {
					f.hangups.Add(1)
					_ = conn.WriteJSON(map[string]any{"type": "session.closed"})
					return
				}
				f.writes <- message
			}
		}
	}))
	t.Cleanup(server.Close)
	p.baseURL, p.sidebandBaseURL = server.URL+"/v1", server.URL+"/v1"
	ctx, cancel := context.WithCancel(t.Context())
	f.cancel = cancel
	t.Cleanup(cancel)
	f.session, err = p.Open(ctx, llmtypes.RealtimeOffer{
		SDP: "v=0\r\n", Instructions: "Work with the existing coding conversation.",
	})
	require.NoError(t, err)
	assert.Equal(t, "v=0\r\n", f.session.Answer())
	f.socket = <-sockets
	t.Cleanup(func() { assert.NoError(t, f.session.Close()) })
	return f
}

func (f *realtimeFixture) send(t *testing.T, frame string) {
	t.Helper()
	require.NoError(t, f.socket.WriteMessage(websocket.TextMessage, []byte(frame)))
}

func realtimeReceive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-channel:
		require.True(t, ok, "channel unexpectedly closed")
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for realtime event")
		var zero T
		return zero
	}
}

func TestRealtimePublicConversation(t *testing.T) {
	f := openRealtimeFixture(t, "openai")
	f.send(t, `{"type":"conversation.item.input_audio_transcription.delta","item_id":"u1","delta":"Fix "}`)
	assert.Equal(t, "Fix ", realtimeReceive(t, f.session.Events()).Text)
	f.send(t, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"u1","transcript":"Fix the test"}`)
	assert.Equal(t, llmtypes.RealtimeEvent{Kind: "transcript", ID: "u1", Role: "user", Text: "Fix the test", Final: true}, realtimeReceive(t, f.session.Events()))
	f.send(t, `{"type":"response.created"}`)
	f.send(t, `{"type":"output_audio_buffer.started"}`)
	f.send(t, `{"type":"response.function_call_arguments.delta","call_id":"h1","delta":"not-final"}`)
	f.send(t, `{"type":"response.function_call_arguments.done","name":"delegate_to_agent","call_id":"h1","item_id":"function-item","arguments":"{\"request\":\"Fix the test\"}"}`)
	assert.Equal(t, llmtypes.RealtimeEvent{Kind: "delegate", HandoffID: "h1", Text: "Fix the test"}, realtimeReceive(t, f.session.Events()))
	require.NoError(t, f.session.Complete(t.Context(), "h1", "Fixed; tests pass."))
	output := realtimeReceive(t, f.writes)
	assert.Equal(t, map[string]any{"type": "function_call_output", "call_id": "h1", "output": "Fixed; tests pass."}, output["item"])
	f.send(t, `{"type":"response.done"}`)
	f.send(t, `{"type":"output_audio_buffer.stopped"}`)
	assert.Equal(t, "response.create", realtimeReceive(t, f.writes)["type"])
	require.NoError(t, f.session.Complete(t.Context(), "h1", "Repeated completion is ignored."))
	f.send(t, `{"type":"response.output_item.done","item":{"type":"function_call","name":"delegate_to_agent","call_id":"h1","arguments":"{\"request\":\"Fix the test\"}"}}`)
	f.send(t, `{"type":"response.output_audio_transcript.delta","item_id":"a1","delta":"Tests "}`)
	assert.Equal(t, "Tests ", realtimeReceive(t, f.session.Events()).Text)
	f.send(t, `{"type":"response.output_audio_transcript.delta","item_id":"a1","delta":"pass."}`)
	assert.Equal(t, "Tests pass.", realtimeReceive(t, f.session.Events()).Text)
	f.send(t, `{"type":"response.output_audio_transcript.done","item_id":"a1","transcript":"Tests pass."}`)
	assert.True(t, realtimeReceive(t, f.session.Events()).Final)
	require.NoError(t, f.session.Update(t.Context(), "Agent is idle."))
	update := realtimeReceive(t, f.writes)
	assert.Equal(t, "system", update["item"].(map[string]any)["role"])
	require.NoError(t, f.session.Close())
	assert.EqualValues(t, 1, f.hangups.Load())
}

func TestRealtimeCodexNativeConversation(t *testing.T) {
	f := openRealtimeFixture(t, "codex")
	f.send(t, `{"type":"input_transcript.added","item":{"id":"input1","text":"Fix "}}`)
	assert.Equal(t, "Fix ", realtimeReceive(t, f.session.Events()).Text)
	f.send(t, `{"type":"input_transcript.added","item":{"id":"input2","text":"the test"}}`)
	assert.Equal(t, llmtypes.RealtimeEvent{Kind: "transcript", ID: "input1", Role: "user", Text: "Fix the test"}, realtimeReceive(t, f.session.Events()))
	f.send(t, `{"type":"turn.done","turn":{"id":"turn1","role":"user","transcript":"Fix the test."}}`)
	assert.Equal(t, llmtypes.RealtimeEvent{Kind: "transcript", ID: "input1", Role: "user", Text: "Fix the test.", Final: true}, realtimeReceive(t, f.session.Events()))
	f.send(t, `{"type":"delegation.created","item":{"id":"h1","type":"delegation","target":"client","content":[{"type":"input_text","text":"Fix the test."}]}}`)
	assert.Equal(t, "h1", realtimeReceive(t, f.session.Events()).HandoffID)
	result := strings.Repeat("é", 400)
	require.NoError(t, f.session.Complete(t.Context(), "h1", result))
	var combined string
	for range 2 {
		message := realtimeReceive(t, f.writes)
		assert.Equal(t, "delegation.context.append", message["type"])
		assert.Equal(t, "h1", message["delegation_item_id"])
		assert.Equal(t, "speakable", message["channel"])
		text := message["content"].([]any)[0].(map[string]any)["text"].(string)
		assert.LessOrEqual(t, len(text), 500)
		assert.True(t, utf8.ValidString(text))
		combined += text
	}
	assert.Equal(t, result, combined)
	require.NoError(t, f.session.Update(t.Context(), "Agent is idle."))
	update := realtimeReceive(t, f.writes)
	assert.Equal(t, "session.context.append", update["type"])
	assert.Equal(t, "commentary", update["channel"])
	require.NoError(t, f.session.Close())
	assert.EqualValues(t, 1, f.hangups.Load())
}

func TestRealtimePublicSpeechDefersCompletion(t *testing.T) {
	f := openRealtimeFixture(t, "openai")
	f.send(t, `{"type":"response.function_call_arguments.done","name":"delegate_to_agent","call_id":"h1","arguments":"{\"request\":\"Fix it\"}"}`)
	realtimeReceive(t, f.session.Events())
	f.send(t, `{"type":"input_audio_buffer.speech_started","item_id":"u2"}`)
	assert.Equal(t, "speech_started", realtimeReceive(t, f.session.Events()).Kind)
	require.NoError(t, f.session.Complete(t.Context(), "h1", "Done."))
	assert.Equal(t, "conversation.item.create", realtimeReceive(t, f.writes)["type"])
	f.send(t, `{"type":"conversation.item.created","item":{"type":"function_call_output","call_id":"h1"}}`)
	f.send(t, `{"type":"input_audio_buffer.speech_stopped"}`)
	f.send(t, `{"type":"response.created"}`)
	f.send(t, `{"type":"response.done"}`)
	require.NoError(t, f.session.Update(t.Context(), "Quiet status."))
	// No competing response.create while VAD starts its automatic reply.
	assert.Equal(t, "conversation.item.create", realtimeReceive(t, f.writes)["type"])
}

func TestRealtimeCodexMetadataDelegationWaitsForFinalSpeech(t *testing.T) {
	for _, order := range []string{"before transcript", "during transcript", "after transcript"} {
		t.Run(order, func(t *testing.T) {
			f := openRealtimeFixture(t, "codex")
			metadata := `{"type":"delegation.created","item":{"id":"h1","type":"delegation","target":"client"}}`
			if order == "before transcript" {
				f.send(t, metadata)
			}
			f.send(t, `{"type":"input_transcript.added","item":{"id":"u1","text":"Run the tests"}}`)
			assert.Equal(t, "transcript", realtimeReceive(t, f.session.Events()).Kind)
			if order == "during transcript" {
				f.send(t, metadata)
			}
			// Missing final text seals the accumulated text; no partial execution.
			f.send(t, `{"type":"turn.done","turn":{"id":"t1","role":"user"}}`)
			assert.Equal(t, llmtypes.RealtimeEvent{Kind: "transcript", ID: "u1", Role: "user", Text: "Run the tests", Final: true}, realtimeReceive(t, f.session.Events()))
			if order == "after transcript" {
				f.send(t, metadata)
			}
			assert.Equal(t, llmtypes.RealtimeEvent{Kind: "delegate", HandoffID: "h1", ItemID: "u1", Text: "Run the tests"}, realtimeReceive(t, f.session.Events()))
			f.send(t, metadata) // A duplicate cannot execute twice.
			f.send(t, `{"type":"delegation.created","item":{"id":"h2","type":"delegation","target":"client"}}`)
			refusal := realtimeReceive(t, f.writes)
			assert.Equal(t, "h2", refusal["delegation_item_id"])
			assert.Contains(t, refusal["content"].([]any)[0].(map[string]any)["text"], "No coding task was started")
		})
	}
}

func TestRealtimeSetupFailuresAreSanitizedAndCleanedUp(t *testing.T) {
	for _, scenario := range []string{"redirect", "provider error", "bad answer", "sideband failure"} {
		t.Run(scenario, func(t *testing.T) {
			var hangups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/hangup") {
					hangups.Add(1)
					return
				}
				if r.URL.Path == "/v1/realtime" {
					http.Error(w, "private-token", http.StatusForbidden)
					return
				}
				w.Header().Set("Location", "https://untrusted.invalid/v1/realtime/calls/rtc_test")
				switch scenario {
				case "redirect":
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "provider error":
					w.WriteHeader(http.StatusUnauthorized)
				case "bad answer":
					return
				}
				_, _ = w.Write([]byte("private-token"))
			}))
			defer server.Close()
			config := transcriptionConfig(t, "openai")
			config.OpenAI.BaseURL = server.URL + "/v1"
			t.Setenv("OPENAI_API_KEY", "private-token")
			p, err := NewRealtimeProvider(config, "", "")
			require.NoError(t, err)
			_, err = p.Open(t.Context(), llmtypes.RealtimeOffer{SDP: "v=0\r\n"})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
			assert.NotContains(t, err.Error(), "untrusted.invalid")
			if scenario == "bad answer" || scenario == "sideband failure" {
				assert.EqualValues(t, 1, hangups.Load())
			}
		})
	}
}

func TestRealtimeCodexFailedAttachmentUsesCleanupOnlySideband(t *testing.T) {
	for _, cleanupAllowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "hangup acknowledged", false: "cleanup unavailable"}[cleanupAllowed], func(t *testing.T) {
			var dials, hangups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "quicksilver=v2", r.Header.Get("OpenAI-Alpha"))
				if r.URL.Path == "/v1/realtime/calls" {
					w.Header().Set("Location", "/v1/live/rtc_test")
					_, _ = w.Write([]byte("v=0\r\n"))
					return
				}
				assert.Equal(t, "/v1/live/rtc_test", r.URL.Path)
				if dials.Add(1) == 1 || !cleanupAllowed {
					http.Error(w, "private-token", http.StatusForbidden)
					return
				}
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if !assert.NoError(t, err) {
					return
				}
				defer conn.Close()
				var message map[string]any
				if assert.NoError(t, conn.ReadJSON(&message)) {
					assert.Equal(t, "session.close", message["type"])
					hangups.Add(1)
					assert.NoError(t, conn.WriteJSON(map[string]any{"type": "session.closed"}))
				}
			}))
			defer server.Close()
			p, err := NewRealtimeProvider(transcriptionConfig(t, "codex"), "", "")
			require.NoError(t, err)
			p.baseURL, p.sidebandBaseURL = server.URL+"/v1", server.URL+"/v1"
			p.requestCodex = func(context.Context) (*auth.CodexCredentials, error) {
				return &auth.CodexCredentials{AccessToken: "private-token", AccountID: "test-account"}, nil
			}
			session, err := p.Open(t.Context(), llmtypes.RealtimeOffer{SDP: "v=0\r\n"})
			require.ErrorContains(t, err, "sideband connection failed")
			assert.NotContains(t, err.Error(), "private-token")
			assert.Nil(t, session) // Never return an SDP without server ownership.
			assert.EqualValues(t, 2, dials.Load())
			if cleanupAllowed {
				assert.EqualValues(t, 1, hangups.Load())
			} else {
				assert.ErrorContains(t, err, "cleanup could not be confirmed")
			}
		})
	}
}

func TestRealtimeSessionFailuresAndCancellation(t *testing.T) {
	for _, scenario := range []string{"cancel", "large frame", "invalid handoff", "provider error"} {
		t.Run(scenario, func(t *testing.T) {
			f := openRealtimeFixture(t, "openai")
			switch scenario {
			case "cancel":
				f.cancel()
			case "large frame":
				f.send(t, strings.Repeat("x", realtimeMaxFrame+1))
			case "invalid handoff":
				f.send(t, `{"type":"response.function_call_arguments.done","name":"delegate_to_agent","call_id":"h1","arguments":"{}"}`)
			case "provider error":
				f.send(t, `{"type":"error","error":{"message":"private-token","code":"invalid_session"}}`)
			}
			if scenario != "cancel" {
				event := realtimeReceive(t, f.session.Events())
				assert.Equal(t, "error", event.Kind)
				assert.NotContains(t, event.Text, "private-token")
			}
			assert.Eventually(t, func() bool { return f.hangups.Load() == 1 }, time.Second, time.Millisecond)
		})
	}
}
