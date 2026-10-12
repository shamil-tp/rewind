// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/prefs"
)

// maxReorderBodyBytes fits a reorder request listing MaxPlaylistTracks IDs.
const maxReorderBodyBytes = 256 << 10

// playlistAPI serves playlists and preferences. Every route is wrapped in
// requireAuth, and every query is scoped to the signed-in user, so another
// user's playlist is indistinguishable from a missing one (404).
type playlistAPI struct {
	playlists *library.Playlists
	prefs     *prefs.Store
	logger    *slog.Logger
}

func (p *playlistAPI) internalError(w http.ResponseWriter, action string, err error) {
	p.logger.Error("request failed", "action", action, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func validationFailed(w http.ResponseWriter, format string, args ...any) {
	writeError(w, http.StatusUnprocessableEntity, "validation_failed", fmt.Sprintf(format, args...))
}

// user returns the signed-in user. requireAuth guarantees one; a missing user
// is a programming error and fails closed.
func (p *playlistAPI) user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		p.internalError(w, "playlist request", errors.New("no user in request context"))
	}
	return u, ok
}

func pathID(w http.ResponseWriter, r *http.Request, name, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		invalidParam(w, "%s id must be a positive integer", what)
		return 0, false
	}
	return id, true
}

// playlistError writes the response for an error from library.Playlists.
func (p *playlistAPI) playlistError(w http.ResponseWriter, action string, err error) {
	var input *library.PlaylistInputError
	switch {
	case errors.As(err, &input):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", input.Msg)
	case errors.Is(err, library.ErrPlaylistNotFound):
		writeError(w, http.StatusNotFound, "not_found", "playlist not found")
	case errors.Is(err, library.ErrTrackNotFound):
		writeError(w, http.StatusNotFound, "track_not_found", "track not found in the library")
	case errors.Is(err, library.ErrNotInPlaylist):
		writeError(w, http.StatusNotFound, "not_in_playlist", err.Error())
	case errors.Is(err, library.ErrPlaylistNameTaken):
		writeError(w, http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, library.ErrTooManyPlaylists):
		writeError(w, http.StatusConflict, "too_many_playlists", err.Error())
	case errors.Is(err, library.ErrPlaylistFull):
		writeError(w, http.StatusConflict, "playlist_full", err.Error())
	case errors.Is(err, library.ErrAlreadyInPlaylist):
		writeError(w, http.StatusConflict, "already_in_playlist", err.Error())
	case errors.Is(err, library.ErrOrderMismatch):
		writeError(w, http.StatusConflict, "order_mismatch", err.Error()+"; reload the playlist and try again")
	default:
		p.internalError(w, action, err)
	}
}

// --- /api/playlists ---

type createPlaylistRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (p *playlistAPI) collection(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := p.user(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		items, err := p.playlists.List(r.Context(), u.ID)
		if err != nil {
			p.internalError(w, "listing playlists", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	var req createPlaylistRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		validationFailed(w, "name is required")
		return
	}
	desc := ""
	if req.Description != nil {
		desc = *req.Description
	}
	pl, err := p.playlists.Create(r.Context(), u.ID, *req.Name, desc)
	if err != nil {
		p.playlistError(w, "creating playlist", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"playlist": pl})
}

// --- /api/playlists/{id} ---

type updatePlaylistRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (p *playlistAPI) item(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := pathID(w, r, "id", "playlist")
	if !ok {
		return
	}
	u, ok := p.user(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req updatePlaylistRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == nil && req.Description == nil {
			validationFailed(w, "give a new name or description")
			return
		}
		pl, err := p.playlists.Update(r.Context(), u.ID, id, library.PlaylistUpdate{Name: req.Name, Description: req.Description})
		if err != nil {
			p.playlistError(w, "updating playlist", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"playlist": pl})
	case http.MethodDelete:
		if err := p.playlists.Delete(r.Context(), u.ID, id); err != nil {
			p.playlistError(w, "deleting playlist", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		d, err := p.playlists.Get(r.Context(), u.ID, id)
		if err != nil {
			p.playlistError(w, "reading playlist", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"playlist": d})
	}
}

// --- /api/playlists/{id}/tracks ---

type addTrackRequest struct {
	TrackID int64 `json:"track_id"`
}

type reorderRequest struct {
	TrackIDs []int64 `json:"track_ids"`
}

func (p *playlistAPI) tracks(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := pathID(w, r, "id", "playlist")
	if !ok {
		return
	}
	u, ok := p.user(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var req addTrackRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.TrackID < 1 {
			validationFailed(w, "track_id must be a positive integer")
			return
		}
		e, err := p.playlists.AddTrack(r.Context(), u.ID, id, req.TrackID)
		if err != nil {
			p.playlistError(w, "adding playlist track", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"entry": e})
		return
	}

	// PUT: replace the order.
	var req reorderRequest
	if !decodeJSONLimit(w, r, &req, maxReorderBodyBytes) {
		return
	}
	if req.TrackIDs == nil {
		validationFailed(w, "track_ids is required")
		return
	}
	if len(req.TrackIDs) > library.MaxPlaylistTracks {
		validationFailed(w, "track_ids may list at most %d tracks", library.MaxPlaylistTracks)
		return
	}
	for _, t := range req.TrackIDs {
		if t < 1 {
			validationFailed(w, "track_ids must contain positive integers")
			return
		}
	}
	if err := p.playlists.Reorder(r.Context(), u.ID, id, req.TrackIDs); err != nil {
		p.playlistError(w, "reordering playlist", err)
		return
	}
	d, err := p.playlists.Get(r.Context(), u.ID, id)
	if err != nil {
		p.playlistError(w, "reading playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlist": d})
}

// --- DELETE /api/playlists/{id}/tracks/{trackId} ---

func (p *playlistAPI) entry(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := pathID(w, r, "id", "playlist")
	if !ok {
		return
	}
	trackID, ok := pathID(w, r, "trackId", "track")
	if !ok {
		return
	}
	u, ok := p.user(w, r)
	if !ok {
		return
	}
	if err := p.playlists.RemoveTrack(r.Context(), u.ID, id, trackID); err != nil {
		p.playlistError(w, "removing playlist track", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- /api/preferences ---

func (p *playlistAPI) preferences(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := p.user(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		v, err := p.prefs.Get(r.Context(), u.ID)
		if err != nil {
			p.internalError(w, "reading preferences", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preferences": v, "defaults": prefs.Defaults()})
		return
	}
	// Decoded as a map so unknown keys reach the allowlist check and get a
	// message naming them.
	var changes map[string]json.RawMessage
	if !decodeJSON(w, r, &changes) {
		return
	}
	v, err := p.prefs.Update(r.Context(), u.ID, changes)
	var input *prefs.InputError
	switch {
	case errors.As(err, &input):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", input.Msg)
		return
	case err != nil:
		p.internalError(w, "saving preferences", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": v, "defaults": prefs.Defaults()})
}
