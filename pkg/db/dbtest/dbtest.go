// Package dbtest opens migrated Kodelet databases for tests.
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
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

// OpenDefault opens the migrated database at the default path. Tests must
// first point KODELET_BASE_PATH at a temporary directory.
func OpenDefault(t testing.TB) *db.DB {
	t.Helper()
	require.NotEmpty(t, os.Getenv("KODELET_BASE_PATH"), "tests must not open the user's database")
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	return Open(t, path)
}

// VerifyConfiguration reports whether database has the settings db.Open
// applies: WAL, NORMAL synchronous mode and foreign keys on the writer, and
// read-only mode on every reader connection. It holds all reader connections
// at once, so it waits for readers the caller is still using.
func VerifyConfiguration(ctx context.Context, database *db.DB) error {
	for _, setting := range []struct{ pragma, want string }{
		{pragma: "journal_mode", want: "wal"},
		{pragma: "synchronous", want: "1"},
		{pragma: "foreign_keys", want: "1"},
	} {
		var got string
		if err := database.Writer.GetContext(ctx, &got, "PRAGMA "+setting.pragma); err != nil {
			return errors.Wrapf(err, "failed to query the writer's %s", setting.pragma)
		}
		if strings.ToLower(got) != setting.want {
			return errors.Errorf("expected writer %s %s, got %s", setting.pragma, setting.want, got)
		}
	}

	// Check each connection, not just whichever the pool hands out first.
	readers := max(database.Reader.Stats().MaxOpenConnections, 1)
	connections := make([]*sqlx.Conn, 0, readers)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for range readers {
		connection, err := database.Reader.Connx(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to open a reader connection")
		}
		connections = append(connections, connection)
		var queryOnly string
		if err := connection.GetContext(ctx, &queryOnly, "PRAGMA query_only"); err != nil {
			return errors.Wrap(err, "failed to query a reader's query_only mode")
		}
		if queryOnly != "1" {
			return errors.Errorf("expected read-only readers, got query_only %s", queryOnly)
		}
	}
	return nil
}
