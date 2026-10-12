// SPDX-License-Identifier: Apache-2.0

package metadata

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

var fullTags = map[string][]string{
	"TITLE":       {"Song Title"},
	"ARTIST":      {"The Artist"},
	"ALBUM":       {"The Album"},
	"ALBUMARTIST": {"Various Artists"},
	"GENRE":       {"Jazz"},
	"DATE":        {"1999-05-01"},
	"TRACKNUMBER": {"3/12"},
	"DISCNUMBER":  {"2/2"},
}

func intp(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func TestExtractCompleteTagsAllFormats(t *testing.T) {
	dir := t.TempDir()
	wantCodec := map[string]string{".mp3": "mpeg", ".flac": "flac", ".m4a": "aac", ".ogg": "vorbis", ".wav": "pcm"}
	for ext := range testaudio.Generators {
		t.Run(ext, func(t *testing.T) {
			p := testaudio.Write(t, dir, "full"+ext, 3, fullTags)
			m := Extract(p)
			if m.Status != StatusOK || m.Error != "" {
				t.Fatalf("status = %s (%s)", m.Status, m.Error)
			}
			if m.Title != "Song Title" || m.Artist != "The Artist" || m.Album != "The Album" ||
				m.AlbumArtist != "Various Artists" || m.Genre != "Jazz" {
				t.Errorf("text fields = %+v", m)
			}
			if intp(m.TrackNumber) != 3 || intp(m.DiscNumber) != 2 || intp(m.Year) != 1999 || m.Date != "1999-05-01" {
				t.Errorf("track=%d disc=%d year=%d date=%q", intp(m.TrackNumber), intp(m.DiscNumber), intp(m.Year), m.Date)
			}
			if m.DurationMS == nil || *m.DurationMS < 2900 || *m.DurationMS > 3100 {
				t.Errorf("duration = %v, want about 3000 ms", m.DurationMS)
			}
			if m.Format != FormatOf(p) || m.Codec != wantCodec[ext] {
				t.Errorf("format=%q codec=%q, want codec %q", m.Format, m.Codec, wantCodec[ext])
			}
		})
	}
}

func TestExtractMissingTagsFallsBackToFileName(t *testing.T) {
	dir := t.TempDir()
	for ext := range testaudio.Generators {
		p := testaudio.Write(t, dir, "Artist Dir/Album Dir/07 - My Song"+ext, 2, nil)
		m := Extract(p)
		if m.Status != StatusNoTags {
			t.Errorf("%s: status = %s (%s), want no_tags", ext, m.Status, m.Error)
		}
		if m.Title != "07 - My Song" {
			t.Errorf("%s: title = %q, want file name without extension", ext, m.Title)
		}
		// Directory names are never used as artist or album.
		if m.Artist != "" || m.Album != "" || m.AlbumArtist != "" || m.TrackNumber != nil {
			t.Errorf("%s: invented metadata from path: %+v", ext, m)
		}
		if m.DurationMS == nil {
			t.Errorf("%s: duration missing although audio properties are readable", ext)
		}
	}
}

func TestExtractPartialTags(t *testing.T) {
	dir := t.TempDir()
	p := testaudio.Write(t, dir, "partial.flac", 2, map[string][]string{"ARTIST": {"Only Artist"}, "TRACKNUMBER": {"x"}})
	m := Extract(p)
	if m.Status != StatusNoTags || m.Title != "partial" || m.Artist != "Only Artist" || m.TrackNumber != nil || m.Album != "" {
		t.Errorf("partial tags = %+v", m)
	}
}

func TestExtractMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	flac := testaudio.FLAC(2)
	cases := map[string][]byte{
		"garbage.mp3":    bytes.Repeat([]byte("not audio "), 200),
		"empty.flac":     {},
		"truncated.flac": flac[:20],
		"text.ogg":       []byte("hello, this is a text file pretending to be ogg"),
		"junk.m4a":       bytes.Repeat([]byte{0xAB}, 4096),
		"header.wav":     []byte("RIFF\x00\x00\x00\x00WAVE"),
	}
	for name, data := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		m := Extract(p) // must not panic or hang
		if m.Status != StatusError {
			t.Errorf("%s: status = %s, want error for malformed file", name, m.Status)
		}
		if m.DurationMS != nil {
			t.Errorf("%s: duration %d reported for malformed file", name, *m.DurationMS)
		}
		if m.Title != strings.TrimSuffix(name, filepath.Ext(name)) {
			t.Errorf("%s: title = %q", name, m.Title)
		}
		if m.Status == StatusError && (m.Error == "" || len(m.Error) > 250) {
			t.Errorf("%s: error message %q missing or unbounded", name, m.Error)
		}
	}
}

func TestExtractMissingFile(t *testing.T) {
	m := Extract(filepath.Join(t.TempDir(), "gone.mp3"))
	if m.Status != StatusError || m.Error != "cannot open file: file does not exist" || m.Title != "gone" {
		t.Errorf("missing file = %+v", m)
	}
}

func TestExtractMultiValueAndDirtyTags(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 2000)
	// Ogg keeps free-form DATE values when TagLib writes them (MP3/FLAC writers drop them).
	p := testaudio.Write(t, dir, "multi.ogg", 2, map[string][]string{
		"TITLE":  {"  Padded\tTitle\x01  "},
		"ARTIST": {"One", "Two", "One", ""},
		"ALBUM":  {long},
		"DATE":   {"circa 1970"},
	})
	m := Extract(p)
	if m.Title != "Padded Title" {
		t.Errorf("title = %q, want control characters removed and trimmed", m.Title)
	}
	if m.Artist != "One; Two" {
		t.Errorf("artist = %q, want distinct values joined", m.Artist)
	}
	if n := len([]rune(m.Album)); n != maxFieldRunes {
		t.Errorf("album length = %d, want truncated to %d", n, maxFieldRunes)
	}
	if m.Year != nil || m.Date != "circa 1970" {
		t.Errorf("year = %v date = %q; want no year parsed, raw date kept", m.Year, m.Date)
	}
}

func TestFormatOf(t *testing.T) {
	for name, want := range map[string]string{
		"a.mp3": "mp3", "B.FLAC": "flac", "c.m4a": "m4a", "d.ogg": "ogg", "e.oga": "ogg", "f.wav": "wav",
		"g.opus": "", "h.wma": "", "i.aac": "", "j.txt": "", "noext": "", "cover.jpg": "",
	} {
		if got := FormatOf(name); got != want {
			t.Errorf("FormatOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestParseIndexAndYear(t *testing.T) {
	for in, want := range map[string]int{"3": 3, "03": 3, "3/12": 3, " 7 / 9": 7, "0": -1, "x": -1, "": -1, "10000": -1} {
		if got := intp(parseIndex(in)); got != want {
			t.Errorf("parseIndex(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]int{"1999": 1999, "1999-05-01": 1999, "99": -1, "": -1, "abcd": -1} {
		if got := intp(parseYear(in)); got != want {
			t.Errorf("parseYear(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestExtractNeverWritesSource checks, on real files, that reading metadata
// leaves the bytes and modification time untouched, including for files the
// process could not write anyway.
func TestExtractNeverWritesSource(t *testing.T) {
	dir := t.TempDir()
	for ext := range testaudio.Generators {
		p := testaudio.Write(t, dir, "ro"+ext, 2, fullTags)
		before, _ := os.ReadFile(p)
		infoBefore, _ := os.Stat(p)
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatal(err)
		}
		m := Extract(p)
		if m.Status != StatusOK {
			t.Errorf("%s: read-only file not readable: %s", ext, m.Error)
		}
		after, _ := os.ReadFile(p)
		infoAfter, _ := os.Stat(p)
		if !bytes.Equal(before, after) || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
			t.Errorf("%s: source file changed by Extract", ext)
		}
		os.Chmod(p, 0o644) // let TempDir cleanup remove it on Windows
	}
}
