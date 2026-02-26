package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260226120000AddMetadataToSummaries adds metadata column to conversation_summaries table.
func Migration20260226120000AddMetadataToSummaries() db.Migration {
	return db.Migration{
		Version:     20260226120000,
		Description: "Add metadata column to conversation_summaries table",
		Up: func(tx *sql.Tx) error {
			var hasColumn bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('conversation_summaries') WHERE name = 'metadata'
			`).Scan(&hasColumn)
			if err != nil {
				return errors.Wrap(err, "failed to check if metadata column exists")
			}

			if !hasColumn {
				if _, err := tx.Exec("ALTER TABLE conversation_summaries ADD COLUMN metadata TEXT"); err != nil {
					return errors.Wrap(err, "failed to add metadata column")
				}
			}

			_, err = tx.Exec(`
				UPDATE conversation_summaries
				SET metadata = (
					SELECT metadata
					FROM conversations
					WHERE conversations.id = conversation_summaries.id
				)
				WHERE metadata IS NULL
			`)
			if err != nil {
				return errors.Wrap(err, "failed to backfill metadata")
			}

			return nil
		},
		Down: func(_ *sql.Tx) error {
			return nil
		},
	}
}
