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
	// Theme is the ID of the active theme: a bundled or installed theme
	// (internal/theme). The theme API (/api/themes) changes it; an ID that is
	// no longer available is read as the default.
	Theme string `json:"theme"`
	// AccentColor, PrimaryColor and SecondaryColor are the custom colors of
	// Rewind 0.x before themes could be customized one by one. They are kept
	// unchanged and still apply to every theme the user has not customized
	// (internal/theme, "legacy" customization).
	//
	// AccentColor replaces the theme's accent (progress, playing markers,
	// focus ring) when set: "" (use the theme's) or a lower-case "#rrggbb"
	// color. The web UI also checks its contrast against the theme in use and
	// ignores it where it would be unreadable.
	AccentColor string `json:"accent_color"`
	// PrimaryColor replaces the theme's primary color (main buttons, links,
	// selected navigation); same format and checks as AccentColor. While it
	// is "", the web UI uses a custom accent for it, as before this key
	// existed.
	PrimaryColor string `json:"primary_color"`
	// SecondaryColor replaces the theme's secondary color (secondary buttons
	// and supporting surfaces); same format.
	SecondaryColor string `json:"secondary_color"`
	// PlayThresholdPercent is how much of a track (in percent of its length,
	// at most four minutes) must be played before it counts as played and
	// is added to the listening history.
	PlayThresholdPercent int `json:"play_threshold_percent"`
	// RecordHistory turns listening history on; false is a privacy mode in
	// which plays are not recorded.
	RecordHistory bool `json:"record_history"`
}

// Defaults returns the values used when nothing was saved.
func Defaults() Preferences {
	return Preferences{LibrarySort: "artist", AutoplayNext: true, Theme: "rewind-dark", PlayThresholdPercent: 50, RecordHistory: true}
}

// LibrarySorts lists the accepted library_sort values.
var LibrarySorts = []string{"artist", "album", "title", "added", "year", "duration"}

// Themes lists the bundled theme IDs. A Store accepts these unless
// WithThemes gives it the full registry (internal/theme), as Rewind does.
var Themes = []string{"rewind-dark", "light", "oled-black"}

// colorPattern is the only accepted custom color format. Anything else,
// including CSS keywords, functions, url() and short or upper-case hex, is
// rejected, so the value can never be more than a color.
var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// color validates a custom color preference: "" or "#rrggbb".
func color(raw json.RawMessage, dst *string) error {
	var v string
	if err := decode(raw, &v); err != nil || (v != "" && !colorPattern.MatchString(v)) {
		return fmt.Errorf(`must be "" or a color like "#c6ff00" (six lower-case hex digits)`)
	}
	*dst = v
	return nil
}

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
		return color(raw, &p.AccentColor)
	}},
	"primary_color": {apply: func(raw json.RawMessage, p *Preferences) error {
		return color(raw, &p.PrimaryColor)
	}},
	"secondary_color": {apply: func(raw json.RawMessage, p *Preferences) error {
		return color(raw, &p.SecondaryColor)
	}},
	"play_threshold_percent": {apply: func(raw json.RawMessage, p *Preferences) error {
		var v int
		if err := decode(raw, &v); err != nil || v < 10 || v > 100 {
			return fmt.Errorf("must be a whole number from 10 to 100")
		}
		p.PlayThresholdPercent = v
		return nil
	}},
	"record_history": {apply: func(raw json.RawMessage, p *Preferences) error {
		var v bool
		if err := decode(raw, &v); err != nil {
			return fmt.Errorf("must be true or false")
		}
		p.RecordHistory = v
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
	// themeOK reports whether a theme ID may be saved and used.
	themeOK func(id string) bool
}

// Option configures a Store.
type Option func(*Store)

// WithThemes makes the store accept exactly the theme IDs for which ok
// returns true (the available themes). Without it, Themes is used.
func WithThemes(ok func(id string) bool) Option {
	return func(s *Store) { s.themeOK = ok }
}

// NewStore returns a Store over the application database.
func NewStore(db *sql.DB, opts ...Option) *Store {
	s := &Store{db: db, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// apply validates one key's value into p. The theme key is checked against
// the store's theme list.
func (s *Store) apply(k string, spec key, raw json.RawMessage, p *Preferences) error {
	if k == "theme" && s.themeOK != nil {
		var v string
		if decode(raw, &v) != nil || !s.themeOK(v) {
			return fmt.Errorf("must be the ID of an available theme")
		}
		p.Theme = v
		return nil
	}
	return spec.apply(raw, p)
}

// StoredTheme returns the theme ID saved for the user, even if it is no
// longer available, so the theme API can explain why another one is shown.
// ok is false when none is saved.
func (s *Store) StoredTheme(ctx context.Context, userID int64) (id string, ok bool, err error) {
	return storedTheme(ctx, s.db, userID)
}

// StoredThemeTx is StoredTheme inside the caller's transaction.
func (s *Store) StoredThemeTx(ctx context.Context, tx *sql.Tx, userID int64) (id string, ok bool, err error) {
	return storedTheme(ctx, tx, userID)
}

func storedTheme(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) (id string, ok bool, err error) {
	var raw string
	err = q.QueryRowContext(ctx, "SELECT value FROM user_preferences WHERE user_id = ? AND key = 'theme'", userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if json.Unmarshal([]byte(raw), &id) != nil {
		return "", false, nil
	}
	return id, true, nil
}

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
			if s.apply(k, spec, json.RawMessage(v), &next) == nil {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Preferences{}, err
	}
	defer tx.Rollback()
	if err := s.UpdateTx(ctx, tx, userID, changes); err != nil {
		return Preferences{}, err
	}
	if err := tx.Commit(); err != nil {
		return Preferences{}, err
	}
	return s.Get(ctx, userID)
}

// UpdateTx is Update inside the caller's transaction, for changes that must
// be saved together with other data (the theme store saves the active theme
// this way). It returns an *InputError for invalid changes; the caller
// commits or rolls back.
func (s *Store) UpdateTx(ctx context.Context, tx *sql.Tx, userID int64, changes map[string]json.RawMessage) error {
	normalized, err := s.validate(changes)
	if err != nil {
		return err
	}
	now := s.now().Unix()
	for _, k := range sortedKeys(normalized) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			userID, k, normalized[k], now); err != nil {
			return err
		}
	}
	return nil
}

// validate checks every change and returns each value's canonical JSON.
func (s *Store) validate(changes map[string]json.RawMessage) (map[string]string, error) {
	if len(changes) == 0 {
		return nil, &InputError{"no preferences given; known keys: " + strings.Join(Keys(), ", ")}
	}
	var scratch Preferences
	normalized := make(map[string]string, len(changes))
	for _, k := range sortedKeys(changes) {
		spec, ok := keys[k]
		if !ok {
			return nil, &InputError{fmt.Sprintf("unknown preference %q; known keys: %s", truncate(k, 40), strings.Join(Keys(), ", "))}
		}
		if err := s.apply(k, spec, changes[k], &scratch); err != nil {
			return nil, &InputError{fmt.Sprintf("%s %v", k, err)}
		}
		// Store the canonical JSON form, not the client's formatting.
		var v any
		if err := json.Unmarshal(changes[k], &v); err != nil {
			return nil, err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		normalized[k] = string(b)
	}
	return normalized, nil
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
