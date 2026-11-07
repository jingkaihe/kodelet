package controlplane

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServerSharesOneDatabaseHandle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	require.NoError(t, db.RunMigrations(t.Context(), migrations.All()))
	server, err := NewServer(t.Context(), &ServerConfig{
		Host:            "127.0.0.1",
		Port:            1,
		CompactRatio:    0.8,
		AuthToken:       "token",
		RunnerAuthToken: "runner-token",
	}, testFrontendHandler())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, server.Close()) })

	require.NotNil(t, server.database)
	assert.Same(t, server.database, server.turns.db)
	assert.Equal(t, 1, server.database.Writer.Stats().MaxOpenConnections)
	require.NoError(t, conversations.NewConversationStore(server.database).Save(
		t.Context(),
		convtypes.NewConversationRecord("shared"),
	))

	// While the single writer connection is held, writes through the other
	// stores must queue for it. A store with its own handle would instead wait
	// in SQLite's busy handler and leave this pool's wait count unchanged.
	tx, err := server.database.Writer.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	enqueued := make(chan error, 1)
	go func() {
		_, err := server.steer.Enqueue(t.Context(), "shared", "steer", nil)
		enqueued <- err
	}()
	deleted := make(chan error, 1)
	go func() { deleted <- server.conversationService.DeleteConversation(t.Context(), "shared") }()
	require.Eventually(t, func() bool {
		return server.database.Writer.Stats().WaitCount >= 2
	}, 2*time.Second, 10*time.Millisecond, "steering and conversation writes must share the server's writer")
	require.NoError(t, tx.Rollback())
	for _, done := range []chan error{enqueued, deleted} {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "a queued write did not finish after the writer was released")
		}
	}
	assert.Equal(t, 1, server.database.Writer.Stats().OpenConnections)
}
