package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260204163001AddPerformanceIndexes adds performance indexes for conversations and summaries.
func Migration20260204163001AddPerformanceIndexes() db.Migration {
	return db.Migration{
		Version:     20260204163001,
		Description: "Add performance indexes for conversations and summaries",
		Up: func(tx *sql.Tx) error {
			indexes := []string{
				"CREATE INDEX IF NOT EXISTS idx_conversations_created_at ON conversations(created_at DESC)",
				"CREATE INDEX IF NOT EXISTS idx_conversations_updated_at ON conversations(updated_at DESC)",
				"CREATE INDEX IF NOT EXISTS idx_conversations_provider ON conversations(provider)",
				"CREATE INDEX IF NOT EXISTS idx_summaries_created_at ON conversation_summaries(created_at DESC)",
				"CREATE INDEX IF NOT EXISTS idx_summaries_updated_at ON conversation_summaries(updated_at DESC)",
				"CREATE INDEX IF NOT EXISTS idx_summaries_message_count ON conversation_summaries(message_count)",
				"CREATE INDEX IF NOT EXISTS idx_summaries_first_message ON conversation_summaries(first_message)",
				"CREATE INDEX IF NOT EXISTS idx_summaries_summary ON conversation_summaries(summary)",
			}

			for _, idx := range indexes {
				if _, err := tx.Exec(idx); err != nil {
					return errors.Wrap(err, "failed to create index")
				}
			}
			return nil
		},
		Down: func(tx *sql.Tx) error {
			dropIndexes := []string{
				"DROP INDEX IF EXISTS idx_summaries_summary",
				"DROP INDEX IF EXISTS idx_summaries_first_message",
				"DROP INDEX IF EXISTS idx_summaries_message_count",
				"DROP INDEX IF EXISTS idx_summaries_updated_at",
				"DROP INDEX IF EXISTS idx_summaries_created_at",
				"DROP INDEX IF EXISTS idx_conversations_provider",
				"DROP INDEX IF EXISTS idx_conversations_updated_at",
				"DROP INDEX IF EXISTS idx_conversations_created_at",
			}

			for _, drop := range dropIndexes {
				if _, err := tx.Exec(drop); err != nil {
					return errors.Wrap(err, "failed to drop index")
				}
			}
			return nil
		},
	}
}
