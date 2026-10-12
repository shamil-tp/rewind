// SPDX-License-Identifier: Apache-2.0

package library

import (
	"encoding/json"
	"strings"
	"testing"
)

// catalogFixture inserts roots and tracks directly, so every edge case
// (missing tags, missing files, unavailable and disabled roots) is explicit.
func catalogFixture(t *testing.T) (*env, *Catalog, map[string]int64) {
	t.Helper()
	e := newEnv(t)
	mustExec := func(q string, args ...any) int64 {
		res, err := e.db.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	ids := map[string]int64{}
	ids["usb"] = mustExec(`INSERT INTO library_roots (configured_path, path, enabled, status, created_at)
		VALUES ('/media/alex/MUSICSTICK', '/media/alex/MUSICSTICK', 1, 'available', 1)`)
	ids["nas"] = mustExec(`INSERT INTO library_roots (configured_path, path, enabled, status, status_message, created_at)
		VALUES ('/srv/nas-music', '/srv/nas-music', 1, 'unavailable', 'cannot access directory: does not exist', 1)`)
	ids["old"] = mustExec(`INSERT INTO library_roots (configured_path, path, enabled, status, created_at)
		VALUES ('/mnt/old', '/mnt/old', 0, 'available', 1)`)

	type tr struct {
		key, root, rel, title, artist, album, albumArtist, genre, format, status, meta, metaErr string
		year, track, disc, dur                                                                  any
		added                                                                                   int
	}
	tracks := []tr{
		{key: "alpha", root: "usb", rel: "A/Alpha.flac", title: "Alpha", artist: "Zed", album: "First", genre: "Rock", format: "flac", status: "present", meta: "ok", year: 1999, track: 1, disc: 1, dur: 200000, added: 10},
		{key: "beta", root: "usb", rel: "A/beta.mp3", title: "beta", artist: "Zed", album: "First", genre: "Rock", format: "mp3", status: "present", meta: "ok", year: 1999, track: 2, disc: 1, dur: 100000, added: 20},
		{key: "gamma", root: "usb", rel: "B/Gamma.m4a", title: "Gamma 50% off_sale", artist: "Ann", album: "Second", albumArtist: "Various", format: "m4a", status: "present", meta: "ok", year: 2005, track: 1, disc: 1, dur: 300000, added: 30},
		{key: "untagged", root: "usb", rel: "loose/untagged.wav", title: "untagged", format: "wav", status: "present", meta: "no_tags", added: 40},
		{key: "broken", root: "usb", rel: "loose/broken.ogg", title: "broken", format: "ogg", status: "present", meta: "error", metaErr: "open /media/alex/MUSICSTICK/loose/broken.ogg: permission denied", added: 50},
		{key: "gone", root: "usb", rel: "A/gone.mp3", title: "Gone", artist: "Zed", album: "First", format: "mp3", status: "missing", meta: "ok", added: 5},
		{key: "nas1", root: "nas", rel: "x.flac", title: "Offline Song", artist: "Bob", album: "Away", format: "flac", status: "present", meta: "ok", dur: 1000, added: 60},
		{key: "disabled", root: "old", rel: "y.mp3", title: "Old Song", artist: "Zed", format: "mp3", status: "present", meta: "ok", added: 70},
	}
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	for _, x := range tracks {
		var missingSince any
		if x.status == "missing" {
			missingSince = 1234
		}
		ids[x.key] = mustExec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title, artist, album,
			album_artist, genre, year, track_number, disc_number, duration_ms, metadata_status, metadata_error,
			status, missing_since, first_seen_at, updated_at)
			VALUES (?, ?, 100, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids[x.root], x.rel, x.format, x.title, null(x.artist), null(x.album), null(x.albumArtist), null(x.genre),
			x.year, x.track, x.disc, x.dur, x.meta, null(x.metaErr), x.status, missingSince, x.added, x.added)
	}
	mustExec(`INSERT INTO scans (root_id, started_at, finished_at, status, files_found, tracks_added)
		VALUES (?, 100, 110, 'completed', 6, 6)`, ids["usb"])
	mustExec(`INSERT INTO scans (root_id, started_at, finished_at, status, error)
		VALUES (?, 200, 201, 'failed', 'library root unavailable: cannot access directory: does not exist')`, ids["nas"])
	return e, NewCatalog(e.db), ids
}

func titles(p TrackPage) []string {
	out := make([]string, len(p.Tracks))
	for i, t := range p.Tracks {
		out[i] = t.Title
	}
	return out
}

func list(t *testing.T, c *Catalog, q TrackQuery) TrackPage {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 50
	}
	p, err := c.Tracks(bg, q)
	if err != nil {
		t.Fatalf("Tracks(%+v) error = %v", q, err)
	}
	return p
}

func TestCatalogDefaultListing(t *testing.T) {
	_, c, _ := catalogFixture(t)
	p := list(t, c, TrackQuery{})
	// Present tracks from enabled roots only, sorted by title (case-insensitive).
	want := []string{"Alpha", "beta", "broken", "Gamma 50% off_sale", "Offline Song", "untagged"}
	if strings.Join(titles(p), "|") != strings.Join(want, "|") || p.Total != int64(len(want)) {
		t.Fatalf("titles = %v (total %d), want %v", titles(p), p.Total, want)
	}
	for _, tr := range p.Tracks {
		switch tr.Title {
		case "Offline Song":
			if tr.Available || tr.Status != "present" {
				t.Errorf("track on unavailable root: available=%v status=%s", tr.Available, tr.Status)
			}
		default:
			if !tr.Available {
				t.Errorf("%s: available = false", tr.Title)
			}
		}
	}
}

func TestCatalogPagination(t *testing.T) {
	_, c, _ := catalogFixture(t)
	var all []string
	for off := 0; off < 10; off += 2 {
		p := list(t, c, TrackQuery{Limit: 2, Offset: off})
		if p.Total != 6 {
			t.Fatalf("total = %d", p.Total)
		}
		all = append(all, titles(p)...)
	}
	if len(all) != 6 {
		t.Fatalf("pages returned %d tracks: %v", len(all), all)
	}
	if p := list(t, c, TrackQuery{Limit: 2, Offset: 100}); len(p.Tracks) != 0 || p.Total != 6 {
		t.Errorf("page past the end = %v, total %d", titles(p), p.Total)
	}
	if _, err := c.Tracks(bg, TrackQuery{Limit: 0}); err == nil {
		t.Error("Limit 0 accepted")
	}
}

func TestCatalogSearchAndFilters(t *testing.T) {
	e, c, ids := catalogFixture(t)
	cases := []struct {
		q    TrackQuery
		want string
	}{
		{TrackQuery{Search: "alp"}, "Alpha"},
		{TrackQuery{Search: "ZED"}, "Alpha|beta"},             // artist, case-insensitive
		{TrackQuery{Search: "second"}, "Gamma 50% off_sale"},  // album
		{TrackQuery{Search: "various"}, "Gamma 50% off_sale"}, // album artist
		{TrackQuery{Search: "50%"}, "Gamma 50% off_sale"},     // % is literal
		{TrackQuery{Search: "%"}, "Gamma 50% off_sale"},       // not a wildcard
		{TrackQuery{Search: "_"}, "Gamma 50% off_sale"},       // not a wildcard
		{TrackQuery{Search: "nothing-matches"}, ""},
		{TrackQuery{Artist: "zed"}, "Alpha|beta"},
		{TrackQuery{Album: "First"}, "Alpha|beta"},
		{TrackQuery{AlbumArtist: "Various"}, "Gamma 50% off_sale"},
		{TrackQuery{Genre: "rock"}, "Alpha|beta"},
		{TrackQuery{Format: "wav"}, "untagged"},
		{TrackQuery{Year: 2005}, "Gamma 50% off_sale"},
		{TrackQuery{RootID: ids["nas"]}, "Offline Song"},
		{TrackQuery{RootID: ids["old"]}, ""}, // disabled roots are not listed
		{TrackQuery{Status: "missing"}, "Gone"},
		{TrackQuery{Status: "all", Artist: "Zed"}, "Alpha|beta|Gone"},
	}
	for _, tc := range cases {
		if got := strings.Join(titles(list(t, c, tc.q)), "|"); got != tc.want {
			t.Errorf("%+v -> %q, want %q", tc.q, got, tc.want)
		}
	}
	// Injection attempts are just text that matches nothing.
	for _, evil := range []string{"'; DROP TABLE tracks; --", `" OR 1=1 --`, "') OR ('1'='1"} {
		if p := list(t, c, TrackQuery{Search: evil, Artist: evil, Genre: evil}); p.Total != 0 {
			t.Errorf("injection %q matched %d tracks", evil, p.Total)
		}
	}
	var n int
	e.db.QueryRow("SELECT count(*) FROM tracks").Scan(&n)
	if n != 8 {
		t.Errorf("tracks table has %d rows after injection attempts, want 8", n)
	}
}

func TestCatalogSorting(t *testing.T) {
	_, c, _ := catalogFixture(t)
	cases := []struct {
		sort string
		desc bool
		want string
	}{
		{"title", true, "untagged|Offline Song|Gamma 50% off_sale|broken|beta|Alpha"},
		// Tracks without an artist or album always sort last, in either direction.
		{"artist", false, "Gamma 50% off_sale|Offline Song|Alpha|beta|broken|untagged"},
		{"artist", true, "beta|Alpha|Offline Song|Gamma 50% off_sale|untagged|broken"},
		{"album", false, "Offline Song|Alpha|beta|Gamma 50% off_sale|broken|untagged"},
		// Ties (here: no duration) fall back to the track ID.
		{"duration", false, "Offline Song|beta|Alpha|Gamma 50% off_sale|untagged|broken"},
		// No year: last; then by album (no album sorts before "Away"), then ID.
		{"year", false, "Alpha|beta|Gamma 50% off_sale|untagged|broken|Offline Song"},
		{"added", true, "Offline Song|broken|untagged|Gamma 50% off_sale|beta|Alpha"},
	}
	for _, tc := range cases {
		got := strings.Join(titles(list(t, c, TrackQuery{Sort: tc.sort, Desc: tc.desc})), "|")
		if got != tc.want {
			t.Errorf("sort=%s desc=%v:\n got %s\nwant %s", tc.sort, tc.desc, got, tc.want)
		}
	}
	// Keys outside the allowlist are refused even when they are valid SQL, so
	// user input can never become an ORDER BY expression.
	for _, key := range []string{"t.rel_path", "(SELECT 1)", "random()", "title; DROP TABLE tracks"} {
		if _, err := c.Tracks(bg, TrackQuery{Sort: key, Limit: 10}); err == nil {
			t.Errorf("sort key %q accepted", key)
		}
	}
	for _, k := range SortKeys() {
		if !IsSortKey(k) {
			t.Errorf("SortKeys lists %q but IsSortKey rejects it", k)
		}
	}
}

func TestCatalogTrackDetail(t *testing.T) {
	_, c, ids := catalogFixture(t)
	tr, err := c.Track(bg, ids["gamma"])
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title != "Gamma 50% off_sale" || *tr.Artist != "Ann" || *tr.AlbumArtist != "Various" || *tr.Year != 2005 ||
		*tr.TrackNumber != 1 || *tr.DurationMS != 300000 || tr.Format != "m4a" || tr.RelativePath != "B/Gamma.m4a" {
		t.Errorf("track = %+v", tr)
	}

	// Missing tags are omitted, not invented.
	u, _ := c.Track(bg, ids["untagged"])
	b, _ := json.Marshal(u)
	for _, field := range []string{`"artist"`, `"album"`, `"genre"`, `"year"`, `"duration_ms"`, `"track_number"`} {
		if strings.Contains(string(b), field) {
			t.Errorf("untagged track JSON contains %s: %s", field, b)
		}
	}
	if u.MetadataStatus != "no_tags" {
		t.Errorf("metadata_status = %q", u.MetadataStatus)
	}

	// Stored error messages never reveal the root's absolute path.
	br, _ := c.Track(bg, ids["broken"])
	if br.MetadataError == nil || strings.Contains(*br.MetadataError, "/media/alex") ||
		!strings.Contains(*br.MetadataError, "<library root>/loose/broken.ogg") {
		t.Errorf("metadata_error = %v", br.MetadataError)
	}

	g, _ := c.Track(bg, ids["gone"])
	if g.Status != "missing" || g.Available || g.MissingSince == nil {
		t.Errorf("missing track = %+v", g)
	}
	d, err := c.Track(bg, ids["disabled"])
	if err != nil || d.Available {
		t.Errorf("track of disabled root = %+v, %v", d, err)
	}
	if _, err := c.Track(bg, 99999); err != ErrTrackNotFound {
		t.Errorf("unknown track error = %v", err)
	}
}

func TestCatalogRoots(t *testing.T) {
	_, c, ids := catalogFixture(t)
	roots, err := c.Roots(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 3 {
		t.Fatalf("roots = %+v", roots)
	}
	byID := map[int64]RootStatus{}
	for _, r := range roots {
		byID[r.ID] = r
	}
	usb := byID[ids["usb"]]
	if usb.Name != "MUSICSTICK" || usb.Status != "available" || !usb.Enabled || usb.TrackCount != 5 || usb.MissingCount != 1 ||
		usb.LastScan == nil || usb.LastScan.Status != "completed" || usb.LastScan.Counts.TracksAdded != 6 || usb.LastScan.FinishedAt == nil {
		t.Errorf("usb root = %+v (last scan %+v)", usb, usb.LastScan)
	}
	nas := byID[ids["nas"]]
	// Unavailable, but its indexed track is still counted: not an empty library.
	if nas.Status != "unavailable" || nas.TrackCount != 1 || nas.StatusMessage == nil || nas.LastScan.Status != "failed" || nas.LastScan.Error == nil {
		t.Errorf("nas root = %+v", nas)
	}
	if old := byID[ids["old"]]; old.Enabled || old.LastScan != nil || old.TrackCount != 1 {
		t.Errorf("disabled root = %+v", old)
	}
	b, _ := json.Marshal(roots)
	for _, p := range []string{"/media/alex", "/srv/nas-music", "/mnt/old"} {
		if strings.Contains(string(b), p) {
			t.Errorf("roots JSON reveals path %s: %s", p, b)
		}
	}
}

func TestCatalogSummary(t *testing.T) {
	_, c, _ := catalogFixture(t)
	s, err := c.Summary(bg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tracks != 6 || s.AvailableTracks != 5 || s.MissingTracks != 1 || s.Artists != 3 || s.Albums != 3 ||
		s.TotalDurationMS != 601000 {
		t.Errorf("summary = %+v", s)
	}
	if s.Roots != (RootCounts{Total: 2, Available: 1, Unavailable: 1, Pending: 0, Disabled: 1}) {
		t.Errorf("root counts = %+v", s.Roots)
	}
	if s.LastSuccessfulScan == nil || s.LastSuccessfulScan.Unix() != 110 {
		t.Errorf("last successful scan = %v", s.LastSuccessfulScan)
	}
}

func TestCatalogEmptyLibrary(t *testing.T) {
	e := newEnv(t)
	c := NewCatalog(e.db)
	if p := list(t, c, TrackQuery{}); p.Total != 0 || p.Tracks == nil || len(p.Tracks) != 0 {
		t.Errorf("empty listing = %+v", p)
	}
	if s, err := c.Summary(bg); err != nil || s.Tracks != 0 || s.LastSuccessfulScan != nil || s.Roots.Total != 0 {
		t.Errorf("empty summary = %+v, %v", s, err)
	}
	if r, err := c.Roots(bg); err != nil || len(r) != 0 {
		t.Errorf("empty roots = %+v, %v", r, err)
	}
}

func TestDisplayNameAndSanitize(t *testing.T) {
	for in, want := range map[string]string{"/media/alex/MUSICSTICK": "MUSICSTICK", "/srv/music/": "music", "/": "library"} {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Sanitize("open /a/b/c.mp3: x", "/a/b", ""); got != "open <library root>/c.mp3: x" {
		t.Errorf("Sanitize = %q", got)
	}
}
