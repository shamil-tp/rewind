// SPDX-License-Identifier: Apache-2.0

// Package metadata reads tags and audio properties from music files. It wraps
// go.senan.xyz/taglib (TagLib compiled to WebAssembly, LGPL-2.1) so the rest
// of Rewind does not depend on it directly and it can be replaced.
//
// Reading is strictly read-only: taglib's read functions mount only the
// file's directory, read-only, inside the WebAssembly sandbox. This package
// never calls taglib's write functions.
package metadata

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.senan.xyz/taglib"
)

// Formats maps supported file extensions (lowercase) to Rewind's format name.
// Support is limited to what Rewind has verified with TagLib; other
// extensions are skipped by the scanner.
var Formats = map[string]string{
	".mp3":  "mp3",
	".flac": "flac",
	".m4a":  "m4a",
	".ogg":  "ogg",
	".oga":  "ogg",
	".wav":  "wav",
}

// FormatOf returns Rewind's format name for a file name, or "" if the
// extension is not supported.
func FormatOf(name string) string {
	return Formats[strings.ToLower(filepath.Ext(name))]
}

// maxFieldRunes caps stored tag values; longer values are truncated.
const maxFieldRunes = 512

// Status says how much metadata could be read.
type Status string

const (
	// StatusOK means tags and audio properties were read.
	StatusOK Status = "ok"
	// StatusNoTags means the file was read but has no usable title tag; the
	// title falls back to the file name.
	StatusNoTags Status = "no_tags"
	// StatusError means the file could not be parsed; only file-name
	// information is available.
	StatusError Status = "error"
)

// Metadata is what Rewind stores about a track. Pointer and empty-string
// fields are unknown when nil or "".
type Metadata struct {
	Title       string // from tags, or the file name without extension
	Artist      string
	Album       string
	AlbumArtist string
	Genre       string
	Date        string // raw date tag, e.g. "1999" or "1999-05-01"
	Year        *int
	TrackNumber *int
	DiscNumber  *int
	DurationMS  *int64
	Format      string // Rewind format name (from the extension)
	Codec       string // e.g. "aac", "alac", "vorbis", "pcm", "mpeg", "flac"
	Status      Status
	Error       string // set when Status is StatusError; short and bounded
}

// UsePrivateTempDir points the process's temporary directory (TMPDIR, or TMP
// on Windows) at a new directory that only this user can access (mode 0700,
// unpredictable name), and returns a function that restores the variable and
// removes the directory. Call it once at startup, before the first Extract.
//
// taglib caches its compiled WebAssembly module, which is machine code, in
// os.TempDir()/go-taglib-wasm. It creates that directory only if it is
// missing, and runs what it finds there after a CRC check that is not an
// authenticity check. In a shared /tmp, another local user could create the
// directory first and plant code for Rewind to run. Inside a private
// directory the cache is still used, for the lifetime of the process.
func UsePrivateTempDir() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "rewind-")
	if err != nil {
		return nil, fmt.Errorf("creating a private temporary directory: %w", err)
	}
	key := "TMPDIR"
	if runtime.GOOS == "windows" {
		key = "TMP"
	}
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, dir); err != nil {
		os.Remove(dir)
		return nil, fmt.Errorf("setting %s: %w", key, err)
	}
	return func() {
		if had {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
		os.RemoveAll(dir)
	}, nil
}

// Extract reads metadata from the file at path. It never returns an error
// for a file that exists but cannot be parsed: such files get StatusError and
// a file-name title, so a single bad file never stops a scan.
func Extract(filePath string) Metadata {
	m := Metadata{Format: FormatOf(filePath), Title: FallbackTitle(filePath)}

	// taglib reports a missing or unreadable file only as an invalid file;
	// opening it first gives a precise reason (not found, permission denied).
	f, err := os.Open(filePath)
	if err != nil {
		m.Status = StatusError
		m.Error = "cannot open file: " + openReason(err)
		return m
	}
	f.Close()

	// taglib.ReadProperties never returns an error: an unparseable file
	// yields empty properties. A positive sample rate is the signal that the
	// audio stream was understood.
	props, _ := taglib.ReadProperties(filePath)
	audioOK := props.SampleRate > 0
	tags, terr := taglib.ReadTags(filePath)
	if !audioOK && terr != nil {
		m.Status = StatusError
		m.Error = describe(terr)
		return m
	}

	if audioOK {
		if props.Length > 0 {
			ms := props.Length.Milliseconds()
			m.DurationMS = &ms
		}
		m.Codec = codecName(props)
	}

	if terr == nil {
		if t := field(tags, taglib.Title); t != "" {
			m.Title = t
		}
		m.Artist = field(tags, taglib.Artist)
		m.Album = field(tags, taglib.Album)
		m.AlbumArtist = field(tags, taglib.AlbumArtist)
		m.Genre = field(tags, taglib.Genre)
		m.Date = firstValue(tags, taglib.Date)
		m.Year = parseYear(m.Date)
		m.TrackNumber = parseIndex(firstValue(tags, taglib.TrackNumber))
		m.DiscNumber = parseIndex(firstValue(tags, taglib.DiscNumber))
	}

	switch {
	case !audioOK:
		// The container was recognized but no audio stream was found: index
		// what we have, but flag it so problems are visible.
		m.Status = StatusError
		m.Error = "no readable audio stream"
	case terr != nil || field(tags, taglib.Title) == "":
		m.Status = StatusNoTags
	default:
		m.Status = StatusOK
	}
	return m
}

// FallbackTitle derives a title from the file name: the base name without its
// extension. Directory names are never used as artist or album.
func FallbackTitle(filePath string) string {
	base := path.Base(filepath.ToSlash(filePath))
	return clean(strings.TrimSuffix(base, path.Ext(base)))
}

func openReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "file does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	default:
		return truncate(clean(err.Error()), 200)
	}
}

func describe(err error) string {
	if errors.Is(err, taglib.ErrInvalidFile) {
		return "not a valid or supported audio file"
	}
	return truncate(clean(err.Error()), 200)
}

func codecName(p taglib.Properties) string {
	if p.InnerCodec != "" {
		return clean(p.InnerCodec)
	}
	return clean(p.Format)
}

// field joins the distinct non-empty values of a tag with "; ".
func field(tags map[string][]string, key string) string {
	var out []string
	seen := map[string]bool{}
	for _, v := range tags[key] {
		v = clean(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return truncate(strings.Join(out, "; "), maxFieldRunes)
}

func firstValue(tags map[string][]string, key string) string {
	for _, v := range tags[key] {
		if v = clean(v); v != "" {
			return truncate(v, 64)
		}
	}
	return ""
}

// parseIndex reads track or disc numbers such as "3", "03" or "3/12".
func parseIndex(s string) *int {
	num, _, _ := strings.Cut(s, "/")
	n, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil || n < 1 || n > 9999 {
		return nil
	}
	return &n
}

// parseYear reads a leading four-digit year from a date tag.
func parseYear(date string) *int {
	if len(date) < 4 {
		return nil
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil || y < 1000 || y > 9999 {
		return nil
	}
	return &y
}

// clean makes a tag value safe to store: valid UTF-8, no NUL or other control
// characters, trimmed.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			if r == '\t' || r == '\n' {
				return ' '
			}
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

// String is used in logs.
func (m Metadata) String() string {
	return fmt.Sprintf("%s (%s, %s)", m.Title, m.Format, m.Status)
}
