package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
)

// Migration20261017180000CreateRealtimeTranscript keeps voice presentation out of model history.
func Migration20261017180000CreateRealtimeTranscript() db.Migration {
	return db.Migration{
		Version:     20261017180000,
		Description: "Create realtime transcript projection",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				ALTER TABLE steering_messages
					ADD COLUMN source_id TEXT NOT NULL DEFAULT '';

				CREATE UNIQUE INDEX steering_source_id
					ON steering_messages(conversation_id, source_id)
					WHERE source_id != '';

				CREATE TABLE realtime_transcript (
					sequence INTEGER PRIMARY KEY AUTOINCREMENT,
					conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
					id TEXT NOT NULL,
					anchor INTEGER NOT NULL,
					role TEXT NOT NULL,
					text TEXT NOT NULL DEFAULT '',
					UNIQUE(conversation_id, id)
				);

				CREATE TRIGGER realtime_conversation_deleted
					AFTER DELETE ON conversations
				BEGIN
					DELETE FROM realtime_transcript WHERE conversation_id = OLD.id;
				END;
			`)
			return err
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				DROP TRIGGER IF EXISTS realtime_conversation_deleted;
				DROP TABLE IF EXISTS realtime_transcript;
				DROP INDEX IF EXISTS steering_source_id;
				ALTER TABLE steering_messages DROP COLUMN source_id;
			`)
			return err
		},
	}
}
