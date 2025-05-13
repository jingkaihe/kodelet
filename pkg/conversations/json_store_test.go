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
