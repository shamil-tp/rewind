// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

// TestPerformanceReport measures queries on a large synthetic library. It is
// skipped unless REWIND_PERF=1, and asserts nothing: it prints timings and
// query plans for docs/security-audit.md.
//
//	REWIND_PERF=1 go test ./internal/library -run TestPerformanceReport -v -count=1
func TestPerformanceReport(t *testing.T) {
	if os.Getenv("REWIND_PERF") != "1" {
		t.Skip("set REWIND_PERF=1 to run")
	}
	const nTracks, nPlaylists, bigPlaylist = 100_000, 1000, MaxPlaylistTracks
	e := newEnv(t)
	u := e.user(t, "owner")
	if _, err := e.db.Exec("INSERT INTO library_roots (id, configured_path, path, status, created_at) VALUES (1, '/m', '/m', 'available', 1)"); err != nil {
		t.Fatal(err)
	}
	tx, _ := e.db.Begin()
	for i := 0; i < nTracks; i++ {
		tx.Exec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title, artist, album, duration_ms,
			metadata_status, first_seen_at, updated_at) VALUES (1, ?, 1, 1, 'flac', ?, ?, ?, 200000, 'ok', ?, 1)`,
			fmt.Sprintf("a%d/b%d/%d.flac", i%500, i%5000, i), fmt.Sprintf("Song %d", i),
			fmt.Sprintf("Artist %d", i%500), fmt.Sprintf("Album %d", i%5000), i)
	}
	for p := 1; p <= nPlaylists; p++ {
		tx.Exec("INSERT INTO playlists (id, user_id, name, created_at, updated_at) VALUES (?, ?, ?, 1, 1)", p, u, fmt.Sprintf("List %04d", p))
		n := 20
		if p == 1 {
			n = bigPlaylist
		}
		for k := 0; k < n; k++ {
			tx.Exec("INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, 1)", p, (p*37+k*13)%nTracks+1, k)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.db.Exec("ANALYZE")

	s, c := NewPlaylists(e.db), NewCatalog(e.db)
	measure := func(name string, runs int, fn func() error) {
		var ds []time.Duration
		for i := 0; i < runs; i++ {
			start := time.Now()
			if err := fn(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			ds = append(ds, time.Since(start))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("%-46s median %8s  max %8s", name, ds[len(ds)/2].Round(10*time.Microsecond), ds[len(ds)-1].Round(10*time.Microsecond))
	}
	ids := make([]int64, 0, bigPlaylist)
	measure("playlists: list 1,000 with counts", 9, func() error { _, err := s.List(bg, u); return err })
	measure("playlists: get the 5,000-track playlist", 9, func() error {
		d, err := s.Get(bg, u, 1)
		ids = ids[:0]
		for _, en := range d.Entries {
			ids = append(ids, en.Track.ID)
		}
		return err
	})
	measure("playlists: get a 20-track playlist", 9, func() error { _, err := s.Get(bg, u, 500); return err })
	measure("playlists: reorder 5,000 (reverse)", 5, func() error {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
		return s.Reorder(bg, u, 1, ids)
	})
	measure("playlists: add + remove one in the 5,000 list", 9, func() error {
		if err := s.RemoveTrack(bg, u, 1, ids[0]); err != nil {
			return err
		}
		_, err := s.AddTrack(bg, u, 1, ids[0])
		return err
	})
	measure("tracks: first page sorted by artist", 9, func() error {
		_, err := c.Tracks(bg, TrackQuery{Sort: "artist", Limit: 50})
		return err
	})
	measure("tracks: search 'Song 4242' with count", 9, func() error {
		_, err := c.Tracks(bg, TrackQuery{Search: "Song 4242", Limit: 50})
		return err
	})

	for name, q := range map[string]string{
		"playlist list":      playlistSummary + " WHERE p.user_id = 1 GROUP BY p.id ORDER BY p.name COLLATE NOCASE, p.id",
		"playlist entries":   "SELECT " + trackFields + ", pt.added_at FROM playlist_tracks pt JOIN tracks t ON t.id = pt.track_id JOIN library_roots r ON r.id = t.root_id WHERE pt.playlist_id = 1 ORDER BY pt.position",
		"add (max position)": "SELECT count(*), coalesce(sum(track_id = 5), 0) > 0, coalesce(max(position) + 1, 0) FROM playlist_tracks WHERE playlist_id = 1",
		"rename merge":       "UPDATE OR IGNORE playlist_tracks SET track_id = 1 WHERE track_id = 2",
	} {
		rows, err := e.db.Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			rows.Scan(&id, &parent, &unused, &detail)
			plan = append(plan, detail)
		}
		rows.Close()
		t.Logf("plan %-18s %s", name+":", strings.Join(plan, " | "))
	}
}

// TestScanContentionReport measures API-style reads while a scan writes. It
// is skipped unless REWIND_PERF=1.
func TestScanContentionReport(t *testing.T) {
	if os.Getenv("REWIND_PERF") != "1" {
		t.Skip("set REWIND_PERF=1 to run")
	}
	e := newEnv(t)
	dir := t.TempDir()
	const n = 1500
	for i := 0; i < n; i++ {
		testaudio.Write(t, dir, fmt.Sprintf("d%02d/f%04d.flac", i%40, i), 1, tags(fmt.Sprintf("T%d", i), "A", "B", "1"))
	}
	root := e.addRoot(t, dir)
	s := e.scanner(t, Options{})
	c := NewCatalog(e.db)

	var mu sync.Mutex
	var lat []time.Duration
	ctx, cancel := context.WithCancel(bg)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			start := time.Now()
			c.Tracks(bg, TrackQuery{Limit: 50, Sort: "title"})
			mu.Lock()
			lat = append(lat, time.Since(start))
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()
	start := time.Now()
	res := e.scan(t, s, root)
	scanTime := time.Since(start)
	cancel()
	wg.Wait()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("scan of %d files: %s, status %s, added %d", n, scanTime.Round(time.Millisecond), res.Status, res.Counts.TracksAdded)
	t.Logf("track-page reads during the scan: %d, median %s, p95 %s, max %s", len(lat),
		lat[len(lat)/2].Round(10*time.Microsecond), lat[len(lat)*95/100].Round(10*time.Microsecond), lat[len(lat)-1].Round(10*time.Microsecond))
}
