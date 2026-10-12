// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/metadata"
	"github.com/shamil-tp/rewind/internal/testaudio"
)

// addLibrary registers dirs as library roots and returns their IDs by path.
func (e *testEnv) addLibrary(t testing.TB, dirs ...string) map[string]int64 {
	t.Helper()
	roots, problems, err := library.SyncRoots(context.Background(), e.db, dirs, e.logger, time.Now())
	if err != nil || len(problems) > 0 {
		t.Fatalf("SyncRoots: %v %v", err, problems)
	}
	ids := map[string]int64{}
	for _, r := range roots {
		ids[r.ConfiguredPath] = r.ID
	}
	return ids
}

func (e *testEnv) scanNow(t testing.TB, rootID int64) library.Result {
	t.Helper()
	res, err := e.scanner.Scan(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func musicDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tag := func(title, artist, album, track string) map[string][]string {
		return map[string][]string{"TITLE": {title}, "ARTIST": {artist}, "ALBUM": {album}, "TRACKNUMBER": {track}, "DATE": {"2001"}}
	}
	testaudio.Write(t, dir, "Band/Record/01.flac", 3, tag("Opening", "Band", "Record", "1"))
	testaudio.Write(t, dir, "Band/Record/02.mp3", 2, tag("Second Song", "Band", "Record", "2"))
	testaudio.Write(t, dir, "Solo/Tune.ogg", 1, tag("Tune", "Solo Artist", "Singles", "1"))
	testaudio.Write(t, dir, "loose/no tags.wav", 1, nil)
	return dir
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q; body = %s", ct, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("invalid JSON %s: %v", rec.Body, err)
	}
}

type trackList struct {
	Items      []library.Track `json:"items"`
	Pagination pagination      `json:"pagination"`
}

func getTracks(t *testing.T, e *testEnv, c *http.Cookie, query string) trackList {
	t.Helper()
	rec := e.send(t, http.MethodGet, "/api/tracks"+query, nil, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tracks%s = %d %s", query, rec.Code, rec.Body)
	}
	var l trackList
	decodeBody(t, rec, &l)
	return l
}

func listTitles(l trackList) string {
	var out []string
	for _, tr := range l.Items {
		out = append(out, tr.Title)
	}
	return strings.Join(out, "|")
}

// --- authentication ---

func TestLibraryEndpointsRequireAuth(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	bogus := &http.Cookie{Name: "rewind_session", Value: strings.Repeat("B", 43)}
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/library"},
		{http.MethodGet, "/api/library/roots"},
		{http.MethodGet, "/api/tracks"},
		{http.MethodGet, "/api/tracks?q=x"},
		{http.MethodGet, "/api/tracks/1"},
		{http.MethodPost, "/api/library/scan"},
	} {
		for _, c := range []*http.Cookie{nil, bogus} {
			rec := e.send(t, rt.method, rt.path, nil, withCookie(c))
			expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s: Cache-Control = %q", rt.method, rt.path, rec.Header().Get("Cache-Control"))
			}
		}
	}
	if len(e.scanner.Running())+len(e.scanner.Queued()) != 0 {
		t.Error("an unauthenticated request started a scan")
	}
}

// --- tracks ---

func TestTracksAPI(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)

	empty := getTracks(t, e, c, "")
	if len(empty.Items) != 0 || empty.Items == nil || empty.Pagination != (pagination{Page: 1, Limit: 50, Total: 0, TotalPages: 0}) {
		t.Fatalf("empty library = %+v", empty)
	}

	dir := musicDir(t)
	ids := e.addLibrary(t, dir)
	e.scanNow(t, ids[dir])

	all := getTracks(t, e, c, "")
	if listTitles(all) != "no tags|Opening|Second Song|Tune" || all.Pagination.Total != 4 {
		t.Fatalf("tracks = %q, %+v", listTitles(all), all.Pagination)
	}
	var opening library.Track
	for _, tr := range all.Items {
		if tr.Title == "Opening" {
			opening = tr
		}
	}
	if opening.ID == 0 || *opening.Artist != "Band" || *opening.Album != "Record" || *opening.TrackNumber != 1 ||
		*opening.Year != 2001 || opening.Format != "flac" || *opening.DurationMS < 2900 || !opening.Available ||
		opening.RelativePath != "Band/Record/01.flac" || opening.RootID != ids[dir] {
		t.Errorf("track = %+v", opening)
	}

	if p := getTracks(t, e, c, "?limit=3&page=2"); listTitles(p) != "Tune" || p.Pagination != (pagination{Page: 2, Limit: 3, Total: 4, TotalPages: 2}) {
		t.Errorf("page 2 = %q %+v", listTitles(p), p.Pagination)
	}
	for query, want := range map[string]string{
		"?sort=title&order=desc":  "Tune|Second Song|Opening|no tags",
		"?sort=album":             "Opening|Second Song|Tune|no tags",
		"?q=second":               "Second Song",
		"?q=solo":                 "Tune",
		"?artist=band":            "Opening|Second Song",
		"?format=ogg":             "Tune",
		"?year=2001&sort=album":   "Opening|Second Song|Tune",
		"?root=" + itoa(ids[dir]): "no tags|Opening|Second Song|Tune",
		"?root=9999":              "",
		"?status=missing":         "",
		"?q=" + "%25":             "", // literal "%" matches nothing here
	} {
		if got := listTitles(getTracks(t, e, c, query)); got != want {
			t.Errorf("GET /api/tracks%s -> %q, want %q", query, got, want)
		}
	}

	// Missing tags are omitted from JSON rather than invented.
	rec := e.send(t, http.MethodGet, "/api/tracks?q=no+tags", nil, withCookie(c))
	if strings.Contains(rec.Body.String(), `"artist"`) || !strings.Contains(rec.Body.String(), `"metadata_status":"no_tags"`) {
		t.Errorf("untagged track JSON = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), filepath.ToSlash(dir)) || strings.Contains(rec.Body.String(), dir) {
		t.Error("track JSON reveals the absolute library path")
	}
}

func TestTracksParameterValidation(t *testing.T) {
	e := newTestEnvWith(t, envConfig{maxPageSize: 20})
	c := e.doSetup(t)
	if p := getTracks(t, e, c, ""); p.Pagination.Limit != 20 {
		t.Errorf("default limit with max 20 = %d", p.Pagination.Limit)
	}
	if p := getTracks(t, e, c, "?limit=20"); p.Pagination.Limit != 20 {
		t.Errorf("limit=20 -> %d", p.Pagination.Limit)
	}
	for _, q := range []string{
		"?page=0", "?page=-1", "?page=abc", "?page=1000001",
		"?limit=0", "?limit=21", "?limit=1e3", "?limit=",
		"?sort=bogus", "?sort=title;DROP%20TABLE%20tracks", "?order=up",
		"?status=deleted", "?format=exe", "?year=99", "?year=abc", "?root=0", "?root=-5",
		"?q=" + strings.Repeat("x", maxSearchRunes+1), "?artist=" + strings.Repeat("a", maxFilterRunes+1),
		"?unknown=1", "?page=1&page=2", "?q=%ff",
	} {
		expectError(t, e.send(t, http.MethodGet, "/api/tracks"+q, nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	}
}

func TestTracksSQLInjectionIsInert(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	dir := musicDir(t)
	e.scanNow(t, e.addLibrary(t, dir)[dir])
	for _, evil := range []string{"'; DROP TABLE tracks; --", "\" OR 1=1 --", "x' OR '1'='1"} {
		q := "?q=" + urlEscape(evil) + "&artist=" + urlEscape(evil)
		if l := getTracks(t, e, c, q); l.Pagination.Total != 0 {
			t.Errorf("injection %q matched %d tracks", evil, l.Pagination.Total)
		}
	}
	if l := getTracks(t, e, c, ""); l.Pagination.Total != 4 {
		t.Errorf("tracks after injection attempts = %d, want 4", l.Pagination.Total)
	}
}

func TestTrackDetailAPI(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	dir := musicDir(t)
	e.scanNow(t, e.addLibrary(t, dir)[dir])
	id := getTracks(t, e, c, "?q=opening").Items[0].ID

	rec := e.send(t, http.MethodGet, "/api/tracks/"+itoa(id), nil, withCookie(c))
	var body struct {
		Track library.Track `json:"track"`
	}
	decodeBody(t, rec, &body)
	if rec.Code != http.StatusOK || body.Track.ID != id || body.Track.Title != "Opening" {
		t.Fatalf("detail = %d %+v", rec.Code, body)
	}
	expectError(t, e.send(t, http.MethodGet, "/api/tracks/99999", nil, withCookie(c)), http.StatusNotFound, "not_found")
	for _, bad := range []string{"abc", "0", "-1", "1.5", "99999999999999999999"} {
		expectError(t, e.send(t, http.MethodGet, "/api/tracks/"+bad, nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	}
	expectError(t, e.send(t, http.MethodGet, "/api/tracks/1/extra", nil, withCookie(c)), http.StatusNotFound, "not_found")
	expectError(t, e.send(t, http.MethodGet, "/api/tracks/1?x=1", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	expectError(t, e.send(t, http.MethodDelete, "/api/tracks/1", nil, withCookie(c)), http.StatusMethodNotAllowed, "method_not_allowed")
}

// --- roots and summary ---

func TestRootsAndSummaryAPI(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	base := t.TempDir()
	stick := filepath.Join(base, "MUSICSTICK")
	nas := filepath.Join(base, "nas")
	testaudio.Write(t, stick, "a.mp3", 1, map[string][]string{"TITLE": {"A"}, "ARTIST": {"X"}, "ALBUM": {"Y"}})
	testaudio.Write(t, nas, "b.mp3", 2, map[string][]string{"TITLE": {"B"}, "ARTIST": {"Z"}})
	ids := e.addLibrary(t, stick, nas)
	e.scanNow(t, ids[stick])
	e.scanNow(t, ids[nas])
	os.Rename(nas, filepath.Join(base, "unplugged"))
	e.scanNow(t, ids[nas]) // fails: unavailable

	rec := e.send(t, http.MethodGet, "/api/library/roots", nil, withCookie(c))
	var roots struct {
		Items []library.RootStatus `json:"items"`
	}
	decodeBody(t, rec, &roots)
	if rec.Code != http.StatusOK || len(roots.Items) != 2 {
		t.Fatalf("roots = %d %s", rec.Code, rec.Body)
	}
	for _, r := range roots.Items {
		switch r.ID {
		case ids[stick]:
			if r.Name != "MUSICSTICK" || r.Status != "available" || r.TrackCount != 1 || r.LastScan.Status != "completed" {
				t.Errorf("stick root = %+v", r)
			}
		case ids[nas]:
			// Unavailable, but its track is still indexed and counted.
			if r.Status != "unavailable" || r.TrackCount != 1 || r.LastScan.Status != "failed" || r.LastScan.Error == nil || r.StatusMessage == nil {
				t.Errorf("nas root = %+v", r)
			}
		}
	}
	if strings.Contains(rec.Body.String(), base) || strings.Contains(rec.Body.String(), filepath.ToSlash(base)) {
		t.Errorf("roots JSON reveals absolute paths: %s", rec.Body)
	}

	rec = e.send(t, http.MethodGet, "/api/library", nil, withCookie(c))
	var sum summaryResponse
	decodeBody(t, rec, &sum)
	if sum.Tracks != 2 || sum.AvailableTracks != 1 || sum.Roots.Total != 2 || sum.Roots.Available != 1 ||
		sum.Roots.Unavailable != 1 || sum.LastSuccessfulScan == nil || sum.Scan.Running || sum.Scan.Active == nil || sum.Scan.Queued == nil {
		t.Errorf("summary = %+v", sum)
	}
	// The unavailable root's track is still listed, marked unavailable.
	l := getTracks(t, e, c, "?root="+itoa(ids[nas]))
	if len(l.Items) != 1 || l.Items[0].Available || l.Items[0].Status != "present" {
		t.Errorf("track on unavailable root = %+v", l.Items)
	}
}

// --- rescan ---

func TestScanAPIRunsInBackground(t *testing.T) {
	started := make(chan string, 20)
	release := make(chan struct{})
	e := newTestEnvWith(t, envConfig{extract: func(p string) metadata.Metadata {
		started <- p
		<-release
		return metadata.Extract(p)
	}})
	c := e.doSetup(t)
	dir := musicDir(t)
	id := e.addLibrary(t, dir)[dir]

	begin := time.Now()
	rec := e.send(t, http.MethodPost, "/api/library/scan", nil, withCookie(c))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST scan = %d %s", rec.Code, rec.Body)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("POST scan took %v; it must not wait for the scan", elapsed)
	}
	var sr scanResponse
	decodeBody(t, rec, &sr)
	if len(sr.Started) != 1 || sr.Started[0] != id || sr.AlreadyRunning == nil {
		t.Errorf("scan response = %+v", sr)
	}
	<-started // the scan is now blocked inside metadata reading

	var sum summaryResponse
	decodeBody(t, e.send(t, http.MethodGet, "/api/library", nil, withCookie(c)), &sum)
	if !sum.Scan.Running || len(sum.Scan.Active) != 1 || sum.Scan.Active[0].RootID != id {
		t.Errorf("scan state while running = %+v", sum.Scan)
	}
	var roots struct {
		Items []library.RootStatus `json:"items"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/library/roots", nil, withCookie(c)), &roots)
	if !roots.Items[0].Scanning {
		t.Error("root not reported as scanning")
	}

	// A second request while the scan runs is refused, not duplicated.
	expectError(t, e.send(t, http.MethodPost, "/api/library/scan", nil, withCookie(c)), http.StatusConflict, "scan_in_progress")
	expectError(t, e.send(t, http.MethodPost, "/api/library/scan", map[string][]int64{"root_ids": {id}}, withCookie(c)), http.StatusConflict, "scan_in_progress")

	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if last, ok, _ := library.LastScan(context.Background(), e.db, id); ok && last.Status == library.ScanCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background scan did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l := getTracks(t, e, c, ""); l.Pagination.Total != 4 {
		t.Errorf("tracks after background scan = %d", l.Pagination.Total)
	}
	if !strings.Contains(e.logs.String(), "library rescan requested") {
		t.Error("rescan request not logged")
	}
}

func TestScanAPIErrors(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	expectError(t, e.send(t, http.MethodPost, "/api/library/scan", nil, withCookie(c)), http.StatusConflict, "no_library_roots")

	dir := musicDir(t)
	id := e.addLibrary(t, dir)[dir]
	scan := "/api/library/scan"
	expectError(t, e.send(t, http.MethodPost, scan, map[string][]int64{"root_ids": {9999}}, withCookie(c)), http.StatusNotFound, "root_not_found")
	expectError(t, e.send(t, http.MethodPost, scan, map[string][]int64{"root_ids": {0}}, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	expectError(t, e.send(t, http.MethodPost, scan, nil, withCookie(c), withRawBody(`{"root_ids":"all"}`), withContentType("application/json")), http.StatusBadRequest, "invalid_request")
	expectError(t, e.send(t, http.MethodPost, scan, nil, withCookie(c), withRawBody(`{"path":"/etc"}`), withContentType("application/json")), http.StatusBadRequest, "invalid_request")
	expectError(t, e.send(t, http.MethodPost, scan, nil, withCookie(c), withRawBody(`root_ids=1`), withContentType("text/plain")), http.StatusUnsupportedMediaType, "unsupported_media_type")
	expectError(t, e.send(t, http.MethodPost, scan+"?root=1", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	expectError(t, e.send(t, http.MethodGet, scan, nil, withCookie(c)), http.StatusMethodNotAllowed, "method_not_allowed")

	// CSRF: a cross-site POST with a valid cookie is rejected and starts nothing.
	rec := e.send(t, http.MethodPost, scan, nil, withCookie(c),
		withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example"))
	expectError(t, rec, http.StatusForbidden, "cross_origin")
	if len(e.scanner.Running())+len(e.scanner.Queued()) != 0 {
		t.Error("a rejected cross-site request started a scan")
	}
	if _, ok, _ := library.LastScan(context.Background(), e.db, id); ok {
		t.Error("a scan was recorded although every request was rejected")
	}

	// Disabled roots cannot be scanned.
	library.SyncRoots(context.Background(), e.db, nil, e.logger, time.Now())
	expectError(t, e.send(t, http.MethodPost, scan, map[string][]int64{"root_ids": {id}}, withCookie(c)), http.StatusConflict, "root_disabled")
}

func TestScanAPIUnavailableRootPreservesTracks(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	base := t.TempDir()
	dir := filepath.Join(base, "usb")
	testaudio.Write(t, dir, "song.flac", 1, map[string][]string{"TITLE": {"Keep Me"}})
	id := e.addLibrary(t, dir)[dir]
	e.scanNow(t, id)
	os.Rename(dir, filepath.Join(base, "unplugged"))

	rec := e.send(t, http.MethodPost, "/api/library/scan", map[string][]int64{"root_ids": {id}}, withCookie(c))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST scan = %d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	var last library.Result
	for time.Now().Before(deadline) {
		var ok bool
		if last, ok, _ = library.LastScan(context.Background(), e.db, id); ok && last.Status != library.ScanCompleted && last.Status != library.ScanRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if last.Status != library.ScanFailed || last.Counts.TracksMissing != 0 {
		t.Fatalf("scan of unavailable root = %+v", last)
	}
	l := getTracks(t, e, c, "")
	if len(l.Items) != 1 || l.Items[0].Title != "Keep Me" || l.Items[0].Status != "present" || l.Items[0].Available {
		t.Errorf("tracks after failed scan = %+v", l.Items)
	}
	var sum summaryResponse
	decodeBody(t, e.send(t, http.MethodGet, "/api/library", nil, withCookie(c)), &sum)
	if sum.Tracks != 1 || sum.Roots.Unavailable != 1 || sum.LastSuccessfulScan == nil {
		t.Errorf("summary after failed scan = %+v", sum)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func urlEscape(s string) string { return url.QueryEscape(s) }
