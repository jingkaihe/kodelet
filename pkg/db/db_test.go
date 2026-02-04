package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

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
