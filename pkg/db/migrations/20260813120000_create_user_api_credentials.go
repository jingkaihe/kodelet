package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260813120000CreateUserAPICredentials creates Kodelet-issued non-browser user credentials and device-login state.
func Migration20260813120000CreateUserAPICredentials() db.Migration {
	return db.Migration{
		Version:     20260813120000,
		Description: "Create control-plane user API credentials",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS user_api_credentials (
					id TEXT PRIMARY KEY,
					token_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(token_sha256) = 32),
					issuer TEXT NOT NULL,
					subject TEXT NOT NULL,
					name TEXT,
					email TEXT,
					roles_json TEXT NOT NULL DEFAULT '[]',
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL,
					last_used_at DATETIME,
					revoked_at DATETIME,
					revoke_reason TEXT
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create user_api_credentials table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS user_login_authorizations (
					id TEXT PRIMARY KEY,
					device_code_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(device_code_sha256) = 32),
					user_code TEXT NOT NULL UNIQUE,
					token_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(token_sha256) = 32),
					status TEXT NOT NULL
						CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
					client_name TEXT NOT NULL,
					client_os TEXT NOT NULL,
					client_arch TEXT NOT NULL,
					kodelet_version TEXT NOT NULL,
					poll_interval_seconds INTEGER NOT NULL,
					last_polled_at DATETIME,
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL,
					approved_at DATETIME,
					approved_by TEXT,
					denied_at DATETIME,
					denied_by TEXT,
					delivered_at DATETIME,
					credential_id TEXT,
					FOREIGN KEY(credential_id) REFERENCES user_api_credentials(id) ON DELETE SET NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create user_login_authorizations table")
			}

			for _, statement := range []string{
				"CREATE INDEX IF NOT EXISTS idx_user_api_credentials_expiry ON user_api_credentials(expires_at)",
				"CREATE INDEX IF NOT EXISTS idx_user_api_credentials_principal ON user_api_credentials(issuer, subject)",
				"CREATE INDEX IF NOT EXISTS idx_user_login_authorizations_status_expiry ON user_login_authorizations(status, expires_at)",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to create user API credential index")
				}
			}
			return nil
		},
		Down: func(tx *sql.Tx) error {
			for _, statement := range []string{
				"DROP TABLE IF EXISTS user_login_authorizations",
				"DROP TABLE IF EXISTS user_api_credentials",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to drop user API credential table")
				}
			}
			return nil
		},
	}
}
