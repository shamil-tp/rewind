// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// streamFixture is a library root on disk plus direct database rows, so tests
// can create records the scanner never would (traversal paths, symlinks…).
type streamFixture struct {
	e      *testEnv
	cookie *http.Cookie
	dir    string // root directory
	rootID int64
	data   []byte // contents of "music/song.flac"
	songID int64
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	e := newTestEnv(t)
	f := &streamFixture{e: e, cookie: e.doSetup(t), dir: filepath.Join(t.TempDir(), "usb")}
	f.data = make([]byte, 100_000)
	rand.Read(f.data)
	f.write(t, "music/song.flac", f.data)
	res, err := e.db.Exec(`INSERT INTO library_roots (configured_path, path, enabled, status, created_at)
		VALUES (?, ?, 1, 'available', 1)`, f.dir, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.rootID, _ = res.LastInsertId()
	f.songID = f.track(t, "music/song.flac", "flac")
	return f
}

func (f *streamFixture) write(t *testing.T, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// track inserts a track row with the given stored relative path.
func (f *streamFixture) track(t *testing.T, rel, format string) int64 {
	t.Helper()
	res, err := f.e.db.Exec(`INSERT INTO tracks (root_id, rel_path, file_size, file_mtime_ns, format, title,
		metadata_status, first_seen_at, updated_at) VALUES (?, ?, 0, 0, ?, 'x', 'ok', 1, 1)`, f.rootID, rel, format)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *streamFixture) get(t *testing.T, id int64, rangeHeader string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	all := append([]reqOpt{withCookie(f.cookie)}, opts...)
	if rangeHeader != "" {
		all = append(all, withHeader("Range", rangeHeader))
	}
	return f.e.send(t, http.MethodGet, "/api/tracks/"+strconv.FormatInt(id, 10)+"/stream", nil, all...)
}

func TestStreamFullFile(t *testing.T) {
	f := newStreamFixture(t)
	rec := f.get(t, f.songID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "audio/flac" || h.Get("Content-Length") != "100000" || h.Get("Accept-Ranges") != "bytes" {
		t.Errorf("headers = %v", h)
	}
	if h.Get("ETag") == "" || h.Get("Last-Modified") == "" || h.Get("Cache-Control") != "private, no-store" {
		t.Errorf("caching headers = %v", h)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") == "" {
		t.Error("security headers missing on audio response")
	}
	if !bytes.Equal(rec.Body.Bytes(), f.data) {
		t.Errorf("body differs from file (%d bytes)", rec.Body.Len())
	}
}

func TestStreamRanges(t *testing.T) {
	f := newStreamFixture(t)
	size := int64(len(f.data))
	cases := []struct {
		header     string
		start, end int64 // inclusive
	}{
		{"bytes=0-1023", 0, 1023},
		{"bytes=1024-", 1024, size - 1},
		{"bytes=-1024", size - 1024, size - 1},
		{"bytes=99990-200000", 99990, size - 1}, // clipped at EOF
		{"bytes=99999-99999", size - 1, size - 1},
		{"bytes=0-0", 0, 0},
	}
	for _, tc := range cases {
		rec := f.get(t, f.songID, tc.header)
		if rec.Code != http.StatusPartialContent {
			t.Errorf("%s: status = %d", tc.header, rec.Code)
			continue
		}
		wantCR := fmt.Sprintf("bytes %d-%d/%d", tc.start, tc.end, size)
		wantLen := tc.end - tc.start + 1
		if cr := rec.Header().Get("Content-Range"); cr != wantCR {
			t.Errorf("%s: Content-Range = %q, want %q", tc.header, cr, wantCR)
		}
		if cl := rec.Header().Get("Content-Length"); cl != strconv.FormatInt(wantLen, 10) {
			t.Errorf("%s: Content-Length = %q, want %d", tc.header, cl, wantLen)
		}
		if rec.Header().Get("Content-Type") != "audio/flac" {
			t.Errorf("%s: Content-Type = %q", tc.header, rec.Header().Get("Content-Type"))
		}
		if !bytes.Equal(rec.Body.Bytes(), f.data[tc.start:tc.end+1]) {
			t.Errorf("%s: body is not the requested byte range", tc.header)
		}
	}
}

func TestStreamUnsatisfiableAndMalformedRanges(t *testing.T) {
	f := newStreamFixture(t)
	for _, h := range []string{"bytes=100000-", "bytes=200000-300000"} {
		rec := f.get(t, f.songID, h)
		if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Header().Get("Content-Range") != "bytes */100000" {
			t.Errorf("%s: status %d, Content-Range %q", h, rec.Code, rec.Header().Get("Content-Range"))
		}
		if bytes.Contains(rec.Body.Bytes(), f.data[:16]) {
			t.Errorf("%s: file data in a 416 response", h)
		}
	}
	// Zero-length suffix ranges are unsatisfiable (RFC 9110), in every
	// spelling net/http accepts; ServeContent alone would answer them with a
	// 206 and the invalid Content-Range "bytes 100000-99999/100000".
	for _, h := range []string{"bytes=-0", "bytes=-  0", "bytes=-\t0", "bytes= - 0", "bytes=-+0", "bytes=-00", "bytes=-0,-+0"} {
		rec := f.get(t, f.songID, h)
		if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Header().Get("Content-Range") != "bytes */100000" {
			t.Errorf("%q: status %d, Content-Range %q", h, rec.Code, rec.Header().Get("Content-Range"))
		}
	}
	// A satisfiable range next to a zero-length suffix is still served.
	if rec := f.get(t, f.songID, "bytes=-  0, 0-9"); rec.Code != http.StatusPartialContent ||
		rec.Header().Get("Content-Range") != "bytes 0-9/100000" || !bytes.Equal(rec.Body.Bytes(), f.data[:10]) {
		t.Errorf("zero suffix plus 0-9: status %d, Content-Range %q", rec.Code, rec.Header().Get("Content-Range"))
	}
	// Go's ServeContent answers malformed Range headers with 416, too, and
	// never with the whole file.
	for _, h := range []string{"bytes=abc", "bytes=5-2", "items=0-10", "bytes=-"} {
		rec := f.get(t, f.songID, h)
		if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Body.Len() > 100 {
			t.Errorf("%s: status %d, body %d bytes", h, rec.Code, rec.Body.Len())
		}
	}
}

func TestStreamMultipleRanges(t *testing.T) {
	f := newStreamFixture(t)
	rec := f.get(t, f.songID, "bytes=0-9,100-109")
	if rec.Code != http.StatusPartialContent || !strings.HasPrefix(rec.Header().Get("Content-Type"), "multipart/byteranges") {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.Bytes()
	if !bytes.Contains(body, f.data[0:10]) || !bytes.Contains(body, f.data[100:110]) || !bytes.Contains(body, []byte("Content-Type: audio/flac")) {
		t.Error("multipart body does not contain both ranges with the audio type")
	}
}

func TestStreamHead(t *testing.T) {
	f := newStreamFixture(t)
	rec := f.e.send(t, http.MethodHead, "/api/tracks/"+strconv.FormatInt(f.songID, 10)+"/stream", nil, withCookie(f.cookie))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "100000" || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d, length %q, body %d", rec.Code, rec.Header().Get("Content-Length"), rec.Body.Len())
	}
}

func TestStreamConditionalRequests(t *testing.T) {
	f := newStreamFixture(t)
	etag := f.get(t, f.songID, "").Header().Get("ETag")
	// If-Range with the current validator honours the range…
	if rec := f.get(t, f.songID, "bytes=0-9", withHeader("If-Range", etag)); rec.Code != http.StatusPartialContent {
		t.Errorf("If-Range match: %d", rec.Code)
	}
	// …but after the file changes, the stale validator gets the full new file.
	newData := bytes.Repeat([]byte{7}, 500)
	p := f.write(t, "music/song.flac", newData)
	later := time.Now().Add(time.Hour)
	os.Chtimes(p, later, later)
	rec := f.get(t, f.songID, "bytes=0-9", withHeader("If-Range", etag))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), newData) {
		t.Errorf("stale If-Range: status %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestStreamEmptyFile(t *testing.T) {
	f := newStreamFixture(t)
	f.write(t, "empty.mp3", nil)
	id := f.track(t, "empty.mp3", "mp3")
	rec := f.get(t, id, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "0" || rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("empty file: %d %v", rec.Code, rec.Header())
	}
	// ServeContent ignores a Range header for an empty file (200, not 416).
	if rec := f.get(t, id, "bytes=0-10"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("empty file with Range: %d", rec.Code)
	}
}

func TestStreamContentTypes(t *testing.T) {
	f := newStreamFixture(t)
	for format, want := range map[string]string{"mp3": "audio/mpeg", "flac": "audio/flac", "m4a": "audio/mp4", "ogg": "audio/ogg", "wav": "audio/wav", "opus": "application/octet-stream"} {
		f.write(t, "t."+format, []byte("data"))
		id := f.track(t, "t."+format, format)
		// The client's Accept header has no influence.
		if got := f.get(t, id, "", withHeader("Accept", "text/html")).Header().Get("Content-Type"); got != want {
			t.Errorf("%s: Content-Type = %q, want %q", format, got, want)
		}
	}
}

// --- errors and security ---

func TestStreamRequiresAuth(t *testing.T) {
	f := newStreamFixture(t)
	path := "/api/tracks/" + strconv.FormatInt(f.songID, 10) + "/stream"
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		rec := f.e.send(t, m, path, nil, withHeader("Range", "bytes=0-10"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without session = %d", m, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), f.data[:16]) {
			t.Errorf("%s without session returned file data", m)
		}
	}
	f.e.send(t, http.MethodPost, "/api/auth/logout", nil, withCookie(f.cookie))
	if rec := f.get(t, f.songID, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("after logout = %d", rec.Code)
	}
}

func TestStreamInvalidRequests(t *testing.T) {
	f := newStreamFixture(t)
	for _, bad := range []string{"abc", "0", "-1", "1.5"} {
		rec := f.e.send(t, http.MethodGet, "/api/tracks/"+bad+"/stream", nil, withCookie(f.cookie))
		expectError(t, rec, http.StatusBadRequest, "invalid_parameter")
	}
	expectError(t, f.get(t, 99999, ""), http.StatusNotFound, "not_found")
	expectError(t, f.e.send(t, http.MethodGet, "/api/tracks/1/stream?path=/etc/passwd", nil, withCookie(f.cookie)),
		http.StatusBadRequest, "invalid_parameter")
	expectError(t, f.e.send(t, http.MethodPost, "/api/tracks/1/stream", nil, withCookie(f.cookie)), http.StatusMethodNotAllowed, "method_not_allowed")
	expectError(t, f.e.send(t, http.MethodGet, "/api/tracks/1/stream/extra", nil, withCookie(f.cookie)), http.StatusNotFound, "not_found")
}

func TestStreamRejectsUnsafeStoredPaths(t *testing.T) {
	f := newStreamFixture(t)
	secret := []byte("TOP SECRET outside the library")
	outside := filepath.Join(filepath.Dir(f.dir), "secret.txt")
	os.WriteFile(outside, secret, 0o644)
	abs := filepath.ToSlash(outside)
	for _, rel := range []string{
		"../secret.txt",
		"music/../../secret.txt",
		abs, // absolute path stored in the database
		"/etc/passwd",
		"",
		".",
		"music", // a directory
		"C:/Windows/win.ini",
		`..\secret.txt`,
	} {
		id := f.track(t, rel, "mp3")
		rec := f.get(t, id, "")
		if rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
			t.Errorf("stored path %q was served (%d)", rel, rec.Code)
			continue
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("stored path %q: status %d, want 404", rel, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), secret) || strings.Contains(rec.Body.String(), filepath.Dir(f.dir)) ||
			strings.Contains(rec.Body.String(), filepath.ToSlash(filepath.Dir(f.dir))) {
			t.Errorf("stored path %q leaked data or a path: %s", rel, rec.Body)
		}
	}
	if strings.Contains(f.e.logs.String(), string(secret)) {
		t.Error("secret file contents in logs")
	}
}

func TestStreamRejectsSymlinks(t *testing.T) {
	f := newStreamFixture(t)
	outside := filepath.Join(filepath.Dir(f.dir), "outside.mp3")
	os.WriteFile(outside, []byte("outside data"), 0o644)
	if err := os.Symlink(outside, filepath.Join(f.dir, "escape.mp3")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	os.Symlink(filepath.Join(f.dir, "music", "song.flac"), filepath.Join(f.dir, "inside.flac"))
	os.Symlink(filepath.Dir(f.dir), filepath.Join(f.dir, "parentdir"))
	for _, rel := range []string{"escape.mp3", "inside.flac", "parentdir/outside.mp3"} {
		id := f.track(t, rel, "mp3")
		rec := f.get(t, id, "")
		if rec.Code != http.StatusNotFound || bytes.Contains(rec.Body.Bytes(), []byte("outside data")) {
			t.Errorf("symlink %q: status %d, body %q", rel, rec.Code, rec.Body)
		}
	}
}

func TestStreamRejectsFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on Windows")
	}
	f := newStreamFixture(t)
	if err := mkfifo(filepath.Join(f.dir, "pipe.mp3")); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	id := f.track(t, "pipe.mp3", "mp3")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.get(t, id, "") }()
	select {
	case rec := <-done:
		expectError(t, rec, http.StatusNotFound, "file_unavailable")
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked the request")
	}
}

func TestStreamMissingFileAndUnavailableRoot(t *testing.T) {
	f := newStreamFixture(t)
	f.write(t, "gone.mp3", []byte("soon gone"))
	gone := f.track(t, "gone.mp3", "mp3")
	os.Remove(filepath.Join(f.dir, "gone.mp3"))
	rec := f.get(t, gone, "")
	expectError(t, rec, http.StatusNotFound, "file_unavailable")

	// Unplug the drive: the root directory disappears.
	os.Rename(f.dir, f.dir+"-unplugged")
	rec = f.get(t, f.songID, "")
	expectError(t, rec, http.StatusServiceUnavailable, "root_unavailable")
	for _, r := range []*httptest.ResponseRecorder{rec} {
		if strings.Contains(r.Body.String(), "usb") {
			t.Errorf("error reveals a path: %s", r.Body)
		}
	}
	// Nothing was written to the database by a failed stream.
	var n int
	f.e.db.QueryRow("SELECT count(*) FROM tracks WHERE status = 'present'").Scan(&n)
	if n == 0 {
		t.Error("streaming changed track records")
	}
}

func TestStreamDisabledRoot(t *testing.T) {
	f := newStreamFixture(t)
	f.e.db.Exec("UPDATE library_roots SET enabled = 0 WHERE id = ?", f.rootID)
	expectError(t, f.get(t, f.songID, ""), http.StatusConflict, "root_disabled")
}

// TestStreamOverRealServer checks range seeking through the full stack and
// that aborted downloads release their file descriptors (Linux only for the
// descriptor count).
func TestStreamOverRealServer(t *testing.T) {
	f := newStreamFixture(t)
	big := make([]byte, 8<<20)
	rand.Read(big)
	f.write(t, "big.wav", big)
	id := f.track(t, "big.wav", "wav")
	srv := httptest.NewServer(f.e.handler)
	defer srv.Close()
	url := srv.URL + "/api/tracks/" + strconv.FormatInt(id, 10) + "/stream"
	req := func(rng string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, url, nil)
		r.AddCookie(f.cookie)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if rng != "" {
			r.Header.Set("Range", rng)
		}
		return r
	}

	resp, err := http.DefaultClient.Do(req("bytes=4194304-4194399"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, big[4194304:4194400]) {
		t.Fatalf("seek over HTTP: %d, %d bytes", resp.StatusCode, len(got))
	}

	before := openFDs()
	for i := 0; i < 20; i++ {
		resp, err := http.DefaultClient.Do(req(""))
		if err != nil {
			t.Fatal(err)
		}
		io.CopyN(io.Discard, resp.Body, 64<<10) // read a little, then hang up
		resp.Body.Close()
	}
	srv.CloseClientConnections()
	if before >= 0 {
		deadline := time.Now().Add(5 * time.Second)
		for openFDs() > before+2 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if after := openFDs(); after > before+2 {
			t.Errorf("open file descriptors grew from %d to %d after aborted downloads", before, after)
		}
	}
}

func openFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}
