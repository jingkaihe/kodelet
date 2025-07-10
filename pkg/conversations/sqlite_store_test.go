package conversations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestSQLiteConversationStore_BasicOperations(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")
	
	// Create store
	store, err := NewSQLiteConversationStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	
	// Create test conversation record
	now := time.Now()
	record := ConversationRecord{
		ID:          "test-conversation-1",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello world"}]}]`),
		ModelType:   "anthropic",
		FileLastAccess: map[string]time.Time{
			"test.txt": now,
		},
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
		Summary:     "Test conversation",
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    map[string]interface{}{"test": "value"},
		ToolResults: map[string]tools.StructuredToolResult{
			"test_call": {
				ToolName:  "test_tool",
				Success:   true,
				Timestamp: now,
			},
		},
	}
	
	// Test Save
	err = store.Save(record)
	require.NoError(t, err)
	
	// Test Load
	loaded, err := store.Load("test-conversation-1")
	require.NoError(t, err)
	assert.Equal(t, record.ID, loaded.ID)
	assert.Equal(t, record.ModelType, loaded.ModelType)
	assert.Equal(t, record.Summary, loaded.Summary)
	assert.Equal(t, record.Usage.InputTokens, loaded.Usage.InputTokens)
	assert.Equal(t, record.Usage.OutputTokens, loaded.Usage.OutputTokens)
	assert.Equal(t, string(record.RawMessages), string(loaded.RawMessages))
	
	// Test Load non-existent
	_, err = store.Load("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conversation not found")
	
	// Test List
	summaries, err := store.List()
	require.NoError(t, err)
	assert.Len(t, summaries, 1)
	assert.Equal(t, "test-conversation-1", summaries[0].ID)
	assert.Equal(t, "Hello world", summaries[0].FirstMessage)
	
	// Test Delete
	err = store.Delete("test-conversation-1")
	require.NoError(t, err)
	
	// Verify deletion
	_, err = store.Load("test-conversation-1")
	assert.Error(t, err)
	
	summaries, err = store.List()
	require.NoError(t, err)
	assert.Len(t, summaries, 0)
}

func TestSQLiteConversationStore_Query(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")
	
	// Create store
	store, err := NewSQLiteConversationStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	
	// Create test records
	now := time.Now()
	records := []ConversationRecord{
		{
			ID:          "conv-1",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello world"}]}]`),
			ModelType:   "anthropic",
			Usage:       llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
			Summary:     "First conversation",
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-2 * time.Hour),
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
		{
			ID:          "conv-2",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Testing search"}]}]`),
			ModelType:   "openai",
			Usage:       llmtypes.Usage{InputTokens: 200, OutputTokens: 100},
			Summary:     "Second conversation",
			CreatedAt:   now.Add(-1 * time.Hour),
			UpdatedAt:   now.Add(-1 * time.Hour),
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
		{
			ID:          "conv-3",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Another message"}]}]`),
			ModelType:   "anthropic",
			Usage:       llmtypes.Usage{InputTokens: 150, OutputTokens: 75},
			Summary:     "Third conversation",
			CreatedAt:   now,
			UpdatedAt:   now,
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
	}
	
	// Save all records
	for _, record := range records {
		err = store.Save(record)
		require.NoError(t, err)
	}
	
	// Test search by term
	result, err := store.Query(QueryOptions{
		SearchTerm: "search",
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, "conv-2", result.ConversationSummaries[0].ID)
	
	// Test sorting by creation time (default)
	result, err = store.Query(QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 3)
	assert.Equal(t, "conv-3", result.ConversationSummaries[0].ID) // Most recent first
	assert.Equal(t, "conv-2", result.ConversationSummaries[1].ID)
	assert.Equal(t, "conv-1", result.ConversationSummaries[2].ID)
	
	// Test sorting by message count
	result, err = store.Query(QueryOptions{
		SortBy:    "messageCount",
		SortOrder: "desc",
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 3)
	
	// Test pagination
	result, err = store.Query(QueryOptions{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 2)
	assert.Equal(t, 3, result.Total)
	
	// Test offset
	result, err = store.Query(QueryOptions{
		Limit:  2,
		Offset: 1,
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 2)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, "conv-2", result.ConversationSummaries[0].ID)
	assert.Equal(t, "conv-1", result.ConversationSummaries[1].ID)
	
	// Test date filtering
	startDate := now.Add(-90 * time.Minute)
	endDate := now.Add(-30 * time.Minute)
	result, err = store.Query(QueryOptions{
		StartDate: &startDate,
		EndDate:   &endDate,
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, "conv-2", result.ConversationSummaries[0].ID)
}

func TestSQLiteConversationStore_SchemaValidation(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")
	
	// Create store
	store, err := NewSQLiteConversationStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	
	// Test schema validation
	err = store.validateSchema()
	require.NoError(t, err)
	
	// Test schema version
	version, err := store.getCurrentSchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, version)
}

func TestSQLiteConversationStore_Migrations(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")
	
	// Create store - this should run migrations
	store, err := NewSQLiteConversationStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	
	// Check that schema version is current
	version, err := store.getCurrentSchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, version)
	
	// Verify all tables exist
	tables := []string{"schema_version", "conversations", "conversation_summaries"}
	for _, table := range tables {
		var exists bool
		err = store.db.QueryRow(`
			SELECT COUNT(*) > 0 
			FROM sqlite_master 
			WHERE type='table' AND name=?
		`, table).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "Table %s should exist", table)
	}
}

func TestSQLiteConversationStore_Factory(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary directory
	tmpDir := t.TempDir()
	
	// Test factory creation with SQLite config
	config := &Config{
		StoreType: "sqlite",
		BasePath:  tmpDir,
	}
	
	store, err := NewConversationStore(ctx, config)
	require.NoError(t, err)
	defer store.Close()
	
	// Verify it's a SQLite store
	sqliteStore, ok := store.(*SQLiteConversationStore)
	assert.True(t, ok)
	assert.NotNil(t, sqliteStore)
	
	// Test basic functionality
	record := ConversationRecord{
		ID:          "factory-test",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Factory test"}]}]`),
		ModelType:   "anthropic",
		Usage:       llmtypes.Usage{InputTokens: 50, OutputTokens: 25},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Metadata:    map[string]interface{}{},
		ToolResults: map[string]tools.StructuredToolResult{},
	}
	
	err = store.Save(record)
	require.NoError(t, err)
	
	loaded, err := store.Load("factory-test")
	require.NoError(t, err)
	assert.Equal(t, "factory-test", loaded.ID)
}

func TestSQLiteConversationStore_WALMode(t *testing.T) {
	ctx := context.Background()
	
	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_wal.db")
	
	// Create store
	store, err := NewSQLiteConversationStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()
	
	// Verify WAL mode is enabled
	var journalMode string
	err = store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", strings.ToLower(journalMode))
	
	// Verify other pragmas are set correctly
	var synchronous string
	err = store.db.QueryRow("PRAGMA synchronous").Scan(&synchronous)
	require.NoError(t, err)
	assert.Equal(t, "1", synchronous) // NORMAL = 1
	
	var foreignKeys string
	err = store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	require.NoError(t, err)
	assert.Equal(t, "1", foreignKeys) // ON = 1
	
	var busyTimeout string
	err = store.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	require.NoError(t, err)
	assert.Equal(t, "5000", busyTimeout)
	
	// Test that WAL mode actually works by doing some writes
	// This should create WAL files
	now := time.Now()
	record := ConversationRecord{
		ID:          "wal-test",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "WAL test"}]}]`),
		ModelType:   "anthropic",
		Usage:       llmtypes.Usage{InputTokens: 10, OutputTokens: 5},
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    map[string]interface{}{},
		ToolResults: map[string]tools.StructuredToolResult{},
	}
	
	// Save record (this should write to WAL)
	err = store.Save(record)
	require.NoError(t, err)
	
	// Verify we can read it back
	loaded, err := store.Load("wal-test")
	require.NoError(t, err)
	assert.Equal(t, "wal-test", loaded.ID)
	
	// Verify database configuration using the helper function
	err = verifyDatabaseConfiguration(store.db)
	require.NoError(t, err)
}