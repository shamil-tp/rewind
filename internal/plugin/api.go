// SPDX-License-Identifier: Apache-2.0

// Package plugin is Rewind's plugin system: compiled-in, administrator-trusted
// extensions that are discovered through manifests, enabled explicitly, and
// given a small, documented set of extension points.
//
// Plugins are Go packages compiled into the Rewind binary (see Register).
// Nothing is loaded at run time: a manifest on disk only describes and
// authorizes a plugin that is already part of the binary. Plugin code runs in
// the Rewind process with all of its privileges; the interfaces here are a
// contract, not a sandbox. See docs/plugins.md.
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

// APIVersion is the plugin API this version of Rewind provides. A manifest's
// rewind_api must equal it. It changes only when the interfaces in this file
// change incompatibly.
const APIVersion = 1

// Capability names, as listed in manifests.
const (
	// CapLibraryScanEvents receives a ScanEvent after every library scan.
	CapLibraryScanEvents = "events.library_scan"
)

var knownCapabilities = map[string]bool{CapLibraryScanEvents: true}

// Capabilities lists the capability names this version supports, sorted.
func Capabilities() []string {
	out := make([]string, 0, len(knownCapabilities))
	for c := range knownCapabilities {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Plugin is implemented by a compiled-in plugin.
type Plugin interface {
	// ID and Version must equal the installed manifest's id and version.
	ID() string
	Version() string
	// Init is called once at startup, in plugin-ID order, with a deadline
	// (InitTimeout). It may register handlers on host. If it returns an error,
	// panics or misses the deadline, nothing it registered is used and Close
	// is not called. Init must not start goroutines that outlive Close.
	Init(ctx context.Context, host *Host) error
	// Close is called once at shutdown for plugins whose Init succeeded, in
	// reverse order, with a deadline (CloseTimeout). Handlers are no longer
	// called once Close starts.
	Close(ctx context.Context) error
}

// ScanEvent describes a finished library scan. It carries IDs and counts
// only: no paths, file names or error text.
type ScanEvent struct {
	RootID         int64
	ScanID         int64
	Status         string // completed, partial, failed or interrupted
	StartedAt      time.Time
	FinishedAt     time.Time
	FilesFound     int
	FilesFailed    int
	TracksAdded    int
	TracksUpdated  int
	TracksRenamed  int
	TracksMissing  int
	TracksRestored int
}

// ScanHandler handles a ScanEvent. It runs on the plugin's own goroutine, one
// event at a time, with a deadline (HandlerTimeout). Errors are logged.
type ScanHandler func(ctx context.Context, e ScanEvent) error

// ErrNotDeclared is returned when a plugin registers a capability its
// manifest does not list.
var ErrNotDeclared = errors.New("plugin: capability not declared in the manifest")

// ErrSealed is returned when a plugin registers anything after Init returned.
var ErrSealed = errors.New("plugin: registration is only allowed during Init")

// Host is what a plugin receives during Init: a logger and the extension
// points its manifest declares. It deliberately offers no database,
// filesystem, HTTP or configuration access.
type Host struct {
	logger   *slog.Logger
	declared map[string]bool

	mu     sync.Mutex
	sealed bool
	scan   []ScanHandler
}

// Logger returns a logger whose records carry the plugin's ID.
func (h *Host) Logger() *slog.Logger { return h.logger }

// OnLibraryScan registers a handler for finished library scans. It requires
// the events.library_scan capability.
func (h *Host) OnLibraryScan(fn ScanHandler) error {
	if fn == nil {
		return errors.New("plugin: nil handler")
	}
	if !h.declared[CapLibraryScanEvents] {
		return fmt.Errorf("%w: %s", ErrNotDeclared, CapLibraryScanEvents)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sealed {
		return ErrSealed
	}
	h.scan = append(h.scan, fn)
	return nil
}

// seal ends registration and returns what was registered.
func (h *Host) seal() []ScanHandler {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sealed = true
	return h.scan
}

// --- Compile-time registration ---

// Registry holds the plugins compiled into the binary.
type Registry struct {
	mu      sync.Mutex
	plugins map[string]Plugin
}

// NewRegistry returns an empty registry (tests use their own).
func NewRegistry() *Registry { return &Registry{plugins: map[string]Plugin{}} }

// Add registers p. It rejects invalid and duplicate IDs.
func (r *Registry) Add(p Plugin) error {
	if p == nil {
		return errors.New("plugin: nil plugin")
	}
	id := p.ID()
	if !ValidID(id) {
		return fmt.Errorf("plugin: invalid plugin ID %q", id)
	}
	if !versionPattern.MatchString(p.Version()) {
		return fmt.Errorf("plugin %s: invalid version %q", id, p.Version())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.plugins[id]; dup {
		return fmt.Errorf("plugin: %s is registered twice", id)
	}
	r.plugins[id] = p
	return nil
}

func (r *Registry) get(id string) Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plugins[id]
}

var builtin = NewRegistry()

// Builtin is the registry of plugins compiled into this binary.
func Builtin() *Registry { return builtin }

// Register adds a compiled-in plugin. Plugin packages call it from init();
// they are included in the binary by a blank import in
// cmd/rewind/plugins.go. A duplicate or invalid ID is a build mistake, so
// Register panics, stopping Rewind before it serves anything.
func Register(p Plugin) {
	if err := builtin.Add(p); err != nil {
		panic(err)
	}
}
