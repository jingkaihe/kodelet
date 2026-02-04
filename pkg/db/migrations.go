package db

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

// Migration represents a database migration with timestamp-based versioning (Rails-style)
type Migration struct {
	Version     int64 // Timestamp format: YYYYMMDDHHmmss (e.g., 20240204153000)
	Description string
	Up          func(*sql.Tx) error
	Down        func(*sql.Tx) error // Optional rollback function
}

// MigrationRunner handles database migrations
type MigrationRunner struct {
	db *sqlx.DB
}

// NewMigrationRunner creates a new migration runner
func NewMigrationRunner(db *sqlx.DB) *MigrationRunner {
	return &MigrationRunner{db: db}
}

// Run executes all pending migrations in timestamp order
func (r *MigrationRunner) Run(ctx context.Context, migrations []Migration) error {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := r.getAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	// Sort migrations by version (timestamp)
	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Version < sorted[j].Version
	})

	for _, m := range sorted {
		if !applied[m.Version] {
			if err := r.applyMigration(ctx, m); err != nil {
				return errors.Wrapf(err, "failed to apply migration %d: %s", m.Version, m.Description)
			}
		}
	}

	return nil
}

// Rollback rolls back the last applied migration
func (r *MigrationRunner) Rollback(ctx context.Context, migrations []Migration) error {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	// Get the latest applied migration
	var version int64
	err := r.db.GetContext(ctx, &version, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations")
	if err != nil {
		return errors.Wrap(err, "failed to get latest migration version")
	}

	if version == 0 {
		return nil // Nothing to rollback
	}

	// Find the migration to rollback
	for _, m := range migrations {
		if m.Version == version {
			if m.Down == nil {
				return errors.Errorf("migration %d has no rollback function", version)
			}
			return r.rollbackMigration(ctx, m)
		}
	}

	return errors.Errorf("migration %d not found in provided migrations", version)
}

func (r *MigrationRunner) ensureMigrationsTable(ctx context.Context) error {
	// Check if old component-based schema_migrations table exists
	var hasComponent bool
	err := r.db.GetContext(ctx, &hasComponent, `
		SELECT COUNT(*) > 0 FROM pragma_table_info('schema_migrations') WHERE name = 'component'
	`)
	if err == nil && hasComponent {
		// Drop old table - migrations are idempotent (IF NOT EXISTS) so re-running is safe
		if _, err := r.db.ExecContext(ctx, "DROP TABLE schema_migrations"); err != nil {
			return errors.Wrap(err, "failed to drop old schema_migrations table")
		}
	}

	_, err = r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL,
			description TEXT
		)
	`)
	return errors.Wrap(err, "failed to create schema_migrations table")
}

func (r *MigrationRunner) getAppliedMigrations(ctx context.Context) (map[int64]bool, error) {
	var versions []int64
	err := r.db.SelectContext(ctx, &versions, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get applied migrations")
	}

	applied := make(map[int64]bool)
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
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
		"INSERT INTO schema_migrations (version, applied_at, description) VALUES (?, ?, ?)",
		m.Version, time.Now(), m.Description)
	if err != nil {
		return errors.Wrap(err, "failed to record migration")
	}

	return tx.Commit()
}

func (r *MigrationRunner) rollbackMigration(ctx context.Context, m Migration) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()

	if err := m.Down(tx.Tx); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ?", m.Version)
	if err != nil {
		return errors.Wrap(err, "failed to remove migration record")
	}

	return tx.Commit()
}

// GetAppliedVersions returns a list of applied migration versions
func (r *MigrationRunner) GetAppliedVersions(ctx context.Context) ([]int64, error) {
	if err := r.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	var versions []int64
	err := r.db.SelectContext(ctx, &versions, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get applied versions")
	}
	return versions, nil
}
