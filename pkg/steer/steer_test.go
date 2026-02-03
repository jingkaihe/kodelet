package steer

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSteerStore(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)
	assert.NotNil(t, store)
	assert.NotEmpty(t, store.steerDir)
}

func TestWriteAndReadSteer(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "test-conversation-123"
	message := "Test steer message"

	// Write steer
	err = store.WriteSteer(conversationID, message)
	require.NoError(t, err)

	// Read steer
	messages, err := store.ReadPendingSteer(conversationID)
	require.NoError(t, err)
	assert.Len(t, messages, 1)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, message, messages[0].Content)
	assert.WithinDuration(t, time.Now(), messages[0].Timestamp, 5*time.Second)

	// Cleanup
	err = store.ClearPendingSteer(conversationID)
	require.NoError(t, err)
}

func TestMultipleSteerMessages(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "test-conversation-multiple"
	messages := []string{"Message 1", "Message 2", "Message 3"}

	// Write multiple steer messages
	for _, msg := range messages {
		err = store.WriteSteer(conversationID, msg)
		require.NoError(t, err)
	}

	// Read all steer messages
	steerMessages, err := store.ReadPendingSteer(conversationID)
	require.NoError(t, err)
	assert.Len(t, steerMessages, 3)

	for i, msg := range steerMessages {
		assert.Equal(t, "user", msg.Role)
		assert.Equal(t, messages[i], msg.Content)
	}

	// Cleanup
	err = store.ClearPendingSteer(conversationID)
	require.NoError(t, err)
}

func TestHasPendingSteer(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "test-conversation-pending"

	// Initially no steer
	assert.False(t, store.HasPendingSteer(conversationID))

	// Write steer
	err = store.WriteSteer(conversationID, "Test message")
	require.NoError(t, err)

	// Now has steer
	assert.True(t, store.HasPendingSteer(conversationID))

	// Clear steer
	err = store.ClearPendingSteer(conversationID)
	require.NoError(t, err)

	// No longer has steer
	assert.False(t, store.HasPendingSteer(conversationID))
}

func TestReadNonExistentSteer(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "non-existent-conversation"

	// Reading non-existent steer should return empty slice
	messages, err := store.ReadPendingSteer(conversationID)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestClearNonExistentSteer(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "non-existent-conversation"

	// Clearing non-existent steer should not error
	err = store.ClearPendingSteer(conversationID)
	require.NoError(t, err)
}

func TestListSteerFiles(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	// Create some test steer files
	conversationIDs := []string{"test-1", "test-2", "test-3"}
	for _, id := range conversationIDs {
		err = store.WriteSteer(id, "Test message")
		require.NoError(t, err)
	}

	// List steer files
	files, err := store.ListSteerFiles()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 3)

	// Cleanup
	for _, id := range conversationIDs {
		err = store.ClearPendingSteer(id)
		require.NoError(t, err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "test-concurrent"
	numGoroutines := 10
	messagesPerGoroutine := 5

	// Use a channel to coordinate goroutines
	done := make(chan bool, numGoroutines)

	// Start multiple goroutines writing steer
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				err := store.WriteSteer(conversationID, fmt.Sprintf("Message from goroutine %d, iteration %d", id, j))
				assert.NoError(t, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Read all messages
	messages, err := store.ReadPendingSteer(conversationID)
	require.NoError(t, err)
	assert.Len(t, messages, numGoroutines*messagesPerGoroutine)

	// Cleanup
	err = store.ClearPendingSteer(conversationID)
	require.NoError(t, err)
}

func TestGetSteerPath(t *testing.T) {
	store, err := NewSteerStore()
	require.NoError(t, err)

	conversationID := "test-path"
	expectedPath := filepath.Join(store.steerDir, "steer-test-path.json")
	actualPath := store.getSteerPath(conversationID)

	assert.Equal(t, expectedPath, actualPath)
}
