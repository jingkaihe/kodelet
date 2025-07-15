package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/pkg/errors"
)

// Migration represents a database migration
type Migration struct {
	Version     int
	Description string
	Up          func(*sql.Tx) error
	Down        func(*sql.Tx) error // Optional rollback function
}

// migrations contains all database migrations in order
var migrations = []Migration{
	{
		Version:     1,
		Description: "Initial schema creation",
		Up: func(tx *sql.Tx) error {
			// Create schema version table first
			if _, err := tx.Exec(createSchemaVersionTable); err != nil {
				return errors.Wrap(err, "failed to create schema_version table")
			}

			// Create main tables
			if _, err := tx.Exec(createConversationsTable); err != nil {
				return errors.Wrap(err, "failed to create conversations table")
			}

			if _, err := tx.Exec(createConversationSummariesTable); err != nil {
				return errors.Wrap(err, "failed to create conversation_summaries table")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			// Drop tables in reverse order
			if _, err := tx.Exec("DROP TABLE IF EXISTS conversation_summaries"); err != nil {
				return errors.Wrap(err, "failed to drop conversation_summaries table")
			}

			if _, err := tx.Exec("DROP TABLE IF EXISTS conversations"); err != nil {
				return errors.Wrap(err, "failed to drop conversations table")
			}

			if _, err := tx.Exec("DROP TABLE IF EXISTS schema_version"); err != nil {
				return errors.Wrap(err, "failed to drop schema_version table")
			}

			return nil
		},
	},
	{
		Version:     2,
		Description: "Add performance indexes",
		Up: func(tx *sql.Tx) error {
			indexes := []string{
				createIndexConversationsCreatedAt,
				createIndexConversationsUpdatedAt,
				createIndexConversationsProvider,
				createIndexSummariesCreatedAt,
				createIndexSummariesUpdatedAt,
				createIndexSummariesMessageCount,
				createIndexSummariesFirstMessage,
				createIndexSummariesSummary,
			}

			for _, index := range indexes {
				if _, err := tx.Exec(index); err != nil {
					return errors.Wrap(err, "failed to create index")
				}
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			// Drop indexes in reverse order
			dropIndexes := []string{
				dropIndexSummariesSummary,
				dropIndexSummariesFirstMessage,
				dropIndexSummariesMessageCount,
				dropIndexSummariesUpdatedAt,
				dropIndexSummariesCreatedAt,
				dropIndexConversationsProvider,
				dropIndexConversationsUpdatedAt,
				dropIndexConversationsCreatedAt,
			}

			for _, drop := range dropIndexes {
				if _, err := tx.Exec(drop); err != nil {
					return errors.Wrap(err, "failed to drop index")
				}
			}

			return nil
		},
	},
	{
		Version:     3,
		Description: "Add provider to conversation_summaries table",
		Up: func(tx *sql.Tx) error {
			// Add provider column to conversation_summaries table
			if _, err := tx.Exec(addProviderToSummariesTable); err != nil {
				return errors.Wrap(err, "failed to add provider column to conversation_summaries")
			}

			// Create index for provider column
			if _, err := tx.Exec(createIndexSummariesProvider); err != nil {
				return errors.Wrap(err, "failed to create provider index on conversation_summaries")
			}

			// Backfill provider for existing conversation summaries
			// This query updates all existing summaries with the provider from the main conversations table
			_, err := tx.Exec(`
				UPDATE conversation_summaries
				SET provider = (
					SELECT provider
					FROM conversations
					WHERE conversations.id = conversation_summaries.id
				)
				WHERE provider IS NULL
			`)
			if err != nil {
				return errors.Wrap(err, "failed to backfill provider in conversation_summaries")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			// Drop the index first
			if _, err := tx.Exec(dropIndexSummariesProvider); err != nil {
				return errors.Wrap(err, "failed to drop provider index")
			}

			// Note: SQLite doesn't support dropping columns directly
			// We would need to recreate the table to fully rollback
			// For simplicity, we'll just drop the index
			return nil
		},
	},
	{
		Version:     4,
		Description: "Add background_processes to conversations table",
		Up: func(tx *sql.Tx) error {
			// Add background_processes column to conversations table
			if _, err := tx.Exec(addBackgroundProcessesToConversationsTable); err != nil {
				return errors.Wrap(err, "failed to add background_processes column to conversations")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			// Note: SQLite doesn't support dropping columns directly
			// We would need to recreate the table to fully rollback
			// For simplicity, we'll just leave the column (it won't hurt)
			return nil
		},
	},
}

// runMigrations executes all pending migrations
func (s *Store) runMigrations() error {
	currentVersion, err := s.getCurrentSchemaVersion()
	if err != nil {
		return errors.Wrap(err, "failed to get current schema version")
	}

	for _, migration := range migrations {
		if migration.Version > currentVersion {
			if err := s.applyMigration(migration); err != nil {
				return errors.Wrapf(err, "failed to apply migration %d", migration.Version)
			}
		}
	}

	return nil
}

// getCurrentSchemaVersion returns the current schema version
func (s *Store) getCurrentSchemaVersion() (int, error) {
	// Check if schema_version table exists
	var tableExists bool
	err := s.db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM sqlite_master
		WHERE type='table' AND name='schema_version'
	`).Scan(&tableExists)
	if err != nil {
		return 0, errors.Wrap(err, "failed to check if schema_version table exists")
	}

	if !tableExists {
		return 0, nil // No schema version table means version 0
	}

	// Get the highest version number
	var version int
	err = s.db.QueryRow(`
		SELECT COALESCE(MAX(version), 0)
		FROM schema_version
	`).Scan(&version)
	if err != nil {
		return 0, errors.Wrap(err, "failed to get current schema version")
	}

	return version, nil
}

// applyMigration applies a single migration
func (s *Store) applyMigration(migration Migration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Longer timeout for migrations
	defer cancel()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback() // This will be a no-op if transaction is committed

	// Apply migration (note: migration.Up expects *sql.Tx, so we use tx.Tx)
	if err := migration.Up(tx.Tx); err != nil {
		return errors.Wrapf(err, "migration %d failed", migration.Version)
	}

	// Record migration in schema_version table
	_, err = tx.ExecContext(ctx, `
		INSERT INTO schema_version (version, applied_at, description)
		VALUES (?, ?, ?)
	`, migration.Version, time.Now().Format(time.RFC3339), migration.Description)
	if err != nil {
		return errors.Wrap(err, "failed to record migration")
	}

	return tx.Commit()
}

// validateSchema validates that the database schema matches expectations
func (s *Store) validateSchema() error {
	// Check that all required tables exist
	requiredTables := []string{
		"schema_version",
		"conversations",
		"conversation_summaries",
	}

	for _, table := range requiredTables {
		var exists bool
		err := s.db.QueryRow(`
			SELECT COUNT(*) > 0
			FROM sqlite_master
			WHERE type='table' AND name=?
		`, table).Scan(&exists)
		if err != nil {
			return errors.Wrapf(err, "failed to check if table %s exists", table)
		}

		if !exists {
			return errors.Errorf("required table %s does not exist", table)
		}
	}

	// Check that schema version matches current version
	currentVersion, err := s.getCurrentSchemaVersion()
	if err != nil {
		return errors.Wrap(err, "failed to get current schema version")
	}

	if currentVersion != CurrentSchemaVersion {
		return errors.Errorf("schema version mismatch: expected %d, got %d",
			CurrentSchemaVersion, currentVersion)
	}

	return nil
}
