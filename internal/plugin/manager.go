// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Default deadlines. Plugins are expected to honour ctx; Go cannot stop a
// goroutine that ignores it, so a deadline marks the plugin as failed (Init)
// or moves on (Close) rather than blocking Rewind.
const (
	InitTimeout    = 10 * time.Second
	HandlerTimeout = 10 * time.Second
	CloseTimeout   = 5 * time.Second
	// queueSize is how many events wait for a plugin before new ones are
	// dropped (and logged).
	queueSize = 16
)

// State is a plugin's status.
type State string

const (
	StateRunning      State = "running"      // enabled and initialized
	StateDisabled     State = "disabled"     // installed, not listed in REWIND_PLUGINS
	StateFailed       State = "failed"       // enabled, but initialization failed
	StateInvalid      State = "invalid"      // the manifest or directory entry was rejected
	StateIncompatible State = "incompatible" // written for another plugin API version
	StateUnavailable  State = "unavailable"  // enabled, but not compiled into this binary
	StateMissing      State = "missing"      // listed in REWIND_PLUGINS, no manifest installed
)

// Status is the safe, public view of one plugin. Every Error is written by
// Rewind; a plugin's own error text only goes to the server log.
type Status struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	Author       string   `json:"author,omitempty"`
	Homepage     string   `json:"homepage,omitempty"`
	Capabilities []string `json:"capabilities"`
	State        State    `json:"state"`
	Error        string   `json:"error,omitempty"`
}

// Options configures a Manager.
type Options struct {
	// Dir is the plugin directory; empty turns the plugin system off.
	Dir string
	// Enabled lists the IDs the administrator trusts and enables.
	Enabled []string
	// Registry holds the compiled-in plugins (normally Builtin()).
	Registry *Registry
	Logger   *slog.Logger
	// Deadlines; zero values use the defaults above (tests shorten them).
	InitTimeout, HandlerTimeout, CloseTimeout time.Duration
}

// Manager discovers, starts, feeds and stops plugins.
type Manager struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	started  bool
	closed   bool
	statuses []Status
	active   []*active // initialized plugins, in initialization order

	ctx    context.Context // cancelled by Close; parent of handler contexts
	cancel context.CancelFunc
	wg     sync.WaitGroup // event workers
}

type active struct {
	id    string
	p     Plugin
	log   *slog.Logger
	scan  []ScanHandler
	queue chan ScanEvent // nil when the plugin registered no handlers
}

// ErrStarted is returned by a second Start: plugins are never initialized twice.
var ErrStarted = errors.New("plugin: manager already started")

// New returns a Manager. Nothing is read or run until Start.
func New(opts Options) *Manager {
	if opts.Registry == nil {
		opts.Registry = NewRegistry()
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.InitTimeout <= 0 {
		opts.InitTimeout = InitTimeout
	}
	if opts.HandlerTimeout <= 0 {
		opts.HandlerTimeout = HandlerTimeout
	}
	if opts.CloseTimeout <= 0 {
		opts.CloseTimeout = CloseTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{opts: opts, log: opts.Logger, ctx: ctx, cancel: cancel}
}

// Configured reports whether a plugin directory is set.
func (m *Manager) Configured() bool { return m.opts.Dir != "" }

// Start discovers manifests and initializes the enabled plugins, one at a
// time in ID order. Problems with individual plugins are recorded in their
// status and logged; they never stop Rewind. Start runs once.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return ErrStarted
	}
	m.started = true
	m.mu.Unlock()

	if !m.Configured() {
		if len(m.opts.Enabled) > 0 {
			m.log.Warn("REWIND_PLUGINS is set but REWIND_PLUGIN_DIR is not; no plugins are started")
		}
		return nil
	}
	found, err := Discover(m.opts.Dir)
	switch {
	case errors.Is(err, ErrNoDirectory):
		m.log.Info("plugin directory does not exist; no plugins are installed", "dir", m.opts.Dir)
	case err != nil:
		m.log.Error("plugins not started", "err", err)
	}

	enabled := map[string]bool{}
	for _, id := range m.opts.Enabled {
		enabled[id] = true
	}
	var statuses []Status
	var started []*active
	seen := map[string]bool{}
	for _, f := range found {
		st, a := m.prepare(ctx, f, enabled)
		statuses = append(statuses, st)
		// An installed entry counts even if its manifest is invalid, so it is
		// not reported a second time as missing.
		seen[f.Dir] = true
		if a != nil {
			started = append(started, a)
		}
	}
	for _, id := range m.opts.Enabled {
		if !seen[id] {
			m.log.Warn("enabled plugin has no manifest in the plugin directory", "plugin", id)
			statuses = append(statuses, Status{ID: id, Capabilities: []string{}, State: StateMissing,
				Error: "listed in REWIND_PLUGINS, but no manifest is installed"})
		}
	}
	sort.SliceStable(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })

	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses = statuses
	for _, a := range started {
		if len(a.scan) > 0 {
			a.queue = make(chan ScanEvent, queueSize)
			m.wg.Add(1)
			go m.work(a)
		}
	}
	m.active = started
	m.log.Info("plugins ready", "running", len(started), "installed", len(found))
	return nil
}

// prepare decides one entry's state and initializes it if it is enabled,
// compatible and compiled in.
func (m *Manager) prepare(ctx context.Context, f Found, enabled map[string]bool) (Status, *active) {
	if f.Err != nil {
		m.log.Warn("invalid plugin entry", "entry", f.Dir, "reason", f.Err.Error())
		return Status{ID: f.Dir, Capabilities: []string{}, State: StateInvalid, Error: f.Err.Error()}, nil
	}
	man := f.Manifest
	st := Status{
		ID: man.ID, Name: man.Name, Version: man.Version, Description: man.Description,
		Author: man.Author, Homepage: man.Homepage, Capabilities: append([]string{}, man.Capabilities...),
	}
	if man.RewindAPI != APIVersion {
		st.State = StateIncompatible
		st.Error = fmt.Sprintf("written for plugin API %d; this version of Rewind provides API %d", man.RewindAPI, APIVersion)
		m.log.Warn("incompatible plugin", "plugin", man.ID, "rewind_api", man.RewindAPI, "provided", APIVersion)
		return st, nil
	}
	if !enabled[man.ID] {
		st.State = StateDisabled
		return st, nil
	}
	p := m.opts.Registry.get(man.ID)
	if p == nil {
		st.State = StateUnavailable
		st.Error = "not included in this build of Rewind"
		m.log.Warn("enabled plugin is not compiled into this binary", "plugin", man.ID)
		return st, nil
	}
	if p.Version() != man.Version {
		st.State = StateFailed
		st.Error = fmt.Sprintf("the installed manifest is for version %s, but this build includes version %s", man.Version, p.Version())
		m.log.Warn("plugin version mismatch", "plugin", man.ID, "manifest", man.Version, "compiled", p.Version())
		return st, nil
	}

	log := m.log.With("plugin", man.ID)
	declared := map[string]bool{}
	for _, c := range man.Capabilities {
		declared[c] = true
	}
	host := &Host{logger: log, declared: declared}
	handlers, err := m.initOne(ctx, p, host)
	if err != nil {
		st.State = StateFailed
		st.Error = "initialization failed; see the server log"
		m.log.Warn("plugin initialization failed; it stays off", "plugin", man.ID, "err", err)
		return st, nil
	}
	st.State = StateRunning
	m.log.Info("plugin started", "plugin", man.ID, "version", man.Version, "scan_handlers", len(handlers))
	return st, &active{id: man.ID, p: p, log: log, scan: handlers}
}

// initOne runs Init with a deadline and panic recovery. Whatever happens,
// the host is sealed before returning, so a late or failed Init cannot
// register anything; on failure its registrations are discarded.
func (m *Manager) initOne(ctx context.Context, p Plugin, host *Host) ([]ScanHandler, error) {
	ictx, cancel := context.WithTimeout(ctx, m.opts.InitTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- p.Init(ictx, host)
	}()
	select {
	case err := <-done:
		handlers := host.seal()
		if err != nil {
			return nil, err
		}
		return handlers, nil
	case <-ictx.Done():
		host.seal()
		return nil, fmt.Errorf("plugin Init did not return within %s (%w)", m.opts.InitTimeout, ictx.Err())
	}
}

// PublishScan hands a finished scan to every plugin that registered a scan
// handler. It never blocks: each plugin has its own queue, and an event for
// a plugin whose queue is full is dropped and logged.
func (m *Manager) PublishScan(e ScanEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	for _, a := range m.active {
		if a.queue == nil {
			continue
		}
		select {
		case a.queue <- e:
		default:
			a.log.Warn("plugin is not keeping up; scan event dropped", "scan_id", e.ScanID)
		}
	}
}

// work delivers events to one plugin, one at a time, until Close.
func (m *Manager) work(a *active) {
	defer m.wg.Done()
	for e := range a.queue {
		if m.ctx.Err() != nil {
			return
		}
		for _, h := range a.scan {
			m.handle(a, h, e)
		}
	}
}

func (m *Manager) handle(a *active, h ScanHandler, e ScanEvent) {
	ctx, cancel := context.WithTimeout(m.ctx, m.opts.HandlerTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("plugin scan handler panicked", "panic", fmt.Sprint(r))
		}
	}()
	if err := h(ctx, e); err != nil {
		a.log.Warn("plugin scan handler failed", "err", err)
	}
}

// Close stops event delivery, waits for running handlers until ctx ends, and
// calls Close on each started plugin in reverse order, each with its own
// deadline. It is safe to call more than once.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	started := m.active
	for _, a := range started {
		if a.queue != nil {
			close(a.queue)
		}
	}
	m.mu.Unlock()
	m.cancel()

	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		m.log.Warn("plugin event handlers did not stop in time")
	}

	for i := len(started) - 1; i >= 0; i-- {
		a := started[i]
		if err := m.closeOne(ctx, a); err != nil {
			a.log.Warn("plugin did not close cleanly", "err", err)
		}
	}
	if len(started) > 0 {
		m.log.Info("plugins stopped", "count", len(started))
	}
}

func (m *Manager) closeOne(ctx context.Context, a *active) error {
	cctx, cancel := context.WithTimeout(ctx, m.opts.CloseTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- a.p.Close(cctx)
	}()
	select {
	case err := <-done:
		return err
	case <-cctx.Done():
		return fmt.Errorf("plugin Close did not return in time (%w)", cctx.Err())
	}
}

// Status returns a copy of every plugin's status, sorted by ID.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, len(m.statuses))
	for i, s := range m.statuses {
		s.Capabilities = append([]string{}, s.Capabilities...)
		out[i] = s
	}
	return out
}
