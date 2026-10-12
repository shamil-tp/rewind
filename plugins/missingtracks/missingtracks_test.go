// SPDX-License-Identifier: Apache-2.0

package missingtracks

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/plugin"
)

// The shipped manifest must stay valid and describe this code.
func TestManifestMatchesCode(t *testing.T) {
	f, err := os.Open(plugin.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := plugin.ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != ID || m.Version != Version || m.RewindAPI != plugin.APIVersion ||
		strings.Join(m.Capabilities, ",") != plugin.CapLibraryScanEvents {
		t.Errorf("manifest = %+v", m)
	}
}

func TestWarnsOnlyWhenTracksGoMissing(t *testing.T) {
	logs := &syncBuffer{}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ID), 0o755)
	data, _ := os.ReadFile(plugin.ManifestFile)
	os.WriteFile(filepath.Join(dir, ID, plugin.ManifestFile), data, 0o644)
	reg := plugin.NewRegistry()
	if err := reg.Add(&Plugin{}); err != nil {
		t.Fatal(err)
	}
	m := plugin.New(plugin.Options{Dir: dir, Enabled: []string{ID}, Registry: reg, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	// Events are handled in order, so once scan 2's warning is logged,
	// scan 1 has been handled too.
	m.PublishScan(plugin.ScanEvent{ScanID: 1, TracksMissing: 0})
	m.PublishScan(plugin.ScanEvent{ScanID: 2, RootID: 3, TracksMissing: 4})
	for end := time.Now().Add(2 * time.Second); !strings.Contains(logs.String(), "scan_id=2"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("no warning for missing tracks; log: %s", logs.String())
		}
	}
	out := logs.String()
	if strings.Contains(out, "scan_id=1") || !strings.Contains(out, "plugin=missing-tracks") || !strings.Contains(out, "missing=4") {
		t.Errorf("log: %s", out)
	}
}

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
