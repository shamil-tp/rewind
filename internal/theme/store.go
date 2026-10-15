// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/shamil-tp/rewind/internal/prefs"
)

// MaxCustomizationBytes bounds the stored JSON of one theme's customization
// (the theme_settings.tokens column allows 4096).
const MaxCustomizationBytes = 4096

// InputError reports an invalid request. Msg is meant for users.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// ErrNotFound means the theme is not available (unknown, removed, invalid or
// incompatible).
var ErrNotFound = errors.New("theme not found or not available")

// ErrDisabled means the theme is disabled and must be enabled first.
var ErrDisabled = errors.New("theme is disabled")

// Customization sources.
const (
	// CustomizationNone: the theme's own values.
	CustomizationNone = "none"
	// CustomizationSaved: values the user saved for this theme.
	CustomizationSaved = "saved"
	// CustomizationLegacy: the theme was never customized, so the custom
	// colors saved before per-theme customization existed apply.
	CustomizationLegacy = "legacy"
)

// UserTheme is one theme as the user sees it. Text fields come from the
// manifest and are untrusted: they must be shown as plain text.
type UserTheme struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Scheme      string `json:"scheme,omitempty"`
	Source      Source `json:"source"`
	State       State  `json:"state"`
	// Error is Rewind's summary of why the theme cannot be used.
	Error   string `json:"error,omitempty"`
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"`
	// Tokens are the theme's own values (every token), Customizable the
	// tokens the user may change. Both are omitted for unavailable themes.
	Tokens       map[string]any `json:"tokens,omitempty"`
	Customizable []string       `json:"customizable,omitempty"`
	// Customization holds the user's values for this theme (only
	// customizable tokens). The web UI checks colors for readability and
	// ignores unreadable ones.
	Customization       map[string]any `json:"customization"`
	CustomizationSource string         `json:"customization_source"`
}

// UserState is everything the web UI needs about themes for one user.
type UserState struct {
	ThemeAPI int `json:"theme_api"`
	// DirectoryConfigured is true when the administrator set
	// REWIND_THEME_DIR. The path itself is never sent.
	DirectoryConfigured bool `json:"directory_configured"`
	// Active is the theme shown: always an available, enabled theme.
	Active       string `json:"active"`
	DefaultTheme string `json:"default_theme"`
	// Notice explains why Active is not the saved theme, or is "".
	Notice string      `json:"notice,omitempty"`
	Items  []UserTheme `json:"items"`
}

// Store keeps per-user theme settings in the theme_settings table and the
// active theme in the "theme" preference.
type Store struct {
	db    *sql.DB
	reg   *Registry
	prefs *prefs.Store
	now   func() time.Time
}

// NewStore returns a Store. prefs must accept the registry's theme IDs
// (prefs.WithThemes(reg.Has)).
func NewStore(db *sql.DB, reg *Registry, p *prefs.Store) *Store {
	return &Store{db: db, reg: reg, prefs: p, now: time.Now}
}

// row is one theme_settings row.
type row struct {
	enabled       bool
	tokens        sql.NullString
	lastAppliedAt sql.NullInt64
}

func (s *Store) rows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, userID int64) (map[string]row, error) {
	rs, err := q.QueryContext(ctx, "SELECT theme_id, enabled, tokens, last_applied_at FROM theme_settings WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]row{}
	for rs.Next() {
		var id string
		var r row
		if err := rs.Scan(&id, &r.enabled, &r.tokens, &r.lastAppliedAt); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rs.Err()
}

func enabled(rows map[string]row, id string) bool {
	r, ok := rows[id]
	return !ok || r.enabled || id == DefaultID
}

// State returns the user's theme state.
func (s *Store) State(ctx context.Context, userID int64) (UserState, error) {
	rows, err := s.rows(ctx, s.db, userID)
	if err != nil {
		return UserState{}, err
	}
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return UserState{}, err
	}
	stored, saved, err := s.prefs.StoredTheme(ctx, userID)
	if err != nil {
		return UserState{}, err
	}

	st := UserState{ThemeAPI: APIVersion, DirectoryConfigured: s.reg.DirConfigured(), DefaultTheme: DefaultID, Items: []UserTheme{}}
	// prefs.Get already replaced an unavailable theme with the default.
	st.Active = p.Theme
	switch {
	case saved && !s.reg.Has(stored):
		st.Notice = fmt.Sprintf("Your saved theme (%s) is not available any more, so %s is shown.", truncate(stored, maxIDLength), s.name(DefaultID))
	case !enabled(rows, st.Active):
		st.Notice = fmt.Sprintf("Your saved theme (%s) is disabled, so %s is shown.", s.name(st.Active), s.name(DefaultID))
		st.Active = DefaultID
	}

	for _, e := range s.reg.Entries() {
		ut := UserTheme{ID: e.ID, Source: e.Source, State: e.State, Error: e.Error, Customization: map[string]any{}, CustomizationSource: CustomizationNone}
		if t := e.Theme; t != nil {
			ut.Name, ut.Version, ut.Description, ut.Author, ut.Homepage, ut.Scheme = t.Name, t.Version, t.Description, t.Author, t.Homepage, t.Scheme
			ut.Tokens, ut.Customizable = t.Tokens, t.Customizable
			ut.Enabled = enabled(rows, t.ID)
			ut.Active = t.ID == st.Active
			ut.Customization, ut.CustomizationSource = customization(t, rows[t.ID], p)
		}
		st.Items = append(st.Items, ut)
	}
	return st, nil
}

func (s *Store) name(id string) string {
	if t, ok := s.reg.Get(id); ok {
		return t.Name
	}
	return id
}

// customization returns the user's values for t: the saved ones (invalid
// or no longer customizable values are dropped), or, if the theme was never
// customized, the legacy custom colors.
func customization(t *Theme, r row, p prefs.Preferences) (map[string]any, string) {
	out := map[string]any{}
	if r.tokens.Valid {
		var saved map[string]json.RawMessage
		if json.Unmarshal([]byte(r.tokens.String), &saved) == nil {
			for name, raw := range saved {
				if spec, ok := Lookup(name); ok && t.CanCustomize(name) {
					if v, err := spec.Validate(raw); err == nil {
						out[name] = v
					}
				}
			}
		}
		if len(out) == 0 {
			return out, CustomizationNone
		}
		return out, CustomizationSaved
	}
	// Before per-theme customization, an unset primary followed a custom
	// accent; copying that rule keeps those users' colors exactly as they
	// were.
	primary := p.PrimaryColor
	if primary == "" {
		primary = p.AccentColor
	}
	for name, v := range map[string]string{"color.primary": primary, "color.secondary": p.SecondaryColor, "color.accent": p.AccentColor} {
		if v != "" && t.CanCustomize(name) {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return out, CustomizationNone
	}
	return out, CustomizationLegacy
}

// withTx runs fn in a transaction and returns the new state.
func (s *Store) withTx(ctx context.Context, userID int64, fn func(tx *sql.Tx) error) (UserState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserState{}, err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return UserState{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserState{}, err
	}
	return s.State(ctx, userID)
}

func (s *Store) setActive(ctx context.Context, tx *sql.Tx, userID int64, id string, now int64) error {
	v, _ := json.Marshal(id)
	if err := s.prefs.UpdateTx(ctx, tx, userID, map[string]json.RawMessage{"theme": v}); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO theme_settings (user_id, theme_id, last_applied_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, theme_id) DO UPDATE SET last_applied_at = excluded.last_applied_at, updated_at = excluded.updated_at`,
		userID, id, now, now)
	return err
}

// Apply makes an available, enabled theme the active one.
func (s *Store) Apply(ctx context.Context, userID int64, id string) (UserState, error) {
	if !s.reg.Has(id) {
		return UserState{}, ErrNotFound
	}
	return s.withTx(ctx, userID, func(tx *sql.Tx) error {
		rows, err := s.rows(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !enabled(rows, id) {
			return ErrDisabled
		}
		return s.setActive(ctx, tx, userID, id, s.now().Unix())
	})
}

// SetEnabled enables or disables a theme. The default theme cannot be
// disabled. Disabling the active theme returns to the theme applied before
// it (if it is still available and enabled), otherwise to the default.
func (s *Store) SetEnabled(ctx context.Context, userID int64, id string, on bool) (UserState, error) {
	if !s.reg.Has(id) {
		return UserState{}, ErrNotFound
	}
	if id == DefaultID && !on {
		return UserState{}, &InputError{fmt.Sprintf("%s is the fallback theme and cannot be disabled", s.name(DefaultID))}
	}
	return s.withTx(ctx, userID, func(tx *sql.Tx) error {
		now := s.now().Unix()
		if _, err := tx.ExecContext(ctx, `INSERT INTO theme_settings (user_id, theme_id, enabled, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, theme_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
			userID, id, on, now); err != nil {
			return err
		}
		if on {
			return nil
		}
		active, _, err := s.prefs.StoredThemeTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		if active != id {
			return nil
		}
		rows, err := s.rows(ctx, tx, userID)
		if err != nil {
			return err
		}
		return s.setActive(ctx, tx, userID, previous(s.reg, rows, id), now)
	})
}

// previous is the most recently applied theme other than except that is
// still available and enabled, or the default theme.
func previous(reg *Registry, rows map[string]row, except string) string {
	type candidate struct {
		id string
		at int64
	}
	var cs []candidate
	for id, r := range rows {
		if id != except && r.lastAppliedAt.Valid && reg.Has(id) && enabled(rows, id) {
			cs = append(cs, candidate{id, r.lastAppliedAt.Int64})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].at != cs[j].at {
			return cs[i].at > cs[j].at
		}
		return cs[i].id < cs[j].id
	})
	if len(cs) > 0 {
		return cs[0].id
	}
	return DefaultID
}

// SaveCustomization validates and saves the user's values for one theme,
// replacing earlier ones. Values equal to the theme's own are dropped, so an
// empty map resets the theme to its defaults (and stops the legacy colors
// from applying to it). Nothing is saved if any value is invalid.
func (s *Store) SaveCustomization(ctx context.Context, userID int64, id string, values map[string]json.RawMessage) (UserState, error) {
	t, ok := s.reg.Get(id)
	if !ok {
		return UserState{}, ErrNotFound
	}
	if values == nil {
		return UserState{}, &InputError{"tokens is required (use {} to reset the theme to its defaults)"}
	}
	clean := map[string]any{}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec, ok := Lookup(name)
		if !ok {
			return UserState{}, &InputError{fmt.Sprintf("%q is not a theme token", truncate(name, 40))}
		}
		if !t.CanCustomize(name) {
			return UserState{}, &InputError{fmt.Sprintf("%s cannot be customized in %s", name, t.Name)}
		}
		v, err := spec.Validate(values[name])
		if err != nil {
			return UserState{}, &InputError{err.Error()}
		}
		if v != t.Tokens[name] {
			clean[name] = v
		}
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return UserState{}, err
	}
	if len(data) > MaxCustomizationBytes {
		return UserState{}, &InputError{"the customization is too large"}
	}
	return s.withTx(ctx, userID, func(tx *sql.Tx) error {
		now := s.now().Unix()
		_, err := tx.ExecContext(ctx, `INSERT INTO theme_settings (user_id, theme_id, tokens, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, theme_id) DO UPDATE SET tokens = excluded.tokens, updated_at = excluded.updated_at`,
			userID, id, string(data), now)
		return err
	})
}
