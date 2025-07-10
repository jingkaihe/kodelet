package conversations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectJSONConversations(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kodelet-migrate-test")
	require.NoError(t, err, "Failed to create temp dir")
	defer os.RemoveAll(tempDir)

	ctx := context.Background()

	// Test with no conversations
	conversations, err := DetectJSONConversations(ctx, tempDir)
	require.NoError(t, err, "Failed to detect conversations in empty dir")
	assert.Equal(t, 0, len(conversations), "Expected 0 conversations")

	// Create test conversations
	testConversations := []ConversationRecord{
		{
			ID:          "test-conv-1",
			CreatedAt:   time.Now().Add(-24 * time.Hour),
			UpdatedAt:   time.Now().Add(-24 * time.Hour),
			Summary:     "Test conversation 1",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]}]`),
		},
		{
			ID:          "test-conv-2",
			CreatedAt:   time.Now().Add(-12 * time.Hour),
			UpdatedAt:   time.Now().Add(-12 * time.Hour),
			Summary:     "Test conversation 2",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hi there"}]}]`),
		},
	}

	// Write test conversations to JSON files
	for _, conv := range testConversations {
		data, err := json.MarshalIndent(conv, "", "  ")
		require.NoError(t, err, "Failed to marshal conversation")

		filePath := filepath.Join(tempDir, conv.ID+".json")
		err = os.WriteFile(filePath, data, 0644)
		require.NoError(t, err, "Failed to write conversation file")
	}

	// Test detection with conversations
	conversations, err = DetectJSONConversations(ctx, tempDir)
	require.NoError(t, err, "Failed to detect conversations")

	assert.Equal(t, 2, len(conversations), "Expected 2 conversations")

	// Verify conversation IDs
	expectedIDs := map[string]bool{"test-conv-1": true, "test-conv-2": true}
	for _, id := range conversations {
		assert.True(t, expectedIDs[id], "Unexpected conversation ID: %s", id)
		delete(expectedIDs, id)
	}
	assert.Empty(t, expectedIDs, "Missing conversation IDs")
}

func TestMigrateJSONToBBolt(t *testing.T) {
	// Create temporary directories for test
	tempDir, err := os.MkdirTemp("", "kodelet-migrate-test")
	require.NoError(t, err, "Failed to create temp dir")
	defer os.RemoveAll(tempDir)

	jsonPath := filepath.Join(tempDir, "json")
	dbPath := filepath.Join(tempDir, "test.db")
	backupPath := filepath.Join(tempDir, "backup")

	err = os.MkdirAll(jsonPath, 0755)
	require.NoError(t, err, "Failed to create JSON directory")

	ctx := context.Background()

	// Create test conversations
	testConversations := []ConversationRecord{
		{
			ID:          "migrate-test-1",
			CreatedAt:   time.Now().Add(-24 * time.Hour),
			UpdatedAt:   time.Now().Add(-24 * time.Hour),
			Summary:     "Migration test conversation 1",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Test migration 1"}]}]`),
		},
		{
			ID:          "migrate-test-2",
			CreatedAt:   time.Now().Add(-12 * time.Hour),
			UpdatedAt:   time.Now().Add(-12 * time.Hour),
			Summary:     "Migration test conversation 2",
			RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Test migration 2"}]}]`),
		},
	}

	// Write test conversations to JSON files
	for _, conv := range testConversations {
		data, err := json.MarshalIndent(conv, "", "  ")
		require.NoError(t, err, "Failed to marshal conversation")

		filePath := filepath.Join(jsonPath, conv.ID+".json")
		err = os.WriteFile(filePath, data, 0644)
		require.NoError(t, err, "Failed to write conversation file")
	}

	// Test dry run migration
	dryRunOptions := MigrationOptions{
		DryRun:     true,
		Force:      false,
		BackupPath: backupPath,
		Verbose:    false,
	}

	result, err := MigrateJSONToBBolt(ctx, jsonPath, dbPath, dryRunOptions)
	require.NoError(t, err, "Dry run migration failed")

	assert.Equal(t, 2, result.TotalConversations, "Expected 2 total conversations")
	assert.Equal(t, 2, result.MigratedCount, "Expected 2 migrated conversations in dry run")
	assert.Equal(t, 0, result.FailedCount, "Expected 0 failed conversations in dry run")

	// Verify BBolt database doesn't exist after dry run
	_, err = os.Stat(dbPath)
	assert.Error(t, err, "BBolt database should not exist after dry run")

	// Test actual migration
	actualOptions := MigrationOptions{
		DryRun:     false,
		Force:      false,
		BackupPath: backupPath,
		Verbose:    false,
	}

	result, err = MigrateJSONToBBolt(ctx, jsonPath, dbPath, actualOptions)
	require.NoError(t, err, "Actual migration failed")

	assert.Equal(t, 2, result.TotalConversations, "Expected 2 total conversations")
	assert.Equal(t, 2, result.MigratedCount, "Expected 2 migrated conversations")
	assert.Equal(t, 0, result.FailedCount, "Expected 0 failed conversations")

	// Verify BBolt database exists
	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "BBolt database should exist after migration")

	// Verify conversations in BBolt database
	bboltStore, err := NewBBoltConversationStore(ctx, dbPath)
	require.NoError(t, err, "Failed to create BBolt store")
	defer bboltStore.Close()

	for _, originalConv := range testConversations {
		migratedConv, err := bboltStore.Load(originalConv.ID)
		if !assert.NoError(t, err, "Failed to load migrated conversation %s", originalConv.ID) {
			continue
		}

		// Verify key fields
		assert.Equal(t, originalConv.ID, migratedConv.ID, "Conversation ID mismatch")
		assert.Equal(t, originalConv.Summary, migratedConv.Summary, "Summary mismatch for %s", originalConv.ID)
		assert.True(t, migratedConv.CreatedAt.Equal(originalConv.CreatedAt), "CreatedAt mismatch for %s: expected %v, got %v", originalConv.ID, originalConv.CreatedAt, migratedConv.CreatedAt)
		assert.Equal(t, string(originalConv.RawMessages), string(migratedConv.RawMessages), "RawMessages mismatch for %s", originalConv.ID)
	}
}

func TestBackupJSONConversations(t *testing.T) {
	// Create temporary directories for test
	tempDir, err := os.MkdirTemp("", "kodelet-backup-test")
	require.NoError(t, err, "Failed to create temp dir")
	defer os.RemoveAll(tempDir)

	jsonPath := filepath.Join(tempDir, "json")
	backupPath := filepath.Join(tempDir, "backup")

	err = os.MkdirAll(jsonPath, 0755)
	require.NoError(t, err, "Failed to create JSON directory")

	ctx := context.Background()

	// Create test conversation
	testConv := ConversationRecord{
		ID:          "backup-test",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Summary:     "Backup test conversation",
		RawMessages: json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Test backup"}]}]`),
	}

	// Write test conversation to JSON file
	data, err := json.MarshalIndent(testConv, "", "  ")
	require.NoError(t, err, "Failed to marshal conversation")

	originalFile := filepath.Join(jsonPath, testConv.ID+".json")
	err = os.WriteFile(originalFile, data, 0644)
	require.NoError(t, err, "Failed to write conversation file")

	// Test backup
	err = BackupJSONConversations(ctx, jsonPath, backupPath)
	require.NoError(t, err, "Backup failed")

	// Verify backup was created
	backupFile := filepath.Join(backupPath, testConv.ID+".json")
	_, err = os.Stat(backupFile)
	assert.NoError(t, err, "Backup file should exist")

	// Verify backup content matches original
	originalData, err := os.ReadFile(originalFile)
	require.NoError(t, err, "Failed to read original file")

	backupData, err := os.ReadFile(backupFile)
	require.NoError(t, err, "Failed to read backup file")

	assert.Equal(t, string(originalData), string(backupData), "Backup content doesn't match original")
}
