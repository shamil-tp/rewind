// SPDX-License-Identifier: Apache-2.0

package prefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/db"
)

var bg = context.Background()

func newStore(t *testing.T) (*Store, *sql.DB, int64) {
	t.Helper()
	conn, err := db.Open(bg, filepath.Join(t.TempDir(), db.FileName), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	res, err := conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at)
		VALUES ('owner', 'h', 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return NewStore(conn), conn, id
}

func raw(m map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

func TestDefaultsWithoutSavedValues(t *testing.T) {
	s, _, u := newStore(t)
	p, err := s.Get(bg, u)
	if err != nil || p != Defaults() {
		t.Fatalf("Get = %+v, %v; want defaults %+v", p, err, Defaults())
	}
}

func TestUpdatePersists(t *testing.T) {
	s, conn, u := newStore(t)
	p, err := s.Update(bg, u, raw(map[string]any{"library_sort": "added", "autoplay_next": false}))
	if err != nil || p.LibrarySort != "added" || p.AutoplayNext {
		t.Fatalf("Update = %+v, %v", p, err)
	}
	// A partial update keeps the other values.
	if p, err = s.Update(bg, u, raw(map[string]any{"library_sort": "title"})); err != nil || p.LibrarySort != "title" || p.AutoplayNext {
		t.Fatalf("partial Update = %+v, %v", p, err)
	}
	// A new store over the same database (a restart) reads the same values.
	if p, err = NewStore(conn).Get(bg, u); err != nil || p.LibrarySort != "title" || p.AutoplayNext {
		t.Errorf("after restart = %+v, %v", p, err)
	}
	var v string
	conn.QueryRow("SELECT value FROM user_preferences WHERE key = 'library_sort'").Scan(&v)
	if v != `"title"` {
		t.Errorf("stored value = %s, want canonical JSON", v)
	}
}

func TestUpdateValidation(t *testing.T) {
	s, conn, u := newStore(t)
	for name, changes := range map[string]map[string]json.RawMessage{
		"empty":          {},
		"unknown key":    raw(map[string]any{"theme": "dark"}),
		"bad sort":       raw(map[string]any{"library_sort": "path"}),
		"sort type":      raw(map[string]any{"library_sort": 3}),
		"bool type":      raw(map[string]any{"autoplay_next": "yes"}),
		"null":           {"autoplay_next": json.RawMessage("null")},
		"one bad of two": raw(map[string]any{"library_sort": "album", "autoplay_next": 1}),
		"injection":      raw(map[string]any{"library_sort": "artist'; DROP TABLE users; --"}),
	} {
		_, err := s.Update(bg, u, changes)
		var in *InputError
		if !errors.As(err, &in) {
			t.Errorf("%s: error = %v, want InputError", name, err)
		}
	}
	// Nothing was saved by any rejected request.
	var n int
	conn.QueryRow("SELECT count(*) FROM user_preferences").Scan(&n)
	if n != 0 {
		t.Errorf("%d rows saved by rejected updates", n)
	}
}

// Rows written by an older or newer version, or edited by hand, must not
// break reading: invalid values and unknown keys fall back to defaults.
func TestInvalidStoredValuesFallBack(t *testing.T) {
	s, conn, u := newStore(t)
	for k, v := range map[string]string{
		"library_sort":  `"filename"`,
		"autoplay_next": `not json`,
		"retired_key":   `true`,
	} {
		if _, err := conn.Exec("INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, ?, ?, 1)", u, k, v); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Get(bg, u)
	if err != nil || p != Defaults() {
		t.Errorf("Get = %+v, %v; want defaults", p, err)
	}
	// Saving a valid value replaces the bad row.
	if p, err = s.Update(bg, u, raw(map[string]any{"library_sort": "year"})); err != nil || p.LibrarySort != "year" {
		t.Errorf("Update over a bad row = %+v, %v", p, err)
	}
}

func TestPreferencesArePerUser(t *testing.T) {
	s, conn, u := newStore(t)
	res, _ := conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('other', 'h', 1, 1, 1)`)
	other, _ := res.LastInsertId()
	if _, err := s.Update(bg, u, raw(map[string]any{"autoplay_next": false})); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(bg, other); p != Defaults() {
		t.Errorf("other user's preferences = %+v", p)
	}
}

func TestThemeAndAccentValidation(t *testing.T) {
	s, conn, u := newStore(t)
	p, err := s.Update(bg, u, raw(map[string]any{"theme": "light", "accent_color": "#3a1fd6"}))
	if err != nil || p.Theme != "light" || p.AccentColor != "#3a1fd6" {
		t.Fatalf("Update = %+v, %v", p, err)
	}
	if p, err = s.Update(bg, u, raw(map[string]any{"accent_color": ""})); err != nil || p.AccentColor != "" || p.Theme != "light" {
		t.Fatalf("reset accent = %+v, %v", p, err)
	}

	for name, changes := range map[string]map[string]json.RawMessage{
		"unknown theme":    raw(map[string]any{"theme": "solarized"}),
		"theme case":       raw(map[string]any{"theme": "Light"}),
		"theme type":       raw(map[string]any{"theme": 1}),
		"accent word":      raw(map[string]any{"accent_color": "red"}),
		"accent short":     raw(map[string]any{"accent_color": "#f80"}),
		"accent upper":     raw(map[string]any{"accent_color": "#FF8800"}),
		"accent alpha":     raw(map[string]any{"accent_color": "#ff8800aa"}),
		"accent rgb":       raw(map[string]any{"accent_color": "rgb(255,0,0)"}),
		"accent var":       raw(map[string]any{"accent_color": "var(--rw-color-bg)"}),
		"accent url":       raw(map[string]any{"accent_color": "url(https://evil.example/x)"}),
		"accent injection": raw(map[string]any{"accent_color": "#ff8800;}body{display:none"}),
		"accent newline":   raw(map[string]any{"accent_color": "#ff8800\n"}),
		"accent null":      {"accent_color": json.RawMessage("null")},
	} {
		var in *InputError
		if _, err := s.Update(bg, u, changes); !errors.As(err, &in) {
			t.Errorf("%s: error = %v, want InputError", name, err)
		}
	}
	// Rejected requests changed nothing.
	if p, _ := s.Get(bg, u); p.Theme != "light" || p.AccentColor != "" {
		t.Errorf("after rejected updates = %+v", p)
	}

	// Stored values from elsewhere (an older version, a hand edit) fall back.
	conn.Exec(`UPDATE user_preferences SET value = '"retired-theme"' WHERE key = 'theme'`)
	conn.Exec(`INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, 'accent_color', '"url(x)"', 1)
		ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value`, u)
	if p, _ := s.Get(bg, u); p.Theme != "rewind-dark" || p.AccentColor != "" {
		t.Errorf("invalid stored theme values = %+v, want defaults", p)
	}
}

// The server's allowlist and the web UI's built-in themes must agree: a theme
// the server rejects could not be saved, and one the UI lacks would be shown
// as the default.
func TestThemesMatchFrontend(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "services", "themes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	var ui []string
	for _, m := range regexp.MustCompile(`(?m)^\s*id: '([a-z0-9-]+)',$`).FindAllStringSubmatch(string(src), -1) {
		ui = append(ui, m[1])
	}
	if strings.Join(ui, ",") != strings.Join(Themes, ",") {
		t.Errorf("web UI themes %v, server allowlist %v", ui, Themes)
	}
	if Defaults().Theme != Themes[0] || !strings.Contains(string(src), "DEFAULT_THEME: ThemeId = '"+Themes[0]+"'") {
		t.Errorf("default theme differs between server (%q) and web UI", Defaults().Theme)
	}
}

func TestPrimaryAndSecondaryColors(t *testing.T) {
	s, conn, u := newStore(t)
	// A user who saved only an accent before these keys existed: the row is
	// read as before, and the new colors default to "" (theme's).
	if _, err := conn.Exec(`INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, 'accent_color', '"#ff8800"', 1)`, u); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Get(bg, u); err != nil || p.AccentColor != "#ff8800" || p.PrimaryColor != "" || p.SecondaryColor != "" {
		t.Fatalf("existing accent = %+v, %v", p, err)
	}

	p, err := s.Update(bg, u, raw(map[string]any{"primary_color": "#3a1fd6", "secondary_color": "#262626"}))
	if err != nil || p.PrimaryColor != "#3a1fd6" || p.SecondaryColor != "#262626" || p.AccentColor != "#ff8800" {
		t.Fatalf("Update = %+v, %v; the saved accent must be kept", p, err)
	}

	for _, key := range []string{"primary_color", "secondary_color"} {
		for _, bad := range []any{"red", "#FFF", "#ff8800aa", "url(x)", "#ff8800;}body{display:none", 1, nil} {
			var in *InputError
			if _, err := s.Update(bg, u, raw(map[string]any{key: bad})); !errors.As(err, &in) {
				t.Errorf("%s=%v: error = %v, want InputError", key, bad, err)
			}
		}
	}
	// One invalid value rejects the whole request.
	if _, err := s.Update(bg, u, raw(map[string]any{"accent_color": "#00ff00", "primary_color": "blue"})); err == nil {
		t.Error("mixed valid and invalid colors were accepted")
	}
	if p, _ := s.Get(bg, u); p.AccentColor != "#ff8800" || p.PrimaryColor != "#3a1fd6" || p.SecondaryColor != "#262626" {
		t.Errorf("after rejected updates = %+v", p)
	}

	p, err = s.Update(bg, u, raw(map[string]any{"primary_color": "", "secondary_color": "", "accent_color": ""}))
	if err != nil || p != Defaults() {
		t.Errorf("reset = %+v, %v; want defaults", p, err)
	}
}
