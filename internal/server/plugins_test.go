// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/plugin"
)

func pluginManager(t *testing.T) (*plugin.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(id, content string) {
		os.MkdirAll(filepath.Join(dir, id), 0o755)
		os.WriteFile(filepath.Join(dir, id, plugin.ManifestFile), []byte(content), 0o644)
	}
	write("html-name", `{"schema":1,"id":"html-name","name":"<img src=x onerror=alert(1)>","version":"1.0.0",
		"description":"<script>alert(1)</script>","rewind_api":1,"capabilities":[]}`)
	write("broken", `{"schema":1,"id":"broken","command":"rm -rf /"}`)
	m := plugin.New(plugin.Options{Dir: dir, Enabled: []string{"html-name", "not-installed"}, Registry: plugin.NewRegistry()})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	return m, dir
}

type pluginsBody struct {
	Configured bool            `json:"configured"`
	APIVersion int             `json:"api_version"`
	Items      []plugin.Status `json:"items"`
}

func TestPluginStatusRequiresAuth(t *testing.T) {
	m, _ := pluginManager(t)
	e := newTestEnvWith(t, envConfig{plugins: m})
	e.doSetup(t)
	bogus := &http.Cookie{Name: "rewind_session", Value: strings.Repeat("B", 43)}
	for _, c := range []*http.Cookie{nil, bogus} {
		rec := e.send(t, http.MethodGet, "/api/plugins", nil, withCookie(c))
		expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
		if strings.Contains(rec.Body.String(), "html-name") {
			t.Error("plugin data leaked to an unauthenticated request")
		}
	}
}

func TestPluginStatusIsSafeAndReadOnly(t *testing.T) {
	m, dir := pluginManager(t)
	e := newTestEnvWith(t, envConfig{plugins: m})
	c := e.doSetup(t)

	rec := e.send(t, http.MethodGet, "/api/plugins", nil, withCookie(c))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET = %d %v", rec.Code, rec.Header())
	}
	var b pluginsBody
	decodeBody(t, rec, &b)
	if !b.Configured || b.APIVersion != plugin.APIVersion || len(b.Items) != 3 {
		t.Fatalf("body = %+v", b)
	}
	got := map[string]plugin.Status{}
	for _, s := range b.Items {
		got[s.ID] = s
	}
	// html-name is enabled but not compiled in: unavailable, never run.
	if got["html-name"].State != plugin.StateUnavailable || got["broken"].State != plugin.StateInvalid ||
		got["not-installed"].State != plugin.StateMissing {
		t.Errorf("states = %+v", got)
	}
	raw := rec.Body.String()
	// Manifest text is returned as JSON string data (escaped), never as markup the
	// server renders; the UI shows it as text.
	// bs is a backslash: JSON writes < and > as unicode escapes.
	bs := string(rune(92))
	if !strings.Contains(raw, bs+"u003cimg src=x onerror=alert(1)"+bs+"u003e") || strings.Contains(raw, "<img") {
		t.Errorf("HTML in metadata is not escaped JSON text: %s", raw)
	}
	// No paths (raw or JSON-escaped), and nothing echoed from a rejected manifest.
	for _, leak := range []string{dir, strings.ReplaceAll(dir, bs, bs+bs), filepath.Base(dir), "rm -rf"} {
		if strings.Contains(raw, leak) {
			t.Errorf("status leaks %q: %s", leak, raw)
		}
	}

	// No way to change or run plugins over HTTP.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		expectError(t, e.send(t, method, "/api/plugins", map[string]string{"id": "html-name"}, withCookie(c)),
			http.StatusMethodNotAllowed, "method_not_allowed")
	}
	for _, path := range []string{"/api/plugins/html-name", "/api/plugins/html-name/enable", "/api/plugins/install"} {
		expectError(t, e.send(t, http.MethodPost, path, nil, withCookie(c)), http.StatusNotFound, "not_found")
	}
	expectError(t, e.send(t, http.MethodGet, "/api/plugins?run=html-name", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
}

func TestPluginStatusWithoutPlugins(t *testing.T) {
	e := newTestEnv(t)
	c := e.doSetup(t)
	var b pluginsBody
	decodeBody(t, e.send(t, http.MethodGet, "/api/plugins", nil, withCookie(c)), &b)
	if b.Configured || b.Items == nil || len(b.Items) != 0 {
		t.Errorf("body = %+v", b)
	}
}
