package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260719170000CreateSteeringMessages creates the persistent steering queue.
func Migration20260719170000CreateSteeringMessages() db.Migration {
	return db.Migration{
		Version:     20260719170000,
		Description: "Create steering messages table",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS steering_messages (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					conversation_id TEXT NOT NULL,
					content TEXT NOT NULL,
					images_json TEXT NOT NULL DEFAULT '[]',
					created_at DATETIME NOT NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create steering_messages table")
			}

			if _, err := tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_steering_messages_conversation_id
				ON steering_messages(conversation_id, id)
			`); err != nil {
				return errors.Wrap(err, "failed to create steering messages index")
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec("DROP TABLE IF EXISTS steering_messages")
			return errors.Wrap(err, "failed to drop steering_messages table")
		},
	}
}
