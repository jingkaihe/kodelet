package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260807120000AddRunnerEnvironmentProfile persists the runner-local profile with conversation affinity.
func Migration20260807120000AddRunnerEnvironmentProfile() db.Migration {
	return db.Migration{
		Version:     20260807120000,
		Description: "Add runner environment profile to conversation affinity",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				ALTER TABLE conversation_runner_affinity
				ADD COLUMN environment_profile TEXT NOT NULL DEFAULT ''
			`)
			return errors.Wrap(err, "failed to add runner environment profile")
		},
		Down: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				ALTER TABLE conversation_runner_affinity
				DROP COLUMN environment_profile
			`)
			return errors.Wrap(err, "failed to remove runner environment profile")
		},
	}
}
