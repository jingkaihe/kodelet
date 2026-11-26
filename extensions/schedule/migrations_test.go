package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"
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

func appliedVersions(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	applied, err := queryAppliedMigrations(t.Context(), db)
	require.NoError(t, err)
	versions := make([]int64, len(applied))
	for i, m := range applied {
		versions[i] = m.Version
	}
	return versions
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count))
	return count == 1
}

// countingMigration creates table and counts how often it runs.
func countingMigration(version int64, table string, runs *atomic.Int32) migration {
	return migration{
		Version:     version,
		Description: "create " + table,
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if runs != nil {
				runs.Add(1)
			}
			_, err := tx.ExecContext(ctx, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)")
			return err
		},
	}
}

func TestOpenStoreRecordsScheduleMigrations(t *testing.T) {
	_, path := newTestStore(t)
	db := openTestDB(t, path)

	var want []int64
	for _, m := range scheduleMigrations() {
		want = append(want, m.Version)
	}
	assert.Equal(t, want, appliedVersions(t, db))
	for _, table := range []string{"schedules", "runs", "daemon"} {
		assert.True(t, tableExists(t, db, table), table)
	}

	before, err := queryAppliedMigrations(t.Context(), db)
	require.NoError(t, err)
	store, err := openStore(t.Context(), path)
	require.NoError(t, err)
	store.Close()
	after, err := queryAppliedMigrations(t.Context(), db)
	require.NoError(t, err)
	assert.Equal(t, before, after, "reopening applies nothing")
}

func TestMigrateAppliesOnlyPendingMigrations(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	var first, second atomic.Int32
	v1 := countingMigration(20260101000000, "first", &first)
	v2 := countingMigration(20260201000000, "second", &second)

	require.NoError(t, migrate(t.Context(), db, []migration{v1}))
	require.NoError(t, migrate(t.Context(), db, []migration{v1, v2}))
	require.NoError(t, migrate(t.Context(), db, []migration{v1, v2}))

	assert.EqualValues(t, 1, first.Load())
	assert.EqualValues(t, 1, second.Load())
	assert.Equal(t, []int64{20260101000000, 20260201000000}, appliedVersions(t, db))
}

func TestMigrateRollsBackAllPendingMigrationsOnFailure(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	failing := migration{
		Version:     20260201000000,
		Description: "broken",
		Up: func(context.Context, *sql.Tx) error {
			return errors.New("boom")
		},
	}

	err := migrate(t.Context(), db, []migration{countingMigration(20260101000000, "first", nil), failing})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration 20260201000000 (broken): boom")
	assert.False(t, tableExists(t, db, "first"))
	assert.False(t, tableExists(t, db, "schema_migrations"), "nothing is recorded")

	// The pending migrations apply together once fixed.
	require.NoError(t, migrate(t.Context(), db, []migration{countingMigration(20260101000000, "first", nil)}))
	assert.Equal(t, []int64{20260101000000}, appliedVersions(t, db))
}

func TestMigrateRefusesDatabasesFromNewerReleases(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	v1 := countingMigration(20260101000000, "first", nil)
	v2 := countingMigration(20260201000000, "second", nil)
	require.NoError(t, migrate(t.Context(), db, []migration{v1, v2}))

	err := migrate(t.Context(), db, []migration{v1})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer kodelet-extension-schedule (create second)")
}

func TestMigrateValidatesMigrations(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "test.db"))
	v1 := countingMigration(20260101000000, "first", nil)

	tests := map[string][]migration{
		"duplicate":      {v1, v1},
		"out of order":   {countingMigration(20260201000000, "second", nil), v1},
		"no description": {{Version: 1, Up: v1.Up}},
		"no up":          {{Version: 1, Description: "nothing"}},
	}
	for name, migrations := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, migrate(t.Context(), db, migrations))
		})
	}
	assert.False(t, tableExists(t, db, "schema_migrations"), "invalid lists change nothing")
}

func TestMigrateRunsOnceAcrossConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	var runs atomic.Int32
	migrations := []migration{countingMigration(20260101000000, "first", &runs)}

	// Separate pools stand in for the extension and daemon processes.
	const processes = 4
	dbs := make([]*sql.DB, processes)
	for i := range dbs {
		dbs[i] = openTestDB(t, path)
	}
	var wg sync.WaitGroup
	errs := make([]error, processes)
	for i, db := range dbs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = migrate(t.Context(), db, migrations)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
	assert.EqualValues(t, 1, runs.Load())
	assert.Equal(t, []int64{20260101000000}, appliedVersions(t, dbs[0]))
}

func TestScheduleMigrationsAreValid(t *testing.T) {
	assert.NoError(t, validateMigrations(scheduleMigrations()))
}
