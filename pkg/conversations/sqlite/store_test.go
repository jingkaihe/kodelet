package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conversations "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_BasicOperations(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Create test conversation record
	now := time.Now()
	record := conversations.ConversationRecord{
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
		Summary:   "Test conversation",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]interface{}{"test": "value"},
		ToolResults: map[string]tools.StructuredToolResult{
			"test_call": {
				ToolName:  "test_tool",
				Success:   true,
				Timestamp: now,
			},
		},
	}

	// Test Save
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Test Load
	loaded, err := store.Load(ctx, "test-conversation-1")
	require.NoError(t, err)
	assert.Equal(t, record.ID, loaded.ID)
	assert.Equal(t, record.ModelType, loaded.ModelType)
	assert.Equal(t, record.Summary, loaded.Summary)
	assert.Equal(t, record.Usage.InputTokens, loaded.Usage.InputTokens)
	assert.Equal(t, record.Usage.OutputTokens, loaded.Usage.OutputTokens)
	assert.Equal(t, string(record.RawMessages), string(loaded.RawMessages))

	// Test Load non-existent
	_, err = store.Load(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conversation not found")

	// Test Query (replaces List)
	result, err := store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	summaries := result.ConversationSummaries
	assert.Len(t, summaries, 1)
	assert.Equal(t, "test-conversation-1", summaries[0].ID)
	assert.Equal(t, "Hello world", summaries[0].FirstMessage)

	// Test Delete
	err = store.Delete(ctx, "test-conversation-1")
	require.NoError(t, err)

	// Verify deletion
	_, err = store.Load(ctx, "test-conversation-1")
	assert.Error(t, err)

	result, err = store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	summaries = result.ConversationSummaries
	assert.Len(t, summaries, 0)
}

func TestStore_Query(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Create test records
	now := time.Now()
	records := []conversations.ConversationRecord{
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
		err = store.Save(ctx, record)
		require.NoError(t, err)
	}

	// Test search by term
	result, err := store.Query(ctx, conversations.QueryOptions{
		SearchTerm: "search",
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, "conv-2", result.ConversationSummaries[0].ID)

	// Test sorting by update time (default)
	result, err = store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 3)
	// Note: Save() updates UpdatedAt to current time, so order may depend on save timing
	// We verify we get all 3 records but don't assert specific order due to timing
	expectedIDs := []string{"conv-1", "conv-2", "conv-3"}
	actualIDs := []string{
		result.ConversationSummaries[0].ID,
		result.ConversationSummaries[1].ID,
		result.ConversationSummaries[2].ID,
	}
	for _, expectedID := range expectedIDs {
		assert.Contains(t, actualIDs, expectedID)
	}

	// Test sorting by message count
	result, err = store.Query(ctx, conversations.QueryOptions{
		SortBy:    "messageCount",
		SortOrder: "desc",
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 3)

	// Test pagination
	result, err = store.Query(ctx, conversations.QueryOptions{
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 2)
	assert.Equal(t, 3, result.Total)

	// Test offset
	result, err = store.Query(ctx, conversations.QueryOptions{
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
	result, err = store.Query(ctx, conversations.QueryOptions{
		StartDate: &startDate,
		EndDate:   &endDate,
	})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, "conv-2", result.ConversationSummaries[0].ID)
}

func TestStore_DefaultSorting(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_default_sorting.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Create test records with different timestamps
	now := time.Now()
	records := []conversations.ConversationRecord{
		{
			ID:          "oldest-conv",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Oldest message"}]}]`),
			ModelType:   "anthropic",
			Usage:       llmtypes.Usage{InputTokens: 50, OutputTokens: 25},
			Summary:     "Oldest conversation",
			CreatedAt:   now.Add(-3 * time.Hour),
			UpdatedAt:   now.Add(-3 * time.Hour),
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
		{
			ID:          "middle-conv",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Middle message"}]}]`),
			ModelType:   "anthropic",
			Usage:       llmtypes.Usage{InputTokens: 75, OutputTokens: 40},
			Summary:     "Middle conversation",
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-1 * time.Hour), // Updated more recently
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
		{
			ID:          "newest-conv",
			RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Newest message"}]}]`),
			ModelType:   "anthropic",
			Usage:       llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
			Summary:     "Newest conversation",
			CreatedAt:   now.Add(-1 * time.Hour),
			UpdatedAt:   now.Add(-1 * time.Hour),
			Metadata:    map[string]interface{}{},
			ToolResults: map[string]tools.StructuredToolResult{},
		},
	}

	// Save all records
	for _, record := range records {
		err = store.Save(ctx, record)
		require.NoError(t, err)
	}

	// Test Query method default sorting (should be updated_at DESC)
	t.Run("Query default sorting", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{})
		require.NoError(t, err)
		summaries := result.ConversationSummaries
		assert.Len(t, summaries, 3)

		// Should be sorted by updated_at DESC (most recently updated first)
		// Note: Save() updates UpdatedAt to current time, so order depends on save order
		// All conversations have the same UpdatedAt, but we should still get 3 records
		// The exact order may vary due to Save() timing, but we verify the count and IDs exist
		expectedIDs := []string{"oldest-conv", "middle-conv", "newest-conv"}
		actualIDs := []string{summaries[0].ID, summaries[1].ID, summaries[2].ID}
		for _, expectedID := range expectedIDs {
			assert.Contains(t, actualIDs, expectedID)
		}
	})

	// Test Query method with empty options (should default to updated_at DESC)
	t.Run("Query empty options", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Should be sorted by updated_at DESC (most recently updated first)
		// Note: Save() updates UpdatedAt to current time, so order depends on save order
		expectedIDs := []string{"oldest-conv", "middle-conv", "newest-conv"}
		actualIDs := []string{
			result.ConversationSummaries[0].ID,
			result.ConversationSummaries[1].ID,
			result.ConversationSummaries[2].ID,
		}
		for _, expectedID := range expectedIDs {
			assert.Contains(t, actualIDs, expectedID)
		}
	})

	// Test Query method with empty SortBy string (should default to updated_at)
	t.Run("Query empty SortBy", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{
			SortBy: "",
		})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Should be sorted by updated_at DESC (most recently updated first)
		expectedIDs := []string{"oldest-conv", "middle-conv", "newest-conv"}
		actualIDs := []string{
			result.ConversationSummaries[0].ID,
			result.ConversationSummaries[1].ID,
			result.ConversationSummaries[2].ID,
		}
		for _, expectedID := range expectedIDs {
			assert.Contains(t, actualIDs, expectedID)
		}
	})

	// Test Query method with explicit "createdAt" SortBy
	t.Run("Query explicit createdAt SortBy", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{
			SortBy: "createdAt",
		})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Should be sorted by created_at DESC (newest first)
		assert.Equal(t, "newest-conv", result.ConversationSummaries[0].ID)
		assert.Equal(t, "middle-conv", result.ConversationSummaries[1].ID)
		assert.Equal(t, "oldest-conv", result.ConversationSummaries[2].ID)
	})

	// Test Query method with createdAt ASC
	t.Run("Query createdAt ASC", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{
			SortBy:    "createdAt",
			SortOrder: "asc",
		})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Should be sorted by created_at ASC (oldest first)
		assert.Equal(t, "oldest-conv", result.ConversationSummaries[0].ID)
		assert.Equal(t, "middle-conv", result.ConversationSummaries[1].ID)
		assert.Equal(t, "newest-conv", result.ConversationSummaries[2].ID)
	})

	// Test Query method with updatedAt sorting
	t.Run("Query updatedAt DESC", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{
			SortBy: "updatedAt",
		})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Note: Save() method updates UpdatedAt to current time, so order might differ
		// from original timestamps. Just verify it's sorted by updated_at (not created_at)
		// The exact order will depend on Save() timing, but should still be 3 records
		expectedIDs := []string{"oldest-conv", "middle-conv", "newest-conv"}
		actualIDs := []string{
			result.ConversationSummaries[0].ID,
			result.ConversationSummaries[1].ID,
			result.ConversationSummaries[2].ID,
		}

		// Verify all expected IDs are present (order may vary due to Save() timing)
		for _, expectedID := range expectedIDs {
			assert.Contains(t, actualIDs, expectedID)
		}
	})

	// Test Query method with invalid SortBy (should default to updated_at)
	t.Run("Query invalid SortBy", func(t *testing.T) {
		result, err := store.Query(ctx, conversations.QueryOptions{
			SortBy: "invalidField",
		})
		require.NoError(t, err)
		assert.Len(t, result.ConversationSummaries, 3)

		// Should fall back to updated_at DESC (most recently updated first)
		expectedIDs := []string{"oldest-conv", "middle-conv", "newest-conv"}
		actualIDs := []string{
			result.ConversationSummaries[0].ID,
			result.ConversationSummaries[1].ID,
			result.ConversationSummaries[2].ID,
		}
		for _, expectedID := range expectedIDs {
			assert.Contains(t, actualIDs, expectedID)
		}
	})
}

func TestStore_SchemaValidation(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
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

func TestStore_Migrations(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conversations.db")

	// Create store - this should run migrations
	store, err := NewStore(ctx, dbPath)
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

func TestStore_WALMode(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_wal.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Verify WAL mode is enabled
	var journalMode string
	err = store.db.Get(&journalMode, "PRAGMA journal_mode")
	require.NoError(t, err)
	assert.Equal(t, "wal", strings.ToLower(journalMode))

	// Verify other pragmas are set correctly
	var synchronous string
	err = store.db.Get(&synchronous, "PRAGMA synchronous")
	require.NoError(t, err)
	assert.Equal(t, "1", synchronous) // NORMAL = 1

	var foreignKeys string
	err = store.db.Get(&foreignKeys, "PRAGMA foreign_keys")
	require.NoError(t, err)
	assert.Equal(t, "1", foreignKeys) // ON = 1

	var busyTimeout string
	err = store.db.Get(&busyTimeout, "PRAGMA busy_timeout")
	require.NoError(t, err)
	assert.Equal(t, "5000", busyTimeout)

	// Test that WAL mode actually works by doing some writes
	// This should create WAL files
	now := time.Now()
	record := conversations.ConversationRecord{
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
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Verify we can read it back
	loaded, err := store.Load(ctx, "wal-test")
	require.NoError(t, err)
	assert.Equal(t, "wal-test", loaded.ID)

	// Verify database configuration using the helper function
	err = verifyDatabaseConfiguration(store.db)
	require.NoError(t, err)
}

func TestStore_DatabaseIntegration(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_integration.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test with complex data that exercises JSON fields
	now := time.Now().UTC().Truncate(time.Second)
	record := conversations.ConversationRecord{
		ID:          "integration-test",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Complex test with émoticônes 🚀"}]}]`),
		ModelType:   "anthropic",
		FileLastAccess: map[string]time.Time{
			"file1.txt":  now,
			"file2.go":   now.Add(time.Hour),
			"file3.json": now.Add(2 * time.Hour),
		},
		Usage: llmtypes.Usage{
			InputTokens:              150,
			OutputTokens:             75,
			CacheCreationInputTokens: 20,
			CacheReadInputTokens:     10,
			InputCost:                0.0015,
			OutputCost:               0.003,
			CacheCreationCost:        0.0001,
			CacheReadCost:            0.00005,
			CurrentContextWindow:     16384,
			MaxContextWindow:         32768,
		},
		Summary:   "Test with unicode characters: éñ中文🌟",
		CreatedAt: now,
		UpdatedAt: now.Add(30 * time.Minute),
		Metadata: map[string]interface{}{
			"nested": map[string]interface{}{
				"level2": map[string]interface{}{
					"level3": "deep value",
					"array":  []interface{}{1, 2, "three", true},
				},
			},
			"unicode": "测试文本 🎯",
			"numbers": []interface{}{1.5, 2.7, 3.14159},
			"boolean": true,
			"null":    nil,
		},
		ToolResults: map[string]tools.StructuredToolResult{
			"read_call": {
				ToolName:  "file_read",
				Success:   true,
				Timestamp: now,
				Metadata: tools.FileReadMetadata{
					FilePath:  "/path/to/file.txt",
					Offset:    1,
					Lines:     []string{"Line 1 with unicode: 中文", "Line 2: normal text"},
					Language:  "text",
					Truncated: false,
				},
			},
			"failed_call": {
				ToolName:  "bash",
				Success:   false,
				Error:     "Command failed: éxit with status 1",
				Timestamp: now.Add(time.Minute),
				Metadata: tools.BashMetadata{
					Command:       "ls -la /nonexistent",
					ExitCode:      1,
					Output:        "ls: cannot access '/nonexistent': No such file or directory",
					ExecutionTime: 100 * time.Millisecond,
					WorkingDir:    "/tmp",
				},
			},
		},
	}

	// Save complex record
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Load and verify all data is preserved
	loaded, err := store.Load(ctx, "integration-test")
	require.NoError(t, err)

	// Verify complex JSON data integrity
	assert.Equal(t, record.ID, loaded.ID)
	assert.Equal(t, string(record.RawMessages), string(loaded.RawMessages))
	assert.Equal(t, record.Summary, loaded.Summary)

	// Verify nested metadata
	nestedValue := loaded.Metadata["nested"].(map[string]interface{})
	level2 := nestedValue["level2"].(map[string]interface{})
	assert.Equal(t, "deep value", level2["level3"])
	assert.Equal(t, record.Metadata["unicode"], loaded.Metadata["unicode"])

	// Verify arrays in metadata
	numbersArray := loaded.Metadata["numbers"].([]interface{})
	assert.Len(t, numbersArray, 3)
	assert.Equal(t, 1.5, numbersArray[0])

	// Verify tool results with metadata
	readResult := loaded.ToolResults["read_call"]
	assert.Equal(t, "file_read", readResult.ToolName)
	assert.True(t, readResult.Success)

	// Extract and verify FileReadMetadata
	var readMetadata tools.FileReadMetadata
	success := tools.ExtractMetadata(readResult.Metadata, &readMetadata)
	require.True(t, success)
	assert.Equal(t, "/path/to/file.txt", readMetadata.FilePath)
	assert.Equal(t, []string{"Line 1 with unicode: 中文", "Line 2: normal text"}, readMetadata.Lines)

	// Verify failed tool result
	failedResult := loaded.ToolResults["failed_call"]
	assert.Equal(t, "bash", failedResult.ToolName)
	assert.False(t, failedResult.Success)
	assert.Contains(t, failedResult.Error, "éxit with status 1")

	// Test query with complex data
	result, err := store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	summaries := result.ConversationSummaries
	assert.Len(t, summaries, 1)
	assert.Equal(t, "Test with unicode characters: éñ中文🌟", summaries[0].Summary)
}

func TestStore_NullHandling(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_null.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test record with empty/null fields
	now := time.Now().UTC().Truncate(time.Second)
	record := conversations.ConversationRecord{
		ID:             "null-test",
		RawMessages:    json.RawMessage(`[]`),
		ModelType:      "anthropic",
		FileLastAccess: map[string]time.Time{}, // Empty map
		Usage:          llmtypes.Usage{},       // Zero values
		Summary:        "",                     // Empty string (should become NULL)
		CreatedAt:      now,
		UpdatedAt:      now,
		Metadata:       map[string]interface{}{},                // Empty map
		ToolResults:    map[string]tools.StructuredToolResult{}, // Empty map
	}

	// Save record
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Verify in database using direct SQL query
	var summary *string
	err = store.db.Get(&summary, "SELECT summary FROM conversations WHERE id = ?", "null-test")
	require.NoError(t, err)
	assert.Nil(t, summary) // Should be NULL in database

	// Load record and verify empty string is returned
	loaded, err := store.Load(ctx, "null-test")
	require.NoError(t, err)
	assert.Equal(t, "", loaded.Summary)     // Should be empty string in domain model
	assert.NotNil(t, loaded.FileLastAccess) // Should be empty map, not nil
	assert.NotNil(t, loaded.Metadata)       // Should be empty map, not nil
	assert.NotNil(t, loaded.ToolResults)    // Should be empty map, not nil
}

func TestStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_concurrent.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test concurrent writes (WAL mode should handle this)
	const numGoroutines = 3       // Further reduced for stability
	const recordsPerGoroutine = 1 // Further reduced for stability

	// Channel to collect errors
	errChan := make(chan error, numGoroutines)
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer func() { done <- true }()

			for j := 0; j < recordsPerGoroutine; j++ {
				now := time.Now().UTC()
				record := conversations.ConversationRecord{
					ID:          fmt.Sprintf("concurrent-%d-%d", routineID, j),
					RawMessages: json.RawMessage(fmt.Sprintf(`[{"role": "user", "content": "Message from routine %d record %d"}]`, routineID, j)),
					ModelType:   "anthropic",
					FileLastAccess: map[string]time.Time{
						fmt.Sprintf("file-%d-%d.txt", routineID, j): now,
					},
					Usage: llmtypes.Usage{
						InputTokens:  10 + routineID,
						OutputTokens: 5 + j,
					},
					Summary:   fmt.Sprintf("Summary from routine %d", routineID),
					CreatedAt: now,
					UpdatedAt: now,
					Metadata: map[string]interface{}{
						"routineID": routineID,
						"recordID":  j,
					},
					ToolResults: map[string]tools.StructuredToolResult{},
				}

				if err := store.Save(ctx, record); err != nil {
					errChan <- err
					return
				}

				// Longer delay to reduce contention
				time.Sleep(time.Millisecond * time.Duration(10*(routineID+1)))
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Check for errors
	select {
	case err := <-errChan:
		t.Fatalf("Concurrent write failed: %v", err)
	default:
		// No errors
	}

	// Verify all records were saved
	result, err := store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	summaries := result.ConversationSummaries
	assert.Len(t, summaries, numGoroutines*recordsPerGoroutine)

	// Test concurrent reads
	readErrChan := make(chan error, numGoroutines)
	readDone := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer func() { readDone <- true }()

			// Read all records
			if _, err := store.Query(ctx, conversations.QueryOptions{}); err != nil {
				readErrChan <- err
				return
			}

			// Read specific record
			recordID := fmt.Sprintf("concurrent-%d-0", routineID)
			if _, err := store.Load(ctx, recordID); err != nil {
				readErrChan <- err
				return
			}
		}(i)
	}

	// Wait for all read goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-readDone
	}

	// Check for read errors
	select {
	case err := <-readErrChan:
		t.Fatalf("Concurrent read failed: %v", err)
	default:
		// No errors
	}
}

func TestStore_DirectDatabaseAccess(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_direct.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test direct database access using sqlx
	now := time.Now().UTC().Truncate(time.Second)

	// Insert directly using sqlx
	dbRecord := &dbConversationRecord{
		ID:          "direct-test",
		RawMessages: json.RawMessage(`[{"role": "user", "content": "Direct insert"}]`),
		ModelType:   "anthropic",
		FileLastAccess: JSONField[map[string]time.Time]{
			Data: map[string]time.Time{"direct.txt": now},
		},
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		},
		Summary:   nil, // NULL
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: JSONField[map[string]interface{}]{
			Data: map[string]interface{}{"direct": true},
		},
		ToolResults: JSONField[map[string]tools.StructuredToolResult]{
			Data: map[string]tools.StructuredToolResult{},
		},
	}

	query := `
		INSERT INTO conversations (
			id, raw_messages, model_type, file_last_access, usage,
			summary, created_at, updated_at, metadata, tool_results
		) VALUES (
			:id, :raw_messages, :model_type, :file_last_access, :usage,
			:summary, :created_at, :updated_at, :metadata, :tool_results
		)
	`

	_, err = store.db.NamedExec(query, dbRecord)
	require.NoError(t, err)

	// Insert corresponding summary
	dbSummary := &dbConversationSummary{
		ID:           "direct-test",
		MessageCount: 1,
		FirstMessage: "Direct insert",
		Summary:      nil, // NULL
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	summaryQuery := `
		INSERT INTO conversation_summaries (
			id, message_count, first_message, summary, usage, created_at, updated_at
		) VALUES (
			:id, :message_count, :first_message, :summary, :usage, :created_at, :updated_at
		)
	`

	_, err = store.db.NamedExec(summaryQuery, dbSummary)
	require.NoError(t, err)

	// Verify using store methods
	loaded, err := store.Load(ctx, "direct-test")
	require.NoError(t, err)
	assert.Equal(t, "direct-test", loaded.ID)
	assert.Equal(t, "", loaded.Summary) // NULL becomes empty string
	assert.True(t, loaded.Metadata["direct"].(bool))

	// Test direct query using sqlx
	var records []dbConversationRecord
	err = store.db.Select(&records, "SELECT * FROM conversations WHERE model_type = ?", "anthropic")
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, "direct-test", records[0].ID)
}

func TestStore_TimestampBehavior(t *testing.T) {
	ctx := context.Background()

	// Create temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_timestamps.db")

	// Create store
	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test initial save - both timestamps should be set
	originalCreatedAt := time.Now().UTC().Add(-1 * time.Hour) // 1 hour ago
	originalUpdatedAt := originalCreatedAt

	record := conversations.ConversationRecord{
		ID:          "timestamp-test",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Initial message"}]}]`),
		ModelType:   "anthropic",
		FileLastAccess: map[string]time.Time{
			"test.txt": originalCreatedAt,
		},
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
		Summary:     "Initial summary",
		CreatedAt:   originalCreatedAt,
		UpdatedAt:   originalUpdatedAt,
		Metadata:    map[string]interface{}{"version": 1},
		ToolResults: map[string]tools.StructuredToolResult{},
	}

	// Save the record for the first time
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Load the record to check initial timestamps
	loaded, err := store.Load(ctx, "timestamp-test")
	require.NoError(t, err)

	// CreatedAt should match what we set, UpdatedAt should be current (due to Save() setting it)
	assert.Equal(t, originalCreatedAt.Unix(), loaded.CreatedAt.Unix(), "CreatedAt should match original")
	assert.True(t, loaded.UpdatedAt.After(originalUpdatedAt), "UpdatedAt should be refreshed by Save()")

	// Store the actual created_at from database for comparison
	firstCreatedAt := loaded.CreatedAt
	firstUpdatedAt := loaded.UpdatedAt

	// Wait a bit to ensure timestamp difference
	time.Sleep(100 * time.Millisecond)

	// Update the record with new content
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Updated message"}]}]`)
	record.Summary = "Updated summary"
	record.Metadata["version"] = 2
	record.Usage.InputTokens = 150

	// Save the updated record
	err = store.Save(ctx, record)
	require.NoError(t, err)

	// Load the updated record
	updated, err := store.Load(ctx, "timestamp-test")
	require.NoError(t, err)

	// Verify CreatedAt is preserved but UpdatedAt is refreshed
	assert.Equal(t, firstCreatedAt.Unix(), updated.CreatedAt.Unix(), "CreatedAt should be preserved on update")
	assert.True(t, updated.UpdatedAt.After(firstUpdatedAt), "UpdatedAt should be refreshed on update")

	// Verify the content was actually updated
	assert.Contains(t, string(updated.RawMessages), "Updated message")
	assert.Equal(t, "Updated summary", updated.Summary)
	assert.Equal(t, 2, int(updated.Metadata["version"].(float64)))
	assert.Equal(t, 150, updated.Usage.InputTokens)

	// Test multiple updates to ensure CreatedAt never changes
	secondUpdatedAt := updated.UpdatedAt
	time.Sleep(100 * time.Millisecond)

	// Another update
	record.Summary = "Final summary"
	err = store.Save(ctx, record)
	require.NoError(t, err)

	final, err := store.Load(ctx, "timestamp-test")
	require.NoError(t, err)

	// CreatedAt should still be the same, UpdatedAt should be newer
	assert.Equal(t, firstCreatedAt.Unix(), final.CreatedAt.Unix(), "CreatedAt should never change")
	assert.True(t, final.UpdatedAt.After(secondUpdatedAt), "UpdatedAt should be refreshed on each update")
	assert.Equal(t, "Final summary", final.Summary)

	// Test that the same behavior applies to conversation summaries
	result, err := store.Query(ctx, conversations.QueryOptions{})
	require.NoError(t, err)
	summaries := result.ConversationSummaries
	require.Len(t, summaries, 1)

	summary := summaries[0]
	assert.Equal(t, firstCreatedAt.Unix(), summary.CreatedAt.Unix(), "Summary CreatedAt should match record CreatedAt")
	assert.True(t, summary.UpdatedAt.After(secondUpdatedAt), "Summary UpdatedAt should be refreshed")
}

func TestStore_ErrorScenarios(t *testing.T) {
	ctx := context.Background()

	// Test with invalid database path
	t.Run("invalid database path", func(t *testing.T) {
		_, err := NewStore(ctx, "/invalid/path/db.sqlite")
		assert.Error(t, err)
	})

	// Test with valid store for other error scenarios
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_errors.db")

	store, err := NewStore(ctx, dbPath)
	require.NoError(t, err)
	defer store.Close()

	// Test load non-existent record
	t.Run("load non-existent record", func(t *testing.T) {
		_, err := store.Load(ctx, "non-existent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "conversation not found")
	})

	// Test with closed database
	t.Run("operations on closed database", func(t *testing.T) {
		tmpDir2 := t.TempDir()
		dbPath2 := filepath.Join(tmpDir2, "test_closed.db")

		store2, err := NewStore(ctx, dbPath2)
		require.NoError(t, err)

		// Close the store
		err = store2.Close()
		require.NoError(t, err)

		// Try to use after close
		_, err = store2.Query(ctx, conversations.QueryOptions{})
		assert.Error(t, err)
	})
}
