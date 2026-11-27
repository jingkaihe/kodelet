package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := openDB(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateUpgradesAtomicallyAndRefusesDowngrades(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	v1 := migration{Version: 1, Description: "first", Up: execMigration("CREATE TABLE first (id INTEGER PRIMARY KEY)")}
	v2 := migration{Version: 2, Description: "second", Up: execMigration("CREATE TABLE second (id INTEGER PRIMARY KEY)")}
	broken := migration{Version: 3, Description: "broken", Up: execMigration("INSERT INTO missing VALUES (1)")}
	require.NoError(t, migrate(t.Context(), db, []migration{v1}))

	err := migrate(t.Context(), db, []migration{v1, v2, broken})
	require.ErrorContains(t, err, "migration 3 (broken)")
	applied, err := queryAppliedMigrations(t.Context(), db)
	require.NoError(t, err)
	require.Len(t, applied, 1, "the failed upgrade must leave the ledger at v1")
	assert.Equal(t, v1.Version, applied[0].Version)

	// Neither CREATE is idempotent: v1 must be skipped and v2 rolled back.
	require.NoError(t, migrate(t.Context(), db, []migration{v1, v2}))
	require.ErrorContains(t, migrate(t.Context(), db, []migration{v1}), "newer kodelet-extension-schedule (second)")
}

func TestMigrateRejectsInvalidDefinitions(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	v1 := migration{Version: 1, Description: "first", Up: execMigration("SELECT 1")}
	for name, migrations := range map[string][]migration{
		"duplicate version": {v1, v1},
		"out of order":      {{Version: 2, Description: "second", Up: v1.Up}, v1},
		"no description":    {{Version: 1, Up: v1.Up}},
		"no up":             {{Version: 1, Description: "first"}},
	} {
		assert.Error(t, migrate(t.Context(), db, migrations), name)
	}
}

func TestMigrateRunsOnceAcrossConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	// Separate pools stand in for the extension and daemon processes. Both
	// must succeed even though the real migrations cannot be applied twice.
	dbs := []*sql.DB{openTestDB(t, path), openTestDB(t, path)}
	migrations := scheduleMigrations()
	errs := make(chan error, len(dbs))
	start := make(chan struct{})
	for _, db := range dbs {
		go func() {
			<-start
			errs <- migrate(t.Context(), db, migrations)
		}()
	}
	close(start)
	for range dbs {
		assert.NoError(t, <-errs)
	}
	applied, err := queryAppliedMigrations(t.Context(), dbs[0])
	require.NoError(t, err)
	require.Len(t, applied, len(migrations))
	for i, m := range migrations {
		assert.Equal(t, m.Version, applied[i].Version)
	}
}
