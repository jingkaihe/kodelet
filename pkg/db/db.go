// Package db provides shared SQLite database utilities.
package db

import (
	"context"
	stderrors "errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"

	// sqlite driver for database/sql registration
	_ "modernc.org/sqlite"
)

// Both pools apply these. The driver runs busy_timeout first, then the rest
// alphabetically.
var sharedPragmas = []string{
	"busy_timeout=5000",
	"cache_size=1000",
	"temp_store=memory",
}

// writerPragmas configure durability and integrity for writes. Journal mode is
// persistent, so the writer enables WAL for the readers too.
var writerPragmas = []string{
	"foreign_keys=ON",
	"journal_mode=WAL",
	"synchronous=NORMAL",
}

// readerPragmas make a misrouted write fail instead of contending for the lock.
var readerPragmas = []string{
	"query_only=ON",
}

const (
	// readerConnections bounds concurrent reads. WAL readers do not block one
	// another or the writer, but each connection holds its own page cache.
	readerConnections = 4
	// readerIdleTimeout releases read connections opened for a burst of
	// concurrent reads once the process no longer needs them.
	readerIdleTimeout = time.Minute
)

// DB is a SQLite database opened as one writer connection and a pool of
// read-only connections, and WAL lets reads run alongside the writer and each
// other. A process opens one DB per database file and shares it, so its writes
// queue in Go for the writer connection; writers in other processes still wait
// in SQLite's busy handler, up to busy_timeout.
//
// Use Writer for writes and for transactions that read before writing: its
// transactions begin IMMEDIATE, reserving the write lock before reading, so a
// concurrent commit cannot make the snapshot unwritable (SQLITE_BUSY_SNAPSHOT).
// A transaction begun with sql.TxOptions{ReadOnly: true} skips that
// reservation, so begin read-only transactions on Reader. While holding a
// Writer transaction, pass the transaction rather than using Writer again: the
// second call waits for the only writer connection until its context ends.
//
// Use Reader for reads that need no write in the same transaction, including
// read-only transactions; it sees every write committed before the read began.
type DB struct {
	Writer *sqlx.DB
	Reader *sqlx.DB
	path   string
}

// Path returns the absolute path of the database file. Files that belong to
// the database, such as image artifacts, live beside it.
func (d *DB) Path() string {
	return d.path
}

// Close closes both pools, reporting every failure.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	var errs []error
	if d.Reader != nil {
		errs = append(errs, errors.Wrap(d.Reader.Close(), "failed to close database readers"))
	}
	if d.Writer != nil {
		errs = append(errs, errors.Wrap(d.Writer.Close(), "failed to close database writer"))
	}
	return stderrors.Join(errs...)
}

// DefaultDBPath returns the default path for the shared storage database.
func DefaultDBPath() (string, error) {
	if basePath := os.Getenv("KODELET_BASE_PATH"); basePath != "" {
		return filepath.Join(basePath, "storage.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get home directory")
	}
	return filepath.Join(home, ".kodelet", "storage.db"), nil
}

// Open opens or creates a SQLite database at the given path with optimal configuration.
func Open(ctx context.Context, dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, errors.Wrap(err, "failed to create database directory")
	}

	absolutePath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve database path")
	}

	// The writer opens first: it creates the file and enables WAL.
	writer, err := openPool(ctx, absolutePath, url.Values{
		"_pragma": append(slices.Clone(sharedPragmas), writerPragmas...),
		"_txlock": {"immediate"},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to open database writer")
	}
	writer.SetMaxIdleConns(1)
	writer.SetMaxOpenConns(1)
	if err := verifyWAL(ctx, writer); err != nil {
		writer.Close()
		return nil, errors.Wrap(err, "failed to configure database")
	}

	reader, err := openPool(ctx, absolutePath, url.Values{
		"_pragma": append(slices.Clone(sharedPragmas), readerPragmas...),
	})
	if err != nil {
		writer.Close()
		return nil, errors.Wrap(err, "failed to open database readers")
	}
	reader.SetMaxIdleConns(readerConnections)
	reader.SetMaxOpenConns(readerConnections)
	reader.SetConnMaxIdleTime(readerIdleTimeout)
	// A ping does not read the file, so read the schema to surface access
	// errors here rather than on the first query.
	var tables int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master").Scan(&tables); err != nil {
		reader.Close()
		writer.Close()
		return nil, errors.Wrap(err, "failed to read database")
	}

	return &DB{Writer: writer, Reader: reader, path: absolutePath}, nil
}

// openPool opens and pings a pool whose connections all apply query.
func openPool(ctx context.Context, absolutePath string, query url.Values) (*sqlx.DB, error) {
	// DSN parameters also apply to replacement connections opened by the pool.
	dsn := url.URL{
		Scheme:   "file",
		Path:     absolutePath,
		RawQuery: query.Encode(),
	}
	pool, err := sqlx.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, errors.Wrap(err, "failed to ping database")
	}
	return pool, nil
}

// verifyWAL checks that the writer enabled WAL, which concurrent readers need.
func verifyWAL(ctx context.Context, writer *sqlx.DB) error {
	var journalMode string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}

	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("WAL mode not enabled. Current mode: %s", journalMode)
	}

	return nil
}

// RunMigrations runs the provided database migrations. This should be called once at CLI startup.
func RunMigrations(ctx context.Context, migrations []Migration) error {
	dbPath, err := DefaultDBPath()
	if err != nil {
		return err
	}

	sqlDB, err := Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	runner := NewMigrationRunner(sqlDB)
	return runner.Run(ctx, migrations)
}

// RollbackMigration rolls back the last applied migration.
func RollbackMigration(ctx context.Context, migrations []Migration) error {
	dbPath, err := DefaultDBPath()
	if err != nil {
		return err
	}

	sqlDB, err := Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	runner := NewMigrationRunner(sqlDB)
	return runner.Rollback(ctx, migrations)
}

// GetMigrationStatus returns the list of applied migration versions.
func GetMigrationStatus(ctx context.Context) ([]int64, error) {
	dbPath, err := DefaultDBPath()
	if err != nil {
		return nil, err
	}

	sqlDB, err := Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()

	runner := NewMigrationRunner(sqlDB)
	return runner.GetAppliedVersions(ctx)
}

// VerifyConfiguration checks the writer's WAL, durability and integrity
// settings and that a reader connection rejects writes.
func VerifyConfiguration(database *DB) error {
	var journalMode string
	if err := database.Writer.Get(&journalMode, "PRAGMA journal_mode"); err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}
	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("expected WAL mode, got %s", journalMode)
	}

	var synchronous string
	if err := database.Writer.Get(&synchronous, "PRAGMA synchronous"); err != nil {
		return errors.Wrap(err, "failed to query synchronous mode")
	}
	if synchronous != "1" {
		return errors.Errorf("expected NORMAL synchronous mode, got %s", synchronous)
	}

	var foreignKeys string
	if err := database.Writer.Get(&foreignKeys, "PRAGMA foreign_keys"); err != nil {
		return errors.Wrap(err, "failed to query foreign keys")
	}
	if foreignKeys != "1" {
		return errors.Errorf("expected foreign keys ON, got %s", foreignKeys)
	}

	var queryOnly string
	if err := database.Reader.Get(&queryOnly, "PRAGMA query_only"); err != nil {
		return errors.Wrap(err, "failed to query reader query_only mode")
	}
	if queryOnly != "1" {
		return errors.Errorf("expected read-only readers, got query_only %s", queryOnly)
	}

	return nil
}
