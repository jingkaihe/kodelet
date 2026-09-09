package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260906130000CreateChatTurns adds durable receipts to ordinary conversations.
func Migration20260906130000CreateChatTurns() db.Migration {
	return db.Migration{
		Version: 20260906130000, Description: "Create durable conversation turn receipts",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE chat_turns (
				conversation_id TEXT NOT NULL, turn_id TEXT NOT NULL,
				request_hash TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL, cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
				result TEXT, error TEXT NOT NULL DEFAULT '',
				created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
				PRIMARY KEY (conversation_id, turn_id)
			);
			CREATE UNIQUE INDEX idx_chat_turns_active ON chat_turns(conversation_id)
				WHERE status IN ('accepted', 'running');`)
			return errors.Wrap(err, "failed to create durable chat turn receipts")
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec("DROP TABLE IF EXISTS chat_turns")
			return errors.Wrap(err, "failed to drop chat turn receipts")
		},
	}
}
