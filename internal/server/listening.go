// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/prefs"
)

// listeningAPI serves albums, artists, artwork, metadata edits, favorites,
// listening history and the saved play queue. Every route is wrapped in
// requireAuth; per-user data is always scoped to the signed-in user.
type listeningAPI struct {
	catalog     *library.Catalog
	personal    *library.Personal
	editor      *library.Editor
	playback    *library.PlaybackStore
	artwork     *library.ArtworkCache
	prefs       *prefs.Store
	maxPageSize int
	logger      *slog.Logger
}

// Request body limits.
const (
	maxEditBodyBytes     = 128 << 10
	maxPlaybackBodyBytes = 512 << 10
	maxHistoryBodyBytes  = 64 << 10
)

func (a *listeningAPI) internalError(w http.ResponseWriter, action string, err error) {
	a.logger.Error("request failed", "action", action, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func (a *listeningAPI) user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		a.internalError(w, "listening request", errors.New("no user in request context"))
	}
	return u, ok
}

// fail maps library errors to responses.
func (a *listeningAPI) fail(w http.ResponseWriter, action string, err error) {
	var edit *library.EditInputError
	var hist *library.HistoryInputError
	var pb *library.PlaybackInputError
	switch {
	case errors.As(err, &edit):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", edit.Msg)
	case errors.As(err, &hist):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", hist.Msg)
	case errors.As(err, &pb):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", pb.Msg)
	case errors.Is(err, library.ErrTrackNotFound):
		writeError(w, http.StatusNotFound, "track_not_found", "track not found")
	case errors.Is(err, library.ErrAlbumNotFound):
		writeError(w, http.StatusNotFound, "not_found", "album not found")
	case errors.Is(err, library.ErrArtistNotFound):
		writeError(w, http.StatusNotFound, "not_found", "artist not found")
	case errors.Is(err, library.ErrEditConflict):
		writeError(w, http.StatusConflict, "edit_conflict", err.Error())
	case errors.Is(err, library.ErrStaleState):
		writeError(w, http.StatusConflict, "stale_state", err.Error())
	default:
		a.internalError(w, action, err)
	}
}

// paging reads page and limit.
func (a *listeningAPI) paging(w http.ResponseWriter, params map[string]string) (page, limit int64, ok bool) {
	if page, ok = intParam(w, params, "page", 1, 1, maxPage); !ok {
		return 0, 0, false
	}
	limit, ok = intParam(w, params, "limit", int64(min(defaultPageSize, a.maxPageSize)), 1, int64(a.maxPageSize))
	return page, limit, ok
}

func pageOf(page, limit, total int64) pagination {
	return pagination{Page: int(page), Limit: int(limit), Total: total, TotalPages: (total + limit - 1) / limit}
}

func orderParam(w http.ResponseWriter, params map[string]string) (desc, ok bool) {
	switch params["order"] {
	case "", "asc":
		return false, true
	case "desc":
		return true, true
	}
	invalidParam(w, "parameter 'order' must be asc or desc")
	return false, false
}

func (a *listeningAPI) annotate(w http.ResponseWriter, r *http.Request, userID int64, tracks []library.Track) bool {
	if err := a.personal.Annotate(r.Context(), userID, tracks); err != nil {
		a.internalError(w, "reading listening data", err)
		return false
	}
	return true
}

func trackIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		invalidParam(w, "track id must be a positive integer")
		return 0, false
	}
	return id, true
}

// --- Albums ---

func (a *listeningAPI) albums(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "q", "sort", "order", "page", "limit")
	if !ok {
		return
	}
	page, limit, ok := a.paging(w, params)
	if !ok {
		return
	}
	q := library.AlbumQuery{Limit: int(limit), Offset: int((page - 1) * limit), Sort: params["sort"]}
	if q.Search, ok = textParam(w, params, "q", maxSearchRunes); !ok {
		return
	}
	if q.Sort != "" && !library.IsAlbumSort(q.Sort) {
		invalidParam(w, "parameter 'sort' must be one of: title, artist, year, added")
		return
	}
	if q.Desc, ok = orderParam(w, params); !ok {
		return
	}
	items, total, err := a.catalog.Albums(r.Context(), q)
	if err != nil {
		a.fail(w, "listing albums", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pageOf(page, limit, total)})
}

func (a *listeningAPI) album(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	d, err := a.catalog.Album(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, "reading album", err)
		return
	}
	if !a.annotate(w, r, u.ID, d.Tracks) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"album": d})
}

type groupEditRequest struct {
	Version *string                    `json:"version"`
	Changes map[string]json.RawMessage `json:"changes"`
	Name    *string                    `json:"name"`
}

// PATCH /api/albums/{id}/metadata
func (a *listeningAPI) editAlbum(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	var req groupEditRequest
	if !decodeJSONLimit(w, r, &req, maxEditBodyBytes) {
		return
	}
	if req.Version == nil {
		validationFailed(w, "version is required (from GET /api/albums/{id})")
		return
	}
	changes, err := library.ParseChanges(req.Changes, library.AlbumEditFields)
	if err != nil {
		a.fail(w, "editing album", err)
		return
	}
	newID, err := a.editor.UpdateAlbum(r.Context(), r.PathValue("id"), *req.Version, changes)
	if err != nil {
		a.fail(w, "editing album", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": newID})
}

// --- Artists ---

func (a *listeningAPI) artists(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "q", "sort", "order", "page", "limit")
	if !ok {
		return
	}
	page, limit, ok := a.paging(w, params)
	if !ok {
		return
	}
	q := library.ArtistQuery{Limit: int(limit), Offset: int((page - 1) * limit), Sort: params["sort"]}
	if q.Search, ok = textParam(w, params, "q", maxSearchRunes); !ok {
		return
	}
	if q.Sort != "" && !library.IsArtistSort(q.Sort) {
		invalidParam(w, "parameter 'sort' must be one of: name, tracks, albums")
		return
	}
	if q.Desc, ok = orderParam(w, params); !ok {
		return
	}
	items, total, err := a.catalog.Artists(r.Context(), q)
	if err != nil {
		a.fail(w, "listing artists", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pageOf(page, limit, total)})
}

const personalListLimit = 10

func (a *listeningAPI) artist(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	d, err := a.catalog.Artist(r.Context(), id)
	if err != nil {
		a.fail(w, "reading artist", err)
		return
	}
	top, err := a.personal.TopTracks(r.Context(), u.ID, id, personalListLimit)
	if err != nil {
		a.fail(w, "reading top tracks", err)
		return
	}
	recent, err := a.personal.RecentForArtist(r.Context(), u.ID, id, personalListLimit)
	if err != nil {
		a.fail(w, "reading recent tracks", err)
		return
	}
	listened, err := a.personal.ListeningMS(r.Context(), u.ID, id)
	if err != nil {
		a.fail(w, "reading listening time", err)
		return
	}
	for _, list := range [][]library.Track{d.Tracks, top, recent} {
		if !a.annotate(w, r, u.ID, list) {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artist": d, "top_tracks": top, "recent_tracks": recent, "listening_ms": listened})
}

// PATCH /api/artists/{id} {"version": "...", "name": "New name"}
func (a *listeningAPI) editArtist(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	var req groupEditRequest
	if !decodeJSONLimit(w, r, &req, maxEditBodyBytes) {
		return
	}
	if req.Version == nil || req.Name == nil || req.Changes != nil {
		validationFailed(w, "version and name are required")
		return
	}
	newID, err := a.editor.RenameArtist(r.Context(), r.PathValue("id"), *req.Version, *req.Name)
	if err != nil {
		a.fail(w, "renaming artist", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": newID})
}

// --- Artwork ---

// GET /api/tracks/{id}/artwork?size=small|large[&v=...]
//
// v is ignored by the server; clients change it when the track changes, so
// a cached image is not reused for a changed track.
func (a *listeningAPI) trackArtwork(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "size", "v")
	if !ok {
		return
	}
	id, ok := trackIDParam(w, r)
	if !ok {
		return
	}
	size := library.ArtworkSize(params["size"])
	switch size {
	case "":
		size = library.ArtworkSmall
	case library.ArtworkSmall, library.ArtworkLarge:
	default:
		invalidParam(w, "parameter 'size' must be small or large")
		return
	}
	if len(params["v"]) > 64 {
		invalidParam(w, "parameter 'v' must be at most 64 characters")
		return
	}
	p, hash, err := a.artwork.Get(r.Context(), id, size)
	switch {
	case errors.Is(err, library.ErrNoArtwork):
		writeError(w, http.StatusNotFound, "no_artwork", "this track has no artwork")
		return
	case err != nil:
		a.fail(w, "reading artwork", err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		a.internalError(w, "opening artwork", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		a.internalError(w, "opening artwork", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("ETag", `"`+hash+"-"+string(size)+`"`)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// --- Metadata ---

type metadataResponse struct {
	Metadata library.TrackMetadata `json:"metadata"`
	Fields   []library.EditField   `json:"fields"`
	Track    library.Track         `json:"track"`
}

func (a *listeningAPI) metadataResponse(w http.ResponseWriter, r *http.Request, md library.TrackMetadata) {
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	t, err := a.catalog.Track(r.Context(), md.TrackID)
	if err != nil {
		a.fail(w, "reading track", err)
		return
	}
	list := []library.Track{t}
	if !a.annotate(w, r, u.ID, list) {
		return
	}
	writeJSON(w, http.StatusOK, metadataResponse{Metadata: md, Fields: library.EditFields, Track: list[0]})
}

type editRequest struct {
	Revision *int64                     `json:"revision"`
	Changes  map[string]json.RawMessage `json:"changes"`
	Fields   []string                   `json:"fields"`
}

// GET and PATCH /api/tracks/{id}/metadata
func (a *listeningAPI) trackMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := trackIDParam(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		md, err := a.editor.Get(r.Context(), id)
		if err != nil {
			a.fail(w, "reading metadata", err)
			return
		}
		a.metadataResponse(w, r, md)
		return
	}
	var req editRequest
	if !decodeJSONLimit(w, r, &req, maxEditBodyBytes) {
		return
	}
	if req.Revision == nil || req.Fields != nil {
		validationFailed(w, "revision and changes are required")
		return
	}
	changes, err := library.ParseChanges(req.Changes, nil)
	if err != nil {
		a.fail(w, "editing metadata", err)
		return
	}
	md, err := a.editor.Update(r.Context(), id, *req.Revision, changes)
	if err != nil {
		a.fail(w, "editing metadata", err)
		return
	}
	a.metadataResponse(w, r, md)
}

// POST /api/tracks/{id}/metadata/revert {"revision": 3, "fields": ["title"]}
func (a *listeningAPI) revertMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := trackIDParam(w, r)
	if !ok {
		return
	}
	var req editRequest
	if !decodeJSONLimit(w, r, &req, maxEditBodyBytes) {
		return
	}
	if req.Revision == nil || req.Changes != nil || len(req.Fields) > len(library.EditFields) {
		validationFailed(w, "revision is required; fields may list the fields to revert (default: all)")
		return
	}
	md, err := a.editor.Revert(r.Context(), id, *req.Revision, req.Fields)
	if err != nil {
		a.fail(w, "reverting metadata", err)
		return
	}
	a.metadataResponse(w, r, md)
}

// --- Favorites ---

// PUT and DELETE /api/favorites/tracks/{id}
func (a *listeningAPI) favorite(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, ok := trackIDParam(w, r)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	on := r.Method == http.MethodPut
	if err := a.personal.SetFavorite(r.Context(), u.ID, id, on); err != nil {
		a.fail(w, "changing favorite", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"track_id": id, "favorite": on})
}

// GET /api/favorites/tracks
func (a *listeningAPI) favorites(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "page", "limit")
	if !ok {
		return
	}
	page, limit, ok := a.paging(w, params)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	items, total, err := a.personal.Favorites(r.Context(), u.ID, int(limit), int((page-1)*limit))
	if err != nil {
		a.fail(w, "listing favorites", err)
		return
	}
	if !a.annotate(w, r, u.ID, items) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pageOf(page, limit, total)})
}

// --- History ---

type playRequest struct {
	TrackID    int64  `json:"track_id"`
	PlayKey    string `json:"play_key"`
	ListenedMS int64  `json:"listened_ms"`
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
}

type deleteHistoryRequest struct {
	IDs []int64 `json:"ids"`
	All bool    `json:"all"`
}

// GET, POST and DELETE /api/history
func (a *listeningAPI) history(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.recordPlay(w, r)
	case http.MethodDelete:
		a.deleteHistory(w, r)
	default:
		a.listHistory(w, r)
	}
}

func (a *listeningAPI) recordPlay(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	var req playRequest
	if !decodeJSONLimit(w, r, &req, maxHistoryBodyBytes) {
		return
	}
	if req.TrackID < 1 {
		validationFailed(w, "track_id must be a positive integer")
		return
	}
	// With history turned off (privacy mode), nothing is recorded, whatever
	// the client sends.
	pr, err := a.prefs.Get(r.Context(), u.ID)
	if err != nil {
		a.fail(w, "reading preferences", err)
		return
	}
	if !pr.RecordHistory {
		writeJSON(w, http.StatusOK, map[string]any{"recorded": false, "reason": "history_disabled"})
		return
	}
	recorded, err := a.personal.RecordPlay(r.Context(), u.ID, library.PlayEvent{TrackID: req.TrackID, PlayKey: req.PlayKey,
		ListenedMS: req.ListenedMS, SourceKind: req.SourceKind, SourceID: req.SourceID})
	if err != nil {
		a.fail(w, "recording play", err)
		return
	}
	status := http.StatusOK
	if recorded {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"recorded": recorded})
}

func dateParam(w http.ResponseWriter, params map[string]string, name string) (time.Time, bool) {
	v := params[name]
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		invalidParam(w, "parameter '%s' must be an RFC 3339 time such as 2026-01-31T00:00:00Z", name)
		return time.Time{}, false
	}
	return t, true
}

func (a *listeningAPI) listHistory(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "from", "to", "q", "order", "page", "limit")
	if !ok {
		return
	}
	page, limit, ok := a.paging(w, params)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	q := library.HistoryQuery{Limit: int(limit), Offset: int((page - 1) * limit)}
	if q.From, ok = dateParam(w, params, "from"); !ok {
		return
	}
	if q.To, ok = dateParam(w, params, "to"); !ok {
		return
	}
	if q.Search, ok = textParam(w, params, "q", maxSearchRunes); !ok {
		return
	}
	switch params["order"] {
	case "", "desc":
	case "asc":
		q.Oldest = true
	default:
		invalidParam(w, "parameter 'order' must be asc or desc")
		return
	}
	items, total, err := a.personal.History(r.Context(), u.ID, q)
	if err != nil {
		a.fail(w, "listing history", err)
		return
	}
	tracks := make([]library.Track, len(items))
	for i := range items {
		tracks[i] = items[i].Track
	}
	if !a.annotate(w, r, u.ID, tracks) {
		return
	}
	for i := range items {
		items[i].Track = tracks[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pageOf(page, limit, total)})
}

func (a *listeningAPI) deleteHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	var req deleteHistoryRequest
	if !decodeJSONLimit(w, r, &req, maxHistoryBodyBytes) {
		return
	}
	var n int64
	var err error
	switch {
	case req.All && req.IDs == nil:
		n, err = a.personal.ClearHistory(r.Context(), u.ID)
	case !req.All && req.IDs != nil:
		n, err = a.personal.DeleteHistory(r.Context(), u.ID, req.IDs)
	default:
		validationFailed(w, `send either {"ids": [...]} or {"all": true}`)
		return
	}
	if err != nil {
		a.fail(w, "deleting history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// GET /api/history/recent
func (a *listeningAPI) recent(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "limit")
	if !ok {
		return
	}
	limit, ok := intParam(w, params, "limit", 12, 1, 100)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	rec, err := a.personal.Recent(r.Context(), u.ID, int(limit))
	if err != nil {
		a.fail(w, "reading recent plays", err)
		return
	}
	if !a.annotate(w, r, u.ID, rec.Tracks) {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// GET /api/history/top
func (a *listeningAPI) top(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, "limit")
	if !ok {
		return
	}
	limit, ok := intParam(w, params, "limit", 25, 1, 200)
	if !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	items, err := a.personal.TopTracks(r.Context(), u.ID, "", int(limit))
	if err != nil {
		a.fail(w, "reading top tracks", err)
		return
	}
	if !a.annotate(w, r, u.ID, items) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// --- Playback state ---

type playbackRequest struct {
	Revision *int64                 `json:"revision"`
	State    *library.PlaybackState `json:"state"`
}

// GET and PUT /api/playback
func (a *listeningAPI) playbackState(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPut {
		saved, err := a.playback.Get(r.Context(), u.ID)
		if err != nil {
			a.fail(w, "reading playback state", err)
			return
		}
		if !a.annotate(w, r, u.ID, saved.Tracks) {
			return
		}
		writeJSON(w, http.StatusOK, saved)
		return
	}
	var req playbackRequest
	if !decodeJSONLimit(w, r, &req, maxPlaybackBodyBytes) {
		return
	}
	if req.Revision == nil || req.State == nil {
		validationFailed(w, "revision and state are required")
		return
	}
	if err := a.playback.Put(r.Context(), u.ID, *req.Revision, *req.State); err != nil {
		a.fail(w, "saving playback state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": *req.Revision})
}
