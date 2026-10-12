// SPDX-License-Identifier: Apache-2.0

// Package library manages music-library roots and scans them into SQLite.
// Source directories are treated as strictly read-only: this package only
// lists directories, stats files, and hands paths to the metadata reader.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Root status values.
const (
	RootPending     = "pending"
	RootAvailable   = "available"
	RootUnavailable = "unavailable"
)

// Root is a configured library directory.
type Root struct {
	ID             int64
	ConfiguredPath string
	Path           string // canonical path that scans traverse
	Enabled        bool
	Status         string
	StatusMessage  string
}

// RootProblem describes a configured path that could not be registered.
type RootProblem struct {
	Path   string
	Reason string
}

// SyncRoots makes the library_roots table match the configured paths:
//
//   - New paths are registered only if they are existing, readable
//     directories. Symlinks in the configured path are resolved once; the
//     canonical path is what scans traverse.
//   - Paths already registered stay registered while temporarily unavailable
//     (for example, a pendrive that is not plugged in); they are marked
//     unavailable instead.
//   - A path that equals, contains, or lies inside another configured root is
//     rejected, so a file can never belong to two roots.
//   - Registered roots that are no longer configured are disabled, never
//     deleted, so their tracks are preserved.
//
// It returns the enabled roots and any configured paths that were rejected.
func SyncRoots(ctx context.Context, db *sql.DB, configured []string, logger *slog.Logger, now time.Time) ([]Root, []RootProblem, error) {
	existing, err := loadRoots(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	byConfigured := map[string]*Root{}
	byPath := map[string]*Root{}
	for i := range existing {
		r := &existing[i]
		byConfigured[r.ConfiguredPath] = r
		byPath[r.Path] = r
	}

	var problems []RootProblem
	reject := func(p, reason string) {
		problems = append(problems, RootProblem{Path: p, Reason: reason})
		logger.Error("library root not used", "path", p, "reason", reason)
	}

	type accepted struct {
		root       *Root // nil for a new root
		configured string
		canonical  string
		available  bool
		reason     string
	}
	var keep []accepted
	claimed := map[int64]bool{}

	for _, raw := range configured {
		p := filepath.Clean(raw)
		if !filepath.IsAbs(p) {
			reject(raw, "path is not absolute")
			continue
		}
		canonical, cerr := canonicalDir(p)
		r := byConfigured[p]
		if r == nil && cerr == nil {
			r = byPath[canonical]
		}
		if r != nil && claimed[r.ID] {
			reject(raw, "duplicate of another configured root")
			continue
		}
		if r != nil && cerr == nil {
			if other := byPath[canonical]; other != nil && other.ID != r.ID {
				reject(raw, fmt.Sprintf("resolves to %s, which is already registered as root %d", canonical, other.ID))
				continue
			}
		}
		if cerr != nil {
			if r == nil {
				reject(raw, cerr.Error()+"; a new library root must exist and be readable when it is first configured")
				continue
			}
			// Known root, currently unavailable: keep it and its tracks.
			canonical = r.Path
		}

		overlap := ""
		for _, k := range keep {
			if rel := relation(canonical, k.canonical); rel != "" {
				overlap = fmt.Sprintf("%s root %s", rel, k.configured)
				break
			}
		}
		if strings.HasPrefix(overlap, "same as") {
			reject(raw, "duplicate of an earlier configured root ("+overlap+")")
			continue
		}
		if overlap != "" {
			reject(raw, "overlaps with an earlier configured root ("+overlap+")")
			continue
		}
		a := accepted{root: r, configured: p, canonical: canonical, available: cerr == nil}
		if cerr != nil {
			a.reason = cerr.Error()
		}
		if r != nil {
			claimed[r.ID] = true
		}
		keep = append(keep, a)
	}

	ts := now.Unix()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	// Disable roots that are no longer configured (or were rejected) first, so
	// their unique paths cannot collide with updates below.
	for _, r := range existing {
		if !claimed[r.ID] && r.Enabled {
			if _, err := tx.ExecContext(ctx, "UPDATE library_roots SET enabled = 0 WHERE id = ?", r.ID); err != nil {
				return nil, nil, err
			}
			logger.Info("library root disabled (no longer configured); its tracks are kept", "root_id", r.ID, "path", r.ConfiguredPath)
		}
	}
	for _, a := range keep {
		status, msg := RootAvailable, sql.NullString{}
		if !a.available {
			status, msg = RootUnavailable, sql.NullString{String: a.reason, Valid: true}
		}
		if a.root == nil {
			res, err := tx.ExecContext(ctx, `INSERT INTO library_roots
				(configured_path, path, enabled, status, status_message, created_at) VALUES (?, ?, 1, ?, ?, ?)`,
				a.configured, a.canonical, status, msg, ts)
			if err != nil {
				return nil, nil, fmt.Errorf("registering library root %s: %w", a.configured, err)
			}
			id, _ := res.LastInsertId()
			logger.Info("library root registered", "root_id", id, "path", a.configured, "canonical", a.canonical)
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_roots
			SET configured_path = ?, path = ?, enabled = 1, status = ?, status_message = ? WHERE id = ?`,
			a.configured, a.canonical, status, msg, a.root.ID); err != nil {
			return nil, nil, fmt.Errorf("updating library root %s: %w", a.configured, err)
		}
		if !a.available {
			logger.Warn("library root unavailable; its tracks are kept", "root_id", a.root.ID, "path", a.configured, "reason", a.reason)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	all, err := loadRoots(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	var enabled []Root
	for _, r := range all {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	return enabled, problems, nil
}

// canonicalDir resolves symlinks and checks that p is a readable directory.
func canonicalDir(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot access directory: %s", describeFSError(err))
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if err := checkReadableDir(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkReadableDir verifies that dir exists, is a directory, and can be
// listed.
func checkReadableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot access directory: %s", describeFSError(err))
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("cannot open directory: %s", describeFSError(err))
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("cannot list directory: %s", describeFSError(err))
	}
	return nil
}

// relation reports how path a relates to root b: "same as", "inside", or
// "contains"; "" if they are unrelated.
func relation(a, b string) string {
	switch {
	case a == b:
		return "same as"
	case within(a, b):
		return "inside"
	case within(b, a):
		return "contains"
	}
	return ""
}

// within reports whether path a lies strictly inside directory b.
func within(a, b string) bool {
	rel, err := filepath.Rel(b, a)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func loadRoots(ctx context.Context, db *sql.DB) ([]Root, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, configured_path, path, enabled, status, coalesce(status_message, '') FROM library_roots ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Root
	for rows.Next() {
		var r Root
		if err := rows.Scan(&r.ID, &r.ConfiguredPath, &r.Path, &r.Enabled, &r.Status, &r.StatusMessage); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Roots returns every registered root, enabled or not.
func Roots(ctx context.Context, db *sql.DB) ([]Root, error) { return loadRoots(ctx, db) }
