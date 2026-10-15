// SPDX-License-Identifier: Apache-2.0

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
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

var discard = slog.New(slog.DiscardHandler)

func openTemp(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestOpenCreatesDatabaseAndSchema(t *testing.T) {
	db, path := openTemp(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("database file not created at %s: %v", path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("database file mode = %v, want 0600", info.Mode().Perm())
	}

	for _, table := range []string{"schema_migrations", "users", "sessions"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	var name string
	if err := db.QueryRow("SELECT name FROM schema_migrations WHERE version = 1").Scan(&name); err != nil || name != "0001_auth" {
		t.Errorf("schema_migrations row = %q, %v; want 0001_auth", name, err)
	}

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Errorf("journal_mode = %q, %v; want delete", mode, err)
	}
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != busyTimeoutMillis {
		t.Errorf("busy_timeout = %d, %v; want %d", timeout, err, busyTimeoutMillis)
	}
}

func TestOpenPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir with spaces")
	if _, err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at %s: %v", path, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != FileName {
			t.Errorf("unexpected file %q in data dir", e.Name())
		}
	}
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for i := 0; i < 3; i++ {
		db, err := Open(context.Background(), path, discard)
		if err != nil {
			t.Fatalf("Open() #%d error = %v", i+1, err)
		}
		want, _ := loadMigrations(migrationFiles)
		var n int
		if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n != len(want) {
			t.Errorf("Open() #%d: schema_migrations rows = %d, %v; want %d", i+1, n, err, len(want))
		}
		db.Close()
	}
}

func TestDataPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'x', 1, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(context.Background(), path, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow("SELECT username FROM users").Scan(&name); err != nil || name != "owner" {
		t.Errorf("after reopen username = %q, %v", name, err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db, _ := openTemp(t)
	_, err := db.Exec("INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at) VALUES (zeroblob(32), 999, 1, 1, 2)")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("insert with missing user: error = %v, want FOREIGN KEY constraint failure", err)
	}

	// Deleting a user cascades to its sessions.
	res, err := db.Exec("INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'x', 1, 1, 1)")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	if _, err := db.Exec("INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at) VALUES (zeroblob(32), ?, 1, 1, 2)", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM users WHERE id = ?", uid); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("sessions after user delete = %d, want 0 (ON DELETE CASCADE)", n)
	}
}

func TestUsernameUniqueCaseInsensitive(t *testing.T) {
	db, _ := openTemp(t)
	ins := "INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES (?, 'x', 1, 1, 1)"
	if _, err := db.Exec(ins, "Owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ins, "owner"); err == nil {
		t.Error("duplicate username differing only in case was accepted")
	}
}

func TestOpenRefusesNewerDatabase(t *testing.T) {
	db, path := openTemp(t)
	if _, err := db.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_future', 1)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)

	_, err := Open(context.Background(), path, discard)
	if err == nil || !strings.Contains(err.Error(), "newer Rewind version") {
		t.Fatalf("Open() error = %v, want newer-version refusal", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("database file changed after a refused open")
	}
}

func TestOpenRefusesRenamedMigration(t *testing.T) {
	db, path := openTemp(t)
	if _, err := db.Exec("UPDATE schema_migrations SET name = '0001_other' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(context.Background(), path, discard); err == nil {
		t.Fatal("Open() error = nil, want refusal for mismatched migration name")
	}
}

func TestFailedMigrationRollsBackAndKeepsData(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.Exec("INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'x', 1, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"migrations/0099_broken.sql": {Data: []byte("CREATE TABLE half_done (id INTEGER); THIS IS NOT SQL;")}}
	real, _ := fs.ReadDir(migrationFiles, "migrations")
	for _, e := range real {
		files["migrations/"+e.Name()] = &fstest.MapFile{Data: mustRead(t, "migrations/"+e.Name())}
	}
	if _, err := migrate(context.Background(), db, files); err == nil {
		t.Fatal("migrate() with broken migration: error = nil")
	}
	var n int
	db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'half_done'").Scan(&n)
	if n != 0 {
		t.Error("partially applied migration was not rolled back")
	}
	db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version = 99").Scan(&n)
	if n != 0 {
		t.Error("failed migration was recorded as applied")
	}
	db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n != 1 {
		t.Errorf("users after failed migration = %d, want 1 (data kept)", n)
	}
}

func TestLoadMigrationsRejectsBadNames(t *testing.T) {
	for _, files := range []fstest.MapFS{
		{"migrations/auth.sql": {Data: []byte("")}},
		{"migrations/0001_a.sql": {Data: []byte("")}, "migrations/0001_b.sql": {Data: []byte("")}},
	} {
		if _, err := loadMigrations(files); err == nil {
			t.Errorf("loadMigrations(%v) error = nil", files)
		}
	}
}

func TestOpenFailsOnCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	garbage := []byte(strings.Repeat("this is not a sqlite database ", 200))
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path, discard); err == nil {
		t.Fatal("Open() on corrupt file: error = nil")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(garbage) {
		t.Error("corrupt database file was modified or replaced")
	}
}

func TestOpenFailsWhenDirectoryMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", FileName)
	if _, err := Open(context.Background(), path, discard); err == nil {
		t.Fatal("Open() in missing directory: error = nil")
	}
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b")
	created, err := EnsureDir(dir)
	if err != nil || !created {
		t.Fatalf("EnsureDir(new) = %v, %v", created, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Errorf("new dir mode = %v, want 0700", info.Mode().Perm())
		}
		// Existing directories keep their permissions.
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	created, err = EnsureDir(dir)
	if err != nil || created {
		t.Fatalf("EnsureDir(existing) = %v, %v", created, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o755 {
			t.Errorf("existing dir mode changed to %v", info.Mode().Perm())
		}
	}

	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	if _, err := EnsureDir(file); err == nil {
		t.Error("EnsureDir(file) error = nil, want not-a-directory error")
	}
}

func TestInTxRollsBack(t *testing.T) {
	db, _ := openTemp(t)
	boom := errors.New("boom")
	err := InTx(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'x', 1, 1, 1)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx() error = %v, want boom", err)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n != 0 {
		t.Errorf("users = %d after rollback, want 0", n)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := migrationFiles.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestUpgradeFromPhase2KeepsAuthData builds a database that has only the
// Phase 2 migration, adds an account and a session, then runs the current
// migrations as a real upgrade would.
func TestUpgradeFromPhase2KeepsAuthData(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := createFileIfMissing(path); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	phase2 := fstest.MapFS{"migrations/0001_auth.sql": {Data: mustRead(t, "migrations/0001_auth.sql")}}
	if _, err := migrate(context.Background(), conn, phase2); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at)
		VALUES ('owner', '$argon2id$hash', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at)
		VALUES (zeroblob(32), 1, 1, 1, 99999999999)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("upgrade Open() error = %v", err)
	}
	defer db.Close()
	var versions []int
	rows, _ := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	for rows.Next() {
		var v int
		rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	if want := knownVersions(t); fmt.Sprint(versions) != fmt.Sprint(want) {
		t.Fatalf("applied versions = %v, want %v", versions, want)
	}
	var user, hash string
	var sessions int
	db.QueryRow("SELECT username, password_hash FROM users").Scan(&user, &hash)
	db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions)
	if user != "owner" || hash != "$argon2id$hash" || sessions != 1 {
		t.Errorf("auth data after upgrade: user=%q hash=%q sessions=%d", user, hash, sessions)
	}
	for _, table := range []string{"library_roots", "tracks", "scans"} {
		var n int
		db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n)
		if n != 1 {
			t.Errorf("table %s missing after upgrade", table)
		}
	}
}

func TestLibraryConstraints(t *testing.T) {
	db, _ := openTemp(t)
	insertTrack := func(rootID int64, rel string) error {
		_, err := db.Exec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title,
			metadata_status, first_seen_at, updated_at) VALUES (?, ?, 1, 1, 'mp3', 't', 'ok', 1, 1)`, rootID, rel)
		return err
	}
	if err := insertTrack(42, "a.mp3"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("track for unknown root: error = %v, want FOREIGN KEY failure", err)
	}
	res, err := db.Exec("INSERT INTO library_roots (configured_path, path, created_at) VALUES ('/m', '/m', 1)")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := res.LastInsertId()
	if err := insertTrack(root, "a.mp3"); err != nil {
		t.Fatal(err)
	}
	if err := insertTrack(root, "a.mp3"); err == nil {
		t.Error("duplicate (root_id, rel_path) accepted")
	}
	// A root with tracks cannot be deleted: tracks are never removed implicitly.
	if _, err := db.Exec("DELETE FROM library_roots WHERE id = ?", root); err == nil {
		t.Error("deleting a root with tracks succeeded")
	}
	for _, bad := range []string{
		"INSERT INTO library_roots (configured_path, path, created_at, status) VALUES ('/x', '/x', 1, 'bogus')",
		"INSERT INTO scans (root_id, started_at, status) VALUES (1, 1, 'bogus')",
		"UPDATE tracks SET status = 'deleted'",
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Errorf("constraint not enforced: %s", bad)
		}
	}
}

func knownVersions(t *testing.T) []int {
	t.Helper()
	ms, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, m := range ms {
		out = append(out, m.version)
	}
	return out
}

// TestUpgradeFromLibrarySchemaAddsPlaylists upgrades a database that has the
// auth and library migrations (Phases 2-7) and checks that tracks and the
// account survive and the playlist and preference tables appear.
func TestUpgradeFromLibrarySchemaAddsPlaylists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := createFileIfMissing(path); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{
		"migrations/0001_auth.sql":    {Data: mustRead(t, "migrations/0001_auth.sql")},
		"migrations/0002_library.sql": {Data: mustRead(t, "migrations/0002_library.sql")},
	}
	if _, err := migrate(context.Background(), conn, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'h', 1, 1, 1)`,
		`INSERT INTO library_roots (configured_path, path, created_at) VALUES ('/m', '/m', 1)`,
		`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title, metadata_status, first_seen_at, updated_at)
			VALUES (1, 'a.mp3', 1, 1, 'mp3', 'Kept', 'ok', 1, 1)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("upgrade Open() error = %v", err)
	}
	defer db.Close()
	var title string
	var users int
	db.QueryRow("SELECT title FROM tracks").Scan(&title)
	db.QueryRow("SELECT count(*) FROM users").Scan(&users)
	if title != "Kept" || users != 1 {
		t.Errorf("after upgrade: track title %q, users %d", title, users)
	}
	for _, table := range []string{"playlists", "playlist_tracks", "user_preferences"} {
		var n int
		db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n)
		if n != 1 {
			t.Errorf("table %s missing after upgrade", table)
		}
	}
	// Opening again applies nothing.
	db.Close()
	again, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestPlaylistConstraints(t *testing.T) {
	db, _ := openTemp(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, username, password_hash, created_at, updated_at, password_changed_at) VALUES (1, 'owner', 'h', 1, 1, 1)`)
	mustExec(`INSERT INTO library_roots (id, configured_path, path, created_at) VALUES (1, '/m', '/m', 1)`)
	for i := 1; i <= 2; i++ {
		mustExec(`INSERT INTO tracks (id, root_id, rel_path, file_size, file_mtime_ns, format, title, metadata_status, first_seen_at, updated_at)
			VALUES (?, 1, ?, 1, 1, 'mp3', 't', 'ok', 1, 1)`, i, fmt.Sprintf("%d.mp3", i))
	}
	mustExec(`INSERT INTO playlists (id, user_id, name, created_at, updated_at) VALUES (1, 1, 'Mix', 1, 1)`)
	mustExec(`INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 1, 0, 1)`)
	mustExec(`INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 2, 5, 1)`)

	for _, bad := range []string{
		"INSERT INTO playlists (user_id, name, created_at, updated_at) VALUES (1, 'MIX', 1, 1)",        // name, any ASCII case
		"INSERT INTO playlists (user_id, name, created_at, updated_at) VALUES (99, 'Other', 1, 1)",     // unknown user
		"INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 1, 9, 1)",  // duplicate track
		"INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 99, 9, 1)", // unknown track
		"INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (99, 1, 9, 1)", // unknown playlist
		"UPDATE playlist_tracks SET position = 0 WHERE track_id = 2",                                   // duplicate position
		"UPDATE playlist_tracks SET position = -1 WHERE track_id = 2",                                  // negative position
		"INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (99, 'k', '1', 1)",      // unknown user
		"INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (1, '', '1', 1)",        // empty key
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Errorf("constraint not enforced: %s", bad)
		}
	}

	// Deleting a playlist removes its entries but not the tracks.
	mustExec("DELETE FROM playlists WHERE id = 1")
	var entries, tracks int
	db.QueryRow("SELECT count(*) FROM playlist_tracks").Scan(&entries)
	db.QueryRow("SELECT count(*) FROM tracks").Scan(&tracks)
	if entries != 0 || tracks != 2 {
		t.Errorf("after deleting the playlist: %d entries, %d tracks; want 0, 2", entries, tracks)
	}
}

// TestUpgradeAddsThemeSettings upgrades a database with every migration up
// to preferences (0004) and real data, and checks that the theme migration
// keeps accounts, tracks, playlists and preferences, including the legacy
// custom colors, and only adds its table.
func TestUpgradeAddsThemeSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := createFileIfMissing(path); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for _, name := range []string{"0001_auth.sql", "0002_library.sql", "0003_playlists.sql", "0004_preferences.sql"} {
		old["migrations/"+name] = &fstest.MapFile{Data: mustRead(t, "migrations/"+name)}
	}
	if _, err := migrate(context.Background(), conn, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, created_at, updated_at, password_changed_at) VALUES (1, 'owner', 'h', 1, 1, 1)`,
		`INSERT INTO library_roots (id, configured_path, path, created_at) VALUES (1, '/m', '/m', 1)`,
		`INSERT INTO tracks (id, root_id, rel_path, file_size, file_mtime_ns, format, title, metadata_status, first_seen_at, updated_at)
			VALUES (1, 1, 'a.mp3', 1, 1, 'mp3', 'Kept', 'ok', 1, 1)`,
		`INSERT INTO playlists (id, user_id, name, created_at, updated_at) VALUES (1, 1, 'Mix', 1, 1)`,
		`INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 1, 0, 1)`,
		`INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (1, 'theme', '"light"', 1)`,
		`INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (1, 'accent_color', '"#ff8800"', 1)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("upgrade Open() error = %v", err)
	}
	defer db.Close()
	var title, accent, theme string
	var entries, settings int
	db.QueryRow("SELECT title FROM tracks").Scan(&title)
	db.QueryRow("SELECT count(*) FROM playlist_tracks").Scan(&entries)
	db.QueryRow("SELECT value FROM user_preferences WHERE key = 'accent_color'").Scan(&accent)
	db.QueryRow("SELECT value FROM user_preferences WHERE key = 'theme'").Scan(&theme)
	db.QueryRow("SELECT count(*) FROM theme_settings").Scan(&settings)
	if title != "Kept" || entries != 1 || accent != `"#ff8800"` || theme != `"light"` || settings != 0 {
		t.Errorf("after upgrade: title %q, entries %d, accent %s, theme %s, theme rows %d", title, entries, accent, theme, settings)
	}

	for _, bad := range []string{
		`INSERT INTO theme_settings (user_id, theme_id, updated_at) VALUES (99, 'light', 1)`,             // unknown user
		`INSERT INTO theme_settings (user_id, theme_id, updated_at) VALUES (1, '', 1)`,                   // empty ID
		`INSERT INTO theme_settings (user_id, theme_id, enabled, updated_at) VALUES (1, 'light', 2, 1)`,  // enabled not 0/1
		`INSERT INTO theme_settings (user_id, theme_id, tokens, updated_at) VALUES (1, 'light', 'x', 1)`, // tokens too short
		`INSERT INTO theme_settings (user_id, theme_id, tokens, updated_at) VALUES (1, 'light', ?, 1)`,   // tokens too long
	} {
		var args []any
		if strings.Contains(bad, "?") {
			args = append(args, "{"+strings.Repeat(" ", 4096)+"}")
		}
		if _, err := db.Exec(bad, args...); err == nil {
			t.Errorf("constraint not enforced: %s", bad)
		}
	}
	// Deleting the user removes their theme settings.
	if _, err := db.Exec(`INSERT INTO theme_settings (user_id, theme_id, tokens, updated_at) VALUES (1, 'light', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Exec("DELETE FROM users WHERE id = 1")
	db.QueryRow("SELECT count(*) FROM theme_settings").Scan(&settings)
	if settings != 0 {
		t.Errorf("theme rows after deleting the user = %d", settings)
	}
}

// TestUpgradeAddsListeningTables upgrades a database at migration 0005 with
// data and checks that 0006 keeps it, adds its columns with defaults, and
// enforces its constraints.
func TestUpgradeAddsListeningTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := createFileIfMissing(path); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for _, name := range []string{"0001_auth.sql", "0002_library.sql", "0003_playlists.sql", "0004_preferences.sql", "0005_themes.sql"} {
		old["migrations/"+name] = &fstest.MapFile{Data: mustRead(t, "migrations/"+name)}
	}
	if _, err := migrate(context.Background(), conn, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, created_at, updated_at, password_changed_at) VALUES (1, 'owner', 'h', 1, 1, 1)`,
		`INSERT INTO library_roots (id, configured_path, path, created_at) VALUES (1, '/m', '/m', 1)`,
		`INSERT INTO tracks (id, root_id, rel_path, file_size, file_mtime_ns, format, title, album, metadata_status, first_seen_at, updated_at)
			VALUES (1, 1, 'a.mp3', 1, 1, 'mp3', 'Kept', 'Album', 'ok', 1, 1)`,
		`INSERT INTO playlists (id, user_id, name, created_at, updated_at) VALUES (1, 1, 'Mix', 1, 1)`,
		`INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (1, 1, 0, 1)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	db, err := Open(context.Background(), path, discard)
	if err != nil {
		t.Fatalf("upgrade Open() error = %v", err)
	}
	defer db.Close()
	var title string
	var version, revision, entries int
	var artwork sql.NullInt64
	db.QueryRow("SELECT title, metadata_version, revision FROM tracks").Scan(&title, &version, &revision)
	db.QueryRow("SELECT count(*) FROM playlist_tracks").Scan(&entries)
	db.QueryRow("SELECT artwork_track_id FROM playlists").Scan(&artwork)
	if title != "Kept" || version != 0 || revision != 0 || entries != 1 || artwork.Valid {
		t.Errorf("after upgrade: %q v%d r%d entries %d artwork %v", title, version, revision, entries, artwork)
	}
	for _, bad := range []string{
		`INSERT INTO favorites (user_id, track_id, created_at) VALUES (1, 99, 1)`,
		`INSERT INTO play_history (user_id, track_id, played_at, listened_ms, play_key) VALUES (1, 1, 1, 0, 'short')`,
		`INSERT INTO play_history (user_id, track_id, played_at, listened_ms, play_key) VALUES (1, 1, 1, -5, 'long-enough-key')`,
		`INSERT INTO track_artwork (track_id, file_size, file_mtime_ns, status, checked_at) VALUES (1, 1, 1, 'maybe', 1)`,
		`INSERT INTO playback_state (user_id, state, revision, updated_at) VALUES (1, '', 1, 1)`,
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Errorf("constraint not enforced: %s", bad)
		}
	}
	db.Exec(`INSERT INTO play_history (user_id, track_id, played_at, listened_ms, play_key) VALUES (1, 1, 1, 0, 'long-enough-key')`)
	if _, err := db.Exec(`INSERT INTO play_history (user_id, track_id, played_at, listened_ms, play_key) VALUES (1, 1, 2, 0, 'long-enough-key')`); err == nil {
		t.Error("duplicate play key accepted")
	}
}
