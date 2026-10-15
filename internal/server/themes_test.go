// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/theme"
)

const sunsetManifest = `{
  "schema": 1, "id": "sunset", "name": "Sunset <b>bold</b>", "version": "1.2.0", "theme_api": 1, "scheme": "dark",
  "tokens": {
    "color.background": "#101010", "color.surface": "#1a1a1a", "color.text": "#f0f0f0", "color.text-muted": "#b0b0b0",
    "color.border": "#f0f0f0", "color.primary": "#ffcc00", "color.secondary": "#333333", "color.accent": "#66ddff",
    "color.danger": "#ff6666"
  }
}`

func themeEnv(t *testing.T) (*testEnv, *http.Cookie, string) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sunset"), 0o755)
	os.WriteFile(filepath.Join(dir, "sunset", "theme.json"), []byte(sunsetManifest), 0o644)
	os.MkdirAll(filepath.Join(dir, "broken"), 0o755)
	os.WriteFile(filepath.Join(dir, "broken", "theme.json"), []byte(`{"schema":1,"id":"broken"}`), 0o644)
	e := newTestEnvWith(t, envConfig{themeDir: dir})
	return e, e.doSetup(t), dir
}

func themeState(t *testing.T, e *testEnv, c *http.Cookie, method, path string, body any, want int) theme.UserState {
	t.Helper()
	rec := e.send(t, method, path, body, withCookie(c))
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d; body = %s", method, path, rec.Code, want, rec.Body)
	}
	var st theme.UserState
	if want == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func findTheme(st theme.UserState, id string) *theme.UserTheme {
	for i := range st.Items {
		if st.Items[i].ID == id {
			return &st.Items[i]
		}
	}
	return nil
}

func TestThemeEndpointsRequireAuth(t *testing.T) {
	e, _, _ := themeEnv(t)
	bogus := &http.Cookie{Name: "rewind_session", Value: strings.Repeat("B", 43)}
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/themes", nil},
		{http.MethodPut, "/api/themes/active", map[string]string{"id": "light"}},
		{http.MethodPut, "/api/themes/light/enabled", map[string]bool{"enabled": false}},
		{http.MethodPut, "/api/themes/light/customization", map[string]any{"tokens": map[string]any{"font.size": 18}}},
	} {
		for _, c := range []*http.Cookie{nil, bogus} {
			rec := e.send(t, rt.method, rt.path, rt.body, withCookie(c))
			expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s: Cache-Control = %q", rt.method, rt.path, rec.Header().Get("Cache-Control"))
			}
		}
	}
	var n int
	e.db.QueryRow("SELECT (SELECT count(*) FROM theme_settings) + (SELECT count(*) FROM user_preferences)").Scan(&n)
	if n != 0 {
		t.Errorf("unauthenticated requests created %d rows", n)
	}
}

func TestThemeCSRF(t *testing.T) {
	e, c, _ := themeEnv(t)
	attack := []reqOpt{withCookie(c), withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example")}
	for _, rt := range []struct {
		path string
		body any
	}{
		{"/api/themes/active", map[string]string{"id": "light"}},
		{"/api/themes/light/enabled", map[string]bool{"enabled": false}},
		{"/api/themes/light/customization", map[string]any{"tokens": map[string]any{"font.size": 18}}},
	} {
		expectError(t, e.send(t, http.MethodPut, rt.path, rt.body, attack...), http.StatusForbidden, "cross_origin")
	}
	var n int
	e.db.QueryRow("SELECT (SELECT count(*) FROM theme_settings) + (SELECT count(*) FROM user_preferences)").Scan(&n)
	if n != 0 {
		t.Errorf("cross-site requests created %d rows", n)
	}
}

func TestThemeAPIFlow(t *testing.T) {
	e, c, dir := themeEnv(t)
	rec := e.send(t, http.MethodGet, "/api/themes", nil, withCookie(c))
	if strings.Contains(rec.Body.String(), dir) || strings.Contains(rec.Body.String(), filepath.ToSlash(dir)) {
		t.Error("the theme list exposes the theme directory's path")
	}
	st := themeState(t, e, c, http.MethodGet, "/api/themes", nil, http.StatusOK)
	if st.Active != "rewind-dark" || !st.DirectoryConfigured || st.ThemeAPI != 1 || len(st.Items) != 5 {
		t.Fatalf("state = %+v", st)
	}
	sunset := findTheme(st, "sunset")
	if sunset == nil || sunset.State != theme.StateAvailable || sunset.Source != theme.SourceInstalled || sunset.Name != "Sunset <b>bold</b>" {
		t.Fatalf("sunset = %+v", sunset)
	}
	if b := findTheme(st, "broken"); b == nil || b.State != theme.StateInvalid || b.Error == "" || b.Tokens != nil {
		t.Errorf("broken = %+v", b)
	}

	st = themeState(t, e, c, http.MethodPut, "/api/themes/active", map[string]string{"id": "sunset"}, http.StatusOK)
	if st.Active != "sunset" {
		t.Errorf("active = %q", st.Active)
	}
	st = themeState(t, e, c, http.MethodPut, "/api/themes/sunset/customization",
		map[string]any{"tokens": map[string]any{"color.accent": "#ff8800", "radius.button": 4}}, http.StatusOK)
	if cz := findTheme(st, "sunset").Customization; cz["color.accent"] != "#ff8800" || cz["radius.button"] != 4.0 {
		t.Errorf("customization = %v", cz)
	}

	// The preferences API reports the same active theme.
	var p struct {
		Preferences struct{ Theme string } `json:"preferences"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/preferences", nil, withCookie(c)), &p)
	if p.Preferences.Theme != "sunset" {
		t.Errorf("preferences theme = %q", p.Preferences.Theme)
	}

	themeState(t, e, c, http.MethodPut, "/api/themes/active", map[string]string{"id": "light"}, http.StatusOK)
	themeState(t, e, c, http.MethodPut, "/api/themes/active", map[string]string{"id": "sunset"}, http.StatusOK)
	st = themeState(t, e, c, http.MethodPut, "/api/themes/sunset/enabled", map[string]bool{"enabled": false}, http.StatusOK)
	if st.Active != "light" || findTheme(st, "sunset").Enabled {
		t.Errorf("after disabling: active %q", st.Active)
	}
	expectError(t, e.send(t, http.MethodPut, "/api/themes/active", map[string]string{"id": "sunset"}, withCookie(c)),
		http.StatusConflict, "theme_disabled")
	// The customization survives disabling.
	if findTheme(st, "sunset").Customization["color.accent"] != "#ff8800" {
		t.Error("disabling lost the customization")
	}
}

func TestThemeAPIValidation(t *testing.T) {
	e, c, _ := themeEnv(t)
	for _, tc := range []struct {
		name, method, path string
		body               any
		raw                string
		status             int
		code               string
	}{
		{"unknown theme", http.MethodPut, "/api/themes/active", map[string]string{"id": "solarized"}, "", 404, "theme_not_found"},
		{"invalid theme", http.MethodPut, "/api/themes/active", map[string]string{"id": "broken"}, "", 404, "theme_not_found"},
		{"traversal id", http.MethodPut, "/api/themes/active", map[string]string{"id": "../../etc/passwd"}, "", 404, "theme_not_found"},
		{"missing id", http.MethodPut, "/api/themes/active", map[string]string{}, "", 422, "validation_failed"},
		{"extra field", http.MethodPut, "/api/themes/active", map[string]string{"id": "light", "css": "x"}, "", 400, "invalid_request"},
		{"bad json", http.MethodPut, "/api/themes/active", nil, `{"id":`, 400, "invalid_request"},
		{"bad path id", http.MethodPut, "/api/themes/Light/enabled", map[string]bool{"enabled": true}, "", 404, "theme_not_found"},
		{"missing enabled", http.MethodPut, "/api/themes/light/enabled", map[string]any{}, "", 422, "validation_failed"},
		{"disable default", http.MethodPut, "/api/themes/rewind-dark/enabled", map[string]bool{"enabled": false}, "", 422, "validation_failed"},
		{"enabled string", http.MethodPut, "/api/themes/light/enabled", map[string]string{"enabled": "no"}, "", 400, "invalid_request"},
		{"missing tokens", http.MethodPut, "/api/themes/light/customization", map[string]any{}, "", 422, "validation_failed"},
		{"css token", http.MethodPut, "/api/themes/light/customization",
			map[string]any{"tokens": map[string]any{"color.accent": "red;}body{display:none"}}, "", 422, "validation_failed"},
		{"unknown token", http.MethodPut, "/api/themes/light/customization",
			map[string]any{"tokens": map[string]any{"selector": "body"}}, "", 422, "validation_failed"},
		{"locked token", http.MethodPut, "/api/themes/oled-black/customization",
			map[string]any{"tokens": map[string]any{"color.background": "#222222"}}, "", 422, "validation_failed"},
		{"unknown theme tokens", http.MethodPut, "/api/themes/solarized/customization",
			map[string]any{"tokens": map[string]any{}}, "", 404, "theme_not_found"},
		{"too large", http.MethodPut, "/api/themes/light/customization", nil,
			`{"tokens":{"x":"` + strings.Repeat("a", maxThemeBodyBytes) + `"}}`, 413, "body_too_large"},
		{"query", http.MethodGet, "/api/themes?all=1", nil, "", 400, "invalid_parameter"},
		{"install by POST", http.MethodPost, "/api/themes", map[string]string{"id": "x"}, "", 405, "method_not_allowed"},
		{"delete active", http.MethodDelete, "/api/themes/active", nil, "", 405, "method_not_allowed"},
		{"no theme upload route", http.MethodPut, "/api/themes/sunset", map[string]string{}, "", 404, "not_found"},
		{"no theme package route", http.MethodPost, "/api/themes/install", map[string]string{}, "", 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := []reqOpt{withCookie(c)}
			if tc.raw != "" {
				opts = append(opts, withRawBody(tc.raw), withContentType("application/json"))
			}
			expectError(t, e.send(t, tc.method, tc.path, tc.body, opts...), tc.status, tc.code)
		})
	}
	rec := e.send(t, http.MethodPut, "/api/themes/active", nil, withCookie(c), withRawBody(`{"id":"light"}`), withContentType("text/plain"))
	expectError(t, rec, http.StatusUnsupportedMediaType, "unsupported_media_type")

	// Nothing was changed by the rejected requests.
	st := themeState(t, e, c, http.MethodGet, "/api/themes", nil, http.StatusOK)
	if st.Active != "rewind-dark" || len(findTheme(st, "light").Customization) != 0 || !findTheme(st, "light").Enabled {
		t.Errorf("state changed: %+v", st)
	}
}

func TestPreferencesAcceptInstalledThemes(t *testing.T) {
	e, c, _ := themeEnv(t)
	rec := e.send(t, http.MethodPatch, "/api/preferences", map[string]string{"theme": "sunset"}, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"broken", "solarized"} {
		expectError(t, e.send(t, http.MethodPatch, "/api/preferences", map[string]string{"theme": bad}, withCookie(c)),
			http.StatusUnprocessableEntity, "validation_failed")
	}
}
