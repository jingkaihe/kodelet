package session

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

const componentName = "acp"

var migrations = []db.Migration{
	{
		Version:     1,
		Description: "Create acp_session_updates table",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS acp_session_updates (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					session_id TEXT NOT NULL,
					update_data TEXT NOT NULL,
					created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create acp_session_updates table")
			}

			if _, err := tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_acp_session_updates_session_id 
				ON acp_session_updates(session_id)
			`); err != nil {
				return errors.Wrap(err, "failed to create session_id index")
			}

			if _, err := tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_acp_session_updates_created_at 
				ON acp_session_updates(session_id, created_at)
			`); err != nil {
				return errors.Wrap(err, "failed to create created_at index")
			}

			return nil
		},
	},
}
