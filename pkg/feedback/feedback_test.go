package feedback

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFeedbackStore(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)
	assert.NotNil(t, store)
	assert.NotEmpty(t, store.feedbackDir)
}

func TestWriteAndReadFeedback(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "test-conversation-123"
	message := "Test feedback message"

	// Write feedback
	err = store.WriteFeedback(conversationID, message)
	require.NoError(t, err)

	// Read feedback
	messages, err := store.ReadPendingFeedback(conversationID)
	require.NoError(t, err)
	assert.Len(t, messages, 1)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, message, messages[0].Content)
	assert.WithinDuration(t, time.Now(), messages[0].Timestamp, 5*time.Second)

	// Cleanup
	err = store.ClearPendingFeedback(conversationID)
	require.NoError(t, err)
}

func TestMultipleFeedbackMessages(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "test-conversation-multiple"
	messages := []string{"Message 1", "Message 2", "Message 3"}

	// Write multiple feedback messages
	for _, msg := range messages {
		err = store.WriteFeedback(conversationID, msg)
		require.NoError(t, err)
	}

	// Read all feedback messages
	feedbackMessages, err := store.ReadPendingFeedback(conversationID)
	require.NoError(t, err)
	assert.Len(t, feedbackMessages, 3)

	for i, msg := range feedbackMessages {
		assert.Equal(t, "user", msg.Role)
		assert.Equal(t, messages[i], msg.Content)
	}

	// Cleanup
	err = store.ClearPendingFeedback(conversationID)
	require.NoError(t, err)
}

func TestHasPendingFeedback(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "test-conversation-pending"

	// Initially no feedback
	assert.False(t, store.HasPendingFeedback(conversationID))

	// Write feedback
	err = store.WriteFeedback(conversationID, "Test message")
	require.NoError(t, err)

	// Now has feedback
	assert.True(t, store.HasPendingFeedback(conversationID))

	// Clear feedback
	err = store.ClearPendingFeedback(conversationID)
	require.NoError(t, err)

	// No longer has feedback
	assert.False(t, store.HasPendingFeedback(conversationID))
}

func TestReadNonExistentFeedback(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "non-existent-conversation"

	// Reading non-existent feedback should return empty slice
	messages, err := store.ReadPendingFeedback(conversationID)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestClearNonExistentFeedback(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "non-existent-conversation"

	// Clearing non-existent feedback should not error
	err = store.ClearPendingFeedback(conversationID)
	require.NoError(t, err)
}

func TestListFeedbackFiles(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	// Create some test feedback files
	conversationIDs := []string{"test-1", "test-2", "test-3"}
	for _, id := range conversationIDs {
		err = store.WriteFeedback(id, "Test message")
		require.NoError(t, err)
	}

	// List feedback files
	files, err := store.ListFeedbackFiles()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 3)

	// Cleanup
	for _, id := range conversationIDs {
		err = store.ClearPendingFeedback(id)
		require.NoError(t, err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "test-concurrent"
	numGoroutines := 10
	messagesPerGoroutine := 5

	// Use a channel to coordinate goroutines
	done := make(chan bool, numGoroutines)

	// Start multiple goroutines writing feedback
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				err := store.WriteFeedback(conversationID, fmt.Sprintf("Message from goroutine %d, iteration %d", id, j))
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
	messages, err := store.ReadPendingFeedback(conversationID)
	require.NoError(t, err)
	assert.Len(t, messages, numGoroutines*messagesPerGoroutine)

	// Cleanup
	err = store.ClearPendingFeedback(conversationID)
	require.NoError(t, err)
}

func TestGetFeedbackPath(t *testing.T) {
	store, err := NewFeedbackStore()
	require.NoError(t, err)

	conversationID := "test-path"
	expectedPath := filepath.Join(store.feedbackDir, "feedback-test-path.json")
	actualPath := store.getFeedbackPath(conversationID)

	assert.Equal(t, expectedPath, actualPath)
}
