package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRealtimeSession struct {
	events  chan llmtypes.RealtimeEvent
	closed  atomic.Bool
	results chan string
}

func (s *testRealtimeSession) Answer() string                        { return "answer" }
func (s *testRealtimeSession) Events() <-chan llmtypes.RealtimeEvent { return s.events }
func (s *testRealtimeSession) Complete(_ context.Context, _, text string) error {
	s.results <- text
	return nil
}
func (*testRealtimeSession) Update(context.Context, string) error { return nil }
func (s *testRealtimeSession) Close() error                       { s.closed.Store(true); return nil }

type testRealtimeProvider struct {
	session *testRealtimeSession
	offer   llmtypes.RealtimeOffer
}

func (*testRealtimeProvider) CheckAvailability() error { return nil }
func (*testRealtimeProvider) VoiceOptions() (string, []string) {
	return "marin", []string{"marin", "cedar"}
}

func (p *testRealtimeProvider) Open(_ context.Context, offer llmtypes.RealtimeOffer) (llmtypes.RealtimeSession, error) {
	p.offer = offer
	return p.session, nil
}

func realtimeTestServer(t *testing.T) (*Server, *testRealtimeSession) {
	t.Helper()
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "voice.db"))
	_, err := store.db.Exec(`INSERT INTO conversations (id,raw_messages,provider,usage,created_at,updated_at)
		VALUES ('saved','[]','openai','{}',?,?)`, time.Now(), time.Now())
	require.NoError(t, err)
	session := &testRealtimeSession{events: make(chan llmtypes.RealtimeEvent, 16), results: make(chan string, 16)}
	s := &Server{
		turns: store, realtime: &testRealtimeProvider{session: session}, runCtx: t.Context(),
		conversationService: &mockConversationService{getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return &conversations.GetConversationResponse{ID: "saved", Provider: "openai", RawMessages: json.RawMessage("[]")}, nil
		}},
	}
	t.Cleanup(s.closeRealtimeCalls)
	watcher := newSubscriberEventSink()
	watcher.ctx = t.Context()
	s.registerChatSubscriber("saved", watcher)
	return s, session
}

func realtimeRequest(t *testing.T, method, path, body, clientID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(chat.ClientIDHeader, clientID)
	return r.WithContext(contextWithPrincipal(t.Context(), Principal{ID: "owner"}))
}

func TestRealtimeOwnerAttachmentAndDisconnect(t *testing.T) {
	s, session := realtimeTestServer(t)
	w := httptest.NewRecorder()
	s.handleRealtime(w, realtimeRequest(t, "GET", "/api/realtime", "", "browser"))
	assert.JSONEq(t, `{"enabled":true,"available":true,"defaultVoice":"marin","voices":["marin","cedar"]}`, w.Body.String())
	w = httptest.NewRecorder()
	s.handleRealtime(w, realtimeRequest(t, "POST", "/api/realtime", `{"offer":"sdp","voice":"unknown","chat":{"conversationId":"saved"}}`, "browser"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, s.realtime.(*testRealtimeProvider).offer.SDP, "invalid voice must not allocate a call")
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleRealtime(w, realtimeRequest(t, "POST", "/api/realtime", `{"offer":"sdp","voice":"cedar","chat":{"conversationId":"saved"}}`, "browser"))
		return w
	}
	response := post()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "cedar", s.realtime.(*testRealtimeProvider).offer.Voice)
	var created struct{ ID, ConversationID, Answer string }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	assert.Equal(t, "saved", created.ConversationID)
	assert.Equal(t, http.StatusConflict, post().Code)
	r := mux.SetURLVars(realtimeRequest(t, "GET", "/events", "", "other-browser"), map[string]string{"id": created.ID})
	w = httptest.NewRecorder()
	s.handleRealtimeEvents(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)

	ctx, cancel := context.WithCancel(t.Context())
	r = mux.SetURLVars(realtimeRequest(t, "GET", "/events", "", "browser").WithContext(contextWithPrincipal(ctx, Principal{ID: "owner"})), map[string]string{"id": created.ID})
	done := make(chan struct{})
	go func() { s.handleRealtimeEvents(httptest.NewRecorder(), r); close(done) }()
	require.Eventually(t, func() bool {
		s.realtimeMu.Lock()
		call := s.realtimeCalls[created.ID]
		s.realtimeMu.Unlock()
		if call == nil {
			return false
		}
		call.mu.Lock()
		defer call.mu.Unlock()
		return call.attached
	}, time.Second, time.Millisecond)
	cancel()
	<-done
	assert.True(t, session.closed.Load())
	s.realtimeMu.Lock()
	assert.Empty(t, s.realtimeCalls)
	s.realtimeMu.Unlock()
}

func TestRealtimeCreatesConversationWithoutCodingTurn(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("KODELET_BASE_PATH", t.TempDir())
			t.Setenv("OPENAI_API_KEY", "voice-test")
			t.Setenv("ANTHROPIC_API_KEY", "voice-test")
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set("profiles.coding", map[string]any{"provider": provider, "model": "profile-model"})
			require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
			s := newRunnerTestServer(t, "")
			path, err := db.DefaultDBPath()
			require.NoError(t, err)
			s.turns = openTurnTestStore(t, path)
			s.conversationService, err = conversations.GetDefaultConversationService(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.conversationService.Close() })
			session := &testRealtimeSession{events: make(chan llmtypes.RealtimeEvent, 16)}
			s.realtime = &testRealtimeProvider{session: session}
			t.Cleanup(s.closeRealtimeCalls)
			link := newRunnerAPITestLink()
			link.call = func(_ context.Context, method string, params, result any) error {
				require.Equal(t, protocol.MethodWorkspaceCWDHints, method, "starting voice must not open a coding run")
				request := params.(protocol.WorkspaceCWDHintsParams)
				assert.Equal(t, "~/project", request.CWD)
				assert.Equal(t, "coding", request.Profile)
				assert.Equal(t, "review", request.EnvironmentProfile)
				result.(*protocol.WorkspaceCWDHintsResult).BaseDir = "/remote/project"
				return nil
			}
			registration, err := s.runnerRegistry.Register(protocol.RegisterParams{
				ProtocolVersions: []int{protocol.Version},
				Host:             protocol.Host{InstanceID: "voice", Hostname: "host", OS: "linux", Arch: "amd64"},
				Workspace:        protocol.Workspace{Path: "/remote", Name: "remote"},
				Capabilities:     protocol.RunnerCapabilities{WorkspaceDiscovery: true, WorkspaceCWD: true},
			}, link)
			require.NoError(t, err)
			require.NoError(t, s.runnerRegistry.Heartbeat(registration.RunnerID, registration.ConnectionID, registration.Generation,
				protocol.HeartbeatParams{RunnerID: registration.RunnerID, Generation: registration.Generation, State: protocol.RunnerStateIdle}))
			request := chat.ChatRequest{
				ConversationID: "voice-first", RunnerID: registration.RunnerID,
				Profile: "coding", EnvironmentProfile: "review", CWD: "~/project", ReasoningEffort: "high",
				Options: &llmtypes.ExecutionOptions{Model: new("selected-model")},
			}
			body, err := json.Marshal(map[string]any{"offer": "sdp", "chat": request})
			require.NoError(t, err)
			response := httptest.NewRecorder()
			s.handleRealtime(response, realtimeRequest(t, "POST", "/api/realtime", string(body), "browser"))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			record, err := s.conversationService.GetConversation(t.Context(), request.ConversationID)
			require.NoError(t, err)
			assert.Zero(t, record.MessageCount)
			assert.Equal(t, "/remote/project", record.CWD)
			snapshot, present, err := conversations.ConfigSnapshotFromMetadata(record.Metadata)
			require.NoError(t, err)
			require.True(t, present)
			assert.Equal(t, "coding", snapshot.Profile)
			assert.Equal(t, provider, snapshot.Provider)
			assert.Equal(t, "selected-model", snapshot.Model)
			assert.Equal(t, "high", snapshot.ReasoningEffort)
			affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(t.Context(), record.ID)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, registration.RunnerID, affinity.RunnerID)
			assert.Equal(t, "review", affinity.EnvironmentProfile)
			var turns int
			require.NoError(t, s.turns.db.Get(&turns, "SELECT COUNT(*) FROM chat_turns"))
			assert.Zero(t, turns)
			_, saved, err := s.saveRealtimeTranscript(t.Context(), record.ID,
				realtimeEvent{ID: "voice:first", Role: "user", Text: "Hello from a new chat"})
			require.NoError(t, err)
			require.True(t, saved)
			messages, err := s.projectRealtimeTranscript(t.Context(), record.ID, nil)
			require.NoError(t, err)
			require.Len(t, messages, 1)
			assert.Equal(t, "Hello from a new chat", messages[0].Content)
		})
	}
}

func TestRealtimeHandoffDeduplicatesAndOutlivesCall(t *testing.T) {
	s, session := realtimeTestServer(t)
	started, finish := make(chan context.Context, 1), make(chan struct{})
	var calls atomic.Int32
	s.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		calls.Add(1)
		assert.Equal(t, "voice:handoff", req.UserMessageSource)
		assert.Equal(t, "flair", req.Profile)
		started <- ctx
		<-finish
		result := "done"
		return req.ConversationID, sink.Send(chat.ChatEvent{Kind: "result", Result: &result})
	}}
	ctx, cancel := context.WithCancel(t.Context())
	call := &realtimeCall{
		server: s, id: "voice", chat: chat.ChatRequest{ConversationID: "saved", Profile: "flair"},
		ctx: ctx, cancel: cancel, session: session, events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}),
	}
	event := llmtypes.RealtimeEvent{Kind: "delegate", HandoffID: "handoff", Text: "implement it"}
	call.delegate(event)
	t.Cleanup(call.work.Wait)
	call.delegate(event)
	runCtx := <-started
	call.close()
	assert.NoError(t, runCtx.Err(), "hanging up must not cancel accepted coding work")
	close(finish)
	require.Eventually(t, func() bool { return !s.isActiveChat("saved") }, time.Second, time.Millisecond)
	assert.EqualValues(t, 1, calls.Load())
}

func TestRealtimeSteerReconcilesFinishingTurn(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "late steer becomes a turn", true: "consumed unknown outcome never replays"}[consumed], func(t *testing.T) {
			s, session := realtimeTestServer(t)
			req := chat.ChatRequest{ConversationID: "saved", TurnID: "old", Message: "typed"}
			_, admitted, err := s.turns.admit(t.Context(), req)
			require.NoError(t, err)
			require.True(t, admitted)
			run := newActiveChatRun(func() {})
			run.turnID = "old"
			require.True(t, s.registerActiveChat("saved", run))
			var calls atomic.Int32
			s.chatRunner = &mockChatRunner{runFunc: func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
				calls.Add(1)
				return req.ConversationID, nil
			}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			call := &realtimeCall{server: s, id: "voice", chat: chat.ChatRequest{ConversationID: "saved"}, ctx: ctx, cancel: cancel, session: session, events: make(chan realtimeEvent, 16)}
			var wg sync.WaitGroup
			wg.Add(1)
			var receipt chat.TurnReceipt
			go func() {
				defer wg.Done()
				var err error
				receipt, err = call.dispatch(t.Context(), llmtypes.RealtimeEvent{HandoffID: "late", Text: "also fix this"})
				assert.NoError(t, err)
			}()
			require.Eventually(t, func() bool { s.activeChatsMu.Lock(); defer s.activeChatsMu.Unlock(); return len(run.voiceSteers) == 1 }, time.Second, time.Millisecond)
			if consumed {
				_, err = s.turns.db.Exec("DELETE FROM steering_messages WHERE source_id='voice:late'")
				require.NoError(t, err)
			}
			require.NoError(t, s.turns.finish(t.Context(), "saved", "old", "interrupted", nil, nil))
			s.unregisterActiveChat("saved", run)
			wg.Wait()
			if consumed {
				assert.EqualValues(t, 0, calls.Load())
				assert.Equal(t, "interrupted", receipt.Status)
			} else {
				assert.EqualValues(t, 1, calls.Load())
				assert.Equal(t, "succeeded", receipt.Status)
			}
		})
	}
}

func TestRealtimeProjectionIdentityAndCompaction(t *testing.T) {
	s, _ := realtimeTestServer(t)
	_, err := s.turns.db.Exec(`INSERT INTO realtime_transcript(conversation_id,id,anchor,role,text) VALUES
		('saved','voice:user',0,'user','same words'),('saved','voice:assistant',2,'assistant','spoken answer')`)
	require.NoError(t, err)
	record := &conversations.GetConversationResponse{
		Provider: "openai", Metadata: map[string]any{"delegated_user_sources": map[string]string{"0": "voice:handoff", "2": "voice:steer"}},
		CompactionHistory: &convtypes.CompactionHistory{ActiveDisplayStart: 1, Segments: []convtypes.CompactedSegment{{RawMessages: json.RawMessage(`[
			{"role":"user","content":"same words"},{"role":"assistant","content":"coding result"}]`)}}},
		RawMessages: json.RawMessage(`[
			{"role":"user","content":"bootstrap"},{"role":"user","content":"same words"},
			{"role":"user","content":"spoken steer"},{"role":"assistant","content":"more coding text"}]`),
	}
	messages, err := s.convertConversationToWebMessages(record, "openai")
	require.NoError(t, err)
	projected, err := s.projectRealtimeTranscript(t.Context(), "saved", messages)
	require.NoError(t, err)
	var texts []any
	for _, msg := range projected {
		if msg.Kind != "context-compacted" {
			texts = append(texts, msg.Content)
		}
	}
	assert.Equal(t, []any{"same words", "coding result", "same words", "spoken answer", "more coding text"}, texts)
	assert.Equal(t, "voice:user", projected[0].ID)
	// A final update replaces the partial without moving its original position.
	anchor, saved, err := s.saveRealtimeTranscript(t.Context(), "saved", realtimeEvent{ID: "voice:user", Role: "user", Text: "complete words", Final: true})
	require.NoError(t, err)
	require.True(t, saved)
	assert.Zero(t, anchor, "updates return the first occurrence's anchor")
	_, saved, err = s.saveRealtimeTranscript(t.Context(), "saved", realtimeEvent{ID: "voice:user", Role: "assistant", Text: "wrong role"})
	require.NoError(t, err)
	assert.False(t, saved, "an ID never changes role")
	projected, err = s.projectRealtimeTranscript(t.Context(), "saved", messages)
	require.NoError(t, err)
	assert.Equal(t, "complete words", projected[0].Content)
	withoutOverlay, err := s.projectRealtimeTranscript(t.Context(), "fork-without-overlay", messages)
	require.NoError(t, err)
	assert.Len(t, withoutOverlay, len(messages), "a non-Web fork must not hide input when its voice overlay is absent")
}

func TestRealtimeUsesExistingApprovalOwner(t *testing.T) {
	s, session := realtimeTestServer(t)
	subscriber := newSubscriberEventSink()
	subscriber.ctx = t.Context()
	subscriber.clientID = "browser"
	subscriber.capabilities.InteractiveUI = true
	_, registered := s.registerChatSubscriber("saved", subscriber)
	require.True(t, registered)
	s.chatRunner = &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
		response, err := s.uiInputBrokerForRun("saved").Confirm(ctx, extensions.UIConfirmRequest{ID: "approval", Title: "Allow tool?"})
		assert.True(t, response.Confirmed)
		return req.ConversationID, err
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	call := &realtimeCall{
		server: s, id: "voice", clientID: "browser", chat: chat.ChatRequest{ConversationID: "saved", ClientCapabilities: &chat.ChatClientCapabilities{InteractiveUI: true}},
		ctx: ctx, cancel: cancel, session: session, events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}),
	}
	call.delegate(llmtypes.RealtimeEvent{HandoffID: "approval-task", Text: "do the work"})
	t.Cleanup(func() { cancel(); call.work.Wait() })
	viewer := newSubscriberEventSink()
	s.registerChatSubscriber("saved", viewer)
	for {
		select {
		case event := <-subscriber.ch:
			if event.Kind != "ui-confirm-request" {
				continue
			}
			broker := s.uiInputBrokerForRun("saved")
			for len(viewer.ch) > 0 {
				assert.NotEqual(t, "ui-confirm-request", (<-viewer.ch).Kind)
			}
			call.close()
			assert.False(t, broker.respondOwned("other-client", event.UIConfirm.ID, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}))
			assert.True(t, broker.respondOwned("browser", event.UIConfirm.ID, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true}))
			require.Eventually(t, func() bool { return !s.isActiveChat("saved") }, time.Second, time.Millisecond)
			return
		case <-time.After(time.Second):
			t.Fatal("approval or coding completion did not arrive")
		}
	}
}

// gatedRecorder blocks the first stream write so a test can make queued events
// and call cancellation ready at the same time.
type gatedRecorder struct {
	*httptest.ResponseRecorder
	gate chan struct{}
	once sync.Once
}

func (g *gatedRecorder) Write(p []byte) (int, error) {
	g.once.Do(func() { <-g.gate })
	return g.ResponseRecorder.Write(p)
}

func TestRealtimeEventsDeliverQueuedErrorBeforeClosed(t *testing.T) {
	s, session := realtimeTestServer(t)
	// Without draining, select picks cancellation half the time.
	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		call := &realtimeCall{
			server: s, id: convtypes.GenerateID(), ownerID: "owner", clientID: "browser",
			chat: chat.ChatRequest{ConversationID: "saved"}, ctx: ctx, cancel: cancel, session: session,
			events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}),
		}
		s.realtimeMu.Lock()
		if s.realtimeCalls == nil {
			s.realtimeCalls = make(map[string]*realtimeCall)
		}
		s.realtimeCalls[call.id] = call
		s.realtimeMu.Unlock()
		call.emit(realtimeEvent{Kind: "state", Text: "connected"})
		w := &gatedRecorder{ResponseRecorder: httptest.NewRecorder(), gate: make(chan struct{})}
		r := mux.SetURLVars(realtimeRequest(t, "GET", "/events", "", "browser"), map[string]string{"id": call.id})
		done := make(chan struct{})
		go func() { s.handleRealtimeEvents(w, r); close(done) }()
		require.Eventually(t, func() bool {
			call.mu.Lock()
			defer call.mu.Unlock()
			return call.attached
		}, time.Second, time.Millisecond)
		call.emit(realtimeEvent{Kind: "error", Text: "Could not save the voice transcript. Live voice has stopped."})
		call.close()
		close(w.gate)
		<-done

		var kinds []string
		for line := range strings.SplitSeq(strings.TrimSpace(w.Body.String()), "\n") {
			var event realtimeEvent
			require.NoError(t, json.Unmarshal([]byte(line), &event))
			kinds = append(kinds, event.Kind)
		}
		require.Equal(t, []string{"state", "error", "closed"}, kinds)
	}
}

func TestRealtimeRecoverableHandoffFailuresKeepCallOpen(t *testing.T) {
	s, session := realtimeTestServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	call := &realtimeCall{
		server: s, id: "voice", chat: chat.ChatRequest{ConversationID: "saved"},
		ctx: ctx, cancel: cancel, session: session, events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}),
	}
	t.Cleanup(func() { cancel(); call.work.Wait() })

	// An oversized request is refused aloud without starting work.
	call.delegate(llmtypes.RealtimeEvent{HandoffID: "too-long", Text: strings.Repeat("x", 10001)})
	assert.Contains(t, <-session.results, "No coding task was started")
	assert.Equal(t, realtimeEvent{Kind: "warning", Text: "Live voice returned an invalid coding request.", ConversationID: "saved"}, <-call.events)

	// An interactive operation makes the handoff fail; its explanation is still spoken.
	run := newActiveChatRun(func() {})
	require.True(t, s.registerActiveChat("saved", run))
	call.delegate(llmtypes.RealtimeEvent{HandoffID: "busy", Text: "fix it"})
	assert.Contains(t, <-session.results, "could not be confirmed")
	assert.Equal(t, "warning", (<-call.events).Kind)
	assert.Equal(t, "agent-completed", (<-call.events).Kind)
	s.unregisterActiveChat("saved", run)

	call.mu.Lock()
	closed := call.closed
	call.mu.Unlock()
	assert.False(t, closed, "recoverable handoff failures must not end the call")
	assert.False(t, session.closed.Load())
}

func TestRealtimeCaptionsPersistOffTheReadLoop(t *testing.T) {
	s, session := realtimeTestServer(t)
	session.events = make(chan llmtypes.RealtimeEvent, 256)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	call := &realtimeCall{
		server: s, id: "voice", chat: chat.ChatRequest{ConversationID: "saved"}, ctx: ctx, cancel: cancel, session: session,
		events: make(chan realtimeEvent, 16), handoffs: make(map[string]struct{}), captionWake: make(chan struct{}, 1),
	}
	session.events <- llmtypes.RealtimeEvent{Kind: "speech_started", ItemID: "u1"}
	var text strings.Builder
	for i := range 200 {
		text.WriteString("word ")
		session.events <- llmtypes.RealtimeEvent{Kind: "transcript", ID: "a1", Role: "assistant", Text: text.String(), Final: i == 199}
	}
	// With no persister running, the read loop must still drain the provider.
	go call.readProvider(session)
	require.Eventually(t, func() bool { return len(session.events) == 0 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		call.captionMu.Lock()
		defer call.captionMu.Unlock()
		return len(call.captionOrder) == 2 && call.captions["voice:a1"].event.Final
	}, time.Second, time.Millisecond)
	call.captionMu.Lock()
	assert.Equal(t, []string{"voice:u1", "voice:a1"}, call.captionOrder)
	call.captionMu.Unlock()

	go call.persistCaptions()
	event := <-call.events
	assert.Equal(t, "voice:a1", event.ID)
	assert.Equal(t, text.String(), event.Text)
	assert.True(t, event.Final)
	require.NotNil(t, event.Anchor)
	assert.Zero(t, *event.Anchor)
	assert.Empty(t, call.events, "cumulative updates are coalesced and placeholders stay hidden")
	var rows []struct {
		ID   string `db:"id"`
		Text string `db:"text"`
	}
	require.NoError(t, s.turns.db.Select(&rows, "SELECT id,text FROM realtime_transcript WHERE conversation_id='saved' ORDER BY sequence"))
	require.Len(t, rows, 2)
	assert.Equal(t, "voice:u1", rows[0].ID, "first arrival fixes the transcript sequence")
	assert.Empty(t, rows[0].Text)
	assert.Equal(t, text.String(), rows[1].Text)
}
