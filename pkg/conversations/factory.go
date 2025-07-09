package conversations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
)

// NewConversationStore creates the appropriate ConversationStore implementation
// based on the provided configuration
func NewConversationStore(ctx context.Context, config *Config) (ConversationStore, error) {
	if config == nil {
		// Use default config if none provided
		var err error
		config, err = DefaultConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
	}

	switch config.StoreType {
	case "json":
		return NewJSONConversationStore(ctx, config.BasePath)
	case "bbolt":
		dbPath := filepath.Join(config.BasePath, "storage.db")

		// Check for automatic migration opportunity
		if shouldAttemptAutoMigration(ctx, config.BasePath, dbPath) {
			if err := attemptAutoMigration(ctx, config.BasePath, dbPath); err != nil {
				logger.G(ctx).WithError(err).Warn("Automatic migration failed, continuing with BBolt store")
			}
		}

		return NewBBoltConversationStore(ctx, dbPath)
	case "sqlite":
		return nil, errors.New("SQLite store not yet implemented")
	default:
		// Default to BBolt store for better performance
		dbPath := filepath.Join(config.BasePath, "storage.db")

		// Check for automatic migration opportunity
		if shouldAttemptAutoMigration(ctx, config.BasePath, dbPath) {
			if err := attemptAutoMigration(ctx, config.BasePath, dbPath); err != nil {
				logger.G(ctx).WithError(err).Warn("Automatic migration failed, continuing with BBolt store")
			}
		}

		return NewBBoltConversationStore(ctx, dbPath)
	}
}

// GetConversationStore is a convenience function that creates a store
// with default configuration
func GetConversationStore(ctx context.Context) (ConversationStore, error) {
	config, err := DefaultConfig()
	if err != nil {
		return nil, err
	}

	// Check for environment variable override
	if storeType := os.Getenv("KODELET_CONVERSATION_STORE_TYPE"); storeType != "" {
		config.StoreType = storeType
	}

	return NewConversationStore(ctx, config)
}

// shouldAttemptAutoMigration determines if automatic migration should be attempted
func shouldAttemptAutoMigration(ctx context.Context, basePath, dbPath string) bool {
	// Only attempt migration if:
	// 1. BBolt database doesn't exist yet
	// 2. JSON conversations directory exists and has conversations

	// Check if BBolt database already exists
	if _, err := os.Stat(dbPath); err == nil {
		return false // Database already exists, no need to migrate
	}

	// Check if JSON conversations exist
	jsonPath := filepath.Join(basePath, "conversations")
	conversationIDs, err := DetectJSONConversations(ctx, jsonPath)
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to detect JSON conversations for auto-migration")
		return false
	}

	return len(conversationIDs) > 0
}

// attemptAutoMigration performs automatic migration with user consent
func attemptAutoMigration(ctx context.Context, basePath, dbPath string) error {
	jsonPath := filepath.Join(basePath, "conversations")

	// Prompt for migration (with auto-approval for now)
	shouldMigrate, err := PromptForMigration(ctx, jsonPath)
	if err != nil {
		return fmt.Errorf("failed to prompt for migration: %w", err)
	}

	if !shouldMigrate {
		logger.G(ctx).Info("User declined automatic migration")
		return nil
	}

	// Create backup path
	backupPath := filepath.Join(basePath, "backup", time.Now().Format("20060102-150405"))

	// Perform migration
	presenter.Info("Migrating conversations to new BBolt format...")

	migrationOptions := MigrationOptions{
		DryRun:     false,
		Force:      false,
		BackupPath: backupPath,
		Verbose:    false, // Keep quiet for automatic migration
	}

	result, err := MigrateJSONToBBolt(ctx, jsonPath, dbPath, migrationOptions)
	if err != nil {
		return fmt.Errorf("automatic migration failed: %w", err)
	}

	// Report results
	if result.MigratedCount > 0 {
		presenter.Success(fmt.Sprintf("Successfully migrated %d conversations to BBolt format", result.MigratedCount))
		presenter.Info(fmt.Sprintf("Original JSON files backed up to: %s", backupPath))

		if result.FailedCount > 0 {
			presenter.Warning(fmt.Sprintf("Failed to migrate %d conversations", result.FailedCount))
		}
	}

	logger.G(ctx).WithField("migrated", result.MigratedCount).
		WithField("failed", result.FailedCount).
		WithField("duration", result.Duration).
		Info("Automatic migration completed")

	return nil
}
