// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migration is one numbered schema change, loaded from
// migrations/NNNN_name.sql. Applied migrations must never be edited; schema
// changes always go in a new file.
type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		num, name, ok := strings.Cut(base, "_")
		version, err := strconv.Atoi(num)
		if !ok || err != nil || version < 1 || name == "" {
			return nil, fmt.Errorf("migration file %q must be named NNNN_name.sql", e.Name())
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", prev, e.Name(), version)
		}
		seen[version] = e.Name()
		body, err := fs.ReadFile(files, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: base, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Migrate applies every pending migration in order, each in its own
// transaction together with its schema_migrations row. It returns the names
// of the migrations applied. It refuses to run against a database that has
// migrations this build does not know about (for example, one created by a
// newer Rewind version), and it never drops or resets data to recover.
func Migrate(ctx context.Context, db *sql.DB) ([]string, error) {
	return migrate(ctx, db, migrationFiles)
}

func migrate(ctx context.Context, db *sql.DB, files fs.FS) ([]string, error) {
	migrations, err := loadMigrations(files)
	if err != nil {
		return nil, fmt.Errorf("loading migrations: %w", err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := db.QueryContext(ctx, "SELECT version, name FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var n string
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading schema_migrations: %w", err)
		}
		applied[v] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}

	known := map[int]string{}
	for _, m := range migrations {
		known[m.version] = m.name
	}
	for v, n := range applied {
		want, ok := known[v]
		if !ok {
			return nil, fmt.Errorf("database has migration %d (%s) that this version of Rewind does not know; "+
				"it was probably created by a newer Rewind version, so refusing to continue", v, n)
		}
		if want != n {
			return nil, fmt.Errorf("migration %d is recorded as %q but this build calls it %q; refusing to continue", v, n, want)
		}
	}

	var done []string
	for _, m := range migrations {
		if _, ok := applied[m.version]; ok {
			continue
		}
		err := InTx(ctx, db, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
				m.version, m.name, time.Now().Unix())
			return err
		})
		if err != nil {
			return done, fmt.Errorf("migration %s failed: %w", m.name, err)
		}
		done = append(done, m.name)
	}
	return done, nil
}
