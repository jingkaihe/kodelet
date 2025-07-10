package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"go.etcd.io/bbolt"
)

func TestBBoltConversationStore_CRUD(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	// Test Save
	record := NewConversationRecord("test-id-1")
	record.Summary = "Test conversation"
	record.ModelType = "anthropic"
	record.Usage = llmtypes.Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello world"}]}]`)

	err = store.Save(record)
	require.NoError(t, err, "Failed to save conversation")

	// Test Load
	loadedRecord, err := store.Load("test-id-1")
	require.NoError(t, err, "Failed to load conversation")

	assert.Equal(t, record.ID, loadedRecord.ID)
	assert.Equal(t, record.Summary, loadedRecord.Summary)
	assert.Equal(t, record.ModelType, loadedRecord.ModelType)
	assert.Equal(t, record.Usage.TotalTokens(), loadedRecord.Usage.TotalTokens())

	// Test List
	summaries, err := store.List()
	require.NoError(t, err, "Failed to list conversations")

	assert.Equal(t, 1, len(summaries))
	assert.Equal(t, "test-id-1", summaries[0].ID)

	// Test Delete
	err = store.Delete("test-id-1")
	require.NoError(t, err, "Failed to delete conversation")

	// Verify deletion
	_, err = store.Load("test-id-1")
	assert.Error(t, err, "Expected error when loading deleted conversation")

	summaries, err = store.List()
	require.NoError(t, err, "Failed to list conversations after delete")

	assert.Equal(t, 0, len(summaries))
}

func TestBBoltConversationStore_Query(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)

	// Create test conversations
	conversations := []ConversationRecord{
		{
			ID:          "conv-1",
			Summary:     "First conversation about coding",
			CreatedAt:   yesterday,
			UpdatedAt:   yesterday,
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "How to code in Go?"}]}]`),
		},
		{
			ID:          "conv-2",
			Summary:     "Second conversation about testing",
			CreatedAt:   now,
			UpdatedAt:   now,
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "How to write tests?"}]}]`),
		},
		{
			ID:          "conv-3",
			Summary:     "Third conversation about databases",
			CreatedAt:   tomorrow,
			UpdatedAt:   tomorrow,
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "How to use BoltDB?"}]}]`),
		},
	}

	// Save all conversations
	for _, conv := range conversations {
		err = store.Save(conv)
		require.NoError(t, err, "Failed to save conversation")
	}

	// Test basic query (no filters)
	result, err := store.Query(QueryOptions{})
	require.NoError(t, err, "Failed to query conversations")

	assert.Equal(t, 3, len(result.ConversationSummaries))
	assert.Equal(t, 3, result.Total)

	// Test search query
	result, err = store.Query(QueryOptions{
		SearchTerm: "coding",
	})
	require.NoError(t, err, "Failed to query conversations with search")

	assert.Equal(t, 1, len(result.ConversationSummaries))
	assert.Equal(t, "conv-1", result.ConversationSummaries[0].ID)

	// Test search in first message
	result, err = store.Query(QueryOptions{
		SearchTerm: "BoltDB",
	})
	require.NoError(t, err, "Failed to query conversations with message search")

	assert.Equal(t, 1, len(result.ConversationSummaries))
	assert.Equal(t, "conv-3", result.ConversationSummaries[0].ID)

	// Test date filtering
	result, err = store.Query(QueryOptions{
		StartDate: &now,
		EndDate:   &tomorrow,
	})
	require.NoError(t, err, "Failed to query conversations with date filter")

	assert.Equal(t, 2, len(result.ConversationSummaries))

	// Test pagination
	result, err = store.Query(QueryOptions{
		Limit:  2,
		Offset: 1,
	})
	require.NoError(t, err, "Failed to query conversations with pagination")

	assert.Equal(t, 2, len(result.ConversationSummaries))

	// Test sorting
	result, err = store.Query(QueryOptions{
		SortBy:    "createdAt",
		SortOrder: "asc",
	})
	require.NoError(t, err, "Failed to query conversations with sorting")

	assert.Equal(t, 3, len(result.ConversationSummaries))

	// Check ascending order
	assert.Equal(t, "conv-1", result.ConversationSummaries[0].ID)
	assert.Equal(t, "conv-3", result.ConversationSummaries[2].ID)
}

func TestBBoltConversationStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	// Test concurrent saves
	done := make(chan bool)
	errors := make(chan error)

	// Launch 10 concurrent goroutines
	for i := 0; i < 10; i++ {
		go func(id int) {
			record := NewConversationRecord("")
			record.ID = fmt.Sprintf("concurrent-test-%d", id)
			record.Summary = fmt.Sprintf("Concurrent test %d", id)
			record.RawMessages = json.RawMessage(fmt.Sprintf(`[{"role": "user", "content": [{"type": "text", "text": "Test message %d"}]}]`, id))

			if err := store.Save(record); err != nil {
				errors <- err
				return
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		select {
		case <-done:
			// Success
		case err := <-errors:
			require.Fail(t, "Concurrent save failed", err.Error())
		case <-time.After(5 * time.Second):
			require.Fail(t, "Concurrent save timed out")
		}
	}

	// Verify all conversations were saved
	summaries, err := store.List()
	require.NoError(t, err, "Failed to list conversations after concurrent saves")

	assert.Equal(t, 10, len(summaries))
}

func TestBBoltConversationStore_DatabasePersistence(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Create store and save a conversation
	store1, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")

	record := NewConversationRecord("persistence-test")
	record.Summary = "Test persistence"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello persistence"}]}]`)

	err = store1.Save(record)
	require.NoError(t, err, "Failed to save conversation")

	store1.Close()

	// Reopen database and verify data persisted
	store2, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to reopen BBolt store")
	defer store2.Close()

	loadedRecord, err := store2.Load("persistence-test")
	require.NoError(t, err, "Failed to load conversation from reopened store")

	assert.Equal(t, record.ID, loadedRecord.ID)
	assert.Equal(t, record.Summary, loadedRecord.Summary)
}

func TestBBoltConversationStore_ErrorHandling(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	// Test loading non-existent conversation
	_, err = store.Load("non-existent")
	assert.Error(t, err, "Expected error when loading non-existent conversation")

	// Test deleting non-existent conversation (should not error)
	err = store.Delete("non-existent")
	assert.NoError(t, err, "Unexpected error when deleting non-existent conversation")
}

func TestBBoltConversationStore_TripleStorage(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	record := NewConversationRecord("triple-test")
	record.Summary = "Triple storage test"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Test triple storage"}]}]`)

	err = store.Save(record)
	require.NoError(t, err, "Failed to save conversation")

	// Verify data exists in all three buckets
	err = store.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			// Check conversations bucket
			convBucket := tx.Bucket([]byte("conversations"))
			convData := convBucket.Get([]byte("triple-test"))
			assert.NotNil(t, convData, "Conversation data not found in conversations bucket")

			// Check summaries bucket
			summBucket := tx.Bucket([]byte("summaries"))
			summData := summBucket.Get([]byte("conv:triple-test"))
			assert.NotNil(t, summData, "Summary data not found in summaries bucket")

			// Check search index bucket
			searchBucket := tx.Bucket([]byte("search_index"))
			msgData := searchBucket.Get([]byte("msg:triple-test"))
			assert.NotNil(t, msgData, "Message data not found in search index bucket")
			sumData := searchBucket.Get([]byte("sum:triple-test"))
			assert.NotNil(t, sumData, "Summary data not found in search index bucket")

			return nil
		})
	})

	require.NoError(t, err, "Failed to verify triple storage")
}

// Integration test through the service layer
func TestBBoltIntegration_ConversationService(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Create BBolt store
	config := &Config{
		StoreType: "bbolt",
		BasePath:  tempDir,
	}

	store, err := NewConversationStore(ctx, config)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	// Create conversation service
	service := NewConversationService(store)
	defer service.Close()

	// Test creating and saving a conversation directly through the store
	record := NewConversationRecord("")
	record.Summary = "Integration test conversation"
	record.ModelType = "anthropic"
	record.Usage = llmtypes.Usage{
		InputTokens:  100,
		OutputTokens: 50,
		InputCost:    0.001,
		OutputCost:   0.002,
	}
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello integration test"}]}]`)

	err = store.Save(record)
	require.NoError(t, err, "Failed to save conversation")

	// Test listing conversations through the service
	req := &ListConversationsRequest{}
	result, err := service.ListConversations(ctx, req)
	require.NoError(t, err, "Failed to list conversations")

	assert.Equal(t, 1, len(result.Conversations))

	// Test getting a conversation through the service
	retrievedRecord, err := service.GetConversation(ctx, record.ID)
	require.NoError(t, err, "Failed to get conversation")

	assert.Equal(t, record.ID, retrievedRecord.ID)

	// Test searching conversations through the service
	searchReq := &ListConversationsRequest{
		SearchTerm: "integration",
	}
	searchResult, err := service.ListConversations(ctx, searchReq)
	require.NoError(t, err, "Failed to search conversations")

	assert.Equal(t, 1, len(searchResult.Conversations))

	// Test conversation statistics
	stats, err := service.GetConversationStatistics(ctx)
	require.NoError(t, err, "Failed to get conversation statistics")

	assert.Equal(t, 1, stats.TotalConversations)

	// Test deleting a conversation through the service
	err = service.DeleteConversation(ctx, record.ID)
	require.NoError(t, err, "Failed to delete conversation")

	// Verify deletion
	result, err = service.ListConversations(ctx, &ListConversationsRequest{})
	require.NoError(t, err, "Failed to list conversations after delete")

	assert.Equal(t, 0, len(result.Conversations))
}

// Performance test with larger dataset
func TestBBoltIntegration_LargeDataset(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "large-test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer store.Close()

	// Create a larger dataset to test performance
	numConversations := 500
	searchableConversations := make([]string, 0)

	for i := 0; i < numConversations; i++ {
		record := NewConversationRecord("")
		record.ID = fmt.Sprintf("large-test-%d", i)
		record.ModelType = "anthropic"
		record.Usage = llmtypes.Usage{
			InputTokens:  100 + i,
			OutputTokens: 50 + i,
		}

		if i%10 == 0 {
			record.Summary = "This is a searchable conversation for testing"
			record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Searchable test message"}]}]`)
			searchableConversations = append(searchableConversations, record.ID)
		} else {
			record.Summary = fmt.Sprintf("Regular conversation %d", i)
			record.RawMessages = json.RawMessage(fmt.Sprintf(`[{"role": "user", "content": [{"type": "text", "text": "Regular message %d"}]}]`, i))
		}

		record.CreatedAt = time.Now().Add(-time.Duration(numConversations-i) * time.Minute)
		record.UpdatedAt = record.CreatedAt

		err = store.Save(record)
		require.NoError(t, err, "Failed to save conversation")
	}

	// Test listing all conversations
	summaries, err := store.List()
	require.NoError(t, err, "Failed to list conversations")

	assert.Equal(t, numConversations, len(summaries))

	// Test search functionality
	searchResult, err := store.Query(QueryOptions{
		SearchTerm: "searchable",
	})
	require.NoError(t, err, "Failed to search conversations")

	expectedSearchResults := len(searchableConversations)
	assert.Equal(t, expectedSearchResults, len(searchResult.ConversationSummaries))

	// Test pagination
	paginatedResult, err := store.Query(QueryOptions{
		Limit:  10,
		Offset: 5,
	})
	require.NoError(t, err, "Failed to paginate conversations")

	assert.Equal(t, 10, len(paginatedResult.ConversationSummaries))
	assert.Equal(t, numConversations, paginatedResult.Total)
}
