// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/db"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/metadata"
	"github.com/shamil-tp/rewind/internal/plugin"
	"github.com/shamil-tp/rewind/internal/prefs"
	"github.com/shamil-tp/rewind/internal/theme"
)

const (
	ownerName = "owner"
	ownerPass = "correct horse battery staple"
)

// syncBuffer collects log output from concurrent handlers.
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

type testEnv struct {
	handler http.Handler
	opts    Options
	dataDir string
	code    string // display form of the setup code
	logs    *syncBuffer
	db      *sql.DB
	logger  *slog.Logger
	scanner *library.Scanner
}

type envConfig struct {
	secure  bool
	limiter *auth.Limiter
	dataDir string
	// extract replaces the scanner's metadata reader (tests use it to block
	// or observe scans); nil uses the real reader.
	extract     func(string) metadata.Metadata
	maxPageSize int
	// trustedHosts are passed as Options.TrustedHosts.
	trustedHosts []string
	// plugins is passed as Options.Plugins.
	plugins *plugin.Manager
	// themeDir is the theme directory ("" for bundled themes only).
	themeDir string
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWith(t, envConfig{})
}

// newTestEnvWith builds the full handler (all middleware) over a temporary
// database. A protected test-only route is mounted at /api/test/protected.
func newTestEnvWith(t testing.TB, c envConfig) *testEnv {
	t.Helper()
	if c.dataDir == "" {
		c.dataDir = t.TempDir()
	}
	if c.limiter == nil {
		c.limiter = auth.NewLimiterWithLimits(1000, time.Millisecond, 1000, time.Millisecond)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	conn, err := db.Open(context.Background(), filepath.Join(c.dataDir, db.FileName), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	svc, err := auth.NewService(context.Background(), auth.Options{
		DB: conn, Lifetime: 30 * 24 * time.Hour, IdleTimeout: 7 * 24 * time.Hour, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{dataDir: c.dataDir, logs: logs}
	var code *auth.SetupCode
	if req, _ := svc.SetupRequired(context.Background()); req {
		code, err = auth.NewSetupCode(c.dataDir)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(code.Path())
		env.code = strings.TrimSpace(string(raw))
	}
	scanner, err := library.NewScanner(library.Options{DB: conn, Logger: logger, Extract: c.extract})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scanner.Close) // runs before conn.Close (cleanups are LIFO)
	env.db, env.logger, env.scanner = conn, logger, scanner
	themes, err := theme.Load(c.themeDir, logger)
	if err != nil {
		t.Fatal(err)
	}
	preferences := prefs.NewStore(conn, prefs.WithThemes(themes.Has))
	catalog := library.NewCatalog(conn)
	artwork, err := library.NewArtworkCache(conn, catalog, filepath.Join(c.dataDir, "artwork"))
	if err != nil {
		t.Fatal(err)
	}
	env.opts = Options{
		Frontend: testFrontend(), Version: "test", Auth: svc, SetupCode: code,
		Limiter: c.limiter, CookieSecure: c.secure, Logger: logger,
		Catalog: catalog, Scanner: scanner, MaxPageSize: c.maxPageSize,
		Playlists: library.NewPlaylists(conn), Preferences: preferences,
		Themes:   theme.NewStore(conn, themes, preferences),
		Personal: library.NewPersonal(conn), Editor: library.NewEditor(conn),
		Playback: library.NewPlaybackStore(conn), Artwork: artwork,
		TrustedHosts: c.trustedHosts, Plugins: c.plugins,
	}
	env.handler, err = newHandler(env.opts, func(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
		mux.Handle("/api/test/protected", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := auth.UserFrom(r.Context())
			if !ok {
				t.Error("protected handler ran without a user in context")
			}
			writeJSON(w, http.StatusOK, map[string]any{"method": r.Method, "user": u.Username})
		})))
	})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt {
	return func(r *http.Request) {
		if c != nil {
			r.AddCookie(c)
		}
	}
}
func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func fromRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func withRawBody(body string) reqOpt {
	return func(r *http.Request) { r.Body = ioNopCloser(body); r.ContentLength = int64(len(body)) }
}
func withContentType(ct string) reqOpt { return withHeader("Content-Type", ct) }

// send issues a same-origin browser-like request from a loopback peer.
func (e *testEnv) send(t testing.TB, method, target string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Host = "localhost:8090"
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://localhost:8090")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) doSetup(t testing.TB) *http.Cookie {
	t.Helper()
	rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
		"setup_code": e.code, "username": ownerName, "password": ownerPass,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", rec.Code, rec.Body)
	}
	return sessionCookie(t, rec)
}

func (e *testEnv) doLogin(t *testing.T, opts ...reqOpt) *http.Cookie {
	t.Helper()
	rec := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": ownerPass}, opts...)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body)
	}
	return sessionCookie(t, rec)
}

func sessionCookie(t testing.TB, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if strings.HasSuffix(c.Name, "rewind_session") {
			return c
		}
	}
	t.Fatalf("no session cookie in response; headers = %v", rec.Header())
	return nil
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) statusResponse {
	t.Helper()
	var s statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("status body %q: %v", rec.Body, err)
	}
	return s
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, status, rec.Body)
	}
	if e := decodeError(t, rec); e.Code != code {
		t.Fatalf("error code = %q, want %q", e.Code, code)
	}
}

// --- status and setup ---

func TestAuthStatusBeforeAndAfterSetup(t *testing.T) {
	e := newTestEnv(t)
	rec := e.send(t, http.MethodGet, "/api/auth/status", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, Cache-Control = %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if s := decodeStatus(t, rec); !s.SetupRequired || s.Authenticated {
		t.Errorf("before setup: %+v", s)
	}
	cookie := e.doSetup(t)
	if s := decodeStatus(t, e.send(t, http.MethodGet, "/api/auth/status", nil)); s.SetupRequired || s.Authenticated {
		t.Errorf("after setup, no cookie: %+v", s)
	}
	if s := decodeStatus(t, e.send(t, http.MethodGet, "/api/auth/status", nil, withCookie(cookie))); s.SetupRequired || !s.Authenticated {
		t.Errorf("after setup, with cookie: %+v", s)
	}
	// The status response never names the account.
	if strings.Contains(e.send(t, http.MethodGet, "/api/auth/status", nil, withCookie(cookie)).Body.String(), ownerName) {
		t.Error("status response discloses the username")
	}
}

func TestSetupSuccessSignsInAndConsumesCode(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.doSetup(t)
	rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(cookie))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"owner"`) {
		t.Fatalf("me after setup = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, auth.SetupCodeFile)); !os.IsNotExist(err) {
		t.Error("setup code file still exists after setup")
	}
}

func TestSetupRejectedAfterCompletion(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
		"setup_code": e.code, "username": "intruder", "password": "another long password",
	})
	expectError(t, rec, http.StatusConflict, "setup_complete")
}

func TestSetupRequiresValidCode(t *testing.T) {
	e := newTestEnv(t)
	for _, code := range []string{"", "AAAA-BBBB-CCCC-DDDD", e.code + "X"} {
		rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
			"setup_code": code, "username": ownerName, "password": ownerPass,
		})
		expectError(t, rec, http.StatusForbidden, "invalid_setup_code")
	}
	if s := decodeStatus(t, e.send(t, http.MethodGet, "/api/auth/status", nil)); !s.SetupRequired {
		t.Error("account created without a valid setup code")
	}
	if strings.Contains(e.logs.String(), e.code) || strings.Contains(e.logs.String(), strings.ReplaceAll(e.code, "-", "")) {
		t.Error("setup code appears in the logs")
	}
}

func TestSetupOnlyFromLoopbackPeer(t *testing.T) {
	e := newTestEnv(t)
	for _, remote := range []string{"192.168.1.20:40000", "100.101.102.103:40000", "[fd7a:115c:a1e0::1]:40000"} {
		rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
			"setup_code": e.code, "username": ownerName, "password": ownerPass,
		}, fromRemote(remote))
		expectError(t, rec, http.StatusForbidden, "setup_not_local")
	}
	// A spoofed forwarding header does not make a remote peer local.
	rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
		"setup_code": e.code, "username": ownerName, "password": ownerPass,
	}, fromRemote("192.168.1.20:40000"), withHeader("X-Forwarded-For", "127.0.0.1"))
	expectError(t, rec, http.StatusForbidden, "setup_not_local")
	// IPv6 loopback is local.
	rec = e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
		"setup_code": e.code, "username": ownerName, "password": ownerPass,
	}, fromRemote("[::1]:40000"))
	if rec.Code != http.StatusCreated {
		t.Errorf("setup from [::1] = %d %s", rec.Code, rec.Body)
	}
}

func TestSetupValidationKeepsCodeValid(t *testing.T) {
	e := newTestEnv(t)
	rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
		"setup_code": e.code, "username": ownerName, "password": "short",
	})
	expectError(t, rec, http.StatusUnprocessableEntity, "validation_failed")
	if !strings.Contains(decodeError(t, rec).Message, "12 characters") {
		t.Errorf("validation message = %q", decodeError(t, rec).Message)
	}
	e.doSetup(t) // the same code still works
}

func TestSetupConcurrentHTTP(t *testing.T) {
	e := newTestEnv(t)
	const n = 6
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := e.send(t, http.MethodPost, "/api/auth/setup", map[string]string{
				"setup_code": e.code, "username": "user" + string(rune('a'+i)), "password": ownerPass,
			})
			codes <- rec.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	created := 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict, http.StatusForbidden: // complete, or code already consumed
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if created != 1 {
		t.Errorf("accounts created = %d, want 1", created)
	}
}

// --- login, cookies, sessions ---

func TestLoginCookieAttributesHTTP(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	c := e.doLogin(t)
	if c.Name != "rewind_session" || !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("cookie = %+v; want rewind_session, HttpOnly, not Secure, SameSite=Strict, Path=/", c)
	}
	if want := int((30 * 24 * time.Hour).Seconds()); c.MaxAge < want-5 || c.MaxAge > want+1 {
		t.Errorf("Max-Age = %d, want about %d", c.MaxAge, want)
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want none", c.Domain)
	}
}

func TestLoginCookieAttributesSecure(t *testing.T) {
	e := newTestEnvWith(t, envConfig{secure: true})
	e.doSetup(t)
	c := e.doLogin(t)
	if c.Name != "__Host-rewind_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("secure cookie = %+v", c)
	}
	// The secure cookie name is what the server reads back.
	if rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(c)); rec.Code != http.StatusOK {
		t.Errorf("me with secure cookie = %d", rec.Code)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	wrong := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": "wrong password here"})
	unknown := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": "nobody", "password": ownerPass})
	expectError(t, wrong, http.StatusUnauthorized, "invalid_credentials")
	expectError(t, unknown, http.StatusUnauthorized, "invalid_credentials")
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("responses differ: %q vs %q", wrong.Body, unknown.Body)
	}
	if len(wrong.Result().Cookies()) != 0 || len(unknown.Result().Cookies()) != 0 {
		t.Error("failed login set a cookie")
	}
}

func TestLoginRotatesSessionHTTP(t *testing.T) {
	e := newTestEnv(t)
	first := e.doSetup(t)
	second := e.doLogin(t, withCookie(first))
	if second.Value == first.Value {
		t.Fatal("login did not rotate the session token")
	}
	expectError(t, e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(first)), http.StatusUnauthorized, "unauthenticated")
	if rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(second)); rec.Code != http.StatusOK {
		t.Errorf("new session rejected: %d", rec.Code)
	}
}

func TestMeRequiresSession(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	rec := e.send(t, http.MethodGet, "/api/auth/me", nil)
	expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("API 401 redirected to %q", loc)
	}
	// A bogus cookie is rejected and cleared.
	rec = e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(&http.Cookie{Name: "rewind_session", Value: strings.Repeat("A", 43)}))
	expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
	if c := sessionCookie(t, rec); c.MaxAge >= 0 {
		t.Errorf("invalid cookie not cleared: %+v", c)
	}
}

func TestMeReturnsOnlySafeFields(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(c))
	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for k := range body["user"] {
		if k != "id" && k != "username" {
			t.Errorf("me exposes field %q", k)
		}
	}
	if strings.Contains(rec.Body.String(), "argon2") || strings.Contains(rec.Body.String(), c.Value) {
		t.Error("me response contains a hash or the session token")
	}
}

func TestLogoutRevokesSessionHTTP(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	rec := e.send(t, http.MethodPost, "/api/auth/logout", nil, withCookie(c))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", rec.Code, rec.Body)
	}
	if cleared := sessionCookie(t, rec); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout did not clear the cookie: %+v", cleared)
	}
	expectError(t, e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(c)), http.StatusUnauthorized, "unauthenticated")
	// Logging out again, or without a session, still succeeds.
	if rec := e.send(t, http.MethodPost, "/api/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Errorf("logout without session = %d", rec.Code)
	}
}

func TestSessionSurvivesRestartHTTP(t *testing.T) {
	dir := t.TempDir()
	e := newTestEnvWith(t, envConfig{dataDir: dir})
	c := e.doSetup(t)
	restarted := newTestEnvWith(t, envConfig{dataDir: dir}) // new DB handle + service on the same file
	if rec := restarted.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(c)); rec.Code != http.StatusOK {
		t.Fatalf("me after restart = %d %s", rec.Code, rec.Body)
	}
	if restarted.opts.SetupCode != nil {
		t.Error("restart after setup generated a new setup code")
	}
}

func TestChangePasswordHTTP(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	other := e.doLogin(t) // a second device

	expectError(t, e.send(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"current_password": ownerPass, "new_password": "brand new long password"}),
		http.StatusUnauthorized, "unauthenticated")
	expectError(t, e.send(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"current_password": "not my password", "new_password": "brand new long password"}, withCookie(c)),
		http.StatusForbidden, "invalid_current_password")
	expectError(t, e.send(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"current_password": ownerPass, "new_password": "short"}, withCookie(c)),
		http.StatusUnprocessableEntity, "validation_failed")

	rec := e.send(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"current_password": ownerPass, "new_password": "brand new long password"}, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("change-password = %d %s", rec.Code, rec.Body)
	}
	fresh := sessionCookie(t, rec)
	for _, old := range []*http.Cookie{c, other} {
		expectError(t, e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(old)), http.StatusUnauthorized, "unauthenticated")
	}
	if rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(fresh)); rec.Code != http.StatusOK {
		t.Errorf("fresh session rejected: %d", rec.Code)
	}
}

// --- middleware on protected routes ---

func TestProtectedRouteRequiresAuth(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		expectError(t, e.send(t, m, "/api/test/protected", nil), http.StatusUnauthorized, "unauthenticated")
	}
	rec := e.send(t, http.MethodGet, "/api/test/protected", nil, withCookie(c))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user":"owner"`) {
		t.Fatalf("authenticated GET = %d %s", rec.Code, rec.Body)
	}
	e.send(t, http.MethodPost, "/api/auth/logout", nil, withCookie(c))
	expectError(t, e.send(t, http.MethodGet, "/api/test/protected", nil, withCookie(c)), http.StatusUnauthorized, "unauthenticated")
}

// --- CSRF ---

func TestCSRFRejectsCrossOriginStateChanges(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	crossSite := []reqOpt{withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example")}
	originOnly := []reqOpt{withHeader("Sec-Fetch-Site", ""), withHeader("Origin", "https://evil.example")}
	for _, attack := range [][]reqOpt{crossSite, originOnly} {
		for _, target := range []string{"/api/test/protected", "/api/auth/logout", "/api/auth/change-password"} {
			opts := append([]reqOpt{withCookie(c)}, attack...)
			rec := e.send(t, http.MethodPost, target, map[string]string{"current_password": ownerPass, "new_password": "attacker chosen password"}, opts...)
			expectError(t, rec, http.StatusForbidden, "cross_origin")
		}
		// Login and setup are protected too (login CSRF).
		rec := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": ownerPass}, attack...)
		expectError(t, rec, http.StatusForbidden, "cross_origin")
	}
	// The session survived every attack.
	if rec := e.send(t, http.MethodGet, "/api/auth/me", nil, withCookie(c)); rec.Code != http.StatusOK {
		t.Fatalf("session revoked by a rejected cross-origin request: %d", rec.Code)
	}
	// Same-origin and non-browser (no Sec-Fetch-Site, no Origin) requests pass.
	if rec := e.send(t, http.MethodPost, "/api/test/protected", nil, withCookie(c)); rec.Code != http.StatusOK {
		t.Errorf("same-origin POST = %d", rec.Code)
	}
	if rec := e.send(t, http.MethodPost, "/api/test/protected", nil, withCookie(c), withHeader("Sec-Fetch-Site", ""), withHeader("Origin", "")); rec.Code != http.StatusOK {
		t.Errorf("non-browser POST = %d", rec.Code)
	}
	// Safe methods are not blocked, and do not change state.
	if rec := e.send(t, http.MethodGet, "/api/test/protected", nil, append([]reqOpt{withCookie(c)}, crossSite...)...); rec.Code != http.StatusOK {
		t.Errorf("cross-site GET = %d", rec.Code)
	}
}

// --- request validation ---

func TestAuthRequestValidation(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	login := "/api/auth/login"
	expectError(t, e.send(t, http.MethodPost, login, nil, withRawBody(`{"username":"owner","password":"x"}`), withContentType("text/plain")),
		http.StatusUnsupportedMediaType, "unsupported_media_type")
	expectError(t, e.send(t, http.MethodPost, login, nil, withRawBody(`{"username":"owner"`), withContentType("application/json")),
		http.StatusBadRequest, "invalid_request")
	expectError(t, e.send(t, http.MethodPost, login, nil, withRawBody(`{"username":"owner","password":"x","admin":true}`), withContentType("application/json")),
		http.StatusBadRequest, "invalid_request")
	expectError(t, e.send(t, http.MethodPost, login, nil, withRawBody(`{"username":"a","password":"b"}{"x":1}`), withContentType("application/json")),
		http.StatusBadRequest, "invalid_request")
	big := `{"username":"owner","password":"` + strings.Repeat("x", maxAuthBodyBytes) + `"}`
	expectError(t, e.send(t, http.MethodPost, login, nil, withRawBody(big), withContentType("application/json")),
		http.StatusRequestEntityTooLarge, "body_too_large")
	// Content type with parameters is accepted.
	rec := e.send(t, http.MethodPost, login, nil, withRawBody(`{"username":"owner","password":"`+ownerPass+`"}`), withContentType("application/json; charset=utf-8"))
	if rec.Code != http.StatusOK {
		t.Errorf("login with charset parameter = %d %s", rec.Code, rec.Body)
	}
	expectError(t, e.send(t, http.MethodGet, login, nil), http.StatusMethodNotAllowed, "method_not_allowed")
}

func TestAuthEndpointsKeepHostCheckAndHeaders(t *testing.T) {
	e := newTestEnv(t)
	rec := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": ownerPass},
		func(r *http.Request) { r.Host = "evil.example" })
	expectError(t, rec, http.StatusForbidden, "forbidden_host")
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

// --- rate limiting ---

func TestLoginRateLimited(t *testing.T) {
	e := newTestEnvWith(t, envConfig{limiter: auth.NewLimiter()})
	// Setup consumes one attempt from the same budget.
	e.doSetup(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 10; i++ {
		last = e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": "wrong password here"})
		if last.Code == http.StatusTooManyRequests {
			if i < 4 {
				t.Fatalf("rate limited after only %d failed attempts", i)
			}
			break
		}
	}
	expectError(t, last, http.StatusTooManyRequests, "rate_limited")
	if ra := last.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}
	// While limited, even the correct password is refused without being checked.
	expectError(t, e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": ownerPass}),
		http.StatusTooManyRequests, "rate_limited")
}

// --- secrets never leak ---

func TestNoSecretsInLogsOrResponses(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	c2 := e.doLogin(t, withCookie(c))
	e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": "wrong password here"})
	rec := e.send(t, http.MethodPost, "/api/auth/change-password",
		map[string]string{"current_password": ownerPass, "new_password": "brand new long password"}, withCookie(c2))
	logs := e.logs.String()
	for _, secret := range []string{ownerPass, "wrong password here", "brand new long password", c.Value, c2.Value, sessionCookie(t, rec).Value, "argon2id"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain secret %q", secret)
		}
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func ioNopCloser(s string) nopCloser { return nopCloser{strings.NewReader(s)} }
