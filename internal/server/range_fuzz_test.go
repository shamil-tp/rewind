// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

// FuzzStreamRange sends arbitrary Range headers to the streaming endpoint.
// Every response must be a full 200, a 206 whose parts match the file at
// the stated offsets, or a 416; nothing may panic or return other data.
//
//	go test ./internal/server -run '^$' -fuzz FuzzStreamRange -fuzztime 60s
func FuzzStreamRange(f *testing.F) {
	for _, seed := range []string{
		"bytes=0-", "bytes=10-20", "bytes=-5", "bytes=0-0,5-9", "bytes=99999-", "bytes=5-1",
		"bytes=-0", "bytes=-  0", "bytes=-+0", "bytes=0-18446744073709551615", "bytes= 1 - 2 ", "items=0-1", "bytes=,,,", "",
		"bytes=" + strings.Repeat("0-1,", 200),
	} {
		f.Add(seed)
	}
	e := newTestEnvWith(f, envConfig{})
	c := e.doSetup(f)
	dir := f.TempDir()
	path := testaudio.Write(f, dir, "t.flac", 1, nil)
	e.scanNow(f, e.addLibrary(f, dir)[dir])
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz-rewind-range-fuzz")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, rng string) {
		if strings.ContainsAny(rng, "\r\n\x00") {
			return // not representable in a header
		}
		rec := e.send(t, http.MethodGet, "/api/tracks/1/stream", nil, withCookie(c), withHeader("Range", rng))
		body := rec.Body.Bytes()
		switch rec.Code {
		case http.StatusOK:
			if !bytes.Equal(body, data) {
				t.Fatalf("Range %q: 200 with %d bytes", rng, len(body))
			}
		case http.StatusRequestedRangeNotSatisfiable:
			// Go answers syntactically invalid ranges with a bare 416; an
			// unsatisfiable range carries the current length.
			if cr := rec.Header().Get("Content-Range"); cr != "" && cr != fmt.Sprintf("bytes */%d", len(data)) {
				t.Fatalf("Range %q: 416 with Content-Range %q", rng, cr)
			}
		case http.StatusPartialContent:
			mt, params, _ := mime.ParseMediaType(rec.Header().Get("Content-Type"))
			if mt == "multipart/byteranges" {
				mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
				for {
					p, err := mr.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("Range %q: bad multipart: %v", rng, err)
					}
					part, _ := io.ReadAll(p)
					checkPart(t, rng, p.Header.Get("Content-Range"), part, data)
				}
				return
			}
			checkPart(t, rng, rec.Header().Get("Content-Range"), body, data)
		default:
			t.Fatalf("Range %q: unexpected status %d", rng, rec.Code)
		}
	})
}

func checkPart(t *testing.T, rng, contentRange string, part, data []byte) {
	t.Helper()
	var start, end, size int
	if _, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &start, &end, &size); err != nil ||
		size != len(data) || start < 0 || end < start || end >= size {
		t.Fatalf("Range %q: bad Content-Range %q", rng, contentRange)
	}
	if !bytes.Equal(part, data[start:end+1]) {
		t.Fatalf("Range %q: part %s has the wrong %d bytes", rng, contentRange, len(part))
	}
}
