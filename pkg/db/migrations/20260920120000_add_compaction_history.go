package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260920120000AddCompactionHistory preserves transcript segments across compaction.
func Migration20260920120000AddCompactionHistory() db.Migration {
	return db.Migration{
		Version:     20260920120000,
		Description: "Add conversation compaction history",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE conversations ADD COLUMN compaction_history TEXT`)
			return errors.Wrap(err, "failed to add compaction history")
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE conversations DROP COLUMN compaction_history`)
			return errors.Wrap(err, "failed to drop compaction history")
		},
	}
}
