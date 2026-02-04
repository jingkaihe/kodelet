package sqlite

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/pkg/errors"
)

// getAppliedMigrationCount returns the number of applied migrations for testing
func (s *Store) getAppliedMigrationCount(ctx context.Context) (int, error) {
	runner := db.NewMigrationRunner(s.db)
	versions, err := runner.GetAppliedVersions(ctx)
	if err != nil {
		return 0, err
	}
	return len(versions), nil
}

// validateSchema validates that the database schema matches expectations
func (s *Store) validateSchema() error {
	ctx := context.Background()

	// Check that all required tables exist
	requiredTables := []string{
		"schema_migrations",
		"conversations",
		"conversation_summaries",
	}

	for _, table := range requiredTables {
		var exists bool
		err := s.db.QueryRow(`
			SELECT COUNT(*) > 0
			FROM sqlite_master
			WHERE type='table' AND name=?
		`, table).Scan(&exists)
		if err != nil {
			return errors.Wrapf(err, "failed to check if table %s exists", table)
		}

		if !exists {
			return errors.Errorf("required table %s does not exist", table)
		}
	}

	// Verify all migrations have been applied
	runner := db.NewMigrationRunner(s.db)
	applied, err := runner.GetAppliedVersions(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to get applied migrations")
	}

	expected := len(migrations.All())
	if len(applied) != expected {
		return errors.Errorf("migration count mismatch: expected %d, got %d", expected, len(applied))
	}

	return nil
}
