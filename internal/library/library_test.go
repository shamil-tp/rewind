// SPDX-License-Identifier: Apache-2.0

package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/db"
	"github.com/shamil-tp/rewind/internal/metadata"
	"github.com/shamil-tp/rewind/internal/testaudio"
)

var bg = context.Background()

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	db     *sql.DB
	logs   *syncBuffer
	logger *slog.Logger
}

func newEnv(t *testing.T) *env {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	conn, err := db.Open(bg, filepath.Join(t.TempDir(), db.FileName), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &env{db: conn, logs: logs, logger: logger}
}

func (e *env) scanner(t *testing.T, opts Options) *Scanner {
	t.Helper()
	opts.DB, opts.Logger = e.db, e.logger
	s, err := NewScanner(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// sync registers the given paths and returns the enabled roots by configured path.
func (e *env) sync(t *testing.T, paths ...string) (map[string]Root, []RootProblem) {
	t.Helper()
	roots, problems, err := SyncRoots(bg, e.db, paths, e.logger, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Root{}
	for _, r := range roots {
		m[r.ConfiguredPath] = r
	}
	return m, problems
}

func (e *env) addRoot(t *testing.T, path string) Root {
	t.Helper()
	m, problems := e.sync(t, path)
	if len(problems) > 0 {
		t.Fatalf("root %s rejected: %v", path, problems)
	}
	return m[filepath.Clean(path)]
}

func (e *env) scan(t *testing.T, s *Scanner, root Root) Result {
	t.Helper()
	res, err := s.Scan(bg, root.ID)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	return res
}

type row struct {
	ID                          int64
	Title, Status, MetaStatus   string
	Artist, Album, AlbumArtist  sql.NullString
	Track, Disc, Year, Duration sql.NullInt64
	Format                      string
}

func (e *env) tracks(t *testing.T, rootID int64) map[string]row {
	t.Helper()
	rows, err := e.db.Query(`SELECT rel_path, id, title, status, metadata_status, artist, album, album_artist,
		track_number, disc_number, year, duration_ms, format FROM tracks WHERE root_id = ?`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]row{}
	for rows.Next() {
		var rel string
		var r row
		if err := rows.Scan(&rel, &r.ID, &r.Title, &r.Status, &r.MetaStatus, &r.Artist, &r.Album, &r.AlbumArtist,
			&r.Track, &r.Disc, &r.Year, &r.Duration, &r.Format); err != nil {
			t.Fatal(err)
		}
		out[rel] = r
	}
	return out
}

func tags(title, artist, album string, track string) map[string][]string {
	return map[string][]string{"TITLE": {title}, "ARTIST": {artist}, "ALBUM": {album}, "TRACKNUMBER": {track}}
}

// library builds a small library: two albums in nested folders plus noise.
func library(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testaudio.Write(t, dir, "Artist A/Album 1/01 One.mp3", 2, tags("One", "Artist A", "Album 1", "1/2"))
	testaudio.Write(t, dir, "Artist A/Album 1/02 Two.flac", 2, tags("Two", "Artist A", "Album 1", "2/2"))
	testaudio.Write(t, dir, "Artist B/Deep/Er/Album 2/03 Three.m4a", 3, tags("Three", "Artist B", "Album 2", "3"))
	testaudio.Write(t, dir, "Loose/Four.ogg", 2, tags("Four", "Artist C", "", ""))
	testaudio.Write(t, dir, "Loose/untagged song.wav", 1, nil)
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Artist A/Album 1/cover.jpg", []byte("jpeg"))
	write("Artist A/Album 1/notes.txt", []byte("text"))
	write("Loose/song.opus", []byte("unsupported format"))
	write("Loose/._Four.ogg", []byte("macOS resource fork"))
	write(".hidden/secret.mp3", testaudio.MP3(1))
	write("$RECYCLE.BIN/deleted.mp3", testaudio.MP3(1))
	return dir
}

// --- discovery and metadata ---

func TestScanDiscoversRecursivelyAndStoresMetadata(t *testing.T) {
	e := newEnv(t)
	dir := library(t)
	root := e.addRoot(t, dir)
	res := e.scan(t, e.scanner(t, Options{}), root)

	if res.Status != ScanCompleted || res.Error != "" {
		t.Fatalf("status = %s, error = %q", res.Status, res.Error)
	}
	c := res.Counts
	if c.FilesFound != 5 || c.TracksAdded != 5 || c.FilesFailed != 0 {
		t.Errorf("counts = %+v; want 5 found, 5 added, 0 failed", c)
	}
	// cover.jpg, notes.txt, song.opus, ._Four.ogg are skipped files; the
	// hidden folder and $RECYCLE.BIN are skipped without being entered.
	if c.FilesSkipped != 4 {
		t.Errorf("skipped = %d, want 4", c.FilesSkipped)
	}

	got := e.tracks(t, root.ID)
	if len(got) != 5 {
		t.Fatalf("tracks = %d: %v", len(got), got)
	}
	three := got["Artist B/Deep/Er/Album 2/03 Three.m4a"]
	if three.Title != "Three" || three.Artist.String != "Artist B" || three.Album.String != "Album 2" ||
		three.Track.Int64 != 3 || three.Format != "m4a" || three.MetaStatus != "ok" || three.Status != "present" {
		t.Errorf("m4a track = %+v", three)
	}
	if d := three.Duration.Int64; d < 2900 || d > 3100 {
		t.Errorf("m4a duration = %d ms", d)
	}
	if one := got["Artist A/Album 1/01 One.mp3"]; one.Track.Int64 != 1 || one.Format != "mp3" {
		t.Errorf("mp3 track = %+v", one)
	}
	// Untagged: title from file name, nothing invented from folder names.
	u := got["Loose/untagged song.wav"]
	if u.Title != "untagged song" || u.MetaStatus != "no_tags" || u.Artist.Valid || u.Album.Valid {
		t.Errorf("untagged track = %+v", u)
	}
	for rel := range got {
		if strings.Contains(rel, "\\") {
			t.Errorf("rel_path %q is not '/'-separated", rel)
		}
	}
}

func TestMalformedAndUnsupportedFilesDoNotAbort(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "good.flac", 1, tags("Good", "A", "B", "1"))
	os.WriteFile(filepath.Join(dir, "garbage.mp3"), bytes.Repeat([]byte("junk "), 300), 0o644)
	os.WriteFile(filepath.Join(dir, "empty.ogg"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "truncated.flac"), testaudio.FLAC(1)[:16], 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hi"), 0o644)
	root := e.addRoot(t, dir)

	var calls atomic.Int64
	s := e.scanner(t, Options{Extract: func(p string) metadata.Metadata { calls.Add(1); return metadata.Extract(p) }})
	res := e.scan(t, s, root)
	if res.Status != ScanCompleted {
		t.Fatalf("status = %s (%s)", res.Status, res.Error)
	}
	if res.Counts.FilesFound != 4 || res.Counts.FilesFailed != 3 || res.Counts.FilesSkipped != 1 {
		t.Errorf("counts = %+v", res.Counts)
	}
	if !strings.Contains(res.Error, "3 files") {
		t.Errorf("summary = %q", res.Error)
	}
	got := e.tracks(t, root.ID)
	if got["good.flac"].MetaStatus != "ok" {
		t.Errorf("good file = %+v", got["good.flac"])
	}
	for _, bad := range []string{"garbage.mp3", "empty.ogg", "truncated.flac"} {
		if r := got[bad]; r.MetaStatus != "error" || r.Title != strings.TrimSuffix(bad, filepath.Ext(bad)) {
			t.Errorf("%s = %+v; want indexed with error status and file-name title", bad, r)
		}
	}
	// Unchanged good files are not parsed again; failed files are retried.
	calls.Store(0)
	res = e.scan(t, s, root)
	if calls.Load() != 3 || res.Counts.TracksUnchanged != 1 {
		t.Errorf("rescan extract calls = %d, unchanged = %d; want 3 and 1", calls.Load(), res.Counts.TracksUnchanged)
	}
	if n := strings.Count(e.logs.String(), "could not read music file metadata"); n == 0 || n > 2*maxLoggedFileErrors {
		t.Errorf("file error log lines = %d", n)
	}
}

func TestFileErrorLoggingIsBounded(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		os.WriteFile(filepath.Join(dir, "bad"+itoa(i)+".mp3"), []byte("x"), 0o644)
	}
	res := e.scan(t, e.scanner(t, Options{}), e.addRoot(t, dir))
	if res.Counts.FilesFailed != 30 {
		t.Fatalf("failed = %d", res.Counts.FilesFailed)
	}
	if n := strings.Count(e.logs.String(), "could not read music file metadata"); n != maxLoggedFileErrors {
		t.Errorf("logged %d file errors, want %d", n, maxLoggedFileErrors)
	}
	if !strings.Contains(e.logs.String(), "not logged individually") {
		t.Error("missing suppression notice")
	}
}

// --- incremental rescans and identity ---

func TestRescanCreatesNoDuplicatesAndKeepsIDs(t *testing.T) {
	e := newEnv(t)
	root := e.addRoot(t, library(t))
	s := e.scanner(t, Options{})
	e.scan(t, s, root)
	before := e.tracks(t, root.ID)

	var calls atomic.Int64
	s2 := e.scanner(t, Options{Extract: func(p string) metadata.Metadata { calls.Add(1); return metadata.Extract(p) }})
	for i := 0; i < 3; i++ {
		res := e.scan(t, s2, root)
		if res.Counts.TracksUnchanged != 5 || res.Counts.TracksAdded != 0 || res.Counts.TracksUpdated != 0 {
			t.Fatalf("rescan %d counts = %+v", i, res.Counts)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("unchanged files were parsed again (%d calls)", calls.Load())
	}
	after := e.tracks(t, root.ID)
	if len(after) != len(before) {
		t.Fatalf("track count changed: %d -> %d", len(before), len(after))
	}
	for rel, r := range before {
		if after[rel].ID != r.ID {
			t.Errorf("%s: ID changed %d -> %d", rel, r.ID, after[rel].ID)
		}
	}
}

func TestChangedFileIsReprocessedWithSameID(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	p := testaudio.Write(t, dir, "song.flac", 2, tags("Old Title", "A", "B", "1"))
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)
	id := e.tracks(t, root.ID)["song.flac"].ID

	testaudio.Tag(t, p, tags("New Title", "A", "B", "1"))
	future := time.Now().Add(time.Hour)
	os.Chtimes(p, future, future)
	res := e.scan(t, s, root)
	if res.Counts.TracksUpdated != 1 {
		t.Fatalf("counts = %+v", res.Counts)
	}
	r := e.tracks(t, root.ID)["song.flac"]
	if r.ID != id || r.Title != "New Title" {
		t.Errorf("after change: %+v, want ID %d and new title", r, id)
	}
}

func TestRenameWithinRootKeepsID(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "a/song.mp3", 3, tags("Song", "A", "B", "1"))
	testaudio.Write(t, dir, "a/other.mp3", 2, tags("Other", "A", "B", "2"))
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)
	id := e.tracks(t, root.ID)["a/song.mp3"].ID

	os.MkdirAll(filepath.Join(dir, "b"), 0o755)
	if err := os.Rename(filepath.Join(dir, "a/song.mp3"), filepath.Join(dir, "b/renamed.mp3")); err != nil {
		t.Fatal(err)
	}
	res := e.scan(t, s, root)
	if res.Status != ScanCompleted || res.Counts.TracksRenamed != 1 || res.Counts.TracksAdded != 0 || res.Counts.TracksMissing != 0 {
		t.Fatalf("counts = %+v", res.Counts)
	}
	got := e.tracks(t, root.ID)
	if len(got) != 2 || got["b/renamed.mp3"].ID != id || got["b/renamed.mp3"].Status != "present" {
		t.Errorf("tracks after rename = %+v; want renamed.mp3 with ID %d", got, id)
	}
}

func TestAmbiguousRenameIsNotGuessed(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	// Two byte-identical files with identical times: a rename cannot be
	// attributed, so they become missing + added rather than a guess.
	a := testaudio.Write(t, dir, "x1.mp3", 2, nil)
	b := filepath.Join(dir, "x2.mp3")
	data, _ := os.ReadFile(a)
	os.WriteFile(b, data, 0o644)
	ts := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(a, ts, ts)
	os.Chtimes(b, ts, ts)
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)

	os.Rename(a, filepath.Join(dir, "y1.mp3"))
	os.Rename(b, filepath.Join(dir, "y2.mp3"))
	res := e.scan(t, s, root)
	if res.Counts.TracksRenamed != 0 || res.Counts.TracksAdded != 2 || res.Counts.TracksMissing != 2 {
		t.Errorf("counts = %+v; want 0 renamed, 2 added, 2 missing", res.Counts)
	}
}

func TestDeletedFileMarkedMissingThenRestored(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	p := testaudio.Write(t, dir, "keep.mp3", 2, tags("Keep", "A", "B", "1"))
	gone := testaudio.Write(t, dir, "gone.ogg", 2, tags("Gone", "A", "B", "2"))
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)
	id := e.tracks(t, root.ID)["gone.ogg"].ID
	saved, _ := os.ReadFile(gone)
	info, _ := os.Stat(gone)

	os.Remove(gone)
	res := e.scan(t, s, root)
	if res.Counts.TracksMissing != 1 {
		t.Fatalf("counts = %+v", res.Counts)
	}
	if r := e.tracks(t, root.ID)["gone.ogg"]; r.Status != "missing" || r.ID != id {
		t.Errorf("deleted file row = %+v; want kept as missing", r)
	}
	var since sql.NullInt64
	e.db.QueryRow("SELECT missing_since FROM tracks WHERE id = ?", id).Scan(&since)
	if !since.Valid {
		t.Error("missing_since not set")
	}

	os.WriteFile(gone, saved, 0o644)
	os.Chtimes(gone, info.ModTime(), info.ModTime())
	res = e.scan(t, s, root)
	if res.Counts.TracksRestored != 1 || res.Counts.TracksAdded != 0 {
		t.Fatalf("restore counts = %+v", res.Counts)
	}
	if r := e.tracks(t, root.ID)["gone.ogg"]; r.Status != "present" || r.ID != id {
		t.Errorf("restored row = %+v", r)
	}
	_ = p
}

func TestFileMovedBetweenRoots(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	os.MkdirAll(b, 0o755)
	src := testaudio.Write(t, a, "song.flac", 2, tags("Song", "A", "B", "1"))
	// Root A keeps another file; a root that suddenly has no audio files at all
	// is treated as unmounted (see TestEmptyMountPointIsNotAnEmptyLibrary).
	testaudio.Write(t, a, "stays.mp3", 1, nil)
	roots, problems := e.sync(t, a, b)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	s := e.scanner(t, Options{})
	e.scan(t, s, roots[a])
	e.scan(t, s, roots[b])
	oldID := e.tracks(t, roots[a].ID)["song.flac"].ID

	os.Rename(src, filepath.Join(b, "song.flac"))
	e.scan(t, s, roots[a])
	e.scan(t, s, roots[b])
	if r := e.tracks(t, roots[a].ID)["song.flac"]; r.Status != "missing" || r.ID != oldID {
		t.Errorf("old root row = %+v; want missing", r)
	}
	if r := e.tracks(t, roots[b].ID)["song.flac"]; r.Status != "present" || r.ID == oldID {
		t.Errorf("new root row = %+v; want a new present track", r)
	}
}

func TestManyFilesAcrossBatches(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	const n = writeBatch*2 + 37
	data := testaudio.MP3(1)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "d"+string(rune('a'+i%7)), "t"+itoa(i)+".mp3")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	root := e.addRoot(t, dir)
	res := e.scan(t, e.scanner(t, Options{Workers: 4}), root)
	if res.Status != ScanCompleted || res.Counts.TracksAdded != n || len(e.tracks(t, root.ID)) != n {
		t.Errorf("counts = %+v, rows = %d; want %d", res.Counts, len(e.tracks(t, root.ID)), n)
	}
}

// --- disconnected storage and failure modes ---

func TestUnavailableRootKeepsTracks(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	dir := filepath.Join(base, "usb")
	testaudio.Write(t, dir, "song.mp3", 2, tags("Song", "A", "B", "1"))
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)

	// Unplug: the directory disappears entirely.
	if err := os.Rename(dir, filepath.Join(base, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	res := e.scan(t, s, root)
	if res.Status != ScanFailed || !strings.Contains(res.Error, "unavailable") {
		t.Fatalf("status = %s (%s)", res.Status, res.Error)
	}
	if r := e.tracks(t, root.ID)["song.mp3"]; r.Status != "present" {
		t.Errorf("track after unplug = %+v; want untouched", r)
	}
	roots, _ := Roots(bg, e.db)
	if roots[0].Status != RootUnavailable || roots[0].StatusMessage == "" {
		t.Errorf("root = %+v; want unavailable with a reason", roots[0])
	}

	// Startup with the drive still unplugged keeps the root registered.
	m, problems := e.sync(t, dir)
	if len(problems) != 0 || m[dir].ID != root.ID || m[dir].Status != RootUnavailable {
		t.Errorf("sync while unplugged = %+v, %v", m, problems)
	}

	// Plug back in: everything is unchanged.
	os.Rename(filepath.Join(base, "elsewhere"), dir)
	e.sync(t, dir)
	res = e.scan(t, s, root)
	if res.Status != ScanCompleted || res.Counts.TracksUnchanged != 1 {
		t.Errorf("after replug: %s %+v", res.Status, res.Counts)
	}
	roots, _ = Roots(bg, e.db)
	if roots[0].Status != RootAvailable {
		t.Errorf("root status after replug = %s", roots[0].Status)
	}
}

func TestEmptyMountPointIsNotAnEmptyLibrary(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	dir := filepath.Join(base, "mnt")
	testaudio.Write(t, dir, "a.mp3", 2, tags("A", "A", "B", "1"))
	testaudio.Write(t, dir, "b.mp3", 3, tags("B", "A", "B", "2"))
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)

	// Unmounted: the mount point directory exists but is empty.
	os.Rename(dir, filepath.Join(base, "real-drive"))
	os.MkdirAll(dir, 0o755)
	res := e.scan(t, s, root)
	if res.Status != ScanFailed || !strings.Contains(res.Error, "may not be mounted") || res.Counts.TracksMissing != 0 {
		t.Fatalf("result = %+v", res)
	}
	for rel, r := range e.tracks(t, root.ID) {
		if r.Status != "present" {
			t.Errorf("%s marked %s on an empty mount point", rel, r.Status)
		}
	}
}

func TestRootDisappearingDuringScan(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	dir := filepath.Join(base, "usb")
	for i := 0; i < 6; i++ {
		testaudio.Write(t, dir, "f"+itoa(i)+".mp3", 1, nil)
	}
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	// Add new files so the next scan has work to do, then pull the drive
	// during the first metadata read.
	for i := 6; i < 12; i++ {
		testaudio.Write(t, dir, "f"+itoa(i)+".mp3", 1, nil)
	}
	var once sync.Once
	s := e.scanner(t, Options{Workers: 1, Extract: func(p string) metadata.Metadata {
		once.Do(func() { os.Rename(dir, filepath.Join(base, "pulled")) })
		return metadata.Extract(p)
	}})
	res := e.scan(t, s, root)
	if res.Status != ScanFailed || res.Counts.TracksMissing != 0 {
		t.Fatalf("result = %+v", res)
	}
	present := 0
	for _, r := range e.tracks(t, root.ID) {
		if r.Status == "present" {
			present++
		}
	}
	if present < 6 {
		t.Errorf("present tracks = %d; previously indexed tracks must be preserved", present)
	}
}

func TestInterruptedScanMarksNothingMissing(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		testaudio.Write(t, dir, "f"+itoa(i)+".mp3", 1, nil)
	}
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	// Change every file so the next scan must read them, and delete one.
	future := time.Now().Add(time.Hour)
	for i := 1; i < 8; i++ {
		os.Chtimes(filepath.Join(dir, "f"+itoa(i)+".mp3"), future, future)
	}
	os.Remove(filepath.Join(dir, "f0.mp3"))

	ctx, cancel := context.WithCancel(bg)
	s := e.scanner(t, Options{Workers: 1, Extract: func(p string) metadata.Metadata {
		cancel() // shutdown arrives during the scan
		return metadata.Extract(p)
	}})
	res, err := s.Scan(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != ScanInterrupted || res.Counts.TracksMissing != 0 {
		t.Fatalf("result = %+v", res)
	}
	if r := e.tracks(t, root.ID)["f0.mp3"]; r.Status != "present" {
		t.Errorf("deleted file marked %s by an interrupted scan", r.Status)
	}
	last, ok, _ := LastScan(bg, e.db, root.ID)
	if !ok || last.Status != ScanInterrupted || last.FinishedAt.IsZero() {
		t.Errorf("persisted scan = %+v", last)
	}
}

func TestRecoverInterruptedScans(t *testing.T) {
	e := newEnv(t)
	root := e.addRoot(t, t.TempDir())
	e.db.Exec("INSERT INTO scans (root_id, started_at, status) VALUES (?, 1, 'running')", root.ID)
	n, err := RecoverInterruptedScans(bg, e.db, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("recovered %d, %v", n, err)
	}
	last, _, _ := LastScan(bg, e.db, root.ID)
	if last.Status != ScanInterrupted || !strings.Contains(last.Error, "stopped before") {
		t.Errorf("recovered scan = %+v", last)
	}
}

func TestUnreadableSubdirectoryMakesScanPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not enforced this way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "ok/a.mp3", 1, nil)
	testaudio.Write(t, dir, "locked/b.mp3", 1, nil)
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)

	locked := filepath.Join(dir, "locked")
	os.Chmod(locked, 0o000)
	defer os.Chmod(locked, 0o755)
	res := e.scan(t, s, root)
	if res.Status != ScanPartial || res.Counts.DirsFailed != 1 || !strings.Contains(res.Error, "permission denied") {
		t.Fatalf("result = %+v", res)
	}
	if r := e.tracks(t, root.ID)["locked/b.mp3"]; r.Status != "present" {
		t.Errorf("track in unreadable dir marked %s", r.Status)
	}
}

func TestUnreadableFileIsRecorded(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions not enforced for this user")
	}
	e := newEnv(t)
	dir := t.TempDir()
	p := testaudio.Write(t, dir, "secret.mp3", 1, nil)
	os.Chmod(p, 0o000)
	defer os.Chmod(p, 0o644)
	res := e.scan(t, e.scanner(t, Options{}), e.addRoot(t, dir))
	if res.Status != ScanCompleted || res.Counts.FilesFailed != 1 {
		t.Fatalf("result = %+v", res)
	}
	var msg string
	e.db.QueryRow("SELECT metadata_error FROM tracks").Scan(&msg)
	if !strings.Contains(msg, "permission denied") {
		t.Errorf("metadata_error = %q", msg)
	}
}

func TestFileVanishingDuringScan(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	keep := testaudio.Write(t, dir, "keep.mp3", 1, nil)
	vanish := testaudio.Write(t, dir, "vanish.mp3", 2, nil)
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	future := time.Now().Add(time.Hour)
	os.Chtimes(keep, future, future)
	os.Chtimes(vanish, future, future)

	s := e.scanner(t, Options{Extract: func(p string) metadata.Metadata {
		if filepath.Base(p) == "vanish.mp3" {
			os.Remove(p) // deleted after listing, before reading
		}
		return metadata.Extract(p)
	}})
	res := e.scan(t, s, root)
	if res.Status != ScanCompleted || res.Counts.TracksMissing != 1 || res.Counts.FilesFailed != 0 {
		t.Fatalf("result = %+v", res)
	}
	if r := e.tracks(t, root.ID)["vanish.mp3"]; r.Status != "missing" {
		t.Errorf("vanished file = %+v", r)
	}
}

// --- concurrency ---

func TestConcurrentScanOfSameRootIsRejected(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "a.mp3", 1, nil)
	other := t.TempDir()
	testaudio.Write(t, other, "b.mp3", 1, nil)
	roots, _ := e.sync(t, dir, other)

	release := make(chan struct{})
	started := make(chan struct{}, 4)
	s := e.scanner(t, Options{Extract: func(p string) metadata.Metadata {
		started <- struct{}{}
		<-release
		return metadata.Extract(p)
	}})
	done := make(chan Result, 2)
	go func() { r, _ := s.Scan(bg, roots[dir].ID); done <- r }()
	<-started

	if _, err := s.Scan(bg, roots[dir].ID); !errors.Is(err, ErrScanRunning) {
		t.Errorf("second Scan of the same root error = %v, want ErrScanRunning", err)
	}
	running := s.Running()
	if len(running) != 1 || running[0].RootID != roots[dir].ID || running[0].Counts.FilesFound != 1 {
		t.Errorf("Running() = %+v", running)
	}
	// A different root can still be scanned.
	go func() { r, _ := s.Scan(bg, roots[other].ID); done <- r }()
	<-started
	close(release)
	for i := 0; i < 2; i++ {
		if r := <-done; r.Status != ScanCompleted {
			t.Errorf("scan %d = %+v", i, r)
		}
	}
	if len(s.Running()) != 0 {
		t.Error("Running() not empty after scans finished")
	}
	var running0 int
	e.db.QueryRow("SELECT count(*) FROM scans WHERE status = 'running'").Scan(&running0)
	if running0 != 0 {
		t.Errorf("%d scans left 'running'", running0)
	}
}

func TestMetadataReadersAreBounded(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	data := testaudio.MP3(1)
	for i := 0; i < 40; i++ {
		os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".mp3"), data, 0o644)
	}
	var cur, max atomic.Int64
	s := e.scanner(t, Options{Workers: 3, Extract: func(p string) metadata.Metadata {
		n := cur.Add(1)
		for {
			m := max.Load()
			if n <= m || max.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		return metadata.Extract(p)
	}})
	res := e.scan(t, s, e.addRoot(t, dir))
	if res.Counts.TracksAdded != 40 {
		t.Fatalf("counts = %+v", res.Counts)
	}
	if max.Load() > 3 || max.Load() < 2 {
		t.Errorf("max concurrent metadata reads = %d, want 2..3 (bounded by Workers=3)", max.Load())
	}
	if s2, _ := NewScanner(Options{DB: e.db, Logger: e.logger, Workers: 1000}); s2.workers != MaxWorkers {
		t.Errorf("workers not capped: %d", s2.workers)
	}
}

// --- source safety ---

type fileState struct {
	sum  [32]byte
	mod  time.Time
	mode fs.FileMode
}

func snapshot(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		info, _ := d.Info()
		st := fileState{mod: info.ModTime(), mode: info.Mode()}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			st.sum = sha256.Sum256(b)
		}
		out[p] = st
		return nil
	})
	return out
}

func TestScannerNeverModifiesSourceFiles(t *testing.T) {
	e := newEnv(t)
	dir := library(t)
	// Make the library read-only, as recommended for real roots.
	var dirs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			dirs = append(dirs, p)
		} else {
			os.Chmod(p, 0o444)
		}
		return nil
	})
	if runtime.GOOS != "windows" {
		for i := len(dirs) - 1; i >= 0; i-- {
			os.Chmod(dirs[i], 0o555)
		}
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			os.Chmod(d, 0o755)
		}
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				os.Chmod(p, 0o644)
			}
			return nil
		})
	})

	before := snapshot(t, dir)
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	for i := 0; i < 2; i++ {
		if res := e.scan(t, s, root); res.Status != ScanCompleted || res.Counts.FilesFailed != 0 {
			t.Fatalf("scan %d on read-only library = %+v", i, res)
		}
	}
	after := snapshot(t, dir)
	if len(after) != len(before) {
		t.Fatalf("entries in library changed: %d -> %d (files created or removed)", len(before), len(after))
	}
	for p, b := range before {
		a, ok := after[p]
		if !ok || a.sum != b.sum || !a.mod.Equal(b.mod) || a.mode != b.mode {
			t.Errorf("%s changed: before %+v after %+v", p, b, a)
		}
	}
}

// --- roots ---

func TestSyncRootsValidation(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	music := filepath.Join(base, "music")
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	file := filepath.Join(base, "file.txt")
	os.WriteFile(file, nil, 0o644)

	roots, problems := e.sync(t,
		music,
		music+string(filepath.Separator), // same after cleaning
		filepath.Join(music, "sub"),      // nested inside
		base,                             // contains
		"relative/path",
		filepath.Join(base, "missing"),
		file,
	)
	if len(roots) != 1 || roots[music].ID == 0 || roots[music].Status != RootAvailable {
		t.Fatalf("roots = %+v", roots)
	}
	reasons := map[string]string{}
	for _, p := range problems {
		reasons[p.Path] = p.Reason
	}
	expect := map[string]string{
		music + string(filepath.Separator): "duplicate",
		filepath.Join(music, "sub"):        "inside",
		base:                               "contains",
		"relative/path":                    "not absolute",
		filepath.Join(base, "missing"):     "does not exist",
		file:                               "not a directory",
	}
	for p, want := range expect {
		if !strings.Contains(reasons[p], want) {
			t.Errorf("problem for %q = %q, want it to mention %q", p, reasons[p], want)
		}
	}
	// Re-running is idempotent and keeps the ID.
	again, _ := e.sync(t, music)
	if again[music].ID != roots[music].ID {
		t.Errorf("root ID changed on resync")
	}
}

func TestSyncRootsSymlinkResolvedAndDeduplicated(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	real := filepath.Join(base, "real")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	roots, problems := e.sync(t, link, real)
	if len(roots) != 1 || len(problems) != 1 || !strings.Contains(problems[0].Reason, "same as") {
		t.Fatalf("roots = %+v, problems = %+v", roots, problems)
	}
	want, _ := filepath.EvalSymlinks(real)
	if roots[link].Path != want {
		t.Errorf("canonical path = %q, want %q", roots[link].Path, want)
	}
}

func TestSymlinksInsideRootAreNotFollowed(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	dir := filepath.Join(base, "lib")
	outside := filepath.Join(base, "outside")
	testaudio.Write(t, dir, "real.mp3", 1, nil)
	testaudio.Write(t, outside, "escape.mp3", 1, nil)
	if err := os.Symlink(outside, filepath.Join(dir, "dirlink")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	os.Symlink(filepath.Join(dir, "real.mp3"), filepath.Join(dir, "filelink.mp3"))
	os.Symlink(dir, filepath.Join(dir, "loop"))
	res := e.scan(t, e.scanner(t, Options{}), e.addRoot(t, dir))
	got := e.tracks(t, res.RootID)
	if len(got) != 1 || got["real.mp3"].ID == 0 {
		t.Errorf("tracks = %v; symlinks must not be followed", got)
	}
	if res.Counts.FilesSkipped != 3 {
		t.Errorf("skipped = %d, want 3 symlinks", res.Counts.FilesSkipped)
	}
}

func TestRemovedRootIsDisabledNotDeleted(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "a.mp3", 1, nil)
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	e.scan(t, s, root)

	enabled, _ := e.sync(t) // root removed from the configuration
	if len(enabled) != 0 {
		t.Fatalf("enabled roots = %v", enabled)
	}
	all, _ := Roots(bg, e.db)
	if len(all) != 1 || all[0].Enabled {
		t.Fatalf("roots = %+v; want one disabled root", all)
	}
	if len(e.tracks(t, root.ID)) != 1 {
		t.Error("tracks of a disabled root were deleted")
	}
	if _, err := s.Scan(bg, root.ID); !errors.Is(err, ErrRootDisabled) {
		t.Errorf("scan of disabled root error = %v", err)
	}
	if results := s.ScanAll(bg); len(results) != 0 {
		t.Errorf("ScanAll scanned a disabled root: %+v", results)
	}
	// Configuring it again re-enables the same root.
	again, _ := e.sync(t, dir)
	if again[dir].ID != root.ID {
		t.Error("re-adding a root created a new ID")
	}
	if _, err := s.Scan(bg, 9999); !errors.Is(err, ErrRootNotFound) {
		t.Errorf("unknown root error = %v", err)
	}
}

func TestNewRootMustBeAccessible(t *testing.T) {
	e := newEnv(t)
	missing := filepath.Join(t.TempDir(), "not-plugged-in")
	roots, problems := e.sync(t, missing)
	if len(roots) != 0 || len(problems) != 1 || !strings.Contains(problems[0].Reason, "must exist") {
		t.Errorf("roots = %v problems = %v", roots, problems)
	}
}

func TestScanAllAndLastScan(t *testing.T) {
	e := newEnv(t)
	a, b := t.TempDir(), t.TempDir()
	testaudio.Write(t, a, "1.mp3", 1, nil)
	testaudio.Write(t, b, "2.mp3", 1, nil)
	testaudio.Write(t, b, "3.mp3", 1, nil)
	roots, _ := e.sync(t, a, b)
	results := e.scanner(t, Options{}).ScanAll(bg)
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	last, ok, err := LastScan(bg, e.db, roots[b].ID)
	if err != nil || !ok || last.Status != ScanCompleted || last.Counts.TracksAdded != 2 || last.FinishedAt.IsZero() {
		t.Errorf("LastScan = %+v %v %v", last, ok, err)
	}
	var lastID sql.NullInt64
	e.db.QueryRow("SELECT last_scan_id FROM library_roots WHERE id = ?", roots[b].ID).Scan(&lastID)
	if lastID.Int64 != last.ScanID {
		t.Errorf("root last_scan_id = %v, want %d", lastID, last.ScanID)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// --- background scans (Start / Queued / Close) ---

// blockingExtract returns an extractor that signals each call on started and
// waits for release before reading the file.
func blockingExtract(started chan<- string, release <-chan struct{}) func(string) metadata.Metadata {
	return func(p string) metadata.Metadata {
		started <- p
		<-release
		return metadata.Extract(p)
	}
}

func waitForScan(t *testing.T, e *env, rootID int64, want string) Result {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok, _ := LastScan(bg, e.db, rootID); ok && r.Status == want {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _, _ := LastScan(bg, e.db, rootID)
	t.Fatalf("scan of root %d did not reach %q; last = %+v", rootID, want, r)
	return Result{}
}

func TestStartRunsInBackgroundAndRejectsDuplicates(t *testing.T) {
	e := newEnv(t)
	a, b := t.TempDir(), t.TempDir()
	testaudio.Write(t, a, "1.mp3", 1, nil)
	testaudio.Write(t, b, "2.mp3", 1, nil)
	roots, _ := e.sync(t, a, b)
	started := make(chan string, 10)
	release := make(chan struct{})
	s := e.scanner(t, Options{Extract: blockingExtract(started, release)})
	defer s.Close()

	begin := time.Now()
	res, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(begin) > time.Second {
		t.Fatal("Start blocked")
	}
	if len(res.Started) != 2 || len(res.Busy) != 0 {
		t.Fatalf("Start() = %+v", res)
	}
	<-started // the first root is being read; the second is queued

	running, queued := s.Running(), s.Queued()
	if len(running) != 1 || len(queued) != 1 || running[0].RootID == queued[0] {
		t.Fatalf("running = %+v, queued = %v", running, queued)
	}
	// Both roots are busy, whether running or queued, for Start and Scan.
	again, err := s.Start(roots[a].ID, roots[b].ID)
	if err != nil || len(again.Started) != 0 || len(again.Busy) != 2 {
		t.Errorf("second Start() = %+v, %v; want both busy", again, err)
	}
	for _, id := range []int64{roots[a].ID, roots[b].ID} {
		if _, err := s.Scan(bg, id); !errors.Is(err, ErrScanRunning) {
			t.Errorf("Scan(%d) during background scan error = %v, want ErrScanRunning", id, err)
		}
	}
	close(release)
	waitForScan(t, e, roots[a].ID, ScanCompleted)
	waitForScan(t, e, roots[b].ID, ScanCompleted)
	s.Close()
	if len(s.Running()) != 0 || len(s.Queued()) != 0 {
		t.Error("scanner not idle after scans and Close")
	}
	var n int
	e.db.QueryRow("SELECT count(*) FROM tracks").Scan(&n)
	if n != 2 {
		t.Errorf("tracks = %d, want 2", n)
	}
}

func TestStartValidatesBeforeQueueing(t *testing.T) {
	e := newEnv(t)
	s := e.scanner(t, Options{})
	defer s.Close()
	if _, err := s.Start(); !errors.Is(err, ErrNoRoots) {
		t.Errorf("Start() with no roots error = %v, want ErrNoRoots", err)
	}
	dir := t.TempDir()
	testaudio.Write(t, dir, "a.mp3", 1, nil)
	root := e.addRoot(t, dir)
	if _, err := s.Start(root.ID, 9999); !errors.Is(err, ErrRootNotFound) {
		t.Errorf("Start(unknown) error = %v", err)
	}
	if len(s.Queued()) != 0 || len(s.Running()) != 0 {
		t.Error("a failed Start queued roots")
	}
	e.sync(t) // disable the root
	if _, err := s.Start(root.ID); !errors.Is(err, ErrRootDisabled) {
		t.Errorf("Start(disabled) error = %v", err)
	}
	if _, err := s.Start(); !errors.Is(err, ErrNoRoots) {
		t.Errorf("Start() with only disabled roots error = %v", err)
	}
}

func TestCloseInterruptsBackgroundScanAndWaits(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		testaudio.Write(t, dir, "f"+itoa(i)+".mp3", 1, nil)
	}
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	future := time.Now().Add(time.Hour)
	for i := 0; i < 5; i++ {
		os.Chtimes(filepath.Join(dir, "f"+itoa(i)+".mp3"), future, future)
	}
	os.Remove(filepath.Join(dir, "f0.mp3"))

	started := make(chan string, 10)
	release := make(chan struct{})
	s := e.scanner(t, Options{Workers: 1, Extract: blockingExtract(started, release)})
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	<-started
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a scan was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	last, _, _ := LastScan(bg, e.db, root.ID)
	if last.Status != ScanInterrupted || last.Counts.TracksMissing != 0 {
		t.Errorf("scan after Close = %+v; want interrupted, nothing marked missing", last)
	}
	if _, err := s.Start(); !errors.Is(err, ErrScannerClosed) {
		t.Errorf("Start after Close error = %v", err)
	}
}

func TestStartOnUnavailableRootKeepsTracks(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	dir := filepath.Join(base, "usb")
	testaudio.Write(t, dir, "song.mp3", 1, nil)
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	os.Rename(dir, filepath.Join(base, "unplugged"))

	s := e.scanner(t, Options{})
	defer s.Close()
	if res, err := s.Start(root.ID); err != nil || len(res.Started) != 1 {
		t.Fatalf("Start() = %+v, %v", res, err)
	}
	waitForScan(t, e, root.ID, ScanFailed)
	if r := e.tracks(t, root.ID)["song.mp3"]; r.Status != "present" {
		t.Errorf("track after failed background scan = %+v", r)
	}
}

func TestOnFinishedReceivesEveryResult(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "song.flac", 1, nil)
	root := e.addRoot(t, dir)
	var got []Result
	s := e.scanner(t, Options{OnFinished: func(r Result) { got = append(got, r) }})
	res := e.scan(t, s, root)
	os.Rename(dir, dir+"-gone")
	failed := e.scan(t, s, root)
	if len(got) != 2 || got[0].ScanID != res.ScanID || got[0].Counts.TracksAdded != 1 ||
		got[1].ScanID != failed.ScanID || got[1].Status != ScanFailed {
		t.Errorf("OnFinished results = %+v", got)
	}
}
