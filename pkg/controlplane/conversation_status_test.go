package controlplane

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	conversationtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationStatusReportsTurnsAndSavedConversation(t *testing.T) {
	ctx := t.Context()
	turns := openTurnTestStore(t, filepath.Join(t.TempDir(), "storage.db"))
	store := sqlite.NewStore(turns.db)
	s := &Server{
		router:              mux.NewRouter(),
		turns:               turns,
		conversationService: conversations.NewConversationService(store),
	}
	s.setupRoutes()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	client, err := chat.NewClient(server.URL, "", "")
	require.NoError(t, err)

	_, err = client.GetConversationStatus(ctx, "missing")
	require.ErrorIs(t, err, conversationtypes.ErrConversationNotFound)
	_, err = client.GetConversationStatus(ctx, "../escape")
	require.Error(t, err)

	_, err = turns.stop(ctx, "conversation", "older-turn")
	require.NoError(t, err)
	_, _, err = turns.admit(ctx, chat.ChatRequest{ConversationID: "conversation", TurnID: "turn", Message: "work"})
	require.NoError(t, err)
	status, err := client.GetConversationStatus(ctx, "conversation")
	require.NoError(t, err)
	assert.Equal(t, "conversation", status.ID)
	assert.True(t, status.Running)
	assert.Nil(t, status.CreatedAt, "the conversation has not been saved yet")
	require.NotNil(t, status.LatestTurn)
	assert.Equal(t, "turn", status.LatestTurn.ID)
	assert.Equal(t, "accepted", status.LatestTurn.Status)

	record := conversationtypes.NewConversationRecord("conversation")
	record.CWD = "/work"
	record.Summary = "did the work"
	record.Metadata["profile"] = "nightly"
	record.Metadata["model"] = "claude"
	record.Usage = llmtypes.Usage{InputTokens: 10, OutputTokens: 5, InputCost: 0.5, OutputCost: 0.25, MaxContextWindow: 1000}
	require.NoError(t, store.Save(ctx, record))
	// Polling status must not decode the conversation's message history.
	_, err = turns.db.Writer.ExecContext(ctx, "UPDATE conversations SET raw_messages = 'unreadable' WHERE id = ?", record.ID)
	require.NoError(t, err)
	require.NoError(t, turns.finish(ctx, record.ID, "turn", "succeeded", new("all done"), nil))

	status, err = client.GetConversationStatus(ctx, record.ID)
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
