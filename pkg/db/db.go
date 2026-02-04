// Package db provides shared SQLite database utilities.
package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	_ "modernc.org/sqlite"
)

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
func Open(ctx context.Context, dbPath string) (*sqlx.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, errors.Wrap(err, "failed to create database directory")
	}

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open database")
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to ping database")
	}

	if err := Configure(ctx, db); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "failed to configure database")
	}

	return db, nil
}

// Configure sets up SQLite pragmas for optimal WAL mode performance.
func Configure(ctx context.Context, db *sqlx.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=1000",
		"PRAGMA temp_store=memory",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return errors.Wrapf(err, "failed to execute pragma: %s", pragma)
		}
	}

	db.SetMaxIdleConns(1)
	db.SetMaxOpenConns(1)

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
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

// VerifyConfiguration checks if the database is properly configured with WAL mode.
func VerifyConfiguration(db *sqlx.DB) error {
	var journalMode string
	if err := db.Get(&journalMode, "PRAGMA journal_mode"); err != nil {
		return errors.Wrap(err, "failed to query journal mode")
	}
	if strings.ToLower(journalMode) != "wal" {
		return errors.Errorf("expected WAL mode, got %s", journalMode)
	}

	var synchronous string
	if err := db.Get(&synchronous, "PRAGMA synchronous"); err != nil {
		return errors.Wrap(err, "failed to query synchronous mode")
	}
	if synchronous != "1" {
		return errors.Errorf("expected NORMAL synchronous mode, got %s", synchronous)
	}

	var foreignKeys string
	if err := db.Get(&foreignKeys, "PRAGMA foreign_keys"); err != nil {
		return errors.Wrap(err, "failed to query foreign keys")
	}
	if foreignKeys != "1" {
		return errors.Errorf("expected foreign keys ON, got %s", foreignKeys)
	}

	return nil
}
