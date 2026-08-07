package migrations

import (
	"database/sql"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/pkg/errors"
)

// Migration20260806120000CreateRunnerState creates durable runner registrations, runs, and conversation affinity.
func Migration20260806120000CreateRunnerState() db.Migration {
	return db.Migration{
		Version:     20260806120000,
		Description: "Create runner registrations, runs, and conversation affinity",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_registrations (
					id TEXT PRIMARY KEY,
					owner_id TEXT NOT NULL,
					display_name TEXT,
					host_instance_id TEXT NOT NULL,
					hostname TEXT,
					host_os TEXT,
					host_arch TEXT,
					host_pid INTEGER,
					workspace_path TEXT NOT NULL,
					workspace_name TEXT NOT NULL,
					kodelet_version TEXT,
					manifest_digest TEXT,
					manifest_changed INTEGER NOT NULL DEFAULT 0,
					compatibility_error TEXT,
					status TEXT NOT NULL,
					active_run_id TEXT,
					generation INTEGER NOT NULL DEFAULT 0,
					connected_at DATETIME,
					last_heartbeat_at DATETIME,
					created_at DATETIME NOT NULL,
					updated_at DATETIME NOT NULL,
					UNIQUE(owner_id, host_instance_id, workspace_path)
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create runner_registrations table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS runner_runs (
					id TEXT PRIMARY KEY,
					conversation_id TEXT NOT NULL,
					runner_id TEXT NOT NULL,
					status TEXT NOT NULL,
					manifest_digest TEXT,
					manifest_json TEXT,
					error TEXT,
					created_at DATETIME NOT NULL,
					updated_at DATETIME NOT NULL,
					FOREIGN KEY(runner_id) REFERENCES runner_registrations(id)
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create runner_runs table")
			}

			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS conversation_runner_affinity (
					conversation_id TEXT PRIMARY KEY,
					runner_id TEXT NOT NULL,
					created_at DATETIME NOT NULL,
					updated_at DATETIME NOT NULL,
					FOREIGN KEY(runner_id) REFERENCES runner_registrations(id)
				)
			`); err != nil {
				return errors.Wrap(err, "failed to create conversation_runner_affinity table")
			}

			for _, statement := range []string{
				"CREATE INDEX IF NOT EXISTS idx_runner_registrations_status ON runner_registrations(status)",
				"CREATE INDEX IF NOT EXISTS idx_runner_registrations_workspace ON runner_registrations(owner_id, workspace_path)",
				"CREATE INDEX IF NOT EXISTS idx_runner_runs_conversation_created ON runner_runs(conversation_id, created_at)",
				"CREATE INDEX IF NOT EXISTS idx_runner_runs_runner_created ON runner_runs(runner_id, created_at)",
				"CREATE INDEX IF NOT EXISTS idx_conversation_runner_affinity_runner ON conversation_runner_affinity(runner_id)",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to create runner state index")
				}
			}

			return nil
		},
		Down: func(tx *sql.Tx) error {
			for _, statement := range []string{
				"DROP TABLE IF EXISTS conversation_runner_affinity",
				"DROP TABLE IF EXISTS runner_runs",
				"DROP TABLE IF EXISTS runner_registrations",
			} {
				if _, err := tx.Exec(statement); err != nil {
					return errors.Wrap(err, "failed to drop runner state table")
				}
			}
			return nil
		},
	}
}
