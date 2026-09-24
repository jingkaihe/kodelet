package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260924120000CreateUserRefreshTokens separates access tokens from their revocable session.
func Migration20260924120000CreateUserRefreshTokens() db.Migration {
	return db.Migration{
		Version:     20260924120000,
		Description: "Create rotating user refresh and access tokens",
		Up: func(tx *sql.Tx) error {
			for _, statement := range []string{
				`ALTER TABLE user_login_authorizations ADD COLUMN refresh_token_sha256 BLOB
					CHECK (refresh_token_sha256 IS NULL OR length(refresh_token_sha256) = 32)`,
				`CREATE TABLE user_access_tokens (
					token_sha256 BLOB PRIMARY KEY NOT NULL CHECK (length(token_sha256) = 32),
					credential_id TEXT NOT NULL REFERENCES user_api_credentials(id) ON DELETE CASCADE,
					created_at DATETIME NOT NULL,
					expires_at DATETIME NOT NULL
				)`,
				`CREATE INDEX idx_user_access_tokens_credential ON user_access_tokens(credential_id)`,
				`CREATE TABLE user_refresh_tokens (
					token_sha256 BLOB PRIMARY KEY NOT NULL CHECK (length(token_sha256) = 32),
					credential_id TEXT NOT NULL REFERENCES user_api_credentials(id) ON DELETE CASCADE,
					created_at DATETIME NOT NULL,
					consumed_at DATETIME
				)`,
				`CREATE INDEX idx_user_refresh_tokens_credential ON user_refresh_tokens(credential_id)`,
				// Existing credentials retain their original lifetime and revocation state.
				`INSERT INTO user_access_tokens (token_sha256, credential_id, created_at, expires_at)
				 SELECT token_sha256, id, created_at, expires_at FROM user_api_credentials`,
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to create user token state")
				}
			}
			return nil
		},
		Down: func(tx *sql.Tx) error {
			for _, statement := range []string{
				// New pending flows must restart rather than become legacy logins after downgrade.
				`UPDATE user_login_authorizations SET status = 'expired'
				 WHERE status = 'pending' AND refresh_token_sha256 IS NOT NULL`,
				// An older server must not accept an initial short-lived token for the entire session.
				`UPDATE user_api_credentials SET revoked_at = CURRENT_TIMESTAMP, revoke_reason = 'token schema rollback'
				 WHERE revoked_at IS NULL AND id IN (SELECT credential_id FROM user_refresh_tokens)`,
				`DROP TABLE user_refresh_tokens`,
				`DROP TABLE user_access_tokens`,
				`ALTER TABLE user_login_authorizations DROP COLUMN refresh_token_sha256`,
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to drop user token state")
				}
			}
			return nil
		},
	}
}
