package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationStatusReportsTurnsAndSavedConversation(t *testing.T) {
	database := dbtest.Open(t, filepath.Join(t.TempDir(), "storage.db"))
	turns, err := newTurnStore(t.Context(), database)
	require.NoError(t, err)
	store := sqlite.NewStore(database)
	started, release := make(chan struct{}), make(chan struct{})
	runner := &mockChatRunner{runFunc: func(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		close(started)
		<-release
		return req.ConversationID, sink.Send(ChatEvent{Kind: "result", Result: new("all done")})
	}}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		turns:               turns,
		chatRunner:          runner,
		conversationService: conversations.NewConversationService(store),
		runCtx:              ctx,
		runCancel:           cancel,
		config:              &ServerConfig{AuthToken: "status-token"},
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/chat", s.handleChat).Methods(http.MethodPost)
	router.HandleFunc("/api/conversations/{id}/status", s.handleGetConversationStatus).Methods(http.MethodGet)
	server := httptest.NewServer(s.authMiddleware(router))
	t.Cleanup(func() { cancel(); server.Close() })
	client, err := chat.NewClient(server.URL, "status-token", "")
	require.NoError(t, err)

	_, err = client.GetConversationStatus(t.Context(), "missing")
	require.ErrorIs(t, err, conversationtypes.ErrConversationNotFound)
	_, err = client.GetConversationStatus(t.Context(), "../escape")
	require.Error(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := client.Run(t.Context(), chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "work"}, &recordingChatSink{})
		done <- err
	}()
	<-started
	status, err := client.GetConversationStatus(t.Context(), "conversation")
	require.NoError(t, err)
	assert.Equal(t, "conversation", status.ID)
	assert.True(t, status.Running)
	assert.Nil(t, status.CreatedAt, "the conversation has not been saved yet")
	require.NotNil(t, status.LatestTurn)
	assert.Equal(t, "turn", status.LatestTurn.ID)
	assert.Equal(t, "running", status.LatestTurn.Status)

	record := conversationtypes.NewConversationRecord("conversation")
	record.CWD = "/work"
	record.Summary = "did the work"
	record.Metadata["profile"] = "nightly"
	record.Metadata["model"] = "claude"
	record.Usage = llmtypes.Usage{InputTokens: 10, OutputTokens: 5, InputCost: 0.5, OutputCost: 0.25, MaxContextWindow: 1000}
	require.NoError(t, store.Save(t.Context(), record))
	close(release)
	require.NoError(t, <-done)

	status, err = client.GetConversationStatus(t.Context(), "conversation")
	require.NoError(t, err)
	assert.False(t, status.Running)
	assert.Equal(t, "/work", status.CWD)
	assert.Equal(t, "did the work", status.Summary)
	assert.Equal(t, "nightly", status.Profile)
	assert.Equal(t, "claude", status.Model)
	require.NotNil(t, status.CreatedAt)
	assert.Equal(t, 10, status.Usage.InputTokens)
	assert.InDelta(t, 0.75, status.Usage.TotalCost, 1e-9)
	assert.Equal(t, 1000, status.Usage.MaxContextWindow)
	require.NotNil(t, status.LatestTurn)
	assert.Equal(t, "succeeded", status.LatestTurn.Status)
	assert.Equal(t, "all done", status.LatestTurn.Result)
}
