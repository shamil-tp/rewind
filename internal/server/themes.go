// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/prefs"
	"github.com/shamil-tp/rewind/internal/theme"
)

// maxThemeBodyBytes bounds theme request bodies; a customization is a few
// dozen token values.
const maxThemeBodyBytes = 16 << 10

// themeAPI serves the signed-in user's theme settings. Every route is
// wrapped in requireAuth. There are deliberately no routes to install,
// upload or remove themes: themes are installed by the administrator in
// REWIND_THEME_DIR (docs/themes.md).
type themeAPI struct {
	store  *theme.Store
	logger *slog.Logger
}

func (a *themeAPI) user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		a.fail(w, "theme request", errors.New("no user in request context"))
	}
	return u, ok
}

// respond maps store errors to responses. Every success returns the complete
// theme state, so the web UI has a single source of truth to replace.
func (a *themeAPI) respond(w http.ResponseWriter, action string, st theme.UserState, err error) {
	var input *theme.InputError
	var prefsInput *prefs.InputError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, st)
	case errors.Is(err, theme.ErrNotFound):
		writeError(w, http.StatusNotFound, "theme_not_found", "theme not found or not available")
	case errors.Is(err, theme.ErrDisabled):
		writeError(w, http.StatusConflict, "theme_disabled", "this theme is disabled; enable it before applying it")
	case errors.As(err, &input):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", input.Msg)
	case errors.As(err, &prefsInput):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", prefsInput.Msg)
	default:
		a.fail(w, action, err)
	}
}

func (a *themeAPI) fail(w http.ResponseWriter, action string, err error) {
	a.logger.Error("request failed", "action", action, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// themeID reads the {id} path value. Anything that is not a theme ID is
// simply not found.
func themeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !theme.ValidID(id) {
		writeError(w, http.StatusNotFound, "theme_not_found", "theme not found or not available")
		return "", false
	}
	return id, true
}

// GET /api/themes
func (a *themeAPI) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	st, err := a.store.State(r.Context(), u.ID)
	a.respond(w, "reading themes", st, err)
}

// PUT /api/themes/active {"id": "light"}
func (a *themeAPI) active(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	var body struct {
		ID *string `json:"id"`
	}
	if !decodeJSONLimit(w, r, &body, maxThemeBodyBytes) {
		return
	}
	if body.ID == nil {
		validationFailed(w, "id is required")
		return
	}
	if !theme.ValidID(*body.ID) {
		writeError(w, http.StatusNotFound, "theme_not_found", "theme not found or not available")
		return
	}
	st, err := a.store.Apply(r.Context(), u.ID, *body.ID)
	a.respond(w, "applying theme", st, err)
}

// PUT /api/themes/{id}/enabled {"enabled": false}
func (a *themeAPI) enabled(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := themeID(w, r)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSONLimit(w, r, &body, maxThemeBodyBytes) {
		return
	}
	if body.Enabled == nil {
		validationFailed(w, "enabled is required (true or false)")
		return
	}
	st, err := a.store.SetEnabled(r.Context(), u.ID, id, *body.Enabled)
	a.respond(w, "enabling theme", st, err)
}

// PUT /api/themes/{id}/customization {"tokens": {"color.accent": "#ff8800"}}
func (a *themeAPI) customization(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := themeID(w, r)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	var body struct {
		// A map, so unknown token names reach the store's check and get a
		// message naming them.
		Tokens map[string]json.RawMessage `json:"tokens"`
	}
	if !decodeJSONLimit(w, r, &body, maxThemeBodyBytes) {
		return
	}
	st, err := a.store.SaveCustomization(r.Context(), u.ID, id, body.Tokens)
	a.respond(w, "saving theme customization", st, err)
}
