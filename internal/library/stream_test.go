// SPDX-License-Identifier: Apache-2.0

package library

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// openFixture registers dir as an available root with one track, song.mp3.
func openFixture(t *testing.T) (*Catalog, string, int64) {
	t.Helper()
	e := newEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("song data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.mp3"), []byte("other data"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.db.Exec(`INSERT INTO library_roots (configured_path, path, enabled, status, created_at)
		VALUES (?, ?, 1, 'available', 1)`, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	rootID, _ := res.LastInsertId()
	res, err = e.db.Exec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title,
		metadata_status, status, first_seen_at, updated_at) VALUES (?, 'song.mp3', 9, 1, 'mp3', 'Song', 'ok', 'present', 1, 1)`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return NewCatalog(e.db), dir, id
}

func TestOpenTrackServesRegularFile(t *testing.T) {
	c, _, id := openFixture(t)
	f, err := c.OpenTrack(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "song data" || f.Size != 9 || f.ContentType != "audio/mpeg" {
		t.Errorf("got %q, size %d, type %q", data, f.Size, f.ContentType)
	}
}

// A symbolic link swapped in after the symlink check must not be opened, even
// though it points to another file inside the same root: os.Root follows
// relative links that stay inside the root (it refuses absolute ones).
func TestOpenTrackRejectsSymlinkSwappedInBeforeOpen(t *testing.T) {
	c, dir, id := openFixture(t)
	song := filepath.Join(dir, "song.mp3")
	probe := filepath.Join(dir, "probe")
	if err := os.Symlink(filepath.Join(dir, "other.mp3"), probe); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	os.Remove(probe)

	beforeOpenHook = func(string, string) {
		if err := os.Remove(song); err != nil {
			t.Error(err)
		}
		if err := os.Symlink("other.mp3", song); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeOpenHook = nil })

	f, err := c.OpenTrack(bg, id)
	if err == nil {
		data, _ := io.ReadAll(f)
		f.Close()
		t.Fatalf("opened the swapped-in link target: %q", data)
	}
	if !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("err = %v, want ErrFileUnavailable", err)
	}
}

// A file replaced by a different regular file in the same window is refused
// too: what was checked is what gets served.
func TestOpenTrackRejectsFileReplacedBeforeOpen(t *testing.T) {
	c, dir, id := openFixture(t)
	beforeOpenHook = func(string, string) {
		if err := os.Rename(filepath.Join(dir, "other.mp3"), filepath.Join(dir, "song.mp3")); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeOpenHook = nil })
	if f, err := c.OpenTrack(bg, id); err == nil {
		f.Close()
		t.Fatal("opened a file that replaced the checked one")
	} else if !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("err = %v, want ErrFileUnavailable", err)
	}
}
