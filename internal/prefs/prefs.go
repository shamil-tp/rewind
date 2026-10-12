// SPDX-License-Identifier: Apache-2.0

// Package prefs stores per-user preferences. Only the keys defined here can
// be stored; each has a default and a validator. Stored values that are no
// longer valid (for example after an upgrade changed the allowed values) are
// ignored and the default is used, so a bad row never breaks the UI.
package prefs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Preferences holds every preference with its effective value.
type Preferences struct {
	// LibrarySort is the library's initial sort choice. The values match the
	// web UI's sort menu ("added" sorts newest first, "duration" longest
	// first, the others ascending).
	LibrarySort string `json:"library_sort"`
	// AutoplayNext continues a playlist with its next playable track when a
	// track ends.
	AutoplayNext bool `json:"autoplay_next"`
	// Theme is the ID of a built-in color theme (web/src/services/themes.ts).
	Theme string `json:"theme"`
	// AccentColor replaces the theme's accent when set: "" (use the theme's)
	// or a lower-case "#rrggbb" color. The web UI also checks its contrast
	// against the theme in use and ignores it where it would be unreadable.
	AccentColor string `json:"accent_color"`
}

// Defaults returns the values used when nothing was saved.
func Defaults() Preferences {
	return Preferences{LibrarySort: "artist", AutoplayNext: true, Theme: "rewind-dark", AccentColor: ""}
}

// LibrarySorts lists the accepted library_sort values.
var LibrarySorts = []string{"artist", "album", "title", "added", "year", "duration"}

// Themes lists the built-in theme IDs. They must match the themes defined
// in web/src/services/themes.ts; TestThemesMatchFrontend checks this.
var Themes = []string{"rewind-dark", "light", "oled-black"}

// accentPattern is the only accepted custom color format. Anything else,
// including CSS keywords, functions, url() and short or upper-case hex, is
// rejected, so the value can never be more than a color.
var accentPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func oneOf(raw json.RawMessage, allowed []string, dst *string) error {
	var v string
	if err := decode(raw, &v); err != nil {
		return fmt.Errorf("must be a string")
	}
	for _, s := range allowed {
		if v == s {
			*dst = v
			return nil
		}
	}
	return fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
}

// key describes one preference: how to validate a JSON value and where to
// put it.
type key struct {
	apply func(raw json.RawMessage, p *Preferences) error
}

var keys = map[string]key{
	"library_sort": {apply: func(raw json.RawMessage, p *Preferences) error {
		return oneOf(raw, LibrarySorts, &p.LibrarySort)
	}},
	"theme": {apply: func(raw json.RawMessage, p *Preferences) error {
		return oneOf(raw, Themes, &p.Theme)
	}},
	"accent_color": {apply: func(raw json.RawMessage, p *Preferences) error {
		var v string
		if err := decode(raw, &v); err != nil || (v != "" && !accentPattern.MatchString(v)) {
			return fmt.Errorf(`must be "" or a color like "#c6ff00" (six lower-case hex digits)`)
		}
		p.AccentColor = v
		return nil
	}},
	"autoplay_next": {apply: func(raw json.RawMessage, p *Preferences) error {
		var v bool
		if err := decode(raw, &v); err != nil {
			return fmt.Errorf("must be true or false")
		}
		p.AutoplayNext = v
		return nil
	}},
}

// decode is json.Unmarshal that also rejects null, which Unmarshal would
// accept by leaving dst unchanged.
func decode(raw json.RawMessage, dst any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null")
	}
	return json.Unmarshal(raw, dst)
}

// Keys returns the known preference keys, sorted.
func Keys() []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// InputError reports an unknown key or an invalid value. Msg is meant for
// users.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Store reads and writes preferences in the user_preferences table.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store over the application database.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// Get returns the user's preferences: saved values where valid, defaults
// elsewhere. Unknown keys and invalid stored values are skipped.
func (s *Store) Get(ctx context.Context, userID int64) (Preferences, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM user_preferences WHERE user_id = ?", userID)
	if err != nil {
		return Preferences{}, err
	}
	defer rows.Close()
	p := Defaults()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Preferences{}, err
		}
		if spec, ok := keys[k]; ok {
			next := p
			if spec.apply(json.RawMessage(v), &next) == nil {
				p = next
			}
		}
	}
	return p, rows.Err()
}

// Update validates every change first, then saves them all in one
// transaction and returns the resulting preferences. Nothing is saved if
// any key or value is invalid.
func (s *Store) Update(ctx context.Context, userID int64, changes map[string]json.RawMessage) (Preferences, error) {
	if len(changes) == 0 {
		return Preferences{}, &InputError{"no preferences given; known keys: " + strings.Join(Keys(), ", ")}
	}
	var scratch Preferences
	normalized := make(map[string]string, len(changes))
	for _, k := range sortedKeys(changes) {
		spec, ok := keys[k]
		if !ok {
			return Preferences{}, &InputError{fmt.Sprintf("unknown preference %q; known keys: %s", truncate(k, 40), strings.Join(Keys(), ", "))}
		}
		if err := spec.apply(changes[k], &scratch); err != nil {
			return Preferences{}, &InputError{fmt.Sprintf("%s %v", k, err)}
		}
		// Store the canonical JSON form, not the client's formatting.
		var v any
		if err := json.Unmarshal(changes[k], &v); err != nil {
			return Preferences{}, err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return Preferences{}, err
		}
		normalized[k] = string(b)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preferences{}, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	for _, k := range sortedKeys(normalized) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			userID, k, normalized[k], now); err != nil {
			return Preferences{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Preferences{}, err
	}
	return s.Get(ctx, userID)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
