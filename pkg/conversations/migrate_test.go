package conversations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectJSONConversations(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kodelet-migrate-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()

	// Test with no conversations
	conversations, err := DetectJSONConversations(ctx, tempDir)
	if err != nil {
		t.Fatalf("Failed to detect conversations in empty dir: %v", err)
	}
	if len(conversations) != 0 {
		t.Errorf("Expected 0 conversations, got %d", len(conversations))
	}

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
		if err != nil {
			t.Fatalf("Failed to marshal conversation: %v", err)
		}

		filePath := filepath.Join(tempDir, conv.ID+".json")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatalf("Failed to write conversation file: %v", err)
		}
	}

	// Test detection with conversations
	conversations, err = DetectJSONConversations(ctx, tempDir)
	if err != nil {
		t.Fatalf("Failed to detect conversations: %v", err)
	}

	if len(conversations) != 2 {
		t.Errorf("Expected 2 conversations, got %d", len(conversations))
	}

	// Verify conversation IDs
	expectedIDs := map[string]bool{"test-conv-1": true, "test-conv-2": true}
	for _, id := range conversations {
		if !expectedIDs[id] {
			t.Errorf("Unexpected conversation ID: %s", id)
		}
		delete(expectedIDs, id)
	}
	if len(expectedIDs) > 0 {
		t.Errorf("Missing conversation IDs: %v", expectedIDs)
	}
}

func TestMigrateJSONToBBolt(t *testing.T) {
	// Create temporary directories for test
	tempDir, err := os.MkdirTemp("", "kodelet-migrate-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jsonPath := filepath.Join(tempDir, "json")
	dbPath := filepath.Join(tempDir, "test.db")
	backupPath := filepath.Join(tempDir, "backup")

	if err := os.MkdirAll(jsonPath, 0755); err != nil {
		t.Fatalf("Failed to create JSON directory: %v", err)
	}

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
		if err != nil {
			t.Fatalf("Failed to marshal conversation: %v", err)
		}

		filePath := filepath.Join(jsonPath, conv.ID+".json")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatalf("Failed to write conversation file: %v", err)
		}
	}

	// Test dry run migration
	dryRunOptions := MigrationOptions{
		DryRun:     true,
		Force:      false,
		BackupPath: backupPath,
		Verbose:    false,
	}

	result, err := MigrateJSONToBBolt(ctx, jsonPath, dbPath, dryRunOptions)
	if err != nil {
		t.Fatalf("Dry run migration failed: %v", err)
	}

	if result.TotalConversations != 2 {
		t.Errorf("Expected 2 total conversations, got %d", result.TotalConversations)
	}
	if result.MigratedCount != 2 {
		t.Errorf("Expected 2 migrated conversations in dry run, got %d", result.MigratedCount)
	}
	if result.FailedCount != 0 {
		t.Errorf("Expected 0 failed conversations in dry run, got %d", result.FailedCount)
	}

	// Verify BBolt database doesn't exist after dry run
	if _, err := os.Stat(dbPath); err == nil {
		t.Error("BBolt database should not exist after dry run")
	}

	// Test actual migration
	actualOptions := MigrationOptions{
		DryRun:     false,
		Force:      false,
		BackupPath: backupPath,
		Verbose:    false,
	}

	result, err = MigrateJSONToBBolt(ctx, jsonPath, dbPath, actualOptions)
	if err != nil {
		t.Fatalf("Actual migration failed: %v", err)
	}

	if result.TotalConversations != 2 {
		t.Errorf("Expected 2 total conversations, got %d", result.TotalConversations)
	}
	if result.MigratedCount != 2 {
		t.Errorf("Expected 2 migrated conversations, got %d", result.MigratedCount)
	}
	if result.FailedCount != 0 {
		t.Errorf("Expected 0 failed conversations, got %d", result.FailedCount)
	}

	// Verify BBolt database exists
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("BBolt database should exist after migration: %v", err)
	}

	// Verify conversations in BBolt database
	bboltStore, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer bboltStore.Close()

	for _, originalConv := range testConversations {
		migratedConv, err := bboltStore.Load(originalConv.ID)
		if err != nil {
			t.Errorf("Failed to load migrated conversation %s: %v", originalConv.ID, err)
			continue
		}

		// Verify key fields
		if migratedConv.ID != originalConv.ID {
			t.Errorf("Conversation ID mismatch: expected %s, got %s", originalConv.ID, migratedConv.ID)
		}
		if migratedConv.Summary != originalConv.Summary {
			t.Errorf("Summary mismatch for %s: expected %s, got %s", originalConv.ID, originalConv.Summary, migratedConv.Summary)
		}
		if !migratedConv.CreatedAt.Equal(originalConv.CreatedAt) {
			t.Errorf("CreatedAt mismatch for %s: expected %v, got %v", originalConv.ID, originalConv.CreatedAt, migratedConv.CreatedAt)
		}
		if string(migratedConv.RawMessages) != string(originalConv.RawMessages) {
			t.Errorf("RawMessages mismatch for %s", originalConv.ID)
		}
	}
}

func TestBackupJSONConversations(t *testing.T) {
	// Create temporary directories for test
	tempDir, err := os.MkdirTemp("", "kodelet-backup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jsonPath := filepath.Join(tempDir, "json")
	backupPath := filepath.Join(tempDir, "backup")

	if err := os.MkdirAll(jsonPath, 0755); err != nil {
		t.Fatalf("Failed to create JSON directory: %v", err)
	}

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
	if err != nil {
		t.Fatalf("Failed to marshal conversation: %v", err)
	}

	originalFile := filepath.Join(jsonPath, testConv.ID+".json")
	if err := os.WriteFile(originalFile, data, 0644); err != nil {
		t.Fatalf("Failed to write conversation file: %v", err)
	}

	// Test backup
	err = BackupJSONConversations(ctx, jsonPath, backupPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup was created
	backupFile := filepath.Join(backupPath, testConv.ID+".json")
	if _, err := os.Stat(backupFile); err != nil {
		t.Errorf("Backup file should exist: %v", err)
	}

	// Verify backup content matches original
	originalData, err := os.ReadFile(originalFile)
	if err != nil {
		t.Fatalf("Failed to read original file: %v", err)
	}

	backupData, err := os.ReadFile(backupFile)
	if err != nil {
		t.Fatalf("Failed to read backup file: %v", err)
	}

	if string(originalData) != string(backupData) {
		t.Error("Backup content doesn't match original")
	}
}
