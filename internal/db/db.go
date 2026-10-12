// SPDX-License-Identifier: Apache-2.0

// Package db opens Rewind's SQLite database and applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// FileName is the database file inside the data directory.
const FileName = "rewind.db"

// busyTimeoutMillis is how long SQLite waits for a lock before failing.
const busyTimeoutMillis = 5000

// EnsureDir makes sure dir exists and is a directory. A missing directory is
// created with mode 0700; an existing one is used as-is and its permissions
// are never changed.
func EnsureDir(dir string) (created bool, err error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return false, fmt.Errorf("data directory %s exists but is not a directory", dir)
		}
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, fmt.Errorf("cannot create data directory %s: %w", dir, err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("cannot access data directory %s: %w", dir, err)
	}
}

// Open opens the SQLite database at path, creating the file with mode 0600 if
// it does not exist, and applies pending migrations. It never deletes or
// resets an existing database: on any failure the file is left untouched and
// an error is returned.
func Open(ctx context.Context, path string, logger *slog.Logger) (*sql.DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("database path %q must not contain '?' or '#'", path)
	}
	if err := createFileIfMissing(path); err != nil {
		return nil, err
	}

	// Pragmas are applied by the driver to every new connection.
	// - foreign_keys: enforce REFERENCES constraints (off by default in SQLite).
	// - busy_timeout: wait for locks instead of failing immediately.
	// - journal_mode=DELETE: SQLite's default rollback journal, so the database
	//   is a single file at rest (see docs/architecture.md, "Database").
	// - synchronous=FULL: durable commits in rollback-journal mode.
	// _txlock=immediate takes the write lock at BEGIN, so read-then-write
	// transactions (such as first-run setup) cannot interleave.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)"+
		"&_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)&_txlock=immediate",
		filepath.ToSlash(path), busyTimeoutMillis)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open database %s: %w", path, err)
	}
	// One connection serializes all access. Rewind's write load is small, and
	// this rules out SQLITE_BUSY between the application's own connections.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot open database %s: %w", path, err)
	}
	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		db.Close()
		return nil, fmt.Errorf("database %s: foreign key enforcement could not be enabled (%v)", path, err)
	}

	applied, err := Migrate(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w (the database was not modified beyond the last successful migration; it has not been reset)", path, err)
	}
	if len(applied) > 0 {
		logger.Info("database migrations applied", "path", path, "migrations", applied)
	}
	return db, nil
}

// createFileIfMissing pre-creates the database file with mode 0600 so that
// password and session hashes are not world-readable. Existing files keep
// their permissions.
func createFileIfMissing(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot create database file %s: %w", path, err)
	}
	return f.Close()
}

// InTx runs fn inside a transaction, committing if fn returns nil and rolling
// back otherwise.
func InTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
