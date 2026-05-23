package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, VerifyConfiguration(db))
}

func TestOpen_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "nested", "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = os.Stat(filepath.Dir(dbPath))
	require.NoError(t, err)
}

func TestOpenReturnsDirectoryCreationError(t *testing.T) {
	basePathFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePathFile, []byte("file"), 0o644))

	database, err := Open(context.Background(), filepath.Join(basePathFile, "storage.db"))

	assert.Nil(t, database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create database directory")
}

func TestDefaultDBPath(t *testing.T) {
	origBasePath := os.Getenv("KODELET_BASE_PATH")
	defer os.Setenv("KODELET_BASE_PATH", origBasePath)

	t.Run("with KODELET_BASE_PATH", func(t *testing.T) {
		os.Setenv("KODELET_BASE_PATH", "/custom/path")
		path, err := DefaultDBPath()
		require.NoError(t, err)
		assert.Equal(t, "/custom/path/storage.db", path)
	})

	t.Run("without KODELET_BASE_PATH", func(t *testing.T) {
		os.Setenv("KODELET_BASE_PATH", "")
		path, err := DefaultDBPath()
		require.NoError(t, err)
		home, _ := os.UserHomeDir()
		assert.Equal(t, filepath.Join(home, ".kodelet", "storage.db"), path)
	})
}

func TestVerifyConfiguration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = VerifyConfiguration(db)
	require.NoError(t, err)
}

func TestMigrationRunner(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	migrations := []Migration{
		{
			Version:     20240101000001,
			Description: "Create test table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
		},
		{
			Version:     20240101000002,
			Description: "Add column",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("ALTER TABLE test_table ADD COLUMN name TEXT")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db)
	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	var tableExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='test_table'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.True(t, tableExists)

	versions, err := runner.GetAppliedVersions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int64{20240101000001, 20240101000002}, versions)
}

func TestMigrationRunner_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	migrations := []Migration{
		{
			Version:     20240101000001,
			Description: "Create test table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db)

	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM schema_migrations")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestMigrationRunner_OutOfOrder(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Add migrations out of order - runner should sort by timestamp
	migrations := []Migration{
		{
			Version:     20240101000002,
			Description: "Second migration",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("ALTER TABLE test_table ADD COLUMN name TEXT")
				return err
			},
		},
		{
			Version:     20240101000001,
			Description: "First migration",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db)
	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	versions, err := runner.GetAppliedVersions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int64{20240101000001, 20240101000002}, versions)
}

func TestMigrationRunner_Rollback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	migrations := []Migration{
		{
			Version:     20240101000001,
			Description: "Create test table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
			Down: func(tx *sql.Tx) error {
				_, err := tx.Exec("DROP TABLE test_table")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db)
	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	// Verify table exists
	var tableExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='test_table'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.True(t, tableExists)

	// Rollback
	err = runner.Rollback(context.Background(), migrations)
	require.NoError(t, err)

	// Verify table is gone
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='test_table'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.False(t, tableExists)

	// Verify migration record is removed
	versions, err := runner.GetAppliedVersions(context.Background())
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestRunMigrationWrappersUseDefaultDBPath(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)

	migrations := []Migration{
		{
			Version:     20260523000100,
			Description: "create wrapper table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE wrapper_table (id INTEGER PRIMARY KEY)`)
				return err
			},
			Down: func(tx *sql.Tx) error {
				_, err := tx.Exec(`DROP TABLE wrapper_table`)
				return err
			},
		},
	}

	require.NoError(t, RunMigrations(ctx, migrations))
	versions, err := GetMigrationStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int64{20260523000100}, versions)
	assert.FileExists(t, filepath.Join(basePath, "storage.db"))

	require.NoError(t, RollbackMigration(ctx, migrations))
	versions, err = GetMigrationStatus(ctx)
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestMigrationWrappersReturnDefaultPathErrors(t *testing.T) {
	basePathFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePathFile, []byte("file"), 0o644))
	t.Setenv("KODELET_BASE_PATH", basePathFile)

	err := RunMigrations(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create database directory")

	err = RollbackMigration(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create database directory")

	versions, err := GetMigrationStatus(context.Background())
	assert.Nil(t, versions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create database directory")
}

func TestMigrationRunnerRollbackNoopAndErrors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "rollback-errors.db"))
	require.NoError(t, err)
	defer database.Close()
	runner := NewMigrationRunner(database)

	require.NoError(t, runner.Rollback(ctx, nil), "empty schema_migrations table should be a no-op")

	migrationWithoutDown := Migration{
		Version:     20260523000200,
		Description: "no down",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE no_down (id INTEGER PRIMARY KEY)`)
			return err
		},
	}
	require.NoError(t, runner.Run(ctx, []Migration{migrationWithoutDown}))
	err = runner.Rollback(ctx, []Migration{migrationWithoutDown})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rollback function")

	err = runner.Rollback(ctx, []Migration{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in provided migrations")
}

func TestMigrationRunnerReplacesLegacyComponentMigrationsTable(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "legacy-schema.db"))
	require.NoError(t, err)
	defer database.Close()

	_, err = database.ExecContext(ctx, `DROP TABLE IF EXISTS schema_migrations`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `CREATE TABLE schema_migrations (component TEXT NOT NULL, version INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `INSERT INTO schema_migrations (component, version) VALUES ('old', 1)`)
	require.NoError(t, err)

	runner := NewMigrationRunner(database)
	versions, err := runner.GetAppliedVersions(ctx)

	require.NoError(t, err)
	assert.Empty(t, versions)
	var hasComponent bool
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM pragma_table_info('schema_migrations') WHERE name = 'component'`).Scan(&hasComponent))
	assert.False(t, hasComponent)
}

func TestMigrationRunnerRunRollsBackFailedMigration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "failed-migration.db"))
	require.NoError(t, err)
	defer database.Close()
	runner := NewMigrationRunner(database)

	err = runner.Run(ctx, []Migration{
		{
			Version:     20260523000300,
			Description: "failing migration",
			Up: func(tx *sql.Tx) error {
				if _, err := tx.Exec(`CREATE TABLE rolled_back (id INTEGER PRIMARY KEY)`); err != nil {
					return err
				}
				return assert.AnError
			},
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply migration")
	var exists bool
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'rolled_back'`).Scan(&exists))
	assert.False(t, exists)
	versions, err := runner.GetAppliedVersions(ctx)
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestMigrationRunnerRollbackPropagatesDownAndDeleteErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("down error keeps migration record", func(t *testing.T) {
		database, err := Open(ctx, filepath.Join(t.TempDir(), "down-error.db"))
		require.NoError(t, err)
		defer database.Close()
		runner := NewMigrationRunner(database)

		migration := Migration{
			Version:     20260523000450,
			Description: "down error",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE down_error (id INTEGER PRIMARY KEY)`)
				return err
			},
			Down: func(_ *sql.Tx) error { return assert.AnError },
		}
		require.NoError(t, runner.Run(ctx, []Migration{migration}))

		err = runner.Rollback(ctx, []Migration{migration})

		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		versions, err := runner.GetAppliedVersions(ctx)
		require.NoError(t, err)
		assert.Equal(t, []int64{migration.Version}, versions)
	})

	t.Run("delete record error is wrapped", func(t *testing.T) {
		database, err := Open(ctx, filepath.Join(t.TempDir(), "delete-error.db"))
		require.NoError(t, err)
		defer database.Close()
		runner := NewMigrationRunner(database)

		migration := Migration{
			Version:     20260523000460,
			Description: "delete record error",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE delete_error (id INTEGER PRIMARY KEY)`)
				return err
			},
			Down: func(tx *sql.Tx) error {
				_, err := tx.Exec(`DROP TABLE schema_migrations`)
				return err
			},
		}
		require.NoError(t, runner.Run(ctx, []Migration{migration}))

		err = runner.Rollback(ctx, []Migration{migration})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove migration record")
	})
}

func TestVerifyConfigurationReportsPragmaMismatches(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "verify.db"))
	require.NoError(t, err)
	defer database.Close()

	_, err = database.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
	require.NoError(t, err)
	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected foreign keys ON")

	_, err = database.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `PRAGMA synchronous=FULL`)
	require.NoError(t, err)
	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected NORMAL synchronous mode")
}

func TestConfigureAndVerifyConfigurationReturnClosedDatabaseErrors(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "closed.db"))
	require.NoError(t, err)
	require.NoError(t, database.Close())

	err = Configure(context.Background(), database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute pragma")

	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query journal mode")
}

func TestMigrationRunnerRecordsDescriptionAndAppliedAt(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "metadata.db"))
	require.NoError(t, err)
	defer database.Close()
	runner := NewMigrationRunner(database)

	require.NoError(t, runner.Run(ctx, []Migration{
		{
			Version:     20260523000400,
			Description: "metadata migration",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE metadata_table (id INTEGER PRIMARY KEY)`)
				return err
			},
		},
	}))

	var description string
	var appliedAt time.Time
	require.NoError(t, database.QueryRowContext(ctx, `SELECT description, applied_at FROM schema_migrations WHERE version = ?`, 20260523000400).Scan(&description, &appliedAt))
	assert.Equal(t, "metadata migration", description)
	assert.False(t, appliedAt.IsZero())
}
