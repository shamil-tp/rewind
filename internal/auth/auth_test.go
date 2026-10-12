// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/db"
	"golang.org/x/crypto/argon2"
)

const (
	testUser = "owner"
	testPass = "correct horse battery staple"
)

var ctx = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	svc   *Service
	db    *sql.DB
	path  string
	clock *clock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), db.FileName)
	f := &fixture{path: path, clock: &clock{t: time.Unix(1_800_000_000, 0)}}
	f.open(t)
	return f
}

// open (re)opens the database and service, simulating an application start.
func (f *fixture) open(t *testing.T) {
	t.Helper()
	conn, err := db.Open(ctx, f.path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	svc, err := NewService(ctx, Options{
		DB: conn, Lifetime: 30 * 24 * time.Hour, IdleTimeout: 7 * 24 * time.Hour,
		Logger: slog.New(slog.DiscardHandler), Now: f.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.db, f.svc = conn, svc
}

func (f *fixture) setup(t *testing.T) User {
	t.Helper()
	u, err := f.svc.Setup(ctx, testUser, testPass)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	return u
}

func (f *fixture) login(t *testing.T) Session {
	t.Helper()
	_, s, err := f.svc.Login(ctx, testUser, testPass, "")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return s
}

func (f *fixture) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT count(*) FROM sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- setup ---

func TestSetup(t *testing.T) {
	f := newFixture(t)
	if req, err := f.svc.SetupRequired(ctx); err != nil || !req {
		t.Fatalf("SetupRequired() = %v, %v; want true", req, err)
	}
	u := f.setup(t)
	if u.ID == 0 || u.Username != testUser {
		t.Errorf("Setup() user = %+v", u)
	}
	if req, _ := f.svc.SetupRequired(ctx); req {
		t.Error("SetupRequired() = true after setup")
	}
}

func TestSetupRejectedAfterCompletion(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	if _, err := f.svc.Setup(ctx, "intruder", "another long password"); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second Setup() error = %v, want ErrSetupComplete", err)
	}
	var n int
	f.db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestSetupConcurrentCreatesOneAccount(t *testing.T) {
	f := newFixture(t)
	const attempts = 8
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.svc.Setup(ctx, "user"+string(rune('a'+i)), "a sufficiently long password")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	var ok, complete int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrSetupComplete):
			complete++
		default:
			t.Errorf("unexpected Setup() error: %v", err)
		}
	}
	if ok != 1 || complete != attempts-1 {
		t.Errorf("successes = %d, ErrSetupComplete = %d; want 1 and %d", ok, complete, attempts-1)
	}
	var n int
	f.db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestSetupValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct{ user, pass string }{
		{"", testPass},
		{"has space", testPass},
		{"ünïcode", testPass},
		{strings.Repeat("a", 65), testPass},
		{testUser, "short"},
		{testUser, "elevenchars"},
		{"ownerowner12", "OWNEROWNER12"}, // password equals username
		{testUser, strings.Repeat("x", 257)},
		{testUser, "bad utf8 \xff\xfe padding"},
	}
	for _, c := range cases {
		_, err := f.svc.Setup(ctx, c.user, c.pass)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("Setup(%q, len %d) error = %v, want ValidationError", c.user, len(c.pass), err)
		}
	}
	if req, _ := f.svc.SetupRequired(ctx); !req {
		t.Error("an invalid Setup() created an account")
	}
	// Exactly 12 characters, including multi-byte ones, is accepted.
	if _, err := f.svc.Setup(ctx, testUser, "ééééééééééé1"); err != nil {
		t.Errorf("12-character password rejected: %v", err)
	}
}

// --- passwords ---

func TestPasswordNotStoredInPlaintext(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	var hash string
	f.db.QueryRow("SELECT password_hash FROM users").Scan(&hash)
	if strings.Contains(hash, testPass) {
		t.Fatal("password_hash contains the plaintext password")
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("password_hash = %q, want Argon2id PHC string with current parameters", hash)
	}
	f.db.Close()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(testPass)) {
		t.Error("database file contains the plaintext password")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := hashPassword(ctx, testPass)
	b, _ := hashPassword(ctx, testPass)
	if a == b {
		t.Error("two hashes of the same password are identical; salt is not random")
	}
	for _, h := range []string{a, b} {
		if ok, rehash, err := verifyPassword(ctx, testPass, h); !ok || rehash || err != nil {
			t.Errorf("verifyPassword(correct) = %v, %v, %v", ok, rehash, err)
		}
		if ok, _, _ := verifyPassword(ctx, testPass+"x", h); ok {
			t.Error("verifyPassword(wrong) = true")
		}
	}
}

func TestParseHashRejectsMalformedAndAbusive(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5", // memory bomb
		"$argon2id$v=19$m=19456,t=1000,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if _, _, _, err := parseHash(h); err == nil {
			t.Errorf("parseHash(%q) error = nil", h)
		}
	}
}

func TestLoginUpgradesOutdatedHash(t *testing.T) {
	f := newFixture(t)
	u := f.setup(t)
	// Simulate a hash created with weaker, older parameters.
	old := "$argon2id$v=19$m=8192,t=1,p=1$"
	salt := "c2FsdHNhbHRzYWx0c2FsdA"
	key := deriveForTest(testPass, salt, 1, 8192)
	if _, err := f.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", old+salt+"$"+key, u.ID); err != nil {
		t.Fatal(err)
	}
	f.login(t)
	var hash string
	f.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", u.ID).Scan(&hash)
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash not upgraded on login: %q", hash)
	}
}

// --- login and sessions ---

func TestLoginSuccessCreatesPersistentSession(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	u, s, err := f.svc.Login(ctx, "OWNER", testPass, "") // usernames are case-insensitive
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if u.Username != testUser {
		t.Errorf("Login() username = %q, want stored form %q", u.Username, testUser)
	}
	if !wellFormedToken(s.Token) {
		t.Errorf("token %q is not well formed", s.Token)
	}
	if want := f.clock.Now().Add(30 * 24 * time.Hour); !s.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", s.ExpiresAt, want)
	}
	if f.sessionCount(t) != 1 {
		t.Errorf("sessions = %d, want 1", f.sessionCount(t))
	}
	if got, err := f.svc.Authenticate(ctx, s.Token); err != nil || got.ID != u.ID {
		t.Errorf("Authenticate() = %+v, %v", got, err)
	}
}

func TestSessionTokenNotStoredInPlaintext(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	var stored []byte
	f.db.QueryRow("SELECT token_hash FROM sessions").Scan(&stored)
	if !bytes.Equal(stored, hashToken(s.Token)) {
		t.Error("stored token_hash is not SHA-256 of the token")
	}
	f.db.Close()
	raw, _ := os.ReadFile(f.path)
	if bytes.Contains(raw, []byte(s.Token)) {
		t.Error("database file contains the raw session token")
	}
}

func TestLoginWrongPasswordAndUnknownUser(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	for _, c := range []struct{ user, pass string }{
		{testUser, "wrong password entirely"},
		{"nobody", testPass},
		{"", ""},
	} {
		_, _, err := f.svc.Login(ctx, c.user, c.pass, "")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q) error = %v, want ErrInvalidCredentials", c.user, err)
		}
	}
	if f.sessionCount(t) != 0 {
		t.Error("failed login created a session")
	}
}

func TestLoginRotatesPreviousSession(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	first := f.login(t)
	_, second, err := f.svc.Login(ctx, testUser, testPass, first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == first.Token {
		t.Fatal("login reused the previous token")
	}
	if _, err := f.svc.Authenticate(ctx, first.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("previous session still valid after login: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, second.Token); err != nil {
		t.Errorf("new session invalid: %v", err)
	}
	if f.sessionCount(t) != 1 {
		t.Errorf("sessions = %d, want 1", f.sessionCount(t))
	}
}

func TestSessionPersistsAcrossRestart(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	f.db.Close()
	f.open(t) // new DB handle and new Service, same file
	if u, err := f.svc.Authenticate(ctx, s.Token); err != nil || u.Username != testUser {
		t.Fatalf("Authenticate() after restart = %+v, %v", u, err)
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	// Stay active (within the idle timeout) right up to the absolute expiry.
	for i := 0; i < 4; i++ {
		f.clock.Advance(7*24*time.Hour - time.Hour)
		if _, err := f.svc.Authenticate(ctx, s.Token); err != nil {
			t.Fatalf("active session rejected early (step %d): %v", i, err)
		}
	}
	f.clock.Advance(5 * 24 * time.Hour) // now past 30 days
	if _, err := f.svc.Authenticate(ctx, s.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired session accepted: %v", err)
	}
	if f.sessionCount(t) != 0 {
		t.Error("expired session row was not deleted")
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	f.clock.Advance(7*24*time.Hour + time.Second)
	if _, err := f.svc.Authenticate(ctx, s.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session accepted: %v", err)
	}
}

func TestLastSeenUpdatedWithGranularity(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	start := f.clock.Now().Unix()
	f.clock.Advance(30 * time.Second)
	f.svc.Authenticate(ctx, s.Token)
	var seen int64
	f.db.QueryRow("SELECT last_seen_at FROM sessions").Scan(&seen)
	if seen != start {
		t.Errorf("last_seen_at written within granularity: %d", seen)
	}
	f.clock.Advance(time.Minute)
	f.svc.Authenticate(ctx, s.Token)
	f.db.QueryRow("SELECT last_seen_at FROM sessions").Scan(&seen)
	if seen != f.clock.Now().Unix() {
		t.Errorf("last_seen_at = %d, want %d", seen, f.clock.Now().Unix())
	}
}

func TestLoginPurgesExpiredSessions(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	f.login(t)
	f.login(t)
	f.clock.Advance(31 * 24 * time.Hour)
	f.login(t)
	if n := f.sessionCount(t); n != 1 {
		t.Errorf("sessions = %d after login past expiry, want 1", n)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	a := f.login(t)
	b := f.login(t) // another device
	if err := f.svc.Logout(ctx, a.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, a.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("logged-out session still valid: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, b.Token); err != nil {
		t.Errorf("other device's session revoked by logout: %v", err)
	}
	if err := f.svc.Logout(ctx, "not-a-token"); err != nil {
		t.Errorf("Logout(garbage) error = %v", err)
	}
}

func TestAuthenticateRejectsInvalidTokens(t *testing.T) {
	f := newFixture(t)
	f.setup(t)
	s := f.login(t)
	for _, tok := range []string{
		"",
		"short",
		strings.Repeat("A", tokenLen), // well formed but unknown
		s.Token[:tokenLen-1] + "!",    // invalid base64url character
		s.Token + "A",                 // too long
		strings.ToUpper(s.Token[:10]) + s.Token[10:], // tampered
	} {
		if tok == s.Token {
			continue
		}
		if _, err := f.svc.Authenticate(ctx, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("Authenticate(%q) error = %v, want ErrNoSession", tok, err)
		}
	}
}

func TestChangePasswordRevokesAllSessions(t *testing.T) {
	f := newFixture(t)
	u := f.setup(t)
	a := f.login(t)
	b := f.login(t)
	const next = "an entirely new long password"

	if _, err := f.svc.ChangePassword(ctx, u.ID, "wrong current password", next); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ChangePassword(wrong current) error = %v", err)
	}
	var ve *ValidationError
	if _, err := f.svc.ChangePassword(ctx, u.ID, testPass, "short"); !errors.As(err, &ve) {
		t.Fatalf("ChangePassword(short) error = %v, want ValidationError", err)
	}
	if _, err := f.svc.ChangePassword(ctx, u.ID, testPass, testPass); !errors.As(err, &ve) {
		t.Fatalf("ChangePassword(same) error = %v, want ValidationError", err)
	}
	if f.sessionCount(t) != 2 {
		t.Fatal("failed password change revoked sessions")
	}

	fresh, err := f.svc.ChangePassword(ctx, u.ID, testPass, next)
	if err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	for _, old := range []Session{a, b} {
		if _, err := f.svc.Authenticate(ctx, old.Token); !errors.Is(err, ErrNoSession) {
			t.Errorf("old session still valid after password change: %v", err)
		}
	}
	if _, err := f.svc.Authenticate(ctx, fresh.Token); err != nil {
		t.Errorf("new session after password change invalid: %v", err)
	}
	if _, _, err := f.svc.Login(ctx, testUser, testPass, ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("old password still works")
	}
	if _, _, err := f.svc.Login(ctx, testUser, next, ""); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

// --- setup code ---

func TestSetupCode(t *testing.T) {
	dir := t.TempDir()
	// A stale file with loose permissions is replaced.
	os.WriteFile(filepath.Join(dir, SetupCodeFile), []byte("OLD"), 0o644)

	c, err := NewSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	display := strings.TrimSpace(string(raw))
	if len(display) != 19 || strings.Count(display, "-") != 3 {
		t.Fatalf("setup code file content = %q, want XXXX-XXXX-XXXX-XXXX", display)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(c.Path()); info.Mode().Perm() != 0o600 {
			t.Errorf("setup code file mode = %v, want 0600", info.Mode().Perm())
		}
	}
	for _, in := range []string{display, strings.ToLower(display), strings.ReplaceAll(display, "-", ""), " " + strings.ReplaceAll(display, "-", " ") + " "} {
		if !c.Matches(in) {
			t.Errorf("Matches(%q) = false", in)
		}
	}
	for _, in := range []string{"", "AAAA-AAAA-AAAA-AAAA", display[:18], display + "A"} {
		if c.Matches(in) {
			t.Errorf("Matches(%q) = true", in)
		}
	}

	if err := c.Consume(); err != nil {
		t.Fatal(err)
	}
	if c.Matches(display) {
		t.Error("consumed code still matches")
	}
	if _, err := os.Stat(c.Path()); !os.IsNotExist(err) {
		t.Error("setup code file not removed after Consume")
	}

	c2, _ := NewSetupCode(dir)
	raw2, _ := os.ReadFile(c2.Path())
	if strings.TrimSpace(string(raw2)) == display {
		t.Error("a new setup code repeated the previous one")
	}
}

// --- rate limiter ---

func TestLimiterPerKeyAndRefill(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newLimiter(rate{burst: 3, interval: 10 * time.Second}, rate{burst: 100, interval: time.Second}, 10, c.Now)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("attempt %d denied within burst", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > 10*time.Second {
		t.Fatalf("4th attempt = %v, wait %v; want denied with wait in (0,10s]", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("other key denied by key a's limit")
	}
	c.Advance(10 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("attempt denied after refill")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("only one token should have refilled")
	}
}

func TestLimiterGlobalCap(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newLimiter(rate{burst: 5, interval: time.Second}, rate{burst: 4, interval: time.Minute}, 100, c.Now)
	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow(string(rune('a' + i))); ok {
			allowed++
		}
	}
	if allowed != 4 {
		t.Errorf("allowed %d attempts across keys, want global cap 4", allowed)
	}
}

func TestLimiterBoundedMemory(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newLimiter(rate{burst: 2, interval: time.Second}, rate{burst: 1e9, interval: time.Nanosecond}, 5, c.Now)
	for i := 0; i < 1000; i++ {
		l.Allow(strings.Repeat("k", i%50) + string(rune(i)))
	}
	if len(l.keys) > 5 {
		t.Errorf("limiter tracks %d keys, want at most 5", len(l.keys))
	}
}

func TestLimiterNeverLocksOutPermanently(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newLimiter(defaultPerClient, defaultGlobal, 10, c.Now)
	for i := 0; i < 1000; i++ { // a sustained attack
		l.Allow("127.0.0.1")
	}
	c.Advance(defaultPerClient.interval)
	if ok, _ := l.Allow("127.0.0.1"); !ok {
		t.Error("owner still locked out one interval after the attack stopped")
	}
}

func deriveForTest(password, saltB64 string, t, m uint32) string {
	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		panic(err)
	}
	return base64.RawStdEncoding.EncodeToString(argon2.IDKey([]byte(password), salt, t, m, 1, 32))
}
