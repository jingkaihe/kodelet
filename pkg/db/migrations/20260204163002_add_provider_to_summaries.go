package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260204163002AddProviderToSummaries adds provider column to conversation_summaries table.
func Migration20260204163002AddProviderToSummaries() db.Migration {
	return db.Migration{
		Version:     20260204163002,
		Description: "Add provider column to conversation_summaries table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists (for idempotency)
			var hasColumn bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('conversation_summaries') WHERE name = 'provider'
			`).Scan(&hasColumn)
			if err != nil {
				return errors.Wrap(err, "failed to check if provider column exists")
			}

			if !hasColumn {
				if _, err := tx.Exec("ALTER TABLE conversation_summaries ADD COLUMN provider TEXT"); err != nil {
					return errors.Wrap(err, "failed to add provider column")
				}
			}

			if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_summaries_provider ON conversation_summaries(provider)"); err != nil {
				return errors.Wrap(err, "failed to create provider index")
			}

			// Backfill provider from conversations table
			_, err = tx.Exec(`
				UPDATE conversation_summaries
				SET provider = (
					SELECT provider
					FROM conversations
					WHERE conversations.id = conversation_summaries.id
				)
				WHERE provider IS NULL
			`)
			if err != nil {
				return errors.Wrap(err, "failed to backfill provider")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			if _, err := tx.Exec("DROP INDEX IF EXISTS idx_summaries_provider"); err != nil {
				return errors.Wrap(err, "failed to drop provider index")
			}
			// SQLite doesn't support DROP COLUMN, would need table recreation
			return nil
		},
	}
}
