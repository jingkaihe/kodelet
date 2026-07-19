package steer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSteerStoreUsesMigratedSchema(t *testing.T) {
	store, _ := newTestStore(t)

	var tableCount int
	err := store.db.Get(&tableCount, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'steering_messages'
	`)
	require.NoError(t, err)
	assert.Equal(t, 1, tableCount)

	var indexCount int
	err = store.db.Get(&indexCount, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_steering_messages_conversation_id'
	`)
	require.NoError(t, err)
	assert.Equal(t, 1, indexCount)
}

func TestEnqueuePeekAndConsume(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	conversationID := "test-conversation-123"

	alreadyPending, err := store.Enqueue(ctx, conversationID, "First message", nil)
	require.NoError(t, err)
	assert.False(t, alreadyPending)

	alreadyPending, err = store.Enqueue(ctx, conversationID, "Second message", nil)
	require.NoError(t, err)
	assert.True(t, alreadyPending)

	pending, err := store.Peek(ctx, conversationID)
	require.NoError(t, err)
	require.Len(t, pending, 2)
	assert.Equal(t, "user", pending[0].Role)
	assert.Equal(t, "First message", pending[0].Content)
	assert.Equal(t, "Second message", pending[1].Content)
	assert.Empty(t, pending[0].Images)
	assert.WithinDuration(t, time.Now(), pending[0].Timestamp, 5*time.Second)

	consumed, err := store.Consume(ctx, conversationID)
	require.NoError(t, err)
	require.Len(t, consumed, 2)
	assert.Equal(t, "First message", consumed[0].Content)
	assert.Equal(t, "Second message", consumed[1].Content)

	hasPending, err := store.HasPending(ctx, conversationID)
	require.NoError(t, err)
	assert.False(t, hasPending)
}

func TestEnqueuePersistsNormalizedImages(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	images := []string{"/tmp/screenshot.png", "https://example.com/mockup.jpg", "data:image/png;base64,aGVsbG8="}

	_, err := store.Enqueue(ctx, "test-conversation-images", "Use these images", images)
	require.NoError(t, err)

	images[0] = "/tmp/changed.png"
	pending, err := store.Peek(ctx, "test-conversation-images")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, []string{"/tmp/screenshot.png", "https://example.com/mockup.jpg", "data:image/png;base64,aGVsbG8="}, pending[0].Images)
}

func TestEnqueueNormalizesRelativeImagePaths(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	workingDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workingDir))
	defer func() {
		require.NoError(t, os.Chdir(originalWD))
	}()

	_, err = store.Enqueue(ctx, "test-conversation-relative-images", "Use this image", []string{"./screenshot.png"})
	require.NoError(t, err)

	pending, err := store.Peek(ctx, "test-conversation-relative-images")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, []string{filepath.Join(workingDir, "screenshot.png")}, pending[0].Images)
}

func TestNormalizeImageInputsPreservesRemoteAndDataURLs(t *testing.T) {
	normalized, err := normalizeImageInputs([]string{
		" https://example.com/mockup.jpg ",
		"data:image/png;base64,aGVsbG8=",
		"",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://example.com/mockup.jpg",
		"data:image/png;base64,aGVsbG8=",
	}, normalized)
}

func TestQueueSeparatesConversations(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	_, err := store.Enqueue(ctx, "conversation-a", "Message A", nil)
	require.NoError(t, err)
	_, err = store.Enqueue(ctx, "conversation-b", "Message B", nil)
	require.NoError(t, err)

	consumed, err := store.Consume(ctx, "conversation-a")
	require.NoError(t, err)
	require.Len(t, consumed, 1)
	assert.Equal(t, "Message A", consumed[0].Content)

	pending, err := store.Peek(ctx, "conversation-b")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "Message B", pending[0].Content)
}

func TestEmptyQueue(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	pending, err := store.Peek(ctx, "missing-conversation")
	require.NoError(t, err)
	assert.Empty(t, pending)

	consumed, err := store.Consume(ctx, "missing-conversation")
	require.NoError(t, err)
	assert.Empty(t, consumed)

	hasPending, err := store.HasPending(ctx, "missing-conversation")
	require.NoError(t, err)
	assert.False(t, hasPending)
}

func TestConcurrentEnqueueDoesNotLoseMessages(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	const goroutines = 10
	const messagesPerGoroutine = 5

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*messagesPerGoroutine)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				_, err := store.Enqueue(ctx, "test-concurrent", fmt.Sprintf("Message %d-%d", id, j), nil)
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	pending, err := store.Peek(ctx, "test-concurrent")
	require.NoError(t, err)
	assert.Len(t, pending, goroutines*messagesPerGoroutine)
}

func TestCompetingConsumersDoNotDuplicateMessages(t *testing.T) {
	ctx := context.Background()
	firstStore, dbPath := newTestStore(t)
	secondStore, err := NewSteerStore(ctx, WithDBPath(dbPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = secondStore.Close() })

	for i := 0; i < 5; i++ {
		_, err := firstStore.Enqueue(ctx, "test-consumers", fmt.Sprintf("Message %d", i), nil)
		require.NoError(t, err)
	}

	type consumeResult struct {
		messages []Message
		err      error
	}
	resultCh := make(chan consumeResult, 2)
	for _, store := range []*Store{firstStore, secondStore} {
		go func(store *Store) {
			messages, err := store.Consume(ctx, "test-consumers")
			resultCh <- consumeResult{messages: messages, err: err}
		}(store)
	}

	seen := make(map[string]struct{})
	for i := 0; i < 2; i++ {
		result := <-resultCh
		require.NoError(t, result.err)
		for _, message := range result.messages {
			_, duplicate := seen[message.Content]
			assert.False(t, duplicate)
			seen[message.Content] = struct{}{}
		}
	}
	assert.Len(t, seen, 5)
}

func TestConsumeFailureReturnsNoMessages(t *testing.T) {
	ctx := context.Background()
	store, dbPath := newTestStore(t)
	_, err := store.Enqueue(ctx, "test-failure", "Keep me queued", nil)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	messages, err := store.Consume(ctx, "test-failure")
	require.Error(t, err)
	assert.Empty(t, messages)

	reopened, err := NewSteerStore(ctx, WithDBPath(dbPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	pending, err := reopened.Peek(ctx, "test-failure")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "Keep me queued", pending[0].Content)
}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(ctx, dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(ctx, migrations.All()))
	require.NoError(t, database.Close())

	store, err := NewSteerStore(ctx, WithDBPath(dbPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, dbPath
}
