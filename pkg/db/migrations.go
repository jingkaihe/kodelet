package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

// Migration represents a database migration for a component
type Migration struct {
	Version     int
	Description string
	Up          func(*sql.Tx) error
}

// MigrationRunner handles migrations for a specific component
type MigrationRunner struct {
	db        *sqlx.DB
	component string
}

// NewMigrationRunner creates a migration runner for the given component
func NewMigrationRunner(db *sqlx.DB, component string) *MigrationRunner {
	return &MigrationRunner{db: db, component: component}
}

// Run executes all pending migrations for this component
func (r *MigrationRunner) Run(ctx context.Context, migrations []Migration) error {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	currentVersion, err := r.getCurrentVersion(ctx)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.Version > currentVersion {
			if err := r.applyMigration(ctx, m); err != nil {
				return errors.Wrapf(err, "failed to apply migration %s v%d", r.component, m.Version)
			}
		}
	}

	return nil
}

func (r *MigrationRunner) ensureMigrationsTable(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			component TEXT NOT NULL,
			version INTEGER NOT NULL,
			applied_at DATETIME NOT NULL,
			description TEXT,
			PRIMARY KEY (component, version)
		)
	`)
	return errors.Wrap(err, "failed to create schema_migrations table")
}

func (r *MigrationRunner) getCurrentVersion(ctx context.Context) (int, error) {
	var version int
	err := r.db.GetContext(ctx, &version,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE component = ?",
		r.component)
	if err != nil {
		return 0, errors.Wrap(err, "failed to get current version")
	}
	return version, nil
}

func (r *MigrationRunner) applyMigration(ctx context.Context, m Migration) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	if err := m.Up(tx.Tx); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (component, version, applied_at, description) VALUES (?, ?, ?, ?)",
		r.component, m.Version, time.Now(), m.Description)
	if err != nil {
		return errors.Wrap(err, "failed to record migration")
	}

	return tx.Commit()
}
