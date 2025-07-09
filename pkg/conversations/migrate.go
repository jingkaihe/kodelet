package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"go.etcd.io/bbolt"
)

// MigrationResult contains the results of a migration operation
type MigrationResult struct {
	TotalConversations int           `json:"totalConversations"`
	MigratedCount      int           `json:"migratedCount"`
	FailedCount        int           `json:"failedCount"`
	SkippedCount       int           `json:"skippedCount"`
	FailedIDs          []string      `json:"failedIds,omitempty"`
	Duration           time.Duration `json:"duration"`
}

// MigrationOptions contains configuration for migration operations
type MigrationOptions struct {
	DryRun     bool   `json:"dryRun"`     // If true, only validate what would be migrated
	Force      bool   `json:"force"`      // If true, overwrite existing records in target
	BackupPath string `json:"backupPath"` // Path to backup JSON files after migration
	Verbose    bool   `json:"verbose"`    // If true, show detailed progress
}

// DetectJSONConversations checks if there are existing JSON conversations to migrate
func DetectJSONConversations(ctx context.Context, jsonPath string) ([]string, error) {
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		return nil, nil // No JSON directory exists
	}

	var conversationIDs []string

	err := filepath.WalkDir(jsonPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-JSON files
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		// Skip temporary files
		if strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}

		// Extract conversation ID from filename
		filename := d.Name()
		conversationID := strings.TrimSuffix(filename, ".json")

		// Validate this is actually a conversation file by trying to read it
		data, err := os.ReadFile(path)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("path", path).Warn("Failed to read potential conversation file")
			return nil // Continue with other files
		}

		var record ConversationRecord
		if err := json.Unmarshal(data, &record); err != nil {
			logger.G(ctx).WithError(err).WithField("path", path).Warn("Failed to parse conversation file")
			return nil // Continue with other files
		}

		// Validate it has the expected structure
		if record.ID == "" || record.CreatedAt.IsZero() {
			logger.G(ctx).WithField("path", path).Warn("Invalid conversation file structure")
			return nil // Continue with other files
		}

		conversationIDs = append(conversationIDs, conversationID)
		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to scan JSON conversations directory")
	}

	return conversationIDs, nil
}

// MigrateJSONToBBolt migrates conversations from JSON store to BBolt store
func MigrateJSONToBBolt(ctx context.Context, jsonPath, dbPath string, options MigrationOptions) (*MigrationResult, error) {
	startTime := time.Now()

	result := &MigrationResult{
		FailedIDs: make([]string, 0),
	}

	// Detect existing conversations
	conversationIDs, err := DetectJSONConversations(ctx, jsonPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to detect JSON conversations")
	}

	result.TotalConversations = len(conversationIDs)

	if result.TotalConversations == 0 {
		logger.G(ctx).Info("No JSON conversations found to migrate")
		result.Duration = time.Since(startTime)
		return result, nil
	}

	if options.Verbose {
		presenter.Info(fmt.Sprintf("Found %d conversations to migrate", result.TotalConversations))
	}

	// Create JSON store to read from
	jsonStore, err := NewJSONConversationStore(ctx, jsonPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create JSON store")
	}
	defer jsonStore.Close()

	// Load all conversations from JSON store at once
	if options.Verbose {
		presenter.Info("Loading all conversations from JSON store...")
	}

	allConversations, failedLoads := loadAllConversationsFromJSON(ctx, jsonStore, conversationIDs, options)
	result.FailedCount += len(failedLoads)
	result.FailedIDs = append(result.FailedIDs, failedLoads...)

	if len(allConversations) == 0 {
		result.Duration = time.Since(startTime)
		return result, errors.New("no conversations could be loaded from JSON store")
	}

	// If dry run, just validate the conversations can be processed
	if options.DryRun {
		result.MigratedCount = len(allConversations)
		if options.Verbose {
			for _, conv := range allConversations {
				presenter.Info(fmt.Sprintf("✓ Would migrate conversation %s (%s)", conv.ID, conv.Summary))
			}
		}
		result.Duration = time.Since(startTime)
		return result, nil
	}

	// Create BBolt store to write to
	bboltStore, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create BBolt store")
	}
	defer bboltStore.Close()

	// Migrate all conversations in batch
	if options.Verbose {
		presenter.Info("Migrating conversations to BBolt store...")
	}

	migratedCount, failedMigrations := batchMigrateConversations(ctx, bboltStore, allConversations, options)
	result.MigratedCount = migratedCount
	result.FailedCount += len(failedMigrations)
	result.FailedIDs = append(result.FailedIDs, failedMigrations...)

	// Validate migration
	if result.MigratedCount > 0 {
		if err := validateBatchMigration(ctx, bboltStore, allConversations, options); err != nil {
			return nil, errors.Wrap(err, "migration validation failed")
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// loadAllConversationsFromJSON loads all conversation records from JSON store at once
func loadAllConversationsFromJSON(ctx context.Context, jsonStore *JSONConversationStore, conversationIDs []string, options MigrationOptions) ([]ConversationRecord, []string) {
	var conversations []ConversationRecord
	var failedIDs []string

	for i, conversationID := range conversationIDs {
		if options.Verbose && i%100 == 0 {
			presenter.Info(fmt.Sprintf("Loading conversation %d/%d", i+1, len(conversationIDs)))
		}

		record, err := jsonStore.Load(conversationID)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("conversation_id", conversationID).Error("Failed to load conversation from JSON store")
			failedIDs = append(failedIDs, conversationID)
			continue
		}

		conversations = append(conversations, record)
	}

	if options.Verbose {
		presenter.Info(fmt.Sprintf("Successfully loaded %d conversations from JSON store", len(conversations)))
	}

	return conversations, failedIDs
}

// batchMigrateConversations migrates all conversations to BBolt store using a single database connection
func batchMigrateConversations(ctx context.Context, bboltStore *BBoltConversationStore, conversations []ConversationRecord, options MigrationOptions) (int, []string) {
	var failedIDs []string
	migratedCount := 0

	// Use a single database operation to migrate all conversations
	err := bboltStore.withDB(func(db *bbolt.DB) error {
		return db.Update(func(tx *bbolt.Tx) error {
			// Get all buckets
			conversationsBucket := tx.Bucket([]byte("conversations"))
			summariesBucket := tx.Bucket([]byte("summaries"))
			searchBucket := tx.Bucket([]byte("search_index"))

			for i, record := range conversations {
				if options.Verbose && i%100 == 0 {
					presenter.Info(fmt.Sprintf("Migrating conversation %d/%d: %s", i+1, len(conversations), record.ID))
				}

				// Check if conversation already exists (only if not force mode)
				if !options.Force {
					if existing := conversationsBucket.Get([]byte(record.ID)); existing != nil {
						logger.G(ctx).WithField("conversation_id", record.ID).Info("Conversation already exists in BBolt store, skipping")
						continue
					}
				}

				// Save to all three buckets atomically within the transaction
				if err := saveConversationToBuckets(conversationsBucket, summariesBucket, searchBucket, record); err != nil {
					logger.G(ctx).WithError(err).WithField("conversation_id", record.ID).Error("Failed to save conversation to BBolt store")
					failedIDs = append(failedIDs, record.ID)
					continue
				}

				migratedCount++
			}

			return nil
		})
	})

	if err != nil {
		logger.G(ctx).WithError(err).Error("Batch migration transaction failed")
		// If the entire transaction failed, all conversations failed
		for _, conv := range conversations {
			failedIDs = append(failedIDs, conv.ID)
		}
		migratedCount = 0
	}

	if options.Verbose {
		presenter.Info(fmt.Sprintf("Successfully migrated %d conversations to BBolt store", migratedCount))
	}

	return migratedCount, failedIDs
}

// saveConversationToBuckets saves a conversation record to all three BBolt buckets
func saveConversationToBuckets(conversationsBucket, summariesBucket, searchBucket *bbolt.Bucket, record ConversationRecord) error {
	// 1. Save full record
	recordData, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "failed to marshal conversation record")
	}

	// 2. Save summary for efficient listing
	summary := record.ToSummary()
	summaryData, err := json.Marshal(summary)
	if err != nil {
		return errors.Wrap(err, "failed to marshal conversation summary")
	}

	// 3. Atomic writes to all three buckets
	if err := conversationsBucket.Put([]byte(record.ID), recordData); err != nil {
		return errors.Wrap(err, "failed to save conversation record")
	}
	if err := summariesBucket.Put([]byte("conv:"+record.ID), summaryData); err != nil {
		return errors.Wrap(err, "failed to save conversation summary")
	}
	if err := searchBucket.Put([]byte("msg:"+record.ID), []byte(summary.FirstMessage)); err != nil {
		return errors.Wrap(err, "failed to save search index for message")
	}
	if err := searchBucket.Put([]byte("sum:"+record.ID), []byte(summary.Summary)); err != nil {
		return errors.Wrap(err, "failed to save search index for summary")
	}

	return nil
}

// validateBatchMigration validates that all conversations were migrated correctly
func validateBatchMigration(ctx context.Context, bboltStore *BBoltConversationStore, conversations []ConversationRecord, options MigrationOptions) error {
	if options.Verbose {
		presenter.Info("Validating migration...")
	}

	validationErrors := 0

	// Validate using a single database read operation
	err := bboltStore.withDB(func(db *bbolt.DB) error {
		return db.View(func(tx *bbolt.Tx) error {
			conversationsBucket := tx.Bucket([]byte("conversations"))

			for _, originalConv := range conversations {
				// Load from BBolt store
				data := conversationsBucket.Get([]byte(originalConv.ID))
				if data == nil {
					logger.G(ctx).WithField("conversation_id", originalConv.ID).Error("Conversation not found in BBolt store during validation")
					validationErrors++
					continue
				}

				var bboltRecord ConversationRecord
				if err := json.Unmarshal(data, &bboltRecord); err != nil {
					logger.G(ctx).WithError(err).WithField("conversation_id", originalConv.ID).Error("Failed to unmarshal conversation from BBolt store during validation")
					validationErrors++
					continue
				}

				// Compare key fields
				if originalConv.ID != bboltRecord.ID {
					logger.G(ctx).WithField("conversation_id", originalConv.ID).Error("ID mismatch between JSON and BBolt records")
					validationErrors++
					continue
				}

				if originalConv.Summary != bboltRecord.Summary {
					logger.G(ctx).WithField("conversation_id", originalConv.ID).Error("Summary mismatch between JSON and BBolt records")
					validationErrors++
					continue
				}

				if !originalConv.CreatedAt.Equal(bboltRecord.CreatedAt) {
					logger.G(ctx).WithField("conversation_id", originalConv.ID).Error("CreatedAt mismatch between JSON and BBolt records")
					validationErrors++
					continue
				}

				if !jsonMessagesEqual(originalConv.RawMessages, bboltRecord.RawMessages) {
					logger.G(ctx).WithField("conversation_id", originalConv.ID).
						WithField("json_raw", string(originalConv.RawMessages)).
						WithField("bbolt_raw", string(bboltRecord.RawMessages)).
						Error("RawMessages mismatch between JSON and BBolt records")
					validationErrors++
					continue
				}
			}

			return nil
		})
	})

	if err != nil {
		return errors.Wrap(err, "validation database operation failed")
	}

	if validationErrors > 0 {
		return errors.Errorf("validation failed: %d conversations have mismatched data", validationErrors)
	}

	if options.Verbose {
		presenter.Success(fmt.Sprintf("✓ Migration validation passed for %d conversations", len(conversations)))
	}

	return nil
}

// BackupJSONConversations creates a backup of JSON conversations before migration
func BackupJSONConversations(ctx context.Context, jsonPath, backupPath string) error {
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		return nil // No JSON directory to backup
	}

	// Create backup directory
	if err := os.MkdirAll(backupPath, 0755); err != nil {
		return errors.Wrap(err, "failed to create backup directory")
	}

	// Copy all JSON files to backup directory
	err := filepath.WalkDir(jsonPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-JSON files
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		// Skip temporary files
		if strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}

		// Copy file to backup directory
		relativePath, err := filepath.Rel(jsonPath, path)
		if err != nil {
			return err
		}

		backupFilePath := filepath.Join(backupPath, relativePath)
		backupDir := filepath.Dir(backupFilePath)

		if err := os.MkdirAll(backupDir, 0755); err != nil {
			return errors.Wrap(err, "failed to create backup subdirectory")
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrap(err, "failed to read source file")
		}

		if err := os.WriteFile(backupFilePath, data, 0644); err != nil {
			return errors.Wrap(err, "failed to write backup file")
		}

		return nil
	})

	if err != nil {
		return errors.Wrap(err, "failed to backup JSON conversations")
	}

	logger.G(ctx).WithField("backup_path", backupPath).Info("Successfully backed up JSON conversations")
	return nil
}

// PromptForMigration asks the user if they want to migrate existing JSON conversations
func PromptForMigration(ctx context.Context, jsonPath string) (bool, error) {
	conversationIDs, err := DetectJSONConversations(ctx, jsonPath)
	if err != nil {
		return false, err
	}

	if len(conversationIDs) == 0 {
		return false, nil // No conversations to migrate
	}

	presenter.Info(fmt.Sprintf("Found %d existing conversations in JSON format", len(conversationIDs)))
	presenter.Info("Kodelet now uses BBolt for better performance and multi-process support")
	presenter.Info("Would you like to migrate your conversations to the new format?")
	presenter.Info("(Your original JSON files will be backed up)")

	// For now, return true to auto-migrate. In a real implementation,
	// you'd prompt the user for input here
	return true, nil
}

// jsonMessagesEqual compares two JSON messages for semantic equality
// ignoring formatting differences (whitespace, indentation)
func jsonMessagesEqual(json1, json2 json.RawMessage) bool {
	if len(json1) == 0 && len(json2) == 0 {
		return true
	}
	if len(json1) == 0 || len(json2) == 0 {
		return false
	}

	// Parse both JSON messages
	var data1, data2 interface{}

	if err := json.Unmarshal(json1, &data1); err != nil {
		return false
	}
	if err := json.Unmarshal(json2, &data2); err != nil {
		return false
	}

	// Re-marshal both to get normalized JSON
	normalized1, err := json.Marshal(data1)
	if err != nil {
		return false
	}
	normalized2, err := json.Marshal(data2)
	if err != nil {
		return false
	}

	return string(normalized1) == string(normalized2)
}
