package session

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB creates a test database with migrations applied
func setupTestDB(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	defer sqlDB.Close()

	runner := db.NewMigrationRunner(sqlDB)
	require.NoError(t, runner.Run(ctx, migrations.All()))
}

func TestStorage_AppendAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-session-123")

	update1 := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello"},
	}
	update2 := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "World"},
	}

	err = storage.AppendUpdate(sessionID, update1)
	require.NoError(t, err)

	err = storage.AppendUpdate(sessionID, update2)
	require.NoError(t, err)

	err = storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, sessionID, updates[0].SessionID)
	assert.Contains(t, string(updates[0].Update), "HelloWorld")
}

func TestStorage_MergeConsecutiveChunks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-merge")

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello "},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "user!"},
	}))

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]any{"type": "text", "text": "Thinking..."},
	}))

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello "},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "World!"},
	}))

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "tool_call",
		"toolCall":      map[string]any{"id": "123", "name": "test"},
	}))

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Done!"},
	}))

	require.NoError(t, storage.CloseSession(sessionID))

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 5)

	assert.Contains(t, string(updates[0].Update), "Hello user!")
	assert.Contains(t, string(updates[1].Update), "Thinking...")
	assert.Contains(t, string(updates[2].Update), "Hello World!")
	assert.Contains(t, string(updates[3].Update), "tool_call")
	assert.Contains(t, string(updates[4].Update), "Done!")
}

func TestStorage_NonMergeableContent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-non-mergeable")

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "image", "data": "base64..."},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "What is this?"},
	}))

	require.NoError(t, storage.CloseSession(sessionID))

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 2)
}

func TestStorage_FlushExplicit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-flush")

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 1"},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 2"},
	}))

	require.NoError(t, storage.Flush(sessionID))

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 3"},
	}))

	require.NoError(t, storage.CloseSession(sessionID))

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 2)

	assert.Contains(t, string(updates[0].Update), "Part 1Part 2")
	assert.Contains(t, string(updates[1].Update), "Part 3")
}

func TestStorage_ReadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	updates, err := storage.ReadUpdates("non-existent")
	require.NoError(t, err)
	assert.Nil(t, updates)
}

func TestStorage_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-session-456")

	update := map[string]any{"test": "data"}
	err = storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	assert.True(t, storage.Exists(sessionID))

	err = storage.Delete(sessionID)
	require.NoError(t, err)

	assert.False(t, storage.Exists(sessionID))
}

func TestStorage_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("test-session-789")

	assert.False(t, storage.Exists(sessionID))

	update := map[string]any{"test": "data"}
	err = storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	assert.True(t, storage.Exists(sessionID))
}

func TestStorage_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		sessionID := acptypes.SessionID("session-" + string(rune('a'+i)))
		err := storage.AppendUpdate(sessionID, map[string]any{"n": i})
		require.NoError(t, err)
	}

	assert.Len(t, storage.sessions, 3)

	err = storage.Close()
	require.NoError(t, err)

	assert.Len(t, storage.sessions, 0)
}

func TestStorage_LargeUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("large-session")

	largeText := make([]byte, 100*1024)
	for i := range largeText {
		largeText[i] = 'a'
	}

	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": string(largeText)},
	}

	err = storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	err = storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 1)
}

func TestStorage_ConcurrentWritesDifferentSessions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	var wg sync.WaitGroup
	numSessions := 10
	updatesPerSession := 100

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(sessionNum int) {
			defer wg.Done()
			sessionID := acptypes.SessionID("session-" + string(rune('a'+sessionNum)))
			for j := 0; j < updatesPerSession; j++ {
				update := map[string]any{"session": sessionNum, "update": j}
				err := storage.AppendUpdate(sessionID, update)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < numSessions; i++ {
		sessionID := acptypes.SessionID("session-" + string(rune('a'+i)))
		err := storage.CloseSession(sessionID)
		require.NoError(t, err)
	}

	for i := 0; i < numSessions; i++ {
		sessionID := acptypes.SessionID("session-" + string(rune('a'+i)))
		updates, err := storage.ReadUpdates(sessionID)
		require.NoError(t, err)
		assert.Len(t, updates, updatesPerSession)
	}
}

func TestStorage_ConcurrentWritesSameSession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	sessionID := acptypes.SessionID("shared-session")
	var wg sync.WaitGroup
	numGoroutines := 10
	updatesPerGoroutine := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineNum int) {
			defer wg.Done()
			for j := 0; j < updatesPerGoroutine; j++ {
				update := map[string]any{"goroutine": goroutineNum, "update": j}
				err := storage.AppendUpdate(sessionID, update)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	err = storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	assert.Len(t, updates, numGoroutines*updatesPerGoroutine)
}

func TestStorage_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage1, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)

	sessionID := acptypes.SessionID("persist-test")
	err = storage1.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Persisted message"},
	})
	require.NoError(t, err)
	require.NoError(t, storage1.CloseSession(sessionID))
	require.NoError(t, storage1.Close())

	storage2, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage2.Close()

	updates, err := storage2.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Contains(t, string(updates[0].Update), "Persisted message")
}

func TestStorage_MultipleSessions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	setupTestDB(t, dbPath)

	storage, err := NewStorage(context.Background(), WithDBPath(dbPath))
	require.NoError(t, err)
	defer storage.Close()

	session1 := acptypes.SessionID("session-1")
	session2 := acptypes.SessionID("session-2")

	require.NoError(t, storage.AppendUpdate(session1, map[string]any{"msg": "session1-update1"}))
	require.NoError(t, storage.AppendUpdate(session2, map[string]any{"msg": "session2-update1"}))
	require.NoError(t, storage.AppendUpdate(session1, map[string]any{"msg": "session1-update2"}))

	require.NoError(t, storage.CloseSession(session1))
	require.NoError(t, storage.CloseSession(session2))

	updates1, err := storage.ReadUpdates(session1)
	require.NoError(t, err)
	require.Len(t, updates1, 2)

	updates2, err := storage.ReadUpdates(session2)
	require.NoError(t, err)
	require.Len(t, updates2, 1)
}
