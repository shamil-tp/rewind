// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const testIndex = `<!doctype html><title>Rewind test index</title>`

func testFrontend() fstest.MapFS {
	return fstest.MapFS{
		"index.html":          {Data: []byte(testIndex)},
		"favicon.svg":         {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"assets/index-a1.js":  {Data: []byte(`console.log("rewind")`)},
		"assets/index-a1.css": {Data: []byte(`body{}`)},
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return newTestEnv(t).handler
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = "localhost:8090"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json; body = %q", ct, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid error JSON %q: %v", rec.Body.String(), err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("error body missing code or message: %q", rec.Body.String())
	}
	return body.Error
}

func TestHealth(t *testing.T) {
	rec := do(t, newTestHandler(t), http.MethodGet, "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
	}
	want := map[string]any{"status": "ok", "name": "rewind", "version": "test"}
	if len(got) != len(want) {
		t.Errorf("body = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestHealthHead(t *testing.T) {
	rec := do(t, newTestHandler(t), http.MethodHead, "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", rec.Code)
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := do(t, newTestHandler(t), m, "/api/health")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s Allow = %q", m, allow)
		}
		if e := decodeError(t, rec); e.Code != "method_not_allowed" {
			t.Errorf("%s error code = %q", m, e.Code)
		}
	}
}

func TestUnknownAPIRoutesReturnJSON404(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/api", "/api/", "/api/nope", "/api/health/extra", "/api/v1/tracks"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			rec := do(t, h, m, target)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s status = %d, want 404", m, target, rec.Code)
				continue
			}
			if strings.Contains(rec.Body.String(), testIndex) {
				t.Errorf("%s %s returned the frontend index", m, target)
			}
			if e := decodeError(t, rec); e.Code != "not_found" {
				t.Errorf("%s %s error code = %q", m, target, e.Code)
			}
		}
	}
}

func TestFrontendRouting(t *testing.T) {
	h := newTestHandler(t)
	cases := []struct {
		target, wantType, wantCache string
		wantStatus                  int
		wantIndex                   bool
	}{
		{"/", "text/html; charset=utf-8", "no-cache", 200, true},
		{"/index.html", "text/html; charset=utf-8", "no-cache", 200, true},
		{"/library", "text/html; charset=utf-8", "no-cache", 200, true},          // client-side route
		{"/albums/42/tracks", "text/html; charset=utf-8", "no-cache", 200, true}, // nested client route
		{"/assets/", "text/html; charset=utf-8", "no-cache", 200, true},          // directory, never listed
		{"/assets/index-a1.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", 200, false},
		{"/assets/index-a1.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable", 200, false},
		{"/favicon.svg", "image/svg+xml", "no-cache", 200, false},
	}
	for _, tc := range cases {
		rec := do(t, h, http.MethodGet, tc.target)
		if rec.Code != tc.wantStatus {
			t.Errorf("GET %s status = %d, want %d", tc.target, rec.Code, tc.wantStatus)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); ct != tc.wantType {
			t.Errorf("GET %s Content-Type = %q, want %q", tc.target, ct, tc.wantType)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != tc.wantCache {
			t.Errorf("GET %s Cache-Control = %q, want %q", tc.target, cc, tc.wantCache)
		}
		if got := rec.Body.String() == testIndex; got != tc.wantIndex {
			t.Errorf("GET %s served index = %v, want %v", tc.target, got, tc.wantIndex)
		}
	}
}

func TestFrontendMissingAssetIs404(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/assets/missing.js", "/missing.png"} {
		rec := do(t, h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), testIndex) {
			t.Errorf("GET %s returned the frontend index", target)
		}
	}
}

func TestFrontendRejectsWrites(t *testing.T) {
	rec := do(t, newTestHandler(t), http.MethodPost, "/library")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /library status = %d, want 405", rec.Code)
	}
}

func TestNewRequiresIndex(t *testing.T) {
	opts := newTestEnv(t).opts
	opts.Frontend = fstest.MapFS{}
	if _, err := New(opts); err == nil {
		t.Error("New() with no index.html: error = nil, want error")
	}
	opts.Frontend = nil
	if _, err := New(opts); err == nil {
		t.Error("New() with nil frontend: error = nil, want error")
	}
	if _, err := New(Options{Frontend: testFrontend()}); err == nil {
		t.Error("New() without auth: error = nil, want error")
	}
}

func TestHostValidation(t *testing.T) {
	h := newTestHandler(t)
	for host, wantOK := range map[string]bool{
		"localhost":          true,
		"localhost:8090":     true,
		"127.0.0.1:8090":     true,
		"[::1]:8090":         true,
		"[::1]":              true,
		"evil.example":       false,
		"evil.example:8090":  false,
		"192.168.1.20:8090":  false,
		"100.101.102.103":    false,
		"rewind.tail.ts.net": false,
		"":                   false,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if wantOK && rec.Code != http.StatusOK {
			t.Errorf("Host %q status = %d, want 200", host, rec.Code)
		}
		if !wantOK {
			if rec.Code != http.StatusForbidden {
				t.Errorf("Host %q status = %d, want 403", host, rec.Code)
				continue
			}
			if e := decodeError(t, rec); e.Code != "forbidden_host" {
				t.Errorf("Host %q error code = %q", host, e.Code)
			}
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/", "/api/health", "/api/nope"} {
		rec := do(t, h, http.MethodGet, target)
		for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
			if rec.Header().Get(name) == "" {
				t.Errorf("GET %s missing %s", target, name)
			}
		}
	}
	// Rejected requests carry the headers too.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("forbidden-host response missing security headers")
	}
}

func TestRequestBodyLimit(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/health", strings.NewReader("x"))
	req.Host = "localhost"
	req.ContentLength = maxRequestBodyBytes + 1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "body_too_large" {
		t.Errorf("error code = %q", e.Code)
	}
}

// TestServeGracefulShutdown starts a real server, begins a slow request, then
// cancels the context and checks that the request completes and Serve returns
// cleanly.
func TestServeGracefulShutdown(t *testing.T) {
	lns, err := Listen([]string{"127.0.0.1:0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ln := lns[0]
	base := "http://" + ln.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	app := newTestHandler(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, "done")
	})
	mux.Handle("/", app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, lns, mux, slog.New(slog.DiscardHandler)) }()

	resp, err := http.Get(base + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/health status = %d", resp.StatusCode)
	}

	slow := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			slow <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		slow <- string(b)
	}()
	<-started

	cancel() // simulate SIGINT/SIGTERM

	// New connections are refused once shutdown begins.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting connections after shutdown began")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err := <-served:
		t.Fatalf("Serve returned (%v) before the in-flight request finished", err)
	default:
	}

	close(release)
	if got := <-slow; got != "done" {
		t.Errorf("in-flight request result = %q, want %q", got, "done")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}
