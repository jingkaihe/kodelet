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
)

func TestBBoltIntegration_ConversationService(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Create BBolt store
	config := &Config{
		StoreType: "bbolt",
		BasePath:  tempDir,
	}

	store, err := NewConversationStore(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
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
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	// Test listing conversations through the service
	req := &ListConversationsRequest{}
	result, err := service.ListConversations(ctx, req)
	if err != nil {
		t.Fatalf("Failed to list conversations: %v", err)
	}

	if len(result.Conversations) != 1 {
		t.Errorf("Expected 1 conversation, got %d", len(result.Conversations))
	}

	// Test getting a conversation through the service
	retrievedRecord, err := service.GetConversation(ctx, record.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if retrievedRecord.ID != record.ID {
		t.Errorf("Expected ID %s, got %s", record.ID, retrievedRecord.ID)
	}

	// Test searching conversations through the service
	searchReq := &ListConversationsRequest{
		SearchTerm: "integration",
	}
	searchResult, err := service.ListConversations(ctx, searchReq)
	if err != nil {
		t.Fatalf("Failed to search conversations: %v", err)
	}

	if len(searchResult.Conversations) != 1 {
		t.Errorf("Expected 1 conversation in search results, got %d", len(searchResult.Conversations))
	}

	// Test conversation statistics
	stats, err := service.GetConversationStatistics(ctx)
	if err != nil {
		t.Fatalf("Failed to get conversation statistics: %v", err)
	}

	if stats.TotalConversations != 1 {
		t.Errorf("Expected 1 total conversation, got %d", stats.TotalConversations)
	}

	// Test deleting a conversation through the service
	err = service.DeleteConversation(ctx, record.ID)
	if err != nil {
		t.Fatalf("Failed to delete conversation: %v", err)
	}

	// Verify deletion
	result, err = service.ListConversations(ctx, &ListConversationsRequest{})
	if err != nil {
		t.Fatalf("Failed to list conversations after delete: %v", err)
	}

	if len(result.Conversations) != 0 {
		t.Errorf("Expected 0 conversations after delete, got %d", len(result.Conversations))
	}
}

func TestBBoltIntegration_EnvironmentOverride(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Set environment variable to use BBolt
	os.Setenv("KODELET_CONVERSATION_STORE_TYPE", "bbolt")
	defer os.Unsetenv("KODELET_CONVERSATION_STORE_TYPE")

	// Override base path for test
	oldBasePath := os.Getenv("KODELET_BASE_PATH")
	os.Setenv("KODELET_BASE_PATH", tempDir)
	defer func() {
		if oldBasePath != "" {
			os.Setenv("KODELET_BASE_PATH", oldBasePath)
		} else {
			os.Unsetenv("KODELET_BASE_PATH")
		}
	}()

	// Create store using environment override
	store, err := GetConversationStore(ctx)
	if err != nil {
		t.Fatalf("Failed to get conversation store: %v", err)
	}
	defer store.Close()

	// Test that it's actually a BBolt store by checking the file exists
	dbPath := filepath.Join(tempDir, "storage.db")

	// Save a conversation to trigger database creation
	record := NewConversationRecord("env-test")
	record.Summary = "Environment override test"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Test"}]}]`)

	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	// Check if database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Expected BBolt database file to exist at %s", dbPath)
	}
}

func TestBBoltIntegration_DefaultConfiguration(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Override base path for test
	oldBasePath := os.Getenv("KODELET_BASE_PATH")
	os.Setenv("KODELET_BASE_PATH", tempDir)
	defer func() {
		if oldBasePath != "" {
			os.Setenv("KODELET_BASE_PATH", oldBasePath)
		} else {
			os.Unsetenv("KODELET_BASE_PATH")
		}
	}()

	// Get default configuration
	config, err := DefaultConfig()
	if err != nil {
		t.Fatalf("Failed to get default config: %v", err)
	}

	// Verify BBolt is the default
	if config.StoreType != "bbolt" {
		t.Errorf("Expected default store type to be 'bbolt', got %s", config.StoreType)
	}

	// Create store with default config
	store, err := NewConversationStore(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create store with default config: %v", err)
	}
	defer store.Close()

	// Test basic functionality
	record := NewConversationRecord("default-test")
	record.Summary = "Default configuration test"
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Test"}]}]`)

	err = store.Save(record)
	if err != nil {
		t.Fatalf("Failed to save conversation: %v", err)
	}

	loadedRecord, err := store.Load("default-test")
	if err != nil {
		t.Fatalf("Failed to load conversation: %v", err)
	}

	if loadedRecord.ID != record.ID {
		t.Errorf("Expected ID %s, got %s", record.ID, loadedRecord.ID)
	}
}

func TestBBoltIntegration_MultipleStoreTypes(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Test BBolt store
	bboltConfig := &Config{
		StoreType: "bbolt",
		BasePath:  tempDir,
	}

	bboltStore, err := NewConversationStore(ctx, bboltConfig)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer bboltStore.Close()

	// Test JSON store
	jsonConfig := &Config{
		StoreType: "json",
		BasePath:  tempDir,
	}

	jsonStore, err := NewConversationStore(ctx, jsonConfig)
	if err != nil {
		t.Fatalf("Failed to create JSON store: %v", err)
	}
	defer jsonStore.Close()

	// Test that both stores work independently
	bboltRecord := NewConversationRecord("bbolt-test")
	bboltRecord.Summary = "BBolt test conversation"
	bboltRecord.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "BBolt test"}]}]`)

	jsonRecord := NewConversationRecord("json-test")
	jsonRecord.Summary = "JSON test conversation"
	jsonRecord.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "JSON test"}]}]`)

	// Save to both stores
	err = bboltStore.Save(bboltRecord)
	if err != nil {
		t.Fatalf("Failed to save to BBolt store: %v", err)
	}

	err = jsonStore.Save(jsonRecord)
	if err != nil {
		t.Fatalf("Failed to save to JSON store: %v", err)
	}

	// Verify they don't interfere with each other
	bboltSummaries, err := bboltStore.List()
	if err != nil {
		t.Fatalf("Failed to list BBolt conversations: %v", err)
	}

	jsonSummaries, err := jsonStore.List()
	if err != nil {
		t.Fatalf("Failed to list JSON conversations: %v", err)
	}

	if len(bboltSummaries) != 1 {
		t.Errorf("Expected 1 BBolt conversation, got %d", len(bboltSummaries))
	}

	if len(jsonSummaries) != 1 {
		t.Errorf("Expected 1 JSON conversation, got %d", len(jsonSummaries))
	}

	// Verify they contain different conversations
	if bboltSummaries[0].ID == jsonSummaries[0].ID {
		t.Error("BBolt and JSON stores should have different conversations")
	}
}

func TestBBoltIntegration_LargeDataset(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "large-test.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
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
		if err != nil {
			t.Fatalf("Failed to save conversation %d: %v", i, err)
		}
	}

	// Test listing all conversations
	summaries, err := store.List()
	if err != nil {
		t.Fatalf("Failed to list conversations: %v", err)
	}

	if len(summaries) != numConversations {
		t.Errorf("Expected %d conversations, got %d", numConversations, len(summaries))
	}

	// Test search functionality
	searchResult, err := store.Query(QueryOptions{
		SearchTerm: "searchable",
	})
	if err != nil {
		t.Fatalf("Failed to search conversations: %v", err)
	}

	expectedSearchResults := len(searchableConversations)
	if len(searchResult.ConversationSummaries) != expectedSearchResults {
		t.Errorf("Expected %d search results, got %d", expectedSearchResults, len(searchResult.ConversationSummaries))
	}

	// Test pagination
	paginatedResult, err := store.Query(QueryOptions{
		Limit:  10,
		Offset: 5,
	})
	if err != nil {
		t.Fatalf("Failed to paginate conversations: %v", err)
	}

	if len(paginatedResult.ConversationSummaries) != 10 {
		t.Errorf("Expected 10 paginated results, got %d", len(paginatedResult.ConversationSummaries))
	}

	if paginatedResult.Total != numConversations {
		t.Errorf("Expected total %d, got %d", numConversations, paginatedResult.Total)
	}
}
