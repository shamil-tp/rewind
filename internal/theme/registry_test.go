// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func install(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func states(r *Registry) map[string]string {
	out := map[string]string{}
	for _, e := range r.Entries() {
		out[e.ID] = string(e.Source) + "/" + string(e.State)
	}
	return out
}

func TestLoadWithoutDirectory(t *testing.T) {
	r, err := Load("", discard)
	if err != nil {
		t.Fatal(err)
	}
	if r.DirConfigured() || len(r.Entries()) != 3 || !r.Has(DefaultID) || r.Entries()[0].ID != DefaultID {
		t.Errorf("entries = %v", states(r))
	}
	// A missing directory is not fatal either.
	r, err = Load(filepath.Join(t.TempDir(), "missing"), discard)
	if err != nil || !r.DirConfigured() || len(r.Entries()) != 3 {
		t.Errorf("missing dir: %v, %v", states(r), err)
	}
}

func TestLoadInstalledThemes(t *testing.T) {
	dir := t.TempDir()
	install(t, dir, "sunset", encode(t, manifest("sunset")))
	install(t, dir, "mismatch", encode(t, manifest("other")))
	install(t, dir, "broken", []byte(`{"schema": 1, `))
	install(t, dir, "light", encode(t, manifest("light"))) // a bundled ID
	future := manifest("future")
	future["theme_api"] = 99
	install(t, dir, "future", encode(t, future))
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755)
	os.MkdirAll(filepath.Join(dir, "Bad_Name"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignored"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	// Files other than the manifest are ignored: a theme is data only.
	os.WriteFile(filepath.Join(dir, "sunset", "install.sh"), []byte("#!/bin/sh\nrm -rf /"), 0o755)

	r, err := Load(dir, discard)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"rewind-dark": "bundled/available", "light": "installed/invalid", "oled-black": "bundled/available",
		"sunset": "installed/available", "mismatch": "installed/invalid", "broken": "installed/invalid",
		"future": "installed/incompatible", "empty": "installed/invalid", "Bad_Name": "installed/invalid",
	}
	got := states(r)
	if len(got) != len(want) {
		t.Errorf("entries = %v", got)
	}
	for id, s := range want {
		if got[id] != s {
			t.Errorf("%s = %q, want %q", id, got[id], s)
		}
	}
	// The bundled Light theme wins over the installed one with its ID.
	if th, _ := r.Get("light"); th.Name != "Rewind Light" {
		t.Errorf("light = %q", th.Name)
	}
	if !r.Has("sunset") || r.Has("future") || r.Has("broken") {
		t.Error("only valid, compatible themes are available")
	}
	for _, e := range r.Entries() {
		if e.State != StateAvailable && e.Error == "" {
			t.Errorf("%s: no reason given", e.ID)
		}
		if strings.Contains(e.Error, dir) {
			t.Errorf("%s: error exposes the path: %s", e.ID, e.Error)
		}
	}
}

func TestDiscoverRejectsLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	install(t, outside, "evil", encode(t, manifest("evil")))
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(dir, "evil")); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		// Symlinks need a privilege on Windows; a directory junction does not
		// and redirects just the same.
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "evil"), filepath.Join(outside, "evil")).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v %s", err, out)
		}
		found, err := Discover(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 || found[0].State == StateAvailable {
			t.Fatalf("junction to a directory outside was accepted: %+v", found)
		}
		return
	}
	// A real theme directory whose manifest is a symlink to a file elsewhere.
	install(t, outside, "sneaky", encode(t, manifest("sneaky")))
	os.MkdirAll(filepath.Join(dir, "sneaky"), 0o755)
	if err := os.Symlink(filepath.Join(outside, "sneaky", ManifestFile), filepath.Join(dir, "sneaky", ManifestFile)); err != nil {
		t.Fatal(err)
	}
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %+v", found)
	}
	for _, f := range found {
		if f.State == StateAvailable {
			t.Errorf("%s: a linked entry was accepted", f.ID)
		}
	}
}

func TestDiscoverBoundsEntriesAndSize(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxEntries+3; i++ {
		os.MkdirAll(filepath.Join(dir, fmt.Sprintf("t%03d", i)), 0o755)
	}
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != MaxEntries+1 || !strings.Contains(found[MaxEntries].Error, "more than 64") {
		t.Errorf("found %d entries; last = %+v", len(found), found[len(found)-1])
	}

	dir = t.TempDir()
	install(t, dir, "huge", make([]byte, MaxManifestBytes+1))
	found, _ = Discover(dir)
	if len(found) != 1 || !strings.Contains(found[0].Error, "larger than") {
		t.Errorf("oversized manifest: %+v", found)
	}
}
