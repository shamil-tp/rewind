// SPDX-License-Identifier: Apache-2.0

package library

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/metadata"
	"github.com/shamil-tp/rewind/internal/testaudio"
)

// hangingExtract hangs on files named "hang*" until the test ends, as a
// file that made TagLib loop would; other files are read normally.
func hangingExtract(t *testing.T, calls *atomic.Int32) func(string) metadata.Metadata {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(p string) metadata.Metadata {
		if strings.HasPrefix(filepath.Base(p), "hang") {
			calls.Add(1)
			<-release
		}
		return metadata.Extract(p)
	}
}

func TestHangingMetadataReadDoesNotStallTheScan(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "good.flac", 1, tags("Good", "A", "B", "1"))
	hang := testaudio.Write(t, dir, "hang.mp3", 1, tags("Hang", "A", "B", "2"))
	root := e.addRoot(t, dir)
	var calls atomic.Int32
	s := e.scanner(t, Options{Extract: hangingExtract(t, &calls), ExtractTimeout: 200 * time.Millisecond})

	start := time.Now()
	res := e.scan(t, s, root)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("scan took %s", d)
	}
	got := e.tracks(t, root.ID)
	if res.Status != ScanCompleted || got["good.flac"].Title != "Good" || got["hang.mp3"].MetaStatus != "error" ||
		got["hang.mp3"].Title != "hang" {
		t.Fatalf("result %+v; tracks %+v", res, got)
	}
	var msg string
	e.db.QueryRow("SELECT metadata_error FROM tracks WHERE rel_path = 'hang.mp3'").Scan(&msg)
	if msg != "reading metadata timed out" || !strings.Contains(e.logs.String(), "took too long") {
		t.Errorf("metadata_error %q; log: %s", msg, e.logs.String())
	}

	// Unchanged: not read again, so a hanging file costs one stuck read, not
	// one per rescan.
	e.scan(t, s, root)
	if calls.Load() != 1 {
		t.Errorf("hanging file read %d times; want 1", calls.Load())
	}
	e.db.QueryRow("SELECT metadata_error FROM tracks WHERE rel_path = 'hang.mp3'").Scan(&msg)
	if !strings.Contains(msg, "timed out earlier") {
		t.Errorf("rescan metadata_error = %q", msg)
	}

	// Changed: tried again.
	future := time.Now().Add(time.Hour)
	os.Chtimes(hang, future, future)
	e.scan(t, s, root)
	if calls.Load() != 2 {
		t.Errorf("changed file read %d times in total; want 2", calls.Load())
	}
}

func TestCloseIsNotBlockedByAHangingRead(t *testing.T) {
	old := cancelGrace
	cancelGrace = 200 * time.Millisecond
	t.Cleanup(func() { cancelGrace = old })

	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "hang.mp3", 1, nil)
	root := e.addRoot(t, dir)
	var calls atomic.Int32
	s := e.scanner(t, Options{Extract: hangingExtract(t, &calls), ExtractTimeout: time.Hour})
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	for calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	s.Close()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Close took %s with a hanging read", d)
	}
	last, _, _ := LastScan(bg, e.db, root.ID)
	if last.Status != ScanInterrupted {
		t.Errorf("scan after Close = %+v; want interrupted", last)
	}
}
