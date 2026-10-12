// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fake is a test plugin whose behaviour each test chooses.
type fake struct {
	id, version string
	init        func(ctx context.Context, h *Host) error
	close       func(ctx context.Context) error

	inits, closes atomic.Int32
	mu            sync.Mutex
	events        []ScanEvent
}

func (f *fake) ID() string      { return f.id }
func (f *fake) Version() string { return f.version }
func (f *fake) Init(ctx context.Context, h *Host) error {
	f.inits.Add(1)
	if f.init != nil {
		return f.init(ctx, h)
	}
	return h.OnLibraryScan(f.record)
}
func (f *fake) Close(ctx context.Context) error {
	f.closes.Add(1)
	if f.close != nil {
		return f.close(ctx)
	}
	return nil
}
func (f *fake) record(_ context.Context, e ScanEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}
func (f *fake) received() []ScanEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ScanEvent{}, f.events...)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	dir  string
	reg  *Registry
	logs *syncBuf
}

func newEnv(t *testing.T) *env {
	return &env{dir: t.TempDir(), reg: NewRegistry(), logs: &syncBuf{}}
}

// install writes a manifest and registers the compiled-in plugin.
func (e *env) install(t *testing.T, p *fake, caps ...string) *fake {
	t.Helper()
	if caps == nil {
		caps = []string{CapLibraryScanEvents}
	}
	writeManifest(t, e.dir, p.id, manifestFor(p.id, p.version, caps...))
	if err := e.reg.Add(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) manager(enabled ...string) *Manager {
	return New(Options{
		Dir: e.dir, Enabled: enabled, Registry: e.reg,
		Logger:      slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		InitTimeout: 200 * time.Millisecond, HandlerTimeout: 200 * time.Millisecond, CloseTimeout: 200 * time.Millisecond,
	})
}

func states(m *Manager) map[string]Status {
	out := map[string]Status{}
	for _, s := range m.Status() {
		out[s.ID] = s
	}
	return out
}

func closeNow(m *Manager) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Close(ctx)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met in time")
}

func TestNoPluginDirectoryConfigured(t *testing.T) {
	m := New(Options{})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Configured() || len(m.Status()) != 0 {
		t.Errorf("configured=%v status=%v", m.Configured(), m.Status())
	}
	m.PublishScan(ScanEvent{}) // no-op
	closeNow(m)
}

func TestMissingPluginDirectoryIsNotAnError(t *testing.T) {
	e := newEnv(t)
	m := New(Options{Dir: filepath.Join(e.dir, "absent"), Enabled: []string{"x"}, Registry: e.reg})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := states(m)["x"]; s.State != StateMissing {
		t.Errorf("enabled but absent = %+v", s)
	}
	closeNow(m)
}

func TestLifecycle(t *testing.T) {
	e := newEnv(t)
	a := e.install(t, &fake{id: "alpha", version: "1.0.0"})
	b := e.install(t, &fake{id: "beta", version: "2.0.0"})
	off := e.install(t, &fake{id: "gamma", version: "1.0.0"})
	m := e.manager("alpha", "beta")
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := states(m)
	if st["alpha"].State != StateRunning || st["beta"].State != StateRunning || st["gamma"].State != StateDisabled {
		t.Fatalf("states = %+v", st)
	}
	if off.inits.Load() != 0 {
		t.Error("a disabled plugin was initialized")
	}
	if st["alpha"].Name != "Plugin alpha" || st["alpha"].Capabilities[0] != CapLibraryScanEvents {
		t.Errorf("status metadata = %+v", st["alpha"])
	}

	m.PublishScan(ScanEvent{ScanID: 7, TracksMissing: 2})
	waitFor(t, func() bool { return len(a.received()) == 1 && len(b.received()) == 1 })
	if a.received()[0].ScanID != 7 {
		t.Errorf("event = %+v", a.received()[0])
	}

	// Starting again never initializes plugins a second time.
	if err := m.Start(context.Background()); !errors.Is(err, ErrStarted) {
		t.Errorf("second Start error = %v", err)
	}
	closeNow(m)
	closeNow(m) // idempotent
	if a.inits.Load() != 1 || a.closes.Load() != 1 || b.closes.Load() != 1 || off.closes.Load() != 0 {
		t.Errorf("inits/closes: a %d/%d, b %d, gamma %d", a.inits.Load(), a.closes.Load(), b.closes.Load(), off.closes.Load())
	}
	m.PublishScan(ScanEvent{ScanID: 8}) // after Close: ignored
	time.Sleep(20 * time.Millisecond)
	if len(a.received()) != 1 {
		t.Error("event delivered after Close")
	}
}

func TestDeterministicOrder(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var order []string
	for _, id := range []string{"zeta", "alpha", "mid"} {
		e.install(t, &fake{id: id, version: "1.0.0", init: func(context.Context, *Host) error {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return nil
		}, close: func(context.Context) error {
			mu.Lock()
			order = append(order, "close-"+id)
			mu.Unlock()
			return nil
		}})
	}
	m := e.manager("zeta", "mid", "alpha")
	m.Start(context.Background())
	closeNow(m)
	if got := strings.Join(order, ","); got != "alpha,mid,zeta,close-zeta,close-mid,close-alpha" {
		t.Errorf("order = %s", got)
	}
}

func TestInitFailuresAreContained(t *testing.T) {
	e := newEnv(t)
	good := e.install(t, &fake{id: "good", version: "1.0.0"})
	failing := e.install(t, &fake{id: "failing", version: "1.0.0", init: func(_ context.Context, h *Host) error {
		// Registers, then fails: the registration must not be used.
		h.OnLibraryScan(func(context.Context, ScanEvent) error { panic("must never run") })
		return errors.New("secret token=abc123 in /home/owner/.config")
	}})
	panicking := e.install(t, &fake{id: "panicking", version: "1.0.0", init: func(context.Context, *Host) error {
		panic("boom")
	}})
	release := make(chan struct{})
	defer close(release)
	var late atomic.Value
	slow := e.install(t, &fake{id: "slow", version: "1.0.0", init: func(ctx context.Context, h *Host) error {
		<-release // ignores ctx: misses the deadline
		late.Store(h.OnLibraryScan(func(context.Context, ScanEvent) error { panic("must never run") }))
		return nil
	}})
	m := e.manager("good", "failing", "panicking", "slow")
	start := time.Now()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("a hanging Init blocked startup")
	}
	st := states(m)
	for _, id := range []string{"failing", "panicking", "slow"} {
		if st[id].State != StateFailed || st[id].Error != "initialization failed; see the server log" {
			t.Errorf("%s = %+v", id, st[id])
		}
	}
	if st["good"].State != StateRunning {
		t.Fatalf("good = %+v", st["good"])
	}
	// The plugin's own error text is logged, never put in the status.
	if strings.Contains(st["failing"].Error, "abc123") || !strings.Contains(e.logs.String(), "abc123") {
		t.Error("plugin error text placement wrong")
	}

	m.PublishScan(ScanEvent{ScanID: 1})
	waitFor(t, func() bool { return len(good.received()) == 1 })
	closeNow(m)
	if failing.closes.Load()+panicking.closes.Load()+slow.closes.Load() != 0 {
		t.Error("Close was called on a plugin whose Init failed")
	}
	if good.closes.Load() != 1 {
		t.Error("good plugin not closed")
	}
	release <- struct{}{}
	waitFor(t, func() bool { return late.Load() != nil })
	if err, _ := late.Load().(error); !errors.Is(err, ErrSealed) {
		t.Errorf("registration after the deadline = %v, want ErrSealed", err)
	}
}

func TestStateChecksBeforeInit(t *testing.T) {
	e := newEnv(t)
	// Manifest version differs from the compiled-in code.
	mismatch := &fake{id: "mismatch", version: "2.0.0"}
	writeManifest(t, e.dir, "mismatch", manifestFor("mismatch", "1.0.0", CapLibraryScanEvents))
	e.reg.Add(mismatch)
	// Manifest for code that is not in this build.
	writeManifest(t, e.dir, "absent", manifestFor("absent", "1.0.0"))
	// Written for a future plugin API.
	future := &fake{id: "future", version: "1.0.0"}
	writeManifest(t, e.dir, "future", strings.Replace(manifestFor("future", "1.0.0"), `"rewind_api":1`, `"rewind_api":2`, 1))
	e.reg.Add(future)
	// Invalid manifest whose code is compiled in.
	invalidCode := &fake{id: "broken", version: "1.0.0"}
	writeManifest(t, e.dir, "broken", `{"schema":1,"id":"broken","command":"rm -rf /"}`)
	e.reg.Add(invalidCode)

	m := e.manager("mismatch", "absent", "future", "broken")
	m.Start(context.Background())
	defer closeNow(m)
	st := states(m)
	want := map[string]State{"mismatch": StateFailed, "absent": StateUnavailable, "future": StateIncompatible, "broken": StateInvalid}
	for id, s := range want {
		if st[id].State != s {
			t.Errorf("%s = %+v, want %s", id, st[id], s)
		}
	}
	if n := len(m.Status()); n != len(want) {
		t.Errorf("%d status entries, want one per plugin: %+v", n, m.Status())
	}
	for _, p := range []*fake{mismatch, future, invalidCode} {
		if p.inits.Load() != 0 {
			t.Errorf("%s was initialized", p.id)
		}
	}
}

func TestUndeclaredCapabilityFailsInit(t *testing.T) {
	e := newEnv(t)
	// The manifest declares no capabilities; the plugin still tries to
	// register a scan handler.
	e.install(t, &fake{id: "sneaky", version: "1.0.0"}, []string{}...)
	m := e.manager("sneaky")
	m.Start(context.Background())
	defer closeNow(m)
	if st := states(m)["sneaky"]; st.State != StateFailed {
		t.Errorf("state = %+v", st)
	}
	if !strings.Contains(e.logs.String(), "capability not declared") {
		t.Errorf("log = %s", e.logs.String())
	}
}

func TestSlowAndFailingHandlersDoNotBlock(t *testing.T) {
	e := newEnv(t)
	block := make(chan struct{})
	defer close(block)
	var slowCalls atomic.Int32
	e.install(t, &fake{id: "slow", version: "1.0.0", init: func(_ context.Context, h *Host) error {
		return h.OnLibraryScan(func(ctx context.Context, _ ScanEvent) error {
			slowCalls.Add(1)
			select { // honours the deadline
			case <-block:
			case <-ctx.Done():
			}
			return ctx.Err()
		})
	}})
	e.install(t, &fake{id: "panics", version: "1.0.0", init: func(_ context.Context, h *Host) error {
		return h.OnLibraryScan(func(context.Context, ScanEvent) error { panic("handler boom") })
	}})
	fast := e.install(t, &fake{id: "fast", version: "1.0.0"})
	m := e.manager("slow", "panics", "fast")
	m.Start(context.Background())

	start := time.Now()
	for i := 0; i < 3*queueSize; i++ {
		m.PublishScan(ScanEvent{ScanID: int64(i)}) // never blocks, even with a full queue
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("PublishScan blocked")
	}
	// Wait until every worker has taken an event: Close drops events that
	// were not delivered yet, so closing earlier would race with the slow and
	// panicking workers' first delivery.
	waitFor(t, func() bool {
		return len(fast.received()) >= queueSize && slowCalls.Load() > 0 && strings.Contains(e.logs.String(), "handler boom")
	})
	if !strings.Contains(e.logs.String(), "scan event dropped") {
		t.Errorf("drops should be logged: %s", e.logs.String())
	}
	start = time.Now()
	closeNow(m) // cancels the slow handler's context; must return promptly
	if d := time.Since(start); d > time.Second {
		t.Errorf("Close took %s with a handler waiting on its context", d)
	}
}

func TestCloseHonoursDeadlines(t *testing.T) {
	e := newEnv(t)
	hang := make(chan struct{})
	defer close(hang)
	e.install(t, &fake{id: "stuck", version: "1.0.0", close: func(context.Context) error {
		<-hang // ignores its deadline
		return nil
	}})
	ok := e.install(t, &fake{id: "fine", version: "1.0.0"})
	m := e.manager("stuck", "fine")
	m.Start(context.Background())
	start := time.Now()
	closeNow(m)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Close took %s", d)
	}
	if ok.closes.Load() != 1 {
		t.Error("a stuck plugin prevented the others from closing")
	}
	if !strings.Contains(e.logs.String(), "did not close cleanly") {
		t.Errorf("log = %s", e.logs.String())
	}
}

func TestRegistryRejectsBadRegistrations(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(&fake{id: "one", version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]Plugin{
		"duplicate":   &fake{id: "one", version: "2.0.0"},
		"invalid id":  &fake{id: "../one", version: "1.0.0"},
		"bad version": &fake{id: "two", version: "latest"},
		"nil":         nil,
	} {
		if err := r.Add(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("Register with an invalid plugin did not panic")
		}
	}()
	Register(&fake{id: "Bad ID", version: "1.0.0"})
}

func TestHostOffersNothingUndeclaredOrLate(t *testing.T) {
	h := &Host{logger: slog.New(slog.DiscardHandler), declared: map[string]bool{}}
	if err := h.OnLibraryScan(func(context.Context, ScanEvent) error { return nil }); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("undeclared = %v", err)
	}
	h.declared[CapLibraryScanEvents] = true
	if err := h.OnLibraryScan(nil); err == nil {
		t.Error("nil handler accepted")
	}
	h.seal()
	if err := h.OnLibraryScan(func(context.Context, ScanEvent) error { return nil }); !errors.Is(err, ErrSealed) {
		t.Errorf("after seal = %v", err)
	}
}

func TestCapabilitiesList(t *testing.T) {
	if got := strings.Join(Capabilities(), ","); got != CapLibraryScanEvents {
		t.Errorf("capabilities = %s", got)
	}
}
