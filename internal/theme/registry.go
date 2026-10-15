// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
)

// DefaultID is the theme used when nothing else is available: no saved
// choice, a saved theme that is gone, disabled or invalid. It is bundled and
// cannot be disabled.
const DefaultID = "rewind-dark"

// MaxEntries bounds how many entries of the theme directory are examined.
const MaxEntries = 64

//go:embed bundled/*.json
var bundledFiles embed.FS

// Source says where a theme comes from.
type Source string

const (
	// SourceBundled themes are part of the Rewind binary.
	SourceBundled Source = "bundled"
	// SourceInstalled themes were installed by the administrator in the
	// theme directory (REWIND_THEME_DIR).
	SourceInstalled Source = "installed"
)

// State is whether a theme can be used.
type State string

const (
	StateAvailable    State = "available"
	StateInvalid      State = "invalid"
	StateIncompatible State = "incompatible"
)

// Entry is one theme the registry knows about: a usable theme, or an
// installed package that was rejected and why.
type Entry struct {
	// ID is the theme ID, or for a rejected entry its directory name made
	// safe for display.
	ID     string
	Source Source
	State  State
	// Error is a summary written by Rewind, safe to show; "" when available.
	Error string
	// Theme is set only when State is StateAvailable.
	Theme *Theme
}

// Registry holds the themes known to this process. It is built once at
// startup and never changes, so it is safe for concurrent use.
type Registry struct {
	entries       []Entry
	available     map[string]*Theme
	dirConfigured bool
}

// Load builds the registry from the bundled themes and, if dir is not
// empty, the themes installed there. A bundled theme that fails validation
// is a build mistake and returns an error. Problems with installed themes
// never stop Rewind: they are logged and listed as invalid or incompatible.
func Load(dir string, logger *slog.Logger) (*Registry, error) {
	r := &Registry{available: map[string]*Theme{}, dirConfigured: dir != ""}
	bundled, err := loadBundled()
	if err != nil {
		return nil, err
	}
	for _, t := range bundled {
		r.entries = append(r.entries, Entry{ID: t.ID, Source: SourceBundled, State: StateAvailable, Theme: t})
		r.available[t.ID] = t
	}
	if _, ok := r.available[DefaultID]; !ok {
		return nil, fmt.Errorf("theme: the default theme %q is not bundled", DefaultID)
	}
	if dir == "" {
		return r, nil
	}

	found, err := Discover(dir)
	switch {
	case errors.Is(err, ErrNoDirectory):
		logger.Warn("theme directory does not exist; only bundled themes are available", "dir", dir)
		return r, nil
	case err != nil:
		logger.Error("theme directory could not be read; only bundled themes are available", "err", err)
		return r, nil
	}
	for _, e := range found {
		if e.State == StateAvailable {
			if _, dup := r.available[e.ID]; dup {
				e = Entry{ID: e.ID, Source: SourceInstalled, State: StateInvalid, Error: "a bundled theme already uses this ID"}
			} else {
				r.available[e.ID] = e.Theme
			}
		}
		if e.State == StateAvailable {
			logger.Info("theme installed", "theme", e.ID, "version", e.Theme.Version)
		} else {
			logger.Warn("theme not available", "theme", e.ID, "state", string(e.State), "reason", e.Error)
		}
		r.entries = append(r.entries, e)
	}
	return r, nil
}

// loadBundled parses the themes embedded in the binary, default first, then
// by file name.
func loadBundled() ([]*Theme, error) {
	files, err := fs.ReadDir(bundledFiles, "bundled")
	if err != nil {
		return nil, err
	}
	var out []*Theme
	for _, f := range files {
		data, err := bundledFiles.ReadFile(path.Join("bundled", f.Name()))
		if err != nil {
			return nil, err
		}
		t, err := ParseManifest(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("bundled theme %s: %w", f.Name(), err)
		}
		if t.ID+".json" != f.Name() {
			return nil, fmt.Errorf("bundled theme %s: id %q does not match the file name", f.Name(), t.ID)
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID == DefaultID && out[j].ID != DefaultID })
	return out, nil
}

// Entries returns every known theme: bundled first, then installed ones by
// directory name. The result must not be modified.
func (r *Registry) Entries() []Entry { return r.entries }

// Get returns an available theme.
func (r *Registry) Get(id string) (*Theme, bool) {
	t, ok := r.available[id]
	return t, ok
}

// Has reports whether id is an available theme.
func (r *Registry) Has(id string) bool {
	_, ok := r.available[id]
	return ok
}

// DirConfigured reports whether a theme directory is configured.
func (r *Registry) DirConfigured() bool { return r.dirConfigured }

// ErrNoDirectory means the configured theme directory does not exist.
var ErrNoDirectory = errors.New("theme directory does not exist")

// Discover reads every theme in dir: each subdirectory <id>/ holding a
// theme.json. It only reads files; nothing is executed or served.
//
// Files are opened through os.Root, so no path can resolve outside dir.
// Hidden entries and plain files are ignored. Symbolic links and Windows
// junctions (as a theme directory or a manifest) are rejected and reported,
// never followed. Other files in a theme's directory are ignored: a theme is
// its manifest alone. Results are sorted by name, and at most MaxEntries
// entries are examined.
func Discover(dir string) ([]Entry, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoDirectory
	}
	if err != nil {
		return nil, fmt.Errorf("cannot open the theme directory: %w", err)
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("cannot read the theme directory: %w", err)
	}
	var out []Entry
	examined := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.Type().IsRegular() {
			continue // hidden entries, README files and the like
		}
		if examined++; examined > MaxEntries {
			out = append(out, Entry{ID: "(more entries)", Source: SourceInstalled, State: StateInvalid,
				Error: fmt.Sprintf("the theme directory has more than %d entries; the rest were ignored", MaxEntries)})
			break
		}
		out = append(out, readEntry(root, e))
	}
	return out, nil
}

func readEntry(root *os.Root, e fs.DirEntry) Entry {
	name := e.Name()
	rejected := func(format string, args ...any) Entry {
		return Entry{ID: truncate(name, maxIDLength), Source: SourceInstalled, State: StateInvalid, Error: fmt.Sprintf(format, args...)}
	}
	switch {
	case !e.IsDir():
		return rejected("theme directories must be plain directories, not links or junctions")
	case !ValidID(name):
		return rejected("the directory name must be the theme's ID (lower-case letters, digits and hyphens)")
	}

	p := name + "/" + ManifestFile
	info, err := root.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return rejected("%s is missing", ManifestFile)
	case err != nil:
		return rejected("%s could not be read", ManifestFile)
	case !info.Mode().IsRegular():
		return rejected("%s must be a regular file, not a link or special file", ManifestFile)
	case info.Size() > MaxManifestBytes:
		return rejected("%s is larger than %d bytes", ManifestFile, MaxManifestBytes)
	}
	f, err := root.Open(p)
	if err != nil {
		return rejected("%s could not be read", ManifestFile)
	}
	defer f.Close()
	t, err := ParseManifest(f)
	if err != nil {
		var me *ManifestError
		if errors.As(err, &me) && me.Incompatible {
			return Entry{ID: name, Source: SourceInstalled, State: StateIncompatible, Error: me.Msg}
		}
		return rejected("%v", err)
	}
	if t.ID != name {
		return rejected("the manifest's id does not match its directory name")
	}
	return Entry{ID: name, Source: SourceInstalled, State: StateAvailable, Theme: t}
}
