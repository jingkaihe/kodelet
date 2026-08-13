package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260812120000CreateControlPlaneAuth creates durable web and runner authentication state.
func Migration20260812120000CreateControlPlaneAuth() db.Migration {
	return db.Migration{
		Version:     20260812120000,
		Description: "Create control-plane authentication state",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS web_auth_sessions (
					id TEXT PRIMARY KEY,
					token_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(token_sha256) = 32),
					csrf_token_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(csrf_token_sha256) = 32),
					issuer TEXT NOT NULL,
					subject TEXT NOT NULL,
					name TEXT,
					email TEXT,
					roles_json TEXT NOT NULL DEFAULT '[]',
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create web_auth_sessions table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS oidc_login_transactions (
					id TEXT PRIMARY KEY,
					state_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(state_sha256) = 32),
					nonce TEXT NOT NULL,
					pkce_verifier TEXT NOT NULL,
					return_to TEXT NOT NULL,
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create oidc_login_transactions table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_credentials (
					id TEXT PRIMARY KEY,
					runner_id TEXT NOT NULL,
					key_algorithm TEXT NOT NULL
						CHECK (key_algorithm = 'ed25519'),
					public_key BLOB NOT NULL
						CHECK (length(public_key) = 32),
					public_key_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(public_key_sha256) = 32),
					approved_by TEXT NOT NULL,
					created_at DATETIME NOT NULL,
					last_used_at DATETIME,
					revoked_at DATETIME,
					revoke_reason TEXT,
					FOREIGN KEY(runner_id) REFERENCES runner_registrations(id) ON DELETE CASCADE
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create runner_credentials table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_enrollments (
					id TEXT PRIMARY KEY,
					device_code_sha256 BLOB NOT NULL UNIQUE
						CHECK (length(device_code_sha256) = 32),
					user_code TEXT NOT NULL UNIQUE,
					owner_id TEXT,
					status TEXT NOT NULL
						CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
					protocol_version INTEGER NOT NULL,
					key_algorithm TEXT NOT NULL
						CHECK (key_algorithm = 'ed25519'),
					public_key BLOB NOT NULL
						CHECK (length(public_key) = 32),
					public_key_sha256 BLOB NOT NULL
						CHECK (length(public_key_sha256) = 32),
					display_name TEXT,
					host_instance_id TEXT NOT NULL,
					hostname TEXT,
					host_os TEXT,
					host_arch TEXT,
					workspace_path TEXT NOT NULL,
					workspace_name TEXT NOT NULL,
					kodelet_version TEXT,
					poll_interval_seconds INTEGER NOT NULL,
					last_polled_at DATETIME,
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL,
					approved_at DATETIME,
					approved_by TEXT,
					denied_at DATETIME,
					denied_by TEXT,
					delivered_at DATETIME,
					runner_id TEXT,
					credential_id TEXT,
					FOREIGN KEY(runner_id) REFERENCES runner_registrations(id) ON DELETE SET NULL,
					FOREIGN KEY(credential_id) REFERENCES runner_credentials(id) ON DELETE SET NULL
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create runner_enrollments table")
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
				return errors.Wrap(err, "failed to create runner_auth_challenges table")
			}

			for _, statement := range []string{
				"CREATE INDEX IF NOT EXISTS idx_web_auth_sessions_expiry ON web_auth_sessions(expires_at)",
				"CREATE INDEX IF NOT EXISTS idx_web_auth_sessions_principal ON web_auth_sessions(issuer, subject)",
				"CREATE INDEX IF NOT EXISTS idx_oidc_login_transactions_expiry ON oidc_login_transactions(expires_at)",
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_runner_credentials_active_runner ON runner_credentials(runner_id) WHERE revoked_at IS NULL",
				"CREATE INDEX IF NOT EXISTS idx_runner_enrollments_status_expiry ON runner_enrollments(status, expires_at)",
				"CREATE INDEX IF NOT EXISTS idx_runner_enrollments_public_key ON runner_enrollments(public_key_sha256)",
				"CREATE INDEX IF NOT EXISTS idx_runner_auth_challenges_credential_expiry ON runner_auth_challenges(credential_id, expires_at)",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to create control-plane authentication index")
				}
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			for _, statement := range []string{
				"DROP TABLE IF EXISTS runner_auth_challenges",
				"DROP TABLE IF EXISTS runner_enrollments",
				"DROP TABLE IF EXISTS runner_credentials",
				"DROP TABLE IF EXISTS oidc_login_transactions",
				"DROP TABLE IF EXISTS web_auth_sessions",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to drop control-plane authentication table")
				}
			}
			return nil
		},
	}
}
