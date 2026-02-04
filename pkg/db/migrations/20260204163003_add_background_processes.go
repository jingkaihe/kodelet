package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

func Migration20260204163003AddBackgroundProcesses() db.Migration {
	return db.Migration{
		Version:     20260204163003,
		Description: "Add background_processes column to conversations table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists (for idempotency)
			var hasColumn bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('conversations') WHERE name = 'background_processes'
			`).Scan(&hasColumn)
			if err != nil {
				return errors.Wrap(err, "failed to check if background_processes column exists")
			}

			if !hasColumn {
				_, err = tx.Exec("ALTER TABLE conversations ADD COLUMN background_processes TEXT")
				if err != nil {
					return errors.Wrap(err, "failed to add background_processes column")
				}
			}
			return nil
		},
		Down: func(tx *sql.Tx) error {
			// SQLite doesn't support DROP COLUMN, would need table recreation
			return nil
		},
	}
}
