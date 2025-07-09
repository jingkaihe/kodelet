package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"go.etcd.io/bbolt"
)

func TestBBoltConversationStore_CRUD(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
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
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	// Test Load
	loadedRecord, err := store.Load("test-id-1")
	if err != nil {
		t.Fatalf("Failed to load conversation: %v", err)
	}

	if loadedRecord.ID != record.ID {
		t.Errorf("Expected ID %s, got %s", record.ID, loadedRecord.ID)
	}
	if loadedRecord.Summary != record.Summary {
		t.Errorf("Expected summary %s, got %s", record.Summary, loadedRecord.Summary)
	}
	if loadedRecord.ModelType != record.ModelType {
		t.Errorf("Expected model type %s, got %s", record.ModelType, loadedRecord.ModelType)
	}
	if loadedRecord.Usage.TotalTokens() != record.Usage.TotalTokens() {
		t.Errorf("Expected total tokens %d, got %d", record.Usage.TotalTokens(), loadedRecord.Usage.TotalTokens())
	}

	// Test List
	summaries, err := store.List()
	if err != nil {
		t.Fatalf("Failed to list conversations: %v", err)
	}

	if len(summaries) != 1 {
		t.Errorf("Expected 1 conversation, got %d", len(summaries))
	}

	if summaries[0].ID != "test-id-1" {
		t.Errorf("Expected ID test-id-1, got %s", summaries[0].ID)
	}

	// Test Delete
	err = store.Delete("test-id-1")
	if err != nil {
		t.Fatalf("Failed to delete conversation: %v", err)
	}

	// Verify deletion
	_, err = store.Load("test-id-1")
	if err == nil {
		t.Error("Expected error when loading deleted conversation")
	}

	summaries, err = store.List()
	if err != nil {
		t.Fatalf("Failed to list conversations after delete: %v", err)
	}

	if len(summaries) != 0 {
		t.Errorf("Expected 0 conversations after delete, got %d", len(summaries))
	}
}

func TestBBoltConversationStore_Query(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
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
		if err != nil {
			t.Fatalf("Failed to save conversation %s: %v", conv.ID, err)
		}
	}

	// Test basic query (no filters)
	result, err := store.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to query conversations: %v", err)
	}

	if len(result.ConversationSummaries) != 3 {
		t.Errorf("Expected 3 conversations, got %d", len(result.ConversationSummaries))
	}

	if result.Total != 3 {
		t.Errorf("Expected total 3, got %d", result.Total)
	}

	// Test search query
	result, err = store.Query(QueryOptions{
		SearchTerm: "coding",
	})
	if err != nil {
		t.Fatalf("Failed to query conversations with search: %v", err)
	}

	if len(result.ConversationSummaries) != 1 {
		t.Errorf("Expected 1 conversation with 'coding', got %d", len(result.ConversationSummaries))
	}

	if result.ConversationSummaries[0].ID != "conv-1" {
		t.Errorf("Expected conv-1, got %s", result.ConversationSummaries[0].ID)
	}

	// Test search in first message
	result, err = store.Query(QueryOptions{
		SearchTerm: "BoltDB",
	})
	if err != nil {
		t.Fatalf("Failed to query conversations with message search: %v", err)
	}

	if len(result.ConversationSummaries) != 1 {
		t.Errorf("Expected 1 conversation with 'BoltDB', got %d", len(result.ConversationSummaries))
	}

	if result.ConversationSummaries[0].ID != "conv-3" {
		t.Errorf("Expected conv-3, got %s", result.ConversationSummaries[0].ID)
	}

	// Test date filtering
	result, err = store.Query(QueryOptions{
		StartDate: &now,
		EndDate:   &tomorrow,
	})
	if err != nil {
		t.Fatalf("Failed to query conversations with date filter: %v", err)
	}

	if len(result.ConversationSummaries) != 2 {
		t.Errorf("Expected 2 conversations in date range, got %d", len(result.ConversationSummaries))
	}

	// Test pagination
	result, err = store.Query(QueryOptions{
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("Failed to query conversations with pagination: %v", err)
	}

	if len(result.ConversationSummaries) != 2 {
		t.Errorf("Expected 2 conversations with pagination, got %d", len(result.ConversationSummaries))
	}

	// Test sorting
	result, err = store.Query(QueryOptions{
		SortBy:    "createdAt",
		SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("Failed to query conversations with sorting: %v", err)
	}

	if len(result.ConversationSummaries) != 3 {
		t.Errorf("Expected 3 conversations with sorting, got %d", len(result.ConversationSummaries))
	}

	// Check ascending order
	if result.ConversationSummaries[0].ID != "conv-1" {
		t.Errorf("Expected conv-1 first in ascending order, got %s", result.ConversationSummaries[0].ID)
	}
	if result.ConversationSummaries[2].ID != "conv-3" {
		t.Errorf("Expected conv-3 last in ascending order, got %s", result.ConversationSummaries[2].ID)
	}
}

func TestBBoltConversationStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
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
			t.Fatalf("Concurrent save failed: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Concurrent save timed out")
		}
	}

	// Verify all conversations were saved
	summaries, err := store.List()
	if err != nil {
		t.Fatalf("Failed to list conversations after concurrent saves: %v", err)
	}

	if len(summaries) != 10 {
		t.Errorf("Expected 10 conversations after concurrent saves, got %d", len(summaries))
	}
}

func TestBBoltConversationStore_DatabasePersistence(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Create store and save a conversation
	store1, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}

	record := NewConversationRecord("persistence-test")
	record.Summary = "Test persistence"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello persistence"}]}]`)

	err = store1.Save(record)
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	store1.Close()

	// Reopen database and verify data persisted
	store2, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen BBolt store: %v", err)
	}
	defer store2.Close()

	loadedRecord, err := store2.Load("persistence-test")
	if err != nil {
		t.Fatalf("Failed to load conversation from reopened store: %v", err)
	}

	if loadedRecord.ID != record.ID {
		t.Errorf("Expected ID %s, got %s", record.ID, loadedRecord.ID)
	}
	if loadedRecord.Summary != record.Summary {
		t.Errorf("Expected summary %s, got %s", record.Summary, loadedRecord.Summary)
	}
}

func TestBBoltConversationStore_ErrorHandling(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	// Test loading non-existent conversation
	_, err = store.Load("non-existent")
	if err == nil {
		t.Error("Expected error when loading non-existent conversation")
	}

	// Test deleting non-existent conversation (should not error)
	err = store.Delete("non-existent")
	if err != nil {
		t.Errorf("Unexpected error when deleting non-existent conversation: %v", err)
	}
}

func TestBBoltConversationStore_InvalidPath(t *testing.T) {
	ctx := context.Background()

	// Test with invalid path (permission denied)
	if os.Getuid() != 0 { // Skip if running as root
		_, err := NewBBoltConversationStore(ctx, "/root/invalid/path/test.db")
		if err == nil {
			t.Error("Expected error when creating store with invalid path")
		}
	}
}

func TestBBoltConversationStore_TripleStorage(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	record := NewConversationRecord("triple-test")
	record.Summary = "Triple storage test"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Test triple storage"}]}]`)

	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	// Verify data exists in all three buckets
	err = store.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			// Check conversations bucket
			convBucket := tx.Bucket([]byte("conversations"))
			convData := convBucket.Get([]byte("triple-test"))
			if convData == nil {
				t.Error("Conversation data not found in conversations bucket")
			}

			// Check summaries bucket
			summBucket := tx.Bucket([]byte("summaries"))
			summData := summBucket.Get([]byte("conv:triple-test"))
			if summData == nil {
				t.Error("Summary data not found in summaries bucket")
			}

			// Check search index bucket
			searchBucket := tx.Bucket([]byte("search_index"))
			msgData := searchBucket.Get([]byte("msg:triple-test"))
			if msgData == nil {
				t.Error("Message data not found in search index bucket")
			}
			sumData := searchBucket.Get([]byte("sum:triple-test"))
			if sumData == nil {
				t.Error("Summary data not found in search index bucket")
			}

			return nil
		})
	})

	if err != nil {
		t.Fatalf("Failed to verify triple storage: %v", err)
	}
}
