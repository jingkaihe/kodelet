package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
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
	assert.Equal(t, 1, db.Writer.Stats().MaxOpenConnections)
	assert.Equal(t, readerConnections, db.Reader.Stats().MaxOpenConnections)
	assert.NoError(t, (*DB)(nil).Close())
}

func TestOpenReaderRejectsWrites(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "readonly.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Writer.ExecContext(t.Context(), `CREATE TABLE probe (value INTEGER)`)
	require.NoError(t, err)

	_, err = database.Reader.ExecContext(t.Context(), `INSERT INTO probe VALUES (1)`)
	require.ErrorContains(t, err, "readonly")
	tx, err := database.Reader.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(t.Context(), `INSERT INTO probe VALUES (1)`)
	require.ErrorContains(t, err, "readonly")
	require.NoError(t, tx.Rollback())
}

func TestOpenReadersRunAlongsideWriterTransaction(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "concurrent.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Writer.ExecContext(t.Context(), `CREATE TABLE probe (value INTEGER)`)
	require.NoError(t, err)

	tx, err := database.Writer.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `INSERT INTO probe VALUES (1)`)
	require.NoError(t, err)

	// Neither the open write transaction nor another reader may block a read.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	first, err := database.Reader.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer first.Rollback()
	var count int
	require.NoError(t, first.GetContext(ctx, &count, `SELECT COUNT(*) FROM probe`))
	assert.Zero(t, count, "readers must not see uncommitted writes")
	second, err := database.Reader.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer second.Rollback()
	require.NoError(t, second.GetContext(ctx, &count, `SELECT COUNT(*) FROM probe`))
	assert.Zero(t, count)
	assert.Equal(t, 2, database.Reader.Stats().InUse)

	require.NoError(t, tx.Commit())
	require.NoError(t, database.Reader.GetContext(ctx, &count, `SELECT COUNT(*) FROM probe`))
	assert.Equal(t, 1, count, "reads that begin after a commit must see it")
}

func TestOpenWriterTransactionsBeginImmediate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "immediate.db")
	database, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Writer.ExecContext(t.Context(), `CREATE TABLE probe (value INTEGER)`)
	require.NoError(t, err)
	other, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	// The writer pool keeps its only connection, so this applies to later writes.
	_, err = other.Writer.ExecContext(t.Context(), `PRAGMA busy_timeout = 0`)
	require.NoError(t, err)

	// A deferred transaction takes no lock until it first touches the database.
	tx, err := database.Writer.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	_, err = other.Writer.ExecContext(t.Context(), `INSERT INTO probe VALUES (1)`)
	require.ErrorContains(t, err, "database is locked", "writer transactions must reserve the write lock when they begin")
	require.NoError(t, tx.Rollback())

	_, err = other.Writer.ExecContext(t.Context(), `INSERT INTO probe VALUES (1)`)
	require.NoError(t, err)
}

func TestOpenConfiguresReplacementConnections(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "replacement.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Writer.ExecContext(t.Context(), `
		CREATE TABLE parents (id INTEGER PRIMARY KEY);
		CREATE TABLE children (parent_id INTEGER REFERENCES parents(id) ON DELETE CASCADE);
		INSERT INTO parents VALUES (1);
		INSERT INTO children VALUES (1);
	`)
	require.NoError(t, err)

	// Discard each pool's connection so the next query opens a replacement.
	for _, pool := range []*sqlx.DB{database.Writer, database.Reader} {
		connection, err := pool.Conn(t.Context())
		require.NoError(t, err)
		require.ErrorIs(t, connection.Raw(func(any) error { return driver.ErrBadConn }), driver.ErrBadConn)
		require.Zero(t, pool.Stats().OpenConnections)
	}

	for pool, pragmas := range map[*sqlx.DB]map[string]string{
		database.Writer: {
			"journal_mode": "wal",
			"synchronous":  "1",
			"cache_size":   "1000",
			"temp_store":   "2",
			"busy_timeout": "5000",
			"foreign_keys": "1",
			"query_only":   "0",
		},
		database.Reader: {
			"journal_mode": "wal",
			"cache_size":   "1000",
			"temp_store":   "2",
			"busy_timeout": "5000",
			"query_only":   "1",
		},
	} {
		for pragma, expected := range pragmas {
			var actual string
			require.NoError(t, pool.GetContext(t.Context(), &actual, "PRAGMA "+pragma))
			assert.Equal(t, expected, actual, pragma)
		}
	}

	_, err = database.Writer.ExecContext(t.Context(), "INSERT INTO children VALUES (2)")
	require.ErrorContains(t, err, "FOREIGN KEY constraint failed")
	_, err = database.Writer.ExecContext(t.Context(), "DELETE FROM parents WHERE id = 1")
	require.NoError(t, err)
	var remaining int
	require.NoError(t, database.Reader.GetContext(t.Context(), &remaining, "SELECT COUNT(*) FROM children"))
	assert.Zero(t, remaining, "foreign-key cascades must survive connection replacement")
}

func TestOpenEscapesDatabasePath(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, absolute := range []bool{false, true} {
		name := "relative"
		if absolute {
			name = "absolute"
		}
		t.Run(name, func(t *testing.T) {
			const filename = "storage ?#%&+.db"
			path := filepath.Join(name, "nested", filename)
			if absolute {
				var err error
				path, err = filepath.Abs(path)
				require.NoError(t, err)
			}
			database, err := Open(t.Context(), path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, database.Close()) })
			require.NoError(t, VerifyConfiguration(database))

			_, err = os.Stat(path)
			require.NoError(t, err, "URI metacharacters must remain part of the filename")
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			for _, entry := range entries {
				assert.True(t, strings.HasPrefix(entry.Name(), filename), "unexpected database file %q", entry.Name())
			}
		})
	}
}

func TestOpenCanceledQueryReleasesWALSnapshot(t *testing.T) {
	// A leaked read transaction pins its connection to an old WAL snapshot:
	// later writes fail with SQLITE_BUSY_SNAPSHOT and later reads are stale.
	for _, name := range []string{"writer", "reader"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.db")
			database, err := Open(t.Context(), path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, database.Close()) })
			_, err = database.Writer.ExecContext(t.Context(), `
				CREATE TABLE snapshot_probe (value INTEGER);
				INSERT INTO snapshot_probe VALUES (1);
			`)
			require.NoError(t, err)
			other, err := Open(t.Context(), path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, other.Close()) })

			pool := database.Writer
			if name == "reader" {
				// One connection makes the check below reuse the canceled one.
				pool = database.Reader
				pool.SetMaxOpenConns(1)
			}
			// A large argument widens the window for cancelling mid-query.
			argument := strings.Repeat("x", 32<<20)
			canceled := 0
			for range 5 {
				// sql.Conn skips database/sql's context check, so cancellation reaches the driver.
				connection, err := pool.Connx(t.Context())
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(t.Context())
				finished := make(chan struct{})
				time.AfterFunc(100*time.Microsecond, func() {
					cancel()
					close(finished)
				})
				var value int
				err = connection.GetContext(ctx, &value, "SELECT value FROM snapshot_probe WHERE length(?) > 0", argument)
				<-finished
				if err != nil {
					require.ErrorIs(t, err, context.Canceled)
					canceled++
				}
				require.NoError(t, connection.Close())

				var latest int
				require.NoError(t, other.Writer.GetContext(t.Context(), &latest, "UPDATE snapshot_probe SET value = value + 1 RETURNING value"))
				if name == "reader" {
					require.NoError(t, pool.GetContext(t.Context(), &value, "SELECT value FROM snapshot_probe"))
					assert.Equal(t, latest, value, "cancellation must not pin the pooled reader to a stale snapshot")
					continue
				}
				tx, err := pool.BeginTxx(t.Context(), nil)
				require.NoError(t, err, "cancellation must not leave a transaction open")
				_, err = tx.ExecContext(t.Context(), "DELETE FROM snapshot_probe WHERE value < 0")
				assert.NoError(t, err, "cancellation must not poison the pooled connection")
				require.NoError(t, tx.Rollback())
			}
			require.Positive(t, canceled, "exercise cancellation during a query")
		})
	}
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
	err = db.Writer.QueryRow(`
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
	err = db.Writer.Get(&count, "SELECT COUNT(*) FROM schema_migrations")
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
	err = db.Writer.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='test_table'
	`).Scan(&tableExists)
	require.NoError(t, err)
	assert.True(t, tableExists)

	// Rollback
	err = runner.Rollback(context.Background(), migrations)
	require.NoError(t, err)

	// Verify table is gone
	err = db.Writer.QueryRow(`
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

	_, err = database.Writer.ExecContext(ctx, `DROP TABLE IF EXISTS schema_migrations`)
	require.NoError(t, err)
	_, err = database.Writer.ExecContext(ctx, `CREATE TABLE schema_migrations (component TEXT NOT NULL, version INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = database.Writer.ExecContext(ctx, `INSERT INTO schema_migrations (component, version) VALUES ('old', 1)`)
	require.NoError(t, err)

	runner := NewMigrationRunner(database)
	versions, err := runner.GetAppliedVersions(ctx)

	require.NoError(t, err)
	assert.Empty(t, versions)
	var hasComponent bool
	require.NoError(t, database.Writer.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM pragma_table_info('schema_migrations') WHERE name = 'component'`).Scan(&hasComponent))
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
	require.NoError(t, database.Writer.QueryRowContext(ctx, `SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'rolled_back'`).Scan(&exists))
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

	_, err = database.Writer.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
	require.NoError(t, err)
	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected foreign keys ON")

	_, err = database.Writer.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	require.NoError(t, err)
	_, err = database.Writer.ExecContext(ctx, `PRAGMA synchronous=FULL`)
	require.NoError(t, err)
	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected NORMAL synchronous mode")

	_, err = database.Writer.ExecContext(ctx, `PRAGMA synchronous=NORMAL`)
	require.NoError(t, err)
	database.Reader.SetMaxOpenConns(1)
	_, err = database.Reader.ExecContext(ctx, `PRAGMA query_only=OFF`)
	require.NoError(t, err)
	err = VerifyConfiguration(database)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected read-only readers")
}

func TestVerifyWALAndVerifyConfigurationReturnClosedDatabaseErrors(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "closed.db"))
	require.NoError(t, err)
	require.NoError(t, database.Close())

	err = verifyWAL(context.Background(), database.Writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query journal mode")

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
	require.NoError(t, database.Writer.QueryRowContext(ctx, `SELECT description, applied_at FROM schema_migrations WHERE version = ?`, 20260523000400).Scan(&description, &appliedAt))
	assert.Equal(t, "metadata migration", description)
	assert.False(t, appliedAt.IsZero())
}
