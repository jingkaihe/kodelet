package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260813130000CreateRunnerDPoPReplays replaces the custom challenge store with RFC 9449 replay state.
func Migration20260813130000CreateRunnerDPoPReplays() db.Migration {
	return db.Migration{
		Version:     20260813130000,
		Description: "Create runner DPoP replay state",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec("DROP INDEX IF EXISTS idx_runner_auth_challenges_credential_expiry"); err != nil {
				return errors.Wrap(err, "failed to drop runner authentication challenge index")
			}
			if _, err := tx.Exec("DROP TABLE IF EXISTS runner_auth_challenges"); err != nil {
				return errors.Wrap(err, "failed to drop runner authentication challenge table")
			}
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_dpop_replays (
					credential_id TEXT NOT NULL,
					jti_sha256 BLOB NOT NULL
						CHECK (length(jti_sha256) = 32),
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL,
					PRIMARY KEY (credential_id, jti_sha256),
					FOREIGN KEY(credential_id) REFERENCES runner_credentials(id) ON DELETE CASCADE
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create runner_dpop_replays table")
			}
			if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_runner_dpop_replays_expiry ON runner_dpop_replays(expires_at)"); err != nil {
				return errors.Wrap(err, "failed to create runner DPoP replay expiry index")
			}
			return nil
		},
		Down: func(tx *sql.Tx) error {
			if _, err := tx.Exec("DROP TABLE IF EXISTS runner_dpop_replays"); err != nil {
				return errors.Wrap(err, "failed to drop runner_dpop_replays table")
			}
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_auth_challenges (
					id TEXT PRIMARY KEY,
					credential_id TEXT NOT NULL,
					challenge_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(challenge_sha256) = 32),
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL,
					used_at DATETIME,
					FOREIGN KEY(credential_id) REFERENCES runner_credentials(id) ON DELETE CASCADE
				)
			`); err != nil {
				return errors.Wrap(err, "failed to recreate runner_auth_challenges table")
			}
			if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_runner_auth_challenges_credential_expiry ON runner_auth_challenges(credential_id, expires_at)"); err != nil {
				return errors.Wrap(err, "failed to recreate runner authentication challenge index")
			}
			return nil
		},
	}
}
