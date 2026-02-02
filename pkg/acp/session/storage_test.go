package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_AppendAndRead(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-session-123")

	// Consecutive same-type text chunks should be merged
	update1 := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello"},
	}
	update2 := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "World"},
	}

	err := storage.AppendUpdate(sessionID, update1)
	require.NoError(t, err)

	err = storage.AppendUpdate(sessionID, update2)
	require.NoError(t, err)

	err = storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	// Merged into single update
	require.Len(t, updates, 1)

	assert.Equal(t, sessionID, updates[0].SessionID)
	assert.Contains(t, string(updates[0].Update), "HelloWorld")
}

func TestStorage_MergeConsecutiveChunks(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-merge")

	// User message chunk
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello "},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "user!"},
	}))

	// Agent thought chunk (different type - should flush user_message_chunk)
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]any{"type": "text", "text": "Thinking..."},
	}))

	// Agent message chunks
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Hello "},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "World!"},
	}))

	// Non-mergeable update (tool_call)
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "tool_call",
		"toolCall":      map[string]any{"id": "123", "name": "test"},
	}))

	// More agent chunks after tool
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Done!"},
	}))

	require.NoError(t, storage.CloseSession(sessionID))

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	// Expect: merged user (1) + merged thought (1) + merged agent (1) + tool_call (1) + merged agent (1) = 5
	require.Len(t, updates, 5)

	// Verify merged content
	assert.Contains(t, string(updates[0].Update), "Hello user!")
	assert.Contains(t, string(updates[1].Update), "Thinking...")
	assert.Contains(t, string(updates[2].Update), "Hello World!")
	assert.Contains(t, string(updates[3].Update), "tool_call")
	assert.Contains(t, string(updates[4].Update), "Done!")
}

func TestStorage_NonMergeableContent(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-non-mergeable")

	// Non-text content (image) should not be merged
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
	// Image is not mergeable, text is separate
	require.Len(t, updates, 2)
}

func TestStorage_FlushExplicit(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-flush")

	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 1"},
	}))
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 2"},
	}))

	// Explicit flush at turn boundary
	require.NoError(t, storage.Flush(sessionID))

	// More updates after flush
	require.NoError(t, storage.AppendUpdate(sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Part 3"},
	}))

	require.NoError(t, storage.CloseSession(sessionID))

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	// First two merged and flushed (1), then third flushed on close (1) = 2
	require.Len(t, updates, 2)

	assert.Contains(t, string(updates[0].Update), "Part 1Part 2")
	assert.Contains(t, string(updates[1].Update), "Part 3")
}

func TestStorage_ReadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	updates, err := storage.ReadUpdates("non-existent")
	require.NoError(t, err)
	assert.Nil(t, updates)
}

func TestStorage_Delete(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-session-456")

	update := map[string]any{"test": "data"}
	err := storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	assert.True(t, storage.Exists(sessionID))

	err = storage.Delete(sessionID)
	require.NoError(t, err)

	assert.False(t, storage.Exists(sessionID))
}

func TestStorage_Exists(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("test-session-789")

	assert.False(t, storage.Exists(sessionID))

	update := map[string]any{"test": "data"}
	err := storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	assert.True(t, storage.Exists(sessionID))
}

func TestStorage_Close(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	for i := 0; i < 3; i++ {
		sessionID := acptypes.SessionID("session-" + string(rune('a'+i)))
		err := storage.AppendUpdate(sessionID, map[string]any{"n": i})
		require.NoError(t, err)
	}

	assert.Len(t, storage.files, 3)

	err := storage.Close()
	require.NoError(t, err)

	assert.Len(t, storage.files, 0)
}

func TestStorage_LargeUpdate(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

	sessionID := acptypes.SessionID("large-session")

	largeText := make([]byte, 100*1024)
	for i := range largeText {
		largeText[i] = 'a'
	}

	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": string(largeText)},
	}

	err := storage.AppendUpdate(sessionID, update)
	require.NoError(t, err)

	err = storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	require.Len(t, updates, 1)
}

func TestNewStorage(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	storage, err := NewStorage()
	require.NoError(t, err)
	require.NotNil(t, storage)

	expectedPath := filepath.Join(tmpDir, ".kodelet", "acp", "sessions")
	_, err = os.Stat(expectedPath)
	require.NoError(t, err)

	storage.Close()
}

func TestStorage_ConcurrentWritesDifferentSessions(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

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

	// Verify all sessions have correct number of updates
	err := storage.Close()
	require.NoError(t, err)

	for i := 0; i < numSessions; i++ {
		sessionID := acptypes.SessionID("session-" + string(rune('a'+i)))
		updates, err := storage.ReadUpdates(sessionID)
		require.NoError(t, err)
		assert.Len(t, updates, updatesPerSession)
	}
}

func TestStorage_ConcurrentWritesSameSession(t *testing.T) {
	tmpDir := t.TempDir()

	storage := &Storage{
		basePath: tmpDir,
		files:    make(map[acptypes.SessionID]*sessionFile),
	}

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

	err := storage.CloseSession(sessionID)
	require.NoError(t, err)

	updates, err := storage.ReadUpdates(sessionID)
	require.NoError(t, err)
	assert.Len(t, updates, numGoroutines*updatesPerGoroutine)
}
