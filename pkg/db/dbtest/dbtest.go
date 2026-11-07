// Package dbtest opens migrated Kodelet databases for tests.
package dbtest

import (
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/stretchr/testify/require"
)

// Open opens the database at path, applies every migration, and closes the
// handle when the test ends. Opening the same path again returns an
// independent handle, which tests use to stand in for another process.
func Open(t testing.TB, path string) *db.DB {
	t.Helper()
	database, err := db.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	return database
}

// OpenTemp opens a migrated storage.db in a new temporary directory.
func OpenTemp(t testing.TB) *db.DB {
	t.Helper()
	return Open(t, filepath.Join(t.TempDir(), "storage.db"))
}
