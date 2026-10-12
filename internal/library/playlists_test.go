// SPDX-License-Identifier: Apache-2.0

package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

func (e *env) user(t *testing.T, name string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at)
		VALUES (?, 'h', 1, 1, 1)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// playlistEnv scans a small library and returns the track IDs by relative
// path, a playlist store, and an owner user ID.
func playlistEnv(t *testing.T) (*env, *Playlists, int64, string, Root, map[string]int64) {
	t.Helper()
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "a.flac", 1, tags("Alpha", "Band", "Rec", "1"))
	testaudio.Write(t, dir, "b.mp3", 2, tags("Beta", "Band", "Rec", "2"))
	testaudio.Write(t, dir, "c.ogg", 3, tags("Gamma", "Band", "Rec", "3"))
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	ids := map[string]int64{}
	for rel, r := range e.tracks(t, root.ID) {
		ids[rel] = r.ID
	}
	return e, NewPlaylists(e.db), e.user(t, "owner"), dir, root, ids
}

func entryTitles(d PlaylistDetail) string {
	var out []string
	for i, en := range d.Entries {
		if en.Position != i+1 {
			return fmt.Sprintf("bad position %d at index %d", en.Position, i)
		}
		out = append(out, en.Track.Title)
	}
	return strings.Join(out, "|")
}

func mustGet(t *testing.T, s *Playlists, user, id int64) PlaylistDetail {
	t.Helper()
	d, err := s.Get(bg, user, id)
	if err != nil {
		t.Fatalf("Get(%d) error = %v", id, err)
	}
	return d
}

func TestPlaylistLifecycle(t *testing.T) {
	_, s, u, _, _, ids := playlistEnv(t)

	pl, err := s.Create(bg, u, "  Road trip  ", "  for the car\r\nloud  ")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Name != "Road trip" || pl.Description == nil || *pl.Description != "for the car\nloud" || pl.TrackCount != 0 {
		t.Fatalf("created = %+v", pl)
	}
	if d := mustGet(t, s, u, pl.ID); len(d.Entries) != 0 || d.Entries == nil {
		t.Errorf("new playlist entries = %#v, want empty non-nil", d.Entries)
	}

	for _, rel := range []string{"b.mp3", "a.flac", "c.ogg"} {
		if _, err := s.AddTrack(bg, u, pl.ID, ids[rel]); err != nil {
			t.Fatalf("AddTrack(%s) error = %v", rel, err)
		}
	}
	d := mustGet(t, s, u, pl.ID)
	if got := entryTitles(d); got != "Beta|Alpha|Gamma" {
		t.Fatalf("order = %s", got)
	}
	if d.TrackCount != 3 || d.DurationMS < 5900 || d.UnavailableCount != 0 {
		t.Errorf("summary = %+v", d.Playlist)
	}

	// Duplicates are rejected and change nothing.
	if _, err := s.AddTrack(bg, u, pl.ID, ids["a.flac"]); !errors.Is(err, ErrAlreadyInPlaylist) {
		t.Errorf("duplicate add error = %v", err)
	}
	if _, err := s.AddTrack(bg, u, pl.ID, 9999); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("unknown track error = %v", err)
	}

	// Removing keeps the others' order; positions stay contiguous.
	if err := s.RemoveTrack(bg, u, pl.ID, ids["a.flac"]); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTrack(bg, u, pl.ID, ids["a.flac"]); !errors.Is(err, ErrNotInPlaylist) {
		t.Errorf("second remove error = %v", err)
	}
	if got := entryTitles(mustGet(t, s, u, pl.ID)); got != "Beta|Gamma" {
		t.Errorf("after remove = %s", got)
	}
	// A re-added track goes to the end.
	if e, err := s.AddTrack(bg, u, pl.ID, ids["a.flac"]); err != nil || e.Position != 3 || e.Track.Title != "Alpha" {
		t.Errorf("re-add = %+v, %v", e, err)
	}

	if err := s.Reorder(bg, u, pl.ID, []int64{ids["a.flac"], ids["c.ogg"], ids["b.mp3"]}); err != nil {
		t.Fatal(err)
	}
	if got := entryTitles(mustGet(t, s, u, pl.ID)); got != "Alpha|Gamma|Beta" {
		t.Errorf("after reorder = %s", got)
	}

	name, desc := "Night drive", ""
	up, err := s.Update(bg, u, pl.ID, PlaylistUpdate{Name: &name, Description: &desc})
	if err != nil || up.Name != "Night drive" || up.Description != nil || up.TrackCount != 3 {
		t.Errorf("Update = %+v, %v", up, err)
	}

	list, err := s.List(bg, u)
	if err != nil || len(list) != 1 || list[0].TrackCount != 3 {
		t.Fatalf("List = %+v, %v", list, err)
	}

	if err := s.Delete(bg, u, pl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(bg, u, pl.ID); !errors.Is(err, ErrPlaylistNotFound) {
		t.Errorf("Get after delete error = %v", err)
	}
	if err := s.Delete(bg, u, pl.ID); !errors.Is(err, ErrPlaylistNotFound) {
		t.Errorf("second delete error = %v", err)
	}
	// The library is untouched.
	var n int
	s.db.QueryRow("SELECT count(*) FROM tracks").Scan(&n)
	if n != 3 {
		t.Errorf("tracks after playlist delete = %d, want 3", n)
	}
}

func TestPlaylistNameValidation(t *testing.T) {
	_, s, u, _, _, _ := playlistEnv(t)
	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxPlaylistNameRunes+1), "tab\there", "nul\x00"} {
		var in *PlaylistInputError
		if _, err := s.Create(bg, u, bad, ""); !errors.As(err, &in) {
			t.Errorf("Create(%q) error = %v, want input error", bad, err)
		}
	}
	if _, err := s.Create(bg, u, "ok", strings.Repeat("d", MaxPlaylistDescription+1)); err == nil {
		t.Error("over-long description accepted")
	}
	if _, err := s.Create(bg, u, strings.Repeat("é", MaxPlaylistNameRunes), ""); err != nil {
		t.Errorf("name of exactly %d characters rejected: %v", MaxPlaylistNameRunes, err)
	}

	first, err := s.Create(bg, u, "Favourites", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(bg, u, "FAVOURITES", ""); !errors.Is(err, ErrPlaylistNameTaken) {
		t.Errorf("duplicate name error = %v", err)
	}
	other, _ := s.Create(bg, u, "Other", "")
	taken := "favourites"
	if _, err := s.Update(bg, u, other.ID, PlaylistUpdate{Name: &taken}); !errors.Is(err, ErrPlaylistNameTaken) {
		t.Errorf("rename to a taken name error = %v", err)
	}
	// Renaming a playlist to its own name in another case is allowed.
	if _, err := s.Update(bg, u, first.ID, PlaylistUpdate{Name: &taken}); err != nil {
		t.Errorf("case-only rename error = %v", err)
	}
}

func TestPlaylistSQLInjectionIsInert(t *testing.T) {
	_, s, u, _, _, _ := playlistEnv(t)
	name := `x'); DROP TABLE playlists; --`
	pl, err := s.Create(bg, u, name, `" OR 1=1 --`)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(bg, u)
	if err != nil || len(list) != 1 || list[0].Name != name || list[0].ID != pl.ID {
		t.Errorf("List = %+v, %v", list, err)
	}
}

func TestPlaylistsAreScopedToTheirOwner(t *testing.T) {
	e, s, owner, _, _, ids := playlistEnv(t)
	other := e.user(t, "other")
	pl, _ := s.Create(bg, owner, "Mine", "")
	s.AddTrack(bg, owner, pl.ID, ids["a.flac"])

	name := "stolen"
	checks := map[string]error{}
	_, checks["Get"] = s.Get(bg, other, pl.ID)
	_, checks["Update"] = s.Update(bg, other, pl.ID, PlaylistUpdate{Name: &name})
	_, checks["AddTrack"] = s.AddTrack(bg, other, pl.ID, ids["b.mp3"])
	checks["RemoveTrack"] = s.RemoveTrack(bg, other, pl.ID, ids["a.flac"])
	checks["Reorder"] = s.Reorder(bg, other, pl.ID, []int64{ids["a.flac"]})
	checks["Delete"] = s.Delete(bg, other, pl.ID)
	for op, err := range checks {
		if !errors.Is(err, ErrPlaylistNotFound) {
			t.Errorf("%s by another user: error = %v, want ErrPlaylistNotFound", op, err)
		}
	}
	if list, _ := s.List(bg, other); len(list) != 0 {
		t.Errorf("other user's list = %+v", list)
	}
	// Each user has their own name space.
	if _, err := s.Create(bg, other, "Mine", ""); err != nil {
		t.Errorf("same name for another user: %v", err)
	}
	d := mustGet(t, s, owner, pl.ID)
	if d.Name != "Mine" || entryTitles(d) != "Alpha" {
		t.Errorf("owner's playlist changed: %+v", d)
	}
}

func TestPlaylistReorderValidation(t *testing.T) {
	_, s, u, _, _, ids := playlistEnv(t)
	pl, _ := s.Create(bg, u, "P", "")
	a, b, c := ids["a.flac"], ids["b.mp3"], ids["c.ogg"]
	s.AddTrack(bg, u, pl.ID, a)
	s.AddTrack(bg, u, pl.ID, b)
	for _, bad := range [][]int64{{}, {a}, {a, b, b}, {a, a}, {a, c}, {a, b, c}, {a, 9999}} {
		if err := s.Reorder(bg, u, pl.ID, bad); !errors.Is(err, ErrOrderMismatch) {
			t.Errorf("Reorder(%v) error = %v", bad, err)
		}
	}
	if got := entryTitles(mustGet(t, s, u, pl.ID)); got != "Alpha|Beta" {
		t.Errorf("order after rejected reorders = %s", got)
	}
	// An empty playlist accepts an empty order.
	empty, _ := s.Create(bg, u, "Empty", "")
	if err := s.Reorder(bg, u, empty.ID, []int64{}); err != nil {
		t.Errorf("empty reorder error = %v", err)
	}
}

// TestPlaylistReorderRollsBack makes an insert fail halfway through a reorder
// (after the old entries were deleted) and checks that nothing changed.
func TestPlaylistReorderRollsBack(t *testing.T) {
	e, s, u, _, _, ids := playlistEnv(t)
	pl, _ := s.Create(bg, u, "P", "")
	for _, rel := range []string{"a.flac", "b.mp3", "c.ogg"} {
		s.AddTrack(bg, u, pl.ID, ids[rel])
	}
	before := mustGet(t, s, u, pl.ID)
	if _, err := e.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_reorder BEFORE INSERT ON playlist_tracks
		WHEN NEW.track_id = %d BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, ids["b.mp3"])); err != nil {
		t.Fatal(err)
	}
	err := s.Reorder(bg, u, pl.ID, []int64{ids["c.ogg"], ids["b.mp3"], ids["a.flac"]})
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("Reorder error = %v, want the injected failure", err)
	}
	after := mustGet(t, s, u, pl.ID)
	if entryTitles(after) != "Alpha|Beta|Gamma" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("after failed reorder: %s, updated %v (was %v)", entryTitles(after), after.UpdatedAt, before.UpdatedAt)
	}
}

// Disconnecting a drive, or files going missing, keeps playlist entries; they
// are reported unavailable and become playable again when the files return.
func TestPlaylistSurvivesDisconnectAndMissingFiles(t *testing.T) {
	e, s, u, dir, root, ids := playlistEnv(t)
	pl, _ := s.Create(bg, u, "USB", "")
	for _, rel := range []string{"a.flac", "b.mp3", "c.ogg"} {
		s.AddTrack(bg, u, pl.ID, ids[rel])
	}
	sc := e.scanner(t, Options{})

	// Unplug: the root becomes unavailable; nothing is marked missing.
	unplugged := dir + "-unplugged"
	if err := os.Rename(dir, unplugged); err != nil {
		t.Fatal(err)
	}
	e.scan(t, sc, root)
	d := mustGet(t, s, u, pl.ID)
	if entryTitles(d) != "Alpha|Beta|Gamma" || d.UnavailableCount != 3 {
		t.Fatalf("while unplugged: %s, unavailable %d", entryTitles(d), d.UnavailableCount)
	}
	for _, en := range d.Entries {
		if en.Track.Available || en.Track.Status != "present" {
			t.Errorf("entry while unplugged = %+v", en.Track)
		}
	}

	// Plug back in, but one file was deleted meanwhile.
	if err := os.Rename(unplugged, dir); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "b.mp3"))
	e.scan(t, sc, root)
	d = mustGet(t, s, u, pl.ID)
	if entryTitles(d) != "Alpha|Beta|Gamma" || d.UnavailableCount != 1 {
		t.Fatalf("after missing file: %s, unavailable %d", entryTitles(d), d.UnavailableCount)
	}
	if b := d.Entries[1].Track; b.Status != "missing" || b.Available || b.ID != ids["b.mp3"] {
		t.Errorf("missing entry = %+v", b)
	}

	// The file comes back: same track ID, same place in the playlist.
	testaudio.Write(t, dir, "b.mp3", 2, tags("Beta", "Band", "Rec", "2"))
	e.scan(t, sc, root)
	d = mustGet(t, s, u, pl.ID)
	if entryTitles(d) != "Alpha|Beta|Gamma" || d.UnavailableCount != 0 || d.Entries[1].Track.ID != ids["b.mp3"] {
		t.Errorf("after restore: %s, unavailable %d, %+v", entryTitles(d), d.UnavailableCount, d.Entries[1].Track)
	}

	// Removing the root from the configuration disables it; entries stay.
	e.sync(t)
	d = mustGet(t, s, u, pl.ID)
	if entryTitles(d) != "Alpha|Beta|Gamma" || d.UnavailableCount != 3 {
		t.Errorf("after disabling the root: %s, unavailable %d", entryTitles(d), d.UnavailableCount)
	}
}

// mergeRename deletes the row a scan added for a renamed file. If that row
// was put in a playlist during the scan, the entry must move to the kept ID.
func TestMergeRenameMovesPlaylistEntries(t *testing.T) {
	e, s, u, _, _, ids := playlistEnv(t)
	oldID := ids["a.flac"]
	// A second row for the same file under a new path, as a scan inserts it.
	res, err := e.db.Exec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title, metadata_status,
		first_seen_at, updated_at) SELECT root_id, 'moved/a.flac', file_size, file_mtime_ns, format, title, metadata_status, 1, 1
		FROM tracks WHERE id = ?`, oldID)
	if err != nil {
		t.Fatal(err)
	}
	newID, _ := res.LastInsertId()

	onlyNew, _ := s.Create(bg, u, "Only new", "")
	s.AddTrack(bg, u, onlyNew.ID, ids["b.mp3"])
	s.AddTrack(bg, u, onlyNew.ID, newID)
	s.AddTrack(bg, u, onlyNew.ID, ids["c.ogg"])
	both, _ := s.Create(bg, u, "Both", "")
	s.AddTrack(bg, u, both.ID, oldID)
	s.AddTrack(bg, u, both.ID, ids["b.mp3"])
	s.AddTrack(bg, u, both.ID, newID)

	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeRename(bg, tx, oldID, newID, 2); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	d := mustGet(t, s, u, onlyNew.ID)
	if entryTitles(d) != "Beta|Alpha|Gamma" || d.Entries[1].Track.ID != oldID {
		t.Errorf("playlist with the new row: %s, middle ID %d (want %d)", entryTitles(d), d.Entries[1].Track.ID, oldID)
	}
	d = mustGet(t, s, u, both.ID)
	if entryTitles(d) != "Alpha|Beta" || d.Entries[0].Track.ID != oldID {
		t.Errorf("playlist with both rows: %s", entryTitles(d))
	}
	var n int
	e.db.QueryRow("SELECT count(*) FROM playlist_tracks WHERE track_id = ?", newID).Scan(&n)
	if n != 0 {
		t.Errorf("%d entries still reference the deleted row", n)
	}
}

func TestPlaylistLimits(t *testing.T) {
	e, s, u, _, root, ids := playlistEnv(t)
	pl, _ := s.Create(bg, u, "Full", "")
	// Fill to the limit directly; the store enforces the limit on add.
	stmt, err := e.db.Prepare(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title,
		metadata_status, first_seen_at, updated_at) VALUES (?, ?, 1, 1, 'mp3', 't', 'ok', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := e.db.Begin()
	for i := 0; i < MaxPlaylistTracks; i++ {
		res, err := tx.Stmt(stmt).Exec(root.ID, fmt.Sprintf("fill/%d.mp3", i))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec("INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, 1)", pl.ID, id, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	if _, err := s.AddTrack(bg, u, pl.ID, ids["a.flac"]); !errors.Is(err, ErrPlaylistFull) {
		t.Errorf("add to a full playlist: error = %v", err)
	}
	d := mustGet(t, s, u, pl.ID)
	if len(d.Entries) != MaxPlaylistTracks {
		t.Errorf("entries = %d", len(d.Entries))
	}

	if _, err := e.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO playlists (user_id, name, created_at, updated_at) SELECT ?, 'p' || i, 1, 1 FROM n`,
		MaxPlaylistsPerUser-1, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(bg, u, "one too many", ""); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("create beyond the limit: error = %v", err)
	}
}
