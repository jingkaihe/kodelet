package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260204163000CreateConversations creates conversations and conversation_summaries tables.
func Migration20260204163000CreateConversations() db.Migration {
	return db.Migration{
		Version:     20260204163000,
		Description: "Create conversations and conversation_summaries tables",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS conversations (
					id TEXT PRIMARY KEY,
					raw_messages TEXT NOT NULL,
					provider TEXT NOT NULL,
					file_last_access TEXT,
					usage TEXT NOT NULL,
					summary TEXT,
					created_at DATETIME NOT NULL,
					updated_at DATETIME NOT NULL,
					metadata TEXT,
					tool_results TEXT
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create conversations table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS conversation_summaries (
					id TEXT PRIMARY KEY,
					message_count INTEGER NOT NULL,
					first_message TEXT NOT NULL,
					summary TEXT,
					usage TEXT NOT NULL,
					created_at DATETIME NOT NULL,
					updated_at DATETIME NOT NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create conversation_summaries table")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			if _, err := tx.Exec("DROP TABLE IF EXISTS conversation_summaries"); err != nil {
				return errors.Wrap(err, "failed to drop conversation_summaries table")
			}
			if _, err := tx.Exec("DROP TABLE IF EXISTS conversations"); err != nil {
				return errors.Wrap(err, "failed to drop conversations table")
			}
			return nil
		},
	}
}
