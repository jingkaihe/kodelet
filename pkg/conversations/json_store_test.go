package conversations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDir(t *testing.T) (string, func()) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "kodelet-test-*")
	require.NoError(t, err)

	// Return the temp dir and a cleanup function
	return tempDir, func() {
		os.RemoveAll(tempDir)
	}
}

func TestJSONConversationStore(t *testing.T) {
	tempDir, cleanup := setupTestDir(t)
	defer cleanup()

	// Create a new store
	store, err := NewJSONConversationStore(tempDir)
	require.NoError(t, err)

	// Test Save and Load
	t.Run("SaveAndLoad", func(t *testing.T) {
		record := NewConversationRecord("test-save-load")
		record.Summary = "Hello conversation"
		record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]},{"role":"assistant"}]`)
		record.ModelType = "anthropic"

		// Save the record
		err := store.Save(record)
		assert.NoError(t, err)

		// Check file exists
		filePath := filepath.Join(tempDir, "test-save-load.json")
		_, err = os.Stat(filePath)
		assert.NoError(t, err, "File should exist")

		// Load the record
		loaded, err := store.Load("test-save-load")
		assert.NoError(t, err)

		// Verify contents
		assert.Equal(t, record.ID, loaded.ID, "ID should match")
		assert.Equal(t, "Hello conversation", loaded.Summary, "Summary should match")
		assert.Equal(t, "anthropic", loaded.ModelType, "Model type should match")
		assert.JSONEq(t, string(record.RawMessages), string(loaded.RawMessages), "Raw messages should match")
	})

	// Test error when loading non-existent conversation
	t.Run("LoadNonExistent", func(t *testing.T) {
		_, err := store.Load("non-existent-id")
		assert.Error(t, err, "Should error when loading non-existent conversation")
		assert.Contains(t, err.Error(), "not found", "Error should mention not found")
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		record := NewConversationRecord("test-delete")
		record.Summary = "Delete me"
		record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Delete me"}]}]`)

		// Save and confirm it exists
		err := store.Save(record)
		assert.NoError(t, err)

		filePath := filepath.Join(tempDir, "test-delete.json")
		_, err = os.Stat(filePath)
		assert.NoError(t, err, "File should exist")

		// Delete and confirm it's gone
		err = store.Delete("test-delete")
		assert.NoError(t, err)

		_, err = os.Stat(filePath)
		assert.True(t, os.IsNotExist(err), "File should be deleted")

		// Verify load fails
		_, err = store.Load("test-delete")
		assert.Error(t, err)
	})

	// Test List
	t.Run("List", func(t *testing.T) {
		// Create multiple conversations
		for i := 1; i <= 3; i++ {
			record := NewConversationRecord(fmt.Sprintf("test-list-%d", i))
			record.Summary = fmt.Sprintf("Conversation %d", i)
			record.RawMessages = json.RawMessage(fmt.Sprintf(`[{"role":"user","content":[{"type":"text","text":"Message %d"}]}]`, i))
			err := store.Save(record)
			assert.NoError(t, err)

			// Add a small delay to ensure different timestamps
			time.Sleep(10 * time.Millisecond)
		}

		// List all conversations
		summaries, err := store.List()
		assert.NoError(t, err)

		// We should have at least the 3 we just created, plus any from other tests
		assert.GreaterOrEqual(t, len(summaries), 3, "Should have at least 3 conversations")

		// Verify they're sorted by update time (most recent first)
		for i := 0; i < len(summaries)-1; i++ {
			assert.True(t,
				summaries[i].UpdatedAt.After(summaries[i+1].UpdatedAt) ||
					summaries[i].UpdatedAt.Equal(summaries[i+1].UpdatedAt),
				"Summaries should be sorted by update time, newest first")
		}
	})

	// Test Query
	t.Run("Query", func(t *testing.T) {
		// Create records with specific content for testing search
		recordA := NewConversationRecord("test-query-apple")
		recordA.Summary = "Discussion about apples"
		recordA.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"I like apples"}]}]`)

		recordB := NewConversationRecord("test-query-banana")
		recordB.Summary = "Conversation about bananas"
		recordB.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Bananas are great"}]}]`)

		err := store.Save(recordA)
		assert.NoError(t, err)
		err = store.Save(recordB)
		assert.NoError(t, err)

		// Search for "apple"
		results, err := store.Query(QueryOptions{
			SearchTerm: "apple",
		})
		assert.NoError(t, err)

		// Should find at least the apple record
		found := false
		for _, summary := range results {
			if summary.ID == "test-query-apple" {
				found = true
				break
			}
		}
		assert.True(t, found, "Should find record containing 'apple'")

		// Search with limit
		limitResults, err := store.Query(QueryOptions{
			Limit: 2,
		})
		assert.NoError(t, err)
		assert.LessOrEqual(t, len(limitResults), 2, "Should return at most 2 results")
	})
}

// Mock time for testing
type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	return c.now
}

func (c *fixedClock) Add(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestGetMostRecentConversationID(t *testing.T) {
	// Test the core logic that GetMostRecentConversationID uses.
	// We test the Query functionality with the same parameters that
	// GetMostRecentConversationID uses to ensure it works correctly.

	t.Run("NoConversationsExists", func(t *testing.T) {
		tempDir, cleanup := setupTestDir(t)
		defer cleanup()
		store, err := NewJSONConversationStore(tempDir)
		require.NoError(t, err)
		defer store.Close()

		// Test the same query logic that GetMostRecentConversationID uses
		options := QueryOptions{
			Limit:     1,
			Offset:    0,
			SortBy:    "updated_at", // This is what GetMostRecentConversationID uses
			SortOrder: "desc",
		}

		conversations, err := store.Query(options)
		require.NoError(t, err)

		// When no conversations exist, should return empty result
		assert.Equal(t, 0, len(conversations), "Should return no conversations when none exist")
	})

	t.Run("SingleConversation", func(t *testing.T) {
		tempDir, cleanup := setupTestDir(t)
		defer cleanup()
		store, err := NewJSONConversationStore(tempDir)
		require.NoError(t, err)
		defer store.Close()

		// Create a single conversation
		record := NewConversationRecord("single-conversation")
		record.Summary = "Only conversation"
		record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]}]`)
		
		err = store.Save(record)
		require.NoError(t, err)

		// Test the same query logic that GetMostRecentConversationID uses
		options := QueryOptions{
			Limit:     1,
			Offset:    0,
			SortBy:    "updated_at", // This is what GetMostRecentConversationID uses
			SortOrder: "desc",
		}

		conversations, err := store.Query(options)
		require.NoError(t, err)
		require.Equal(t, 1, len(conversations), "Should return exactly one conversation")
		assert.Equal(t, "single-conversation", conversations[0].ID)
	})

	t.Run("MultipleConversationsByTime", func(t *testing.T) {
		tempDir, cleanup := setupTestDir(t)
		defer cleanup()
		store, err := NewJSONConversationStore(tempDir)
		require.NoError(t, err)
		defer store.Close()

		// Create multiple conversations with different timestamps
		baseTime := time.Now().Add(-1 * time.Hour)
		
		// Oldest conversation
		record1 := NewConversationRecord("oldest-conversation")
		record1.Summary = "Oldest"
		record1.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"First"}]}]`)
		record1.CreatedAt = baseTime
		record1.UpdatedAt = baseTime
		err = store.Save(record1)
		require.NoError(t, err)

		// Middle conversation
		record2 := NewConversationRecord("middle-conversation")
		record2.Summary = "Middle"
		record2.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Second"}]}]`)
		record2.CreatedAt = baseTime.Add(30 * time.Minute)
		record2.UpdatedAt = baseTime.Add(30 * time.Minute)
		err = store.Save(record2)
		require.NoError(t, err)

		// Most recent conversation
		record3 := NewConversationRecord("newest-conversation")
		record3.Summary = "Newest"
		record3.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Third"}]}]`)
		record3.CreatedAt = baseTime.Add(60 * time.Minute)
		record3.UpdatedAt = baseTime.Add(60 * time.Minute)
		err = store.Save(record3)
		require.NoError(t, err)

		// Test the same query logic that GetMostRecentConversationID uses
		options := QueryOptions{
			Limit:     1,
			Offset:    0,
			SortBy:    "updated_at", // This is what GetMostRecentConversationID uses
			SortOrder: "desc",
		}

		conversations, err := store.Query(options)
		require.NoError(t, err)
		require.Equal(t, 1, len(conversations), "Should return exactly one conversation")
		assert.Equal(t, "newest-conversation", conversations[0].ID, "Should return the most recent conversation")
	})

	t.Run("ConversationsSortedByUpdatedAt", func(t *testing.T) {
		tempDir, cleanup := setupTestDir(t)
		defer cleanup()
		store, err := NewJSONConversationStore(tempDir)
		require.NoError(t, err)
		defer store.Close()

		// Create first conversation
		record1 := NewConversationRecord("created-first")
		record1.Summary = "Created first"
		record1.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Created first"}]}]`)
		err = store.Save(record1)
		require.NoError(t, err)

		// Add a small delay to ensure different timestamps
		time.Sleep(50 * time.Millisecond)

		// Create second conversation (will have later UpdatedAt due to the delay)
		record2 := NewConversationRecord("created-second")
		record2.Summary = "Created second"
		record2.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Created second"}]}]`)
		err = store.Save(record2)
		require.NoError(t, err)

		// Test the same query logic that GetMostRecentConversationID uses
		options := QueryOptions{
			Limit:     1,
			Offset:    0,
			SortBy:    "updated_at", // This is what GetMostRecentConversationID uses
			SortOrder: "desc",
		}

		conversations, err := store.Query(options)
		require.NoError(t, err)
		require.Equal(t, 1, len(conversations), "Should return exactly one conversation")
		
		// Since record2 was saved after record1, it should have a more recent UpdatedAt timestamp
		// and should be returned as the most recent conversation
		assert.Equal(t, "created-second", conversations[0].ID, "Should return conversation with most recent updated_at timestamp")
	})
}
