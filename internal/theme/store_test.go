// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/db"
	"github.com/shamil-tp/rewind/internal/prefs"
)

var bg = context.Background()

type env struct {
	conn *sql.DB
	reg  *Registry
	user int64
	path string
}

func newEnv(t *testing.T, themeDir string) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), db.FileName)
	conn, err := db.Open(bg, path, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	res, err := conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('owner', 'h', 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	reg, err := Load(themeDir, discard)
	if err != nil {
		t.Fatal(err)
	}
	return &env{conn: conn, reg: reg, user: id, path: path}
}

// store returns a store whose clock advances one second per call, so the
// order of applied themes is well defined.
func (e *env) store() (*Store, *prefs.Store) {
	p := prefs.NewStore(e.conn, prefs.WithThemes(e.reg.Has))
	s := NewStore(e.conn, e.reg, p)
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return s, p
}

func item(t *testing.T, st UserState, id string) UserTheme {
	t.Helper()
	for _, it := range st.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("theme %s not listed", id)
	return UserTheme{}
}

func rawValues(m map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

func TestStateDefaults(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	st, err := s.State(bg, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if st.Active != DefaultID || st.ThemeAPI != APIVersion || st.DirectoryConfigured || st.Notice != "" || len(st.Items) != 3 {
		t.Fatalf("state = %+v", st)
	}
	dark := item(t, st, DefaultID)
	if !dark.Active || !dark.Enabled || dark.CustomizationSource != CustomizationNone || len(dark.Customization) != 0 || dark.Tokens["color.accent"] != "#c6ff00" {
		t.Errorf("rewind-dark = %+v", dark)
	}
}

func TestApplyPersistsAndValidates(t *testing.T) {
	e := newEnv(t, "")
	s, p := e.store()
	st, err := s.Apply(bg, e.user, "light")
	if err != nil || st.Active != "light" || !item(t, st, "light").Active || item(t, st, DefaultID).Active {
		t.Fatalf("Apply = %+v, %v", st.Active, err)
	}
	// The active theme is the existing "theme" preference.
	if got, _ := p.Get(bg, e.user); got.Theme != "light" {
		t.Errorf("theme preference = %q", got.Theme)
	}
	if _, err := s.Apply(bg, e.user, "solarized"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown theme: %v", err)
	}
	// A new store (a restart) reads the same state.
	s2, _ := e.store()
	if st, _ := s2.State(bg, e.user); st.Active != "light" {
		t.Errorf("after restart active = %q", st.Active)
	}
}

func TestDisableReturnsToPreviousTheme(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	s.Apply(bg, e.user, "oled-black")
	s.Apply(bg, e.user, "light")

	st, err := s.SetEnabled(bg, e.user, "light", false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Active != "oled-black" || item(t, st, "light").Enabled {
		t.Fatalf("after disabling the active theme: active %q, light enabled %v", st.Active, item(t, st, "light").Enabled)
	}
	if _, err := s.Apply(bg, e.user, "light"); !errors.Is(err, ErrDisabled) {
		t.Errorf("applying a disabled theme: %v", err)
	}
	// Disabling the fallback too leaves the default.
	if st, _ = s.SetEnabled(bg, e.user, "oled-black", false); st.Active != DefaultID {
		t.Errorf("active = %q, want the default", st.Active)
	}
	var in *InputError
	if _, err := s.SetEnabled(bg, e.user, DefaultID, false); !errors.As(err, &in) {
		t.Errorf("disabling the default: %v", err)
	}
	// Disabling an inactive theme does not change the active one.
	s.SetEnabled(bg, e.user, "light", true)
	s.Apply(bg, e.user, "light")
	s.SetEnabled(bg, e.user, "oled-black", true)
	if st, _ = s.SetEnabled(bg, e.user, "oled-black", false); st.Active != "light" {
		t.Errorf("active = %q", st.Active)
	}
	if st, _ = s.SetEnabled(bg, e.user, "oled-black", true); !item(t, st, "oled-black").Enabled {
		t.Error("re-enabling failed")
	}
}

func TestDisabledThemeSavedThroughPreferencesFallsBack(t *testing.T) {
	e := newEnv(t, "")
	s, p := e.store()
	s.SetEnabled(bg, e.user, "light", false)
	// The preferences API still accepts any available theme ID.
	if _, err := p.Update(bg, e.user, rawValues(map[string]any{"theme": "light"})); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State(bg, e.user)
	if st.Active != DefaultID || !strings.Contains(st.Notice, "disabled") {
		t.Errorf("active %q, notice %q", st.Active, st.Notice)
	}
}

func TestCustomizationIsPerTheme(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	st, err := s.SaveCustomization(bg, e.user, "light", rawValues(map[string]any{"color.accent": "#0055aa", "radius.button": 4, "font.family": "serif"}))
	if err != nil {
		t.Fatal(err)
	}
	light := item(t, st, "light")
	if light.CustomizationSource != CustomizationSaved || light.Customization["color.accent"] != "#0055aa" ||
		light.Customization["radius.button"] != 4.0 || light.Customization["font.family"] != "serif" {
		t.Fatalf("light = %+v", light.Customization)
	}
	// Other themes are untouched.
	if d := item(t, st, DefaultID); len(d.Customization) != 0 {
		t.Errorf("rewind-dark changed: %v", d.Customization)
	}
	s.SaveCustomization(bg, e.user, DefaultID, rawValues(map[string]any{"font.size": 18}))
	st, _ = s.Apply(bg, e.user, DefaultID)
	if item(t, st, "light").Customization["color.accent"] != "#0055aa" || item(t, st, DefaultID).Customization["font.size"] != 18.0 {
		t.Error("switching themes lost a customization")
	}

	// Saving replaces; values equal to the theme's are dropped; {} resets.
	st, _ = s.SaveCustomization(bg, e.user, "light", rawValues(map[string]any{"color.accent": "#3a1fd6", "font.family": "serif"}))
	if c := item(t, st, "light").Customization; len(c) != 1 || c["font.family"] != "serif" {
		t.Errorf("after replace = %v", c)
	}
	st, _ = s.SaveCustomization(bg, e.user, "light", map[string]json.RawMessage{})
	if l := item(t, st, "light"); len(l.Customization) != 0 || l.CustomizationSource != CustomizationNone {
		t.Errorf("after reset = %+v", l)
	}
}

func TestCustomizationValidation(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	s.SaveCustomization(bg, e.user, "light", rawValues(map[string]any{"font.size": 18}))
	for name, values := range map[string]map[string]any{
		"unknown token":  {"color.glow": "#ffffff"},
		"css":            {"color.accent": "red;}body{display:none"},
		"url":            {"color.background": "url(https://evil.example/x)"},
		"range":          {"radius.button": 99},
		"derived":        {"color.on-primary": "#ffffff"},
		"theme-only":     {"color.danger": "#ff0000"},
		"one bad of two": {"font.size": 17, "border.width": 9},
		"choice":         {"font.family": "Papyrus"},
		"wrong type":     {"font.size": "17px"},
	} {
		var in *InputError
		if _, err := s.SaveCustomization(bg, e.user, "light", rawValues(values)); !errors.As(err, &in) {
			t.Errorf("%s: error = %v, want InputError", name, err)
		}
	}
	// Nothing invalid was saved; the earlier value is intact.
	st, _ := s.State(bg, e.user)
	if c := item(t, st, "light").Customization; len(c) != 1 || c["font.size"] != 18.0 {
		t.Errorf("customization = %v", c)
	}
	var in *InputError
	// OLED does not let users change its black background.
	if _, err := s.SaveCustomization(bg, e.user, "oled-black", rawValues(map[string]any{"color.background": "#111111"})); !errors.As(err, &in) {
		t.Errorf("OLED background: %v", err)
	}
	if _, err := s.SaveCustomization(bg, e.user, "solarized", map[string]json.RawMessage{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown theme: %v", err)
	}
	if _, err := s.SaveCustomization(bg, e.user, "light", nil); !errors.As(err, &in) {
		t.Errorf("nil tokens: %v", err)
	}
}

func TestLegacyColorsArePreserved(t *testing.T) {
	e := newEnv(t, "")
	s, p := e.store()
	// A user of the previous version: only an accent, and a secondary.
	if _, err := p.Update(bg, e.user, rawValues(map[string]any{"accent_color": "#ff8800", "secondary_color": "#004466"})); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State(bg, e.user)
	for _, id := range []string{DefaultID, "light"} {
		it := item(t, st, id)
		// The unset primary follows the accent, exactly as before.
		if it.CustomizationSource != CustomizationLegacy || it.Customization["color.accent"] != "#ff8800" ||
			it.Customization["color.primary"] != "#ff8800" || it.Customization["color.secondary"] != "#004466" {
			t.Errorf("%s = %+v %v", id, it.CustomizationSource, it.Customization)
		}
	}
	// Customizing one theme replaces the legacy colors for that theme only.
	st, _ = s.SaveCustomization(bg, e.user, "light", rawValues(map[string]any{"font.size": 17}))
	if c := item(t, st, "light").Customization; len(c) != 1 {
		t.Errorf("light = %v", c)
	}
	if item(t, st, DefaultID).CustomizationSource != CustomizationLegacy {
		t.Error("rewind-dark lost its legacy colors")
	}
	// The legacy preferences themselves are never changed.
	if got, _ := p.Get(bg, e.user); got.AccentColor != "#ff8800" || got.SecondaryColor != "#004466" {
		t.Errorf("legacy preferences = %+v", got)
	}
}

func TestRemovedThemeFallsBackAndKeepsData(t *testing.T) {
	dir := t.TempDir()
	install(t, dir, "sunset", encode(t, manifest("sunset")))
	e := newEnv(t, dir)
	s, _ := e.store()
	if _, err := s.SaveCustomization(bg, e.user, "sunset", rawValues(map[string]any{"font.size": 19})); err != nil {
		t.Fatal(err)
	}
	if st, err := s.Apply(bg, e.user, "sunset"); err != nil || st.Active != "sunset" || !st.DirectoryConfigured {
		t.Fatalf("apply installed theme: %+v, %v", st, err)
	}

	// The administrator removes the theme and restarts Rewind.
	e.reg, _ = Load(t.TempDir(), discard)
	s, _ = e.store()
	st, err := s.State(bg, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if st.Active != DefaultID || !strings.Contains(st.Notice, "sunset") || !strings.Contains(st.Notice, "not available") {
		t.Errorf("active %q, notice %q", st.Active, st.Notice)
	}
	// Its settings are kept and return when it is installed again.
	var tokens string
	if err := e.conn.QueryRow("SELECT tokens FROM theme_settings WHERE theme_id = 'sunset'").Scan(&tokens); err != nil || !strings.Contains(tokens, "19") {
		t.Errorf("stored tokens = %q, %v", tokens, err)
	}
	reinstalled := t.TempDir()
	install(t, reinstalled, "sunset", encode(t, manifest("sunset")))
	e.reg, _ = Load(reinstalled, discard)
	s, _ = e.store()
	if st, _ = s.State(bg, e.user); st.Active != "sunset" || item(t, st, "sunset").Customization["font.size"] != 19.0 {
		t.Errorf("after reinstall: active %q, %+v", st.Active, item(t, st, "sunset").Customization)
	}
}

func TestStoredCustomizationIsRevalidated(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	// A row written by hand or by another version with bad values.
	if _, err := e.conn.Exec(`INSERT INTO theme_settings (user_id, theme_id, tokens, updated_at) VALUES (?, 'light', ?, 1)`,
		e.user, `{"color.accent":"red;}x{","font.size":18,"color.glow":"#ffffff","color.danger":"#000000"}`); err != nil {
		t.Fatal(err)
	}
	st, err := s.State(bg, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if c := item(t, st, "light").Customization; len(c) != 1 || c["font.size"] != 18.0 {
		t.Errorf("customization = %v", c)
	}
}

func TestThemeSettingsArePerUser(t *testing.T) {
	e := newEnv(t, "")
	s, _ := e.store()
	res, _ := e.conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('other', 'h', 1, 1, 1)`)
	other, _ := res.LastInsertId()
	s.Apply(bg, e.user, "light")
	s.SetEnabled(bg, e.user, "oled-black", false)
	s.SaveCustomization(bg, e.user, DefaultID, rawValues(map[string]any{"font.size": 18}))
	st, _ := s.State(bg, other)
	if st.Active != DefaultID || !item(t, st, "oled-black").Enabled || len(item(t, st, DefaultID).Customization) != 0 {
		t.Errorf("other user's state = %+v", st)
	}
}

func TestThemeOperationsLeaveOtherDataAlone(t *testing.T) {
	e := newEnv(t, "")
	s, p := e.store()
	p.Update(bg, e.user, rawValues(map[string]any{"library_sort": "year", "autoplay_next": false}))
	s.Apply(bg, e.user, "light")
	s.SaveCustomization(bg, e.user, "light", rawValues(map[string]any{"font.size": 18}))
	s.SetEnabled(bg, e.user, "light", false)
	if got, _ := p.Get(bg, e.user); got.LibrarySort != "year" || got.AutoplayNext {
		t.Errorf("preferences = %+v", got)
	}
}
