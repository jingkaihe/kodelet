package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
)

func Migration20260906160000ScopeChildSteering() db.Migration {
	return db.Migration{
		Version: 20260906160000, Description: "Scope delegated steering to exact child runs",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE steering_messages ADD COLUMN run_id TEXT NOT NULL DEFAULT ''`)
			return err
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE steering_messages DROP COLUMN run_id`)
			return err
		},
	}
}
