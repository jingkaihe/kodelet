package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260331120000AddCWDToConversations adds cwd columns to persisted conversations.
func Migration20260331120000AddCWDToConversations() db.Migration {
	return db.Migration{
		Version:     20260331120000,
		Description: "Add cwd columns to conversations and conversation_summaries",
		Up: func(tx *sql.Tx) error {
			for _, table := range []string{"conversations", "conversation_summaries"} {
				var hasColumn bool
				err := tx.QueryRow(`
					SELECT COUNT(*) > 0 FROM pragma_table_info(?1) WHERE name = 'cwd'
				`, table).Scan(&hasColumn)
				if err != nil {
					return errors.Wrapf(err, "failed to check cwd column on %s", table)
				}

				if !hasColumn {
					if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN cwd TEXT"); err != nil {
						return errors.Wrapf(err, "failed to add cwd column to %s", table)
					}
				}
			}

			if _, err := tx.Exec(`
				UPDATE conversation_summaries
				SET cwd = (
					SELECT cwd FROM conversations WHERE conversations.id = conversation_summaries.id
				)
				WHERE cwd IS NULL
			`); err != nil {
				return errors.Wrap(err, "failed to backfill conversation summary cwd")
			}

			if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_conversations_cwd_updated_at ON conversations(cwd, updated_at)"); err != nil {
				return errors.Wrap(err, "failed to create conversations cwd index")
			}
			if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_summaries_cwd_updated_at ON conversation_summaries(cwd, updated_at)"); err != nil {
				return errors.Wrap(err, "failed to create conversation summaries cwd index")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			if _, err := tx.Exec("DROP INDEX IF EXISTS idx_conversations_cwd_updated_at"); err != nil {
				return errors.Wrap(err, "failed to drop conversations cwd index")
			}
			if _, err := tx.Exec("DROP INDEX IF EXISTS idx_summaries_cwd_updated_at"); err != nil {
				return errors.Wrap(err, "failed to drop summaries cwd index")
			}
			return nil
		},
	}
}
