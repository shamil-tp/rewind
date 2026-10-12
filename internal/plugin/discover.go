// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxEntries bounds how many entries of the plugin directory are examined.
const MaxEntries = 64

// ErrNoDirectory means the configured plugin directory does not exist.
var ErrNoDirectory = errors.New("plugin directory does not exist")

// Found is one entry of the plugin directory: a valid manifest, or the
// reason it was rejected.
type Found struct {
	// Dir is the entry's name, made safe for display (it equals the plugin
	// ID for valid entries).
	Dir      string
	Manifest Manifest // valid only when Err is nil
	Err      error    // a *ManifestError; safe to show
}

// Discover reads the manifest of every plugin in dir: each subdirectory
// <id>/ holding a plugin.json. It only reads files; nothing is executed.
//
// Files are opened through os.Root, so no path can resolve outside dir.
// Hidden entries and plain files are ignored. Symbolic links and Windows
// junctions (as a plugin directory or a manifest) are rejected and reported,
// never followed. Results are sorted by name, and at most MaxEntries entries
// are examined.
func Discover(dir string) ([]Found, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoDirectory
	}
	if err != nil {
		return nil, fmt.Errorf("cannot open the plugin directory: %w", err)
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("cannot read the plugin directory: %w", err)
	}
	var out []Found
	examined := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.Type().IsRegular() {
			continue // README files and the like
		}
		if examined++; examined > MaxEntries {
			out = append(out, Found{Err: invalid("the plugin directory has more than %d entries; the rest were ignored", MaxEntries)})
			break
		}
		out = append(out, readEntry(root, e))
	}
	return out, nil
}

func readEntry(root *os.Root, e fs.DirEntry) Found {
	name := e.Name()
	f := Found{Dir: displayName(name)}
	switch {
	case !e.IsDir():
		// Symbolic links, Windows junctions and other special entries.
		f.Err = invalid("plugin directories must be plain directories, not links or junctions")
		return f
	case !ValidID(name):
		f.Err = invalid("the directory name must be the plugin's ID (lower-case letters, digits and hyphens)")
		return f
	}

	path := name + "/" + ManifestFile
	info, err := root.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Err = invalid("%s is missing", ManifestFile)
		return f
	case err != nil:
		f.Err = invalid("%s could not be read", ManifestFile)
		return f
	case !info.Mode().IsRegular():
		f.Err = invalid("%s must be a regular file, not a link or special file", ManifestFile)
		return f
	case info.Size() > MaxManifestBytes:
		f.Err = invalid("%s is larger than %d bytes", ManifestFile, MaxManifestBytes)
		return f
	}
	file, err := root.Open(path)
	if err != nil {
		f.Err = invalid("%s could not be read", ManifestFile)
		return f
	}
	defer file.Close()
	m, err := ParseManifest(file)
	if err != nil {
		f.Err = err
		return f
	}
	if m.ID != name {
		f.Err = invalid("the manifest's id does not match its directory name")
		return f
	}
	f.Manifest = m
	return f
}

// displayName bounds an untrusted directory name for logs and status.
func displayName(name string) string {
	if !utf8.ValidString(name) {
		return "(unreadable name)"
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, name)
	if utf8.RuneCountInString(name) > maxIDLength {
		name = string([]rune(name)[:maxIDLength]) + "…"
	}
	return name
}
