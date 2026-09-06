package controlplane

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTurnTestStore(t *testing.T, path string) *turnStore {
	t.Helper()
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	store, err := newTurnStore(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.db.Close()) })
	return store
}

func TestTurnRequestHashPreservesExecutionInputs(t *testing.T) {
	base := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "hello"}
	hash, err := turnRequestHash(base)
	require.NoError(t, err)
	equivalent := base
	equivalent.Message = "ignored because content has text"
	equivalent.Content = []chat.ChatContentBlock{{Type: "text", Text: " hello "}}
	equivalent.ClientCapabilities = &chat.ChatClientCapabilities{InteractiveUI: true}
	otherHash, err := turnRequestHash(equivalent)
	require.NoError(t, err)
	assert.Equal(t, hash, otherHash)
	for name, change := range map[string]func(*chat.ChatRequest){
		"text":           func(req *chat.ChatRequest) { req.Message = "different" },
		"runner":         func(req *chat.ChatRequest) { req.RunnerID = "other-runner" },
		"cwd":            func(req *chat.ChatRequest) { req.CWD = "/other" },
		"profile":        func(req *chat.ChatRequest) { req.Profile = "other" },
		"environment":    func(req *chat.ChatRequest) { req.EnvironmentProfile = "other" },
		"deny tools":     func(req *chat.ChatRequest) { req.Options = &llmtypes.ExecutionOptions{AllowedTools: new([]string{})} },
		"explicit false": func(req *chat.ChatRequest) { req.Options = &llmtypes.ExecutionOptions{NoTools: new(false)} },
		"explicit zero":  func(req *chat.ChatRequest) { req.Options = &llmtypes.ExecutionOptions{MaxTurns: new(0)} },
		"image": func(req *chat.ChatRequest) {
			req.Content = chat.ContentBlocksForUserInput(req.Message, []string{"https://example.com/image.png"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			change(&req)
			actual, err := turnRequestHash(req)
			require.NoError(t, err)
			assert.NotEqual(t, hash, actual)
		})
	}
}

func TestTurnStoreDurableAdmissionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.db")
	store := openTurnTestStore(t, path)
	ctx := t.Context()
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "once"}
	receipt, admitted, err := store.admit(ctx, req)
	require.NoError(t, err)
	require.True(t, admitted)
	require.NotEmpty(t, receipt.RunID)
	assert.Equal(t, "accepted", receipt.Status)
	started, err := store.start(ctx, req.ConversationID, req.TurnID)
	require.NoError(t, err)
	require.True(t, started)
	duplicate, admitted, err := store.admit(ctx, req)
	require.NoError(t, err)
	assert.False(t, admitted)
	assert.Equal(t, receipt.RunID, duplicate.RunID)
	assert.Equal(t, "running", duplicate.Status)
	changed := req
	changed.Message = "another effect"
	_, _, err = store.admit(ctx, changed)
	assert.ErrorIs(t, err, errTurnConflict)
	changed.TurnID = "next-turn"
	_, _, err = store.admit(ctx, changed)
	assert.ErrorIs(t, err, errConversationBusy)

	// Recovery reports loss; it never calls a runner or re-admits the old turn.
	require.NoError(t, store.db.Close())
	store = openTurnTestStore(t, path)
	recovered, admitted, err := store.admit(ctx, req)
	require.NoError(t, err)
	assert.False(t, admitted)
	assert.Equal(t, receipt.RunID, recovered.RunID)
	assert.Equal(t, "interrupted", recovered.Status)
	assert.Contains(t, recovered.Error, "daemon stopped")
	_, admitted, err = store.admit(ctx, changed)
	require.NoError(t, err)
	assert.True(t, admitted)
	output := "saved result"
	require.NoError(t, store.finish(ctx, changed.ConversationID, changed.TurnID, "succeeded", &output, nil))
	stopped, err := store.stop(ctx, req.ConversationID, req.TurnID)
	require.NoError(t, err)
	assert.Equal(t, "interrupted", stopped.Status)

	require.NoError(t, store.db.Close())
	store = openTurnTestStore(t, path)
	recovered, admitted, err = store.admit(ctx, changed)
	require.NoError(t, err)
	assert.False(t, admitted)
	assert.Equal(t, "succeeded", recovered.Status)
	require.NotNil(t, recovered.Result)
	assert.Equal(t, output, *recovered.Result)
}

func TestTurnStoreCancellationFencesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.db")
	store := openTurnTestStore(t, path)
	ctx := t.Context()
	for _, state := range []string{"absent", "accepted", "running"} {
		req := chat.ChatRequest{ConversationID: state, TurnID: "turn", Message: "late submission"}
		if state != "absent" {
			_, _, err := store.admit(ctx, req)
			require.NoError(t, err)
			if state == "running" {
				_, err = store.start(ctx, req.ConversationID, req.TurnID)
				require.NoError(t, err)
			}
		}
		receipt, err := store.stop(ctx, req.ConversationID, req.TurnID)
		require.NoError(t, err)
		assert.True(t, receipt.CancelRequested)
	}
	require.NoError(t, store.db.Close())
	store = openTurnTestStore(t, path)
	for _, state := range []string{"absent", "accepted", "running"} {
		req := chat.ChatRequest{ConversationID: state, TurnID: "turn", Message: "late submission"}
		receipt, admitted, err := store.admit(ctx, req)
		require.NoError(t, err)
		assert.False(t, admitted)
		assert.Equal(t, "cancelled", receipt.Status)
		started, err := store.start(ctx, req.ConversationID, req.TurnID)
		require.NoError(t, err)
		assert.False(t, started)
	}
}

func TestTurnStoreConcurrentDuplicatesAdmitExactlyOnce(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "once"}
	var accepted atomic.Int32
	var callers sync.WaitGroup
	for range 16 {
		callers.Go(func() {
			_, admitted, err := store.admit(t.Context(), req)
			assert.NoError(t, err)
			if admitted {
				accepted.Add(1)
			}
		})
	}
	callers.Wait()
	assert.EqualValues(t, 1, accepted.Load())
}

func serveTurnTestServer(t *testing.T, store *turnStore, runner chat.ChatRunner) (*httptest.Server, *chat.ControlPlaneChatRunner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{turns: store, chatRunner: runner, runCtx: ctx, runCancel: cancel, config: &ServerConfig{AuthToken: "receipt-token"}}
	router := mux.NewRouter()
	router.HandleFunc("/api/chat", s.handleChat).Methods(http.MethodPost)
	router.HandleFunc("/api/conversations/{id}/turns/{turnId}", s.handleGetTurnReceipt).Methods(http.MethodGet)
	router.HandleFunc("/api/conversations/{id}/stop", s.handleStopConversation).Methods(http.MethodPost)
	server := httptest.NewServer(s.authMiddleware(router))
	t.Cleanup(func() { cancel(); server.Close() })
	client, err := chat.NewControlPlaneChatRunner(server.URL, "receipt-token", "")
	require.NoError(t, err)
	return server, client
}

func TestTurnHTTPDropAfterAdmissionReconcilesWithoutReplay(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	started, release := make(chan struct{}), make(chan struct{})
	var effects atomic.Int32
	run := &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		effects.Add(1)
		receipt, err := store.get(ctx, req.ConversationID, req.TurnID)
		assert.NoError(t, err)
		assert.Equal(t, "running", receipt.Status)
		assert.Equal(t, receipt.RunID, ctx.Value(turnRunIDKey{}))
		_ = sink.Send(ChatEvent{Kind: "conversation", ConversationID: req.ConversationID})
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return req.ConversationID, ctx.Err()
		}
		return req.ConversationID, sink.Send(ChatEvent{Kind: "result", Result: new("durable result")})
	}}
	server, client := serveTurnTestServer(t, store, run)
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "side effect once"}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	httpReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/chat", strings.NewReader(string(data)))
	require.NoError(t, err)
	httpReq.Header.Set("Authorization", "Bearer receipt-token")
	response, err := server.Client().Do(httpReq)
	require.NoError(t, err)
	assert.Equal(t, "turn", response.Header.Get("X-Kodelet-Turn-ID"))
	require.NoError(t, response.Body.Close()) // Observer detach must not cancel execution.
	<-started
	_, err = client.Run(t.Context(), req, &recordingChatSink{})
	var pending *chat.TurnPendingError
	require.ErrorAs(t, err, &pending)
	assert.Equal(t, "running", pending.Receipt.Status)
	assert.False(t, pending.Receipt.CancelRequested)
	assert.EqualValues(t, 1, effects.Load())
	changed := req
	changed.Message = "different effect"
	_, err = client.Run(t.Context(), changed, &recordingChatSink{})
	var conflict *chat.ControlPlaneHTTPError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, http.StatusConflict, conflict.StatusCode)
	close(release)
	require.Eventually(t, func() bool {
		receipt, err := client.GetTurnReceipt(t.Context(), req.ConversationID, req.TurnID)
		return err == nil && receipt.Status == "succeeded"
	}, 3*time.Second, 5*time.Millisecond)
	sink := &recordingChatSink{}
	_, err = client.Run(t.Context(), req, sink)
	require.NoError(t, err)
	events := sink.Events()
	require.Len(t, events, 3)
	assert.Equal(t, "result", events[1].Kind)
	assert.Equal(t, "durable result", *events[1].Result)
	assert.EqualValues(t, 1, effects.Load())
	response, err = server.Client().Get(server.URL + "/api/conversations/conversation/turns/turn")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestTurnHTTPExactStopAndEarlyFence(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	started := make(chan struct{}, 2)
	var effects atomic.Int32
	_, client := serveTurnTestServer(t, store, &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
		effects.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return req.ConversationID, ctx.Err()
	}})
	require.NoError(t, client.StopConversationTurn(t.Context(), "conversation", "early"))
	_, err := client.Run(t.Context(), chat.ChatRequest{ConversationID: "conversation", TurnID: "early", Message: "never start"}, &recordingChatSink{})
	require.NoError(t, err) // Cancellation is an explicit done event, not a transport error.
	assert.Zero(t, effects.Load())
	for _, turnID := range []string{"first", "next"} {
		done := make(chan error, 1)
		go func() {
			_, err := client.Run(t.Context(), chat.ChatRequest{ConversationID: "conversation", TurnID: turnID, Message: "wait"}, &recordingChatSink{})
			done <- err
		}()
		<-started
		require.NoError(t, client.StopConversationTurn(t.Context(), "conversation", "early"))
		if turnID == "next" {
			require.NoError(t, client.StopConversationTurn(t.Context(), "conversation", "first"))
		}
		receipt, err := client.GetTurnReceipt(t.Context(), "conversation", turnID)
		require.NoError(t, err)
		assert.Equal(t, "running", receipt.Status)
		require.NoError(t, client.StopConversationTurn(t.Context(), "conversation", turnID))
		require.NoError(t, <-done)
		receipt, err = client.GetTurnReceipt(t.Context(), "conversation", turnID)
		require.NoError(t, err)
		assert.Equal(t, "cancelled", receipt.Status)
		assert.True(t, receipt.CancelRequested)
	}
	assert.EqualValues(t, 2, effects.Load())
}

func TestTurnHTTPTerminalFailureAndClosedStoreFailClosed(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	var calls atomic.Int32
	_, client := serveTurnTestServer(t, store, &mockChatRunner{runFunc: func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
		calls.Add(1)
		return req.ConversationID, errors.New("provider rejected request")
	}})
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "fail"}
	for range 2 {
		_, err := client.Run(t.Context(), req, &recordingChatSink{})
		require.ErrorContains(t, err, "provider rejected request")
		var uncertain *chat.UncertainSubmissionError
		assert.False(t, errors.As(err, &uncertain))
	}
	receipt, err := client.GetTurnReceipt(t.Context(), req.ConversationID, req.TurnID)
	require.NoError(t, err)
	assert.Equal(t, "failed", receipt.Status)
	assert.Equal(t, "provider rejected request", receipt.Error)
	require.NoError(t, store.db.Close())
	req.TurnID = "no-store"
	_, err = client.Run(t.Context(), req, &recordingChatSink{})
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load())
}

func TestTurnHTTPStopDoesNotClaimUnconfirmedCancellation(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "orphan", Message: "wait"}
	_, _, err := store.admit(t.Context(), req)
	require.NoError(t, err)
	_, err = store.start(t.Context(), req.ConversationID, req.TurnID)
	require.NoError(t, err)
	server, _ := serveTurnTestServer(t, store, &mockChatRunner{})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/conversations/conversation/stop?turnId=orphan", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer receipt-token")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusRequestTimeout, response.StatusCode)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Contains(t, string(data), "completion is unconfirmed")
}

func TestTurnHTTPRejectsEmptyScopedStop(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	server, _ := serveTurnTestServer(t, store, &mockChatRunner{})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/conversations/conversation/stop?turnId=", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer receipt-token")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode, "an invalid exact ID must not become a conversation-wide stop")
}

func TestTurnFinishHonorsDurableCancellationBeforeContextCallback(t *testing.T) {
	store := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	req := chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "once"}
	_, _, err := store.admit(t.Context(), req)
	require.NoError(t, err)
	_, err = store.start(t.Context(), req.ConversationID, req.TurnID)
	require.NoError(t, err)
	_, err = store.stop(t.Context(), req.ConversationID, req.TurnID)
	require.NoError(t, err)
	server := &Server{turns: store}
	err = server.finishTurn(t.Context(), req.ConversationID, req.TurnID, &turnEventSink{result: new("finished concurrently")}, nil)
	require.ErrorIs(t, err, context.Canceled)
	receipt, err := store.get(t.Context(), req.ConversationID, req.TurnID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", receipt.Status)
}
