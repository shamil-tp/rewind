// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/metadata"
)

const (
	defaultPageSize = 50
	maxPage         = 1_000_000
	maxSearchRunes  = 200
	maxFilterRunes  = 512
	maxScanRoots    = 100
)

// libraryAPI serves the read-only library endpoints and the rescan trigger.
// Every route is wrapped in requireAuth.
type libraryAPI struct {
	catalog     *library.Catalog
	scanner     *library.Scanner
	maxPageSize int
	logger      *slog.Logger
}

func (l *libraryAPI) internalError(w http.ResponseWriter, action string, err error) {
	l.logger.Error("request failed", "action", action, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func invalidParam(w http.ResponseWriter, format string, args ...any) {
	writeError(w, http.StatusBadRequest, "invalid_parameter", fmt.Sprintf(format, args...))
}

// --- GET /api/library ---

type activeScan struct {
	RootID    int64              `json:"root_id"`
	ScanID    int64              `json:"scan_id"`
	StartedAt time.Time          `json:"started_at"`
	Counts    library.ScanCounts `json:"counts"`
}

type scanState struct {
	Running bool         `json:"running"`
	Active  []activeScan `json:"active"`
	Queued  []int64      `json:"queued"`
}

type summaryResponse struct {
	library.Summary
	Scan scanState `json:"scan"`
}

func (l *libraryAPI) scanState() scanState {
	st := scanState{Active: []activeScan{}, Queued: l.scanner.Queued()}
	for _, r := range l.scanner.Running() {
		st.Active = append(st.Active, activeScan{RootID: r.RootID, ScanID: r.ScanID, StartedAt: r.StartedAt.UTC(), Counts: library.CountsJSON(r.Counts)})
	}
	st.Running = len(st.Active) > 0 || len(st.Queued) > 0
	return st
}

func (l *libraryAPI) summary(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	s, err := l.catalog.Summary(r.Context())
	if err != nil {
		l.internalError(w, "library summary", err)
		return
	}
	writeJSON(w, http.StatusOK, summaryResponse{Summary: s, Scan: l.scanState()})
}

// --- GET /api/library/roots ---

func (l *libraryAPI) roots(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	roots, err := l.catalog.Roots(r.Context())
	if err != nil {
		l.internalError(w, "library roots", err)
		return
	}
	running := map[int64]bool{}
	for _, s := range l.scanner.Running() {
		running[s.RootID] = true
	}
	queued := map[int64]bool{}
	for _, id := range l.scanner.Queued() {
		queued[id] = true
	}
	for i := range roots {
		roots[i].Scanning = running[roots[i].ID]
		roots[i].Queued = queued[roots[i].ID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": roots})
}

// --- GET /api/tracks ---

type pagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

type trackListResponse struct {
	Items      []library.Track `json:"items"`
	Pagination pagination      `json:"pagination"`
}

var trackListParams = []string{"page", "limit", "q", "root", "artist", "album", "album_artist", "genre", "format", "year", "status", "sort", "order"}

func (l *libraryAPI) tracks(w http.ResponseWriter, r *http.Request) {
	params, ok := queryParams(w, r, trackListParams...)
	if !ok {
		return
	}
	page, ok := intParam(w, params, "page", 1, 1, maxPage)
	if !ok {
		return
	}
	limit, ok := intParam(w, params, "limit", int64(min(defaultPageSize, l.maxPageSize)), 1, int64(l.maxPageSize))
	if !ok {
		return
	}
	q := library.TrackQuery{Limit: int(limit), Offset: int((page - 1) * limit), Status: params["status"], Sort: params["sort"]}

	if q.Search, ok = textParam(w, params, "q", maxSearchRunes); !ok {
		return
	}
	for name, dst := range map[string]*string{"artist": &q.Artist, "album": &q.Album, "album_artist": &q.AlbumArtist, "genre": &q.Genre} {
		if *dst, ok = textParam(w, params, name, maxFilterRunes); !ok {
			return
		}
	}
	if q.RootID, ok = intParam(w, params, "root", 0, 1, 1<<62); !ok {
		return
	}
	if q.Year, ok = intParam(w, params, "year", 0, 1000, 9999); !ok {
		return
	}
	if f := params["format"]; f != "" && !validFormat(f) {
		invalidParam(w, "parameter 'format' must be one of: mp3, flac, m4a, ogg, wav")
		return
	}
	q.Format = params["format"]
	switch q.Status {
	case "", "present", "missing", "all":
	default:
		invalidParam(w, "parameter 'status' must be one of: present, missing, all")
		return
	}
	if q.Sort != "" && !library.IsSortKey(q.Sort) {
		invalidParam(w, "parameter 'sort' must be one of: %s", strings.Join(library.SortKeys(), ", "))
		return
	}
	switch params["order"] {
	case "", "asc":
	case "desc":
		q.Desc = true
	default:
		invalidParam(w, "parameter 'order' must be asc or desc")
		return
	}

	res, err := l.catalog.Tracks(r.Context(), q)
	if err != nil {
		l.internalError(w, "listing tracks", err)
		return
	}
	writeJSON(w, http.StatusOK, trackListResponse{
		Items: res.Tracks,
		Pagination: pagination{
			Page: int(page), Limit: int(limit), Total: res.Total,
			TotalPages: (res.Total + limit - 1) / limit,
		},
	})
}

// --- GET /api/tracks/{id} ---

func (l *libraryAPI) track(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		invalidParam(w, "track id must be a positive integer")
		return
	}
	t, err := l.catalog.Track(r.Context(), id)
	if errors.Is(err, library.ErrTrackNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "track not found")
		return
	}
	if err != nil {
		l.internalError(w, "reading track", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"track": t})
}

// --- POST /api/library/scan ---

type scanRequest struct {
	RootIDs []int64 `json:"root_ids"`
}

type scanResponse struct {
	Started        []int64 `json:"started"`
	AlreadyRunning []int64 `json:"already_running"`
}

// scan queues a background rescan with the Phase 3 scanner and returns
// immediately (202). An empty body (or an empty root_ids list) means every
// enabled root.
func (l *libraryAPI) scan(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	var req scanRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	if len(req.RootIDs) > maxScanRoots {
		invalidParam(w, "root_ids may list at most %d roots", maxScanRoots)
		return
	}
	for _, id := range req.RootIDs {
		if id < 1 {
			invalidParam(w, "root_ids must contain positive integers")
			return
		}
	}

	res, err := l.scanner.Start(req.RootIDs...)
	switch {
	case errors.Is(err, library.ErrNoRoots):
		writeError(w, http.StatusConflict, "no_library_roots", "no library roots are configured; set REWIND_LIBRARY_ROOTS")
		return
	case errors.Is(err, library.ErrRootNotFound):
		writeError(w, http.StatusNotFound, "root_not_found", "library root not found")
		return
	case errors.Is(err, library.ErrRootDisabled):
		writeError(w, http.StatusConflict, "root_disabled", "library root is disabled because it is no longer configured")
		return
	case errors.Is(err, library.ErrScannerClosed):
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "the server is shutting down")
		return
	case err != nil:
		l.internalError(w, "starting scan", err)
		return
	}
	if len(res.Started) == 0 {
		writeError(w, http.StatusConflict, "scan_in_progress", "a scan of the requested roots is already running or queued")
		return
	}
	if user, ok := auth.UserFrom(r.Context()); ok {
		l.logger.Info("library rescan requested", "user_id", user.ID, "roots", res.Started, "already_running", res.Busy)
	}
	writeJSON(w, http.StatusAccepted, scanResponse{Started: res.Started, AlreadyRunning: nonNil(res.Busy)})
}

// --- helpers ---

// queryParams parses the query string, rejecting parameters that are not in
// allowed, repeated parameters, and invalid UTF-8. With no allowed names,
// any query parameter is rejected.
func queryParams(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		invalidParam(w, "query string is malformed")
		return nil, false
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	out := map[string]string{}
	for name, vals := range values {
		if !ok[name] {
			invalidParam(w, "unknown parameter %q", truncate(name, 40))
			return nil, false
		}
		if len(vals) != 1 {
			invalidParam(w, "parameter %q must be given once", name)
			return nil, false
		}
		if !utf8.ValidString(vals[0]) {
			invalidParam(w, "parameter %q is not valid UTF-8", name)
			return nil, false
		}
		out[name] = vals[0]
	}
	return out, true
}

func intParam(w http.ResponseWriter, params map[string]string, name string, def, lo, hi int64) (int64, bool) {
	v, present := params[name]
	if !present {
		return def, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo || n > hi {
		invalidParam(w, "parameter '%s' must be an integer from %d to %d", name, lo, hi)
		return 0, false
	}
	return n, true
}

func textParam(w http.ResponseWriter, params map[string]string, name string, maxRunes int) (string, bool) {
	v := strings.TrimSpace(params[name])
	if utf8.RuneCountInString(v) > maxRunes {
		invalidParam(w, "parameter '%s' must be at most %d characters", name, maxRunes)
		return "", false
	}
	return v, true
}

func validFormat(f string) bool {
	for _, known := range metadata.Formats {
		if f == known {
			return true
		}
	}
	return false
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// --- GET|HEAD /api/tracks/{id}/stream ---

// stream sends an indexed track's file. http.ServeContent handles full
// responses, single and multiple byte ranges (206), unsatisfiable or
// malformed ranges (416), HEAD, and conditional requests, copying through a
// small buffer so memory use does not depend on the file size. The file is
// closed when the handler returns, including after a client disconnect.
func (l *libraryAPI) stream(w http.ResponseWriter, r *http.Request) {
	if _, ok := queryParams(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		invalidParam(w, "track id must be a positive integer")
		return
	}
	f, err := l.catalog.OpenTrack(r.Context(), id)
	switch {
	case errors.Is(err, library.ErrTrackNotFound):
		writeError(w, http.StatusNotFound, "not_found", "track not found")
		return
	case errors.Is(err, library.ErrRootDisabled):
		writeError(w, http.StatusConflict, "root_disabled", "the track's library root is no longer configured")
		return
	case errors.Is(err, library.ErrRootUnavailable):
		writeError(w, http.StatusServiceUnavailable, "root_unavailable", "the track's library drive is not available")
		return
	case errors.Is(err, library.ErrFileUnavailable):
		// The reason is logged without paths; clients get a generic message.
		l.logger.Warn("track file not served", "track_id", id, "reason", err.Error())
		writeError(w, http.StatusNotFound, "file_unavailable", "the track's file is not available")
		return
	case err != nil:
		l.internalError(w, "opening track", err)
		return
	}
	defer f.Close()
	// A per-chunk write deadline instead of the fixed one: playback may last
	// as long as the client keeps reading.
	w = newIdleWriter(w, streamWriteIdle)

	h := w.Header()
	h.Set("Content-Type", f.ContentType) // from the indexed format, never the client
	h.Set("Cache-Control", "private, no-store")
	// A validator lets browsers resume and seek safely (If-Range); it changes
	// whenever the file's size or modification time changes.
	h.Set("ETag", fmt.Sprintf(`"%d-%d-%d"`, f.TrackID, f.Size, f.ModTime.UnixNano()))
	if rng, ok := dropZeroSuffixRanges(r.Header.Get("Range")); !ok {
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", f.Size))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable", "the requested range is not satisfiable")
		return
	} else if rng != r.Header.Get("Range") {
		r.Header.Set("Range", rng)
	}
	http.ServeContent(w, r, "", f.ModTime, f.File)
}

// dropZeroSuffixRanges removes zero-length suffix ranges ("-0") from a bytes
// Range header. RFC 9110 defines them as unsatisfiable, but http.ServeContent
// answers them with a 206 and an invalid Content-Range ("bytes 54-53/54").
// ok is false when nothing satisfiable is left. Other headers are returned
// unchanged for ServeContent to handle.
func dropZeroSuffixRanges(header string) (string, bool) {
	unit, specs, found := strings.Cut(header, "=")
	if !found || strings.TrimSpace(unit) != "bytes" {
		return header, true
	}
	var kept []string
	dropped := false
	for _, spec := range strings.Split(specs, ",") {
		if zeroSuffixRange(spec) {
			dropped = true
			continue
		}
		kept = append(kept, spec)
	}
	if !dropped {
		return header, true
	}
	if len(kept) == 0 {
		return "", false
	}
	return unit + "=" + strings.Join(kept, ","), true
}

// zeroSuffixRange reports whether one range spec is a suffix range of length
// zero as net/http parses it: both sides trimmed of spaces and tabs, the
// length read with strconv.ParseInt (which also accepts "+0" and "00"). A
// stricter check would let variants such as "-  0" or "-+0" through.
func zeroSuffixRange(spec string) bool {
	start, end, ok := strings.Cut(textproto.TrimString(spec), "-")
	if !ok || textproto.TrimString(start) != "" {
		return false
	}
	end = textproto.TrimString(end)
	if end == "" || end[0] == '-' {
		return false
	}
	n, err := strconv.ParseInt(end, 10, 64)
	return err == nil && n == 0
}
