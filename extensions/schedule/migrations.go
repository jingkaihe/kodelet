package main

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// migration is one versioned schema change. Versions are UTC timestamps
// (YYYYMMDDHHmmss), so changes from parallel branches do not collide.
//
// Applied migrations are permanent: never edit or remove one after it is
// released. Change the schema by appending a migration, and keep it additive
// where possible. Up runs inside a transaction, where PRAGMA foreign_keys has
// no effect; a migration that rebuilds a table must keep its rows consistent
// with existing references.
type migration struct {
	Version     int64
	Description string
	Up          func(ctx context.Context, tx *sql.Tx) error
}

// scheduleMigrations lists the store's migrations in version order. Append
// new ones at the end.
func scheduleMigrations() []migration {
	return []migration{
		{
			Version:     20261126120000,
			Description: "Create schedules, runs, and daemon tables",
			Up: execMigration(`
CREATE TABLE schedules (
	id TEXT PRIMARY KEY,
	workspace TEXT NOT NULL,
	name TEXT NOT NULL,
	schedule TEXT NOT NULL,
	instruction TEXT NOT NULL,
	cwd TEXT NOT NULL,
	profile TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	next_run_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (workspace, name)
);
CREATE INDEX schedules_due ON schedules (enabled, next_run_at);
CREATE TABLE runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	schedule_id TEXT NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
	scheduled_for INTEGER NOT NULL,
	started_at INTEGER NOT NULL,
	finished_at INTEGER,
	status TEXT NOT NULL,
	conversation_id TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runs_schedule ON runs (schedule_id, id);
CREATE TABLE daemon (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	pid INTEGER NOT NULL,
	hostname TEXT NOT NULL,
	version TEXT NOT NULL,
	started_at INTEGER NOT NULL,
	heartbeat_at INTEGER NOT NULL,
	poll_interval_ms INTEGER NOT NULL,
	stopped_at INTEGER
);
`),
		},
	}
}

// execMigration returns an Up function that executes statements.
func execMigration(statements string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, statements)
		return err
	}
}

// appliedMigration is a row of schema_migrations.
type appliedMigration struct {
	Version     int64
	Description string
	AppliedAt   time.Time
}

// migrate applies pending migrations in one transaction and records them in
// schema_migrations. The transaction takes the write lock before reading, so
// the extension and daemon can open a database at the same time and each
// migration still runs once; if one fails, none of the pending ones apply.
//
// It refuses a database with migrations it does not know, which a newer
// release applied, rather than risk misreading its schema.
func migrate(ctx context.Context, db *sql.DB, migrations []migration) error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to migrate schedule database")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	description TEXT NOT NULL,
	applied_at INTEGER NOT NULL
)`); err != nil {
		return errors.Wrap(err, "failed to create schema_migrations table")
	}
	applied, err := queryAppliedMigrations(ctx, tx)
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(migrations))
	for _, m := range migrations {
		known[m.Version] = true
	}
	var unknown []string
	done := make(map[int64]bool, len(applied))
	for _, m := range applied {
		done[m.Version] = true
		if !known[m.Version] {
			unknown = append(unknown, m.Description)
		}
	}
	if len(unknown) > 0 {
		return errors.Errorf(
			"the schedule database was migrated by a newer kodelet-extension-schedule (%s); upgrade this executable",
			strings.Join(unknown, "; "),
		)
	}
	now := time.Now().UnixMilli()
	pending := 0
	for _, m := range migrations {
		if done[m.Version] {
			continue
		}
		if err := m.Up(ctx, tx); err != nil {
			return errors.Wrapf(err, "failed to apply schedule database migration %d (%s)", m.Version, m.Description)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Description, now,
		); err != nil {
			return errors.Wrapf(err, "failed to record schedule database migration %d", m.Version)
		}
		pending++
	}
	if pending == 0 {
		return nil
	}
	return errors.Wrap(tx.Commit(), "failed to migrate schedule database")
}

func validateMigrations(migrations []migration) error {
	var previous int64
	for _, m := range migrations {
		switch {
		case m.Version <= previous:
			return errors.Errorf("schedule migration %d must have a version greater than %d", m.Version, previous)
		case strings.TrimSpace(m.Description) == "":
			return errors.Errorf("schedule migration %d has no description", m.Version)
		case m.Up == nil:
			return errors.Errorf("schedule migration %d has no Up function", m.Version)
		}
		previous = m.Version
	}
	return nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryAppliedMigrations(ctx context.Context, q queryer) ([]appliedMigration, error) {
	rows, err := q.QueryContext(ctx, `SELECT version, description, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read applied schedule database migrations")
	}
	defer rows.Close()
	var applied []appliedMigration
	for rows.Next() {
		var m appliedMigration
		var appliedAt int64
		if err := rows.Scan(&m.Version, &m.Description, &appliedAt); err != nil {
			return nil, errors.Wrap(err, "failed to read applied schedule database migrations")
		}
		m.AppliedAt = time.UnixMilli(appliedAt)
		applied = append(applied, m)
	}
	return applied, errors.Wrap(rows.Err(), "failed to read applied schedule database migrations")
}
