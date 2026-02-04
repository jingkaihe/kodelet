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

func TestEnsureSchemaVersion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = EnsureSchemaVersion(context.Background(), db)
	require.NoError(t, err)

	var tableExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='schema_version'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.True(t, tableExists)
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
			Version:     1,
			Description: "Create test table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
		},
		{
			Version:     2,
			Description: "Add column",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("ALTER TABLE test_table ADD COLUMN name TEXT")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db, "test")
	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	var tableExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='test_table'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.True(t, tableExists)

	var version int
	err = db.Get(&version, "SELECT MAX(version) FROM schema_migrations WHERE component = 'test'")
	require.NoError(t, err)
	assert.Equal(t, 2, version)
}

func TestMigrationRunner_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	migrations := []Migration{
		{
			Version:     1,
			Description: "Create test table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
		},
	}

	runner := NewMigrationRunner(db, "test")

	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	err = runner.Run(context.Background(), migrations)
	require.NoError(t, err)

	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM schema_migrations WHERE component = 'test'")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestMigrationRunner_MultipleComponents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer db.Close()

	migrationsA := []Migration{
		{Version: 1, Description: "A v1", Up: func(tx *sql.Tx) error {
			_, err := tx.Exec("CREATE TABLE table_a (id INTEGER PRIMARY KEY)")
			return err
		}},
	}

	migrationsB := []Migration{
		{Version: 1, Description: "B v1", Up: func(tx *sql.Tx) error {
			_, err := tx.Exec("CREATE TABLE table_b (id INTEGER PRIMARY KEY)")
			return err
		}},
		{Version: 2, Description: "B v2", Up: func(tx *sql.Tx) error {
			_, err := tx.Exec("ALTER TABLE table_b ADD COLUMN name TEXT")
			return err
		}},
	}

	runnerA := NewMigrationRunner(db, "component_a")
	runnerB := NewMigrationRunner(db, "component_b")

	require.NoError(t, runnerA.Run(context.Background(), migrationsA))
	require.NoError(t, runnerB.Run(context.Background(), migrationsB))

	var versionA, versionB int
	require.NoError(t, db.Get(&versionA, "SELECT MAX(version) FROM schema_migrations WHERE component = 'component_a'"))
	require.NoError(t, db.Get(&versionB, "SELECT MAX(version) FROM schema_migrations WHERE component = 'component_b'"))

	assert.Equal(t, 1, versionA)
	assert.Equal(t, 2, versionB)
}
