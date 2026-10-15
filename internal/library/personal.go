// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Per-user listening data: favorites and listening history. Play counts and
// last-played times are derived from the history, so they always agree with
// it (clearing history resets them).

// Personal stores favorites and listening history.
type Personal struct {
	db  *sql.DB
	now func() time.Time
}

// NewPersonal returns a store over the application database.
func NewPersonal(db *sql.DB) *Personal { return &Personal{db: db, now: time.Now} }

// HistoryInputError reports an invalid play event or query. Msg is meant
// for users.
type HistoryInputError struct{ Msg string }

func (e *HistoryInputError) Error() string { return e.Msg }

const annotateChunk = 500

// Annotate fills in the user's favorite flag, play count and last-played
// time on tracks, with one query per 500 tracks.
func (p *Personal) Annotate(ctx context.Context, userID int64, tracks []Track) error {
	for start := 0; start < len(tracks); start += annotateChunk {
		end := min(start+annotateChunk, len(tracks))
		chunk := tracks[start:end]
		ids := make([]any, 0, len(chunk)*2+2)
		ids = append(ids, userID, userID)
		marks := make([]string, len(chunk))
		index := map[int64][]int{}
		for i := range chunk {
			marks[i] = "?"
			ids = append(ids, chunk[i].ID)
			index[chunk[i].ID] = append(index[chunk[i].ID], start+i)
		}
		rows, err := p.db.QueryContext(ctx, `SELECT t.id,
			EXISTS (SELECT 1 FROM favorites f WHERE f.user_id = ? AND f.track_id = t.id),
			coalesce(h.n, 0), h.last
			FROM tracks t
			LEFT JOIN (SELECT track_id, count(*) AS n, max(played_at) AS last FROM play_history WHERE user_id = ? GROUP BY track_id) h
				ON h.track_id = t.id
			WHERE t.id IN (`+strings.Join(marks, ",")+`)`, ids...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, n int64
			var fav bool
			var last sql.NullInt64
			if err := rows.Scan(&id, &fav, &n, &last); err != nil {
				rows.Close()
				return err
			}
			for _, i := range index[id] {
				tracks[i].Favorite, tracks[i].PlayCount = fav, n
				if last.Valid {
					ts := time.Unix(last.Int64, 0).UTC()
					tracks[i].LastPlayedAt = &ts
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func trackExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) error {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM tracks WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTrackNotFound
	}
	return err
}

// --- Favorites ---

// SetFavorite marks or unmarks a track as a favorite. Repeating it is
// harmless.
func (p *Personal) SetFavorite(ctx context.Context, userID, trackID int64, on bool) error {
	if err := trackExists(ctx, p.db, trackID); err != nil {
		return err
	}
	var err error
	if on {
		_, err = p.db.ExecContext(ctx, "INSERT OR IGNORE INTO favorites (user_id, track_id, created_at) VALUES (?, ?, ?)",
			userID, trackID, p.now().Unix())
	} else {
		_, err = p.db.ExecContext(ctx, "DELETE FROM favorites WHERE user_id = ? AND track_id = ?", userID, trackID)
	}
	return err
}

// Favorites returns the user's favorite tracks, most recently added first.
func (p *Personal) Favorites(ctx context.Context, userID int64, limit, offset int) ([]Track, int64, error) {
	var total int64
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM favorites f JOIN tracks t ON t.id = f.track_id
		JOIN library_roots r ON r.id = t.root_id WHERE f.user_id = ? AND r.enabled = 1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := p.db.QueryContext(ctx, trackSelect+` JOIN favorites f ON f.track_id = t.id
		WHERE f.user_id = ? AND r.enabled = 1 ORDER BY f.created_at DESC, t.id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out, err := collectTracks(rows)
	return out, total, err
}

func collectTracks(rows *sql.Rows, extra ...any) ([]Track, error) {
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		t, err := scanTrack(rows, extra...)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Listening history ---

// SourceKinds are the accepted play sources.
var SourceKinds = map[string]bool{"library": true, "album": true, "artist": true, "playlist": true,
	"history": true, "queue": true, "favorites": true}

var playKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// PlayEvent is one qualified play reported by a client.
type PlayEvent struct {
	TrackID    int64
	PlayKey    string // unique per playback of a track; makes retries idempotent
	ListenedMS int64
	SourceKind string // "" or a key of SourceKinds
	SourceID   string
}

// maxListenedMS bounds a reported play (a whole day).
const maxListenedMS = 24 * 3600 * 1000

// RecordPlay stores a qualified play. It returns false (and no error) when
// a play with the same key was already recorded: retries never count twice.
func (p *Personal) RecordPlay(ctx context.Context, userID int64, e PlayEvent) (bool, error) {
	switch {
	case !playKeyPattern.MatchString(e.PlayKey):
		return false, &HistoryInputError{"play_key must be 8 to 64 letters, digits, '-' or '_'"}
	case e.ListenedMS < 0 || e.ListenedMS > maxListenedMS:
		return false, &HistoryInputError{"listened_ms must be between 0 and 86400000"}
	case e.SourceKind != "" && !SourceKinds[e.SourceKind]:
		return false, &HistoryInputError{"source_kind must be one of library, album, artist, playlist, history, queue, favorites"}
	case len(e.SourceID) > 1024 || (e.SourceID != "" && e.SourceKind == ""):
		return false, &HistoryInputError{"source_id must be at most 1024 characters and needs a source_kind"}
	}
	if err := trackExists(ctx, p.db, e.TrackID); err != nil {
		return false, err
	}
	res, err := p.db.ExecContext(ctx, `INSERT OR IGNORE INTO play_history
		(user_id, track_id, played_at, listened_ms, source_kind, source_id, play_key) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, e.TrackID, p.now().Unix(), e.ListenedMS, nullString(e.SourceKind), nullString(e.SourceID), e.PlayKey)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// HistoryEntry is one play with its track.
type HistoryEntry struct {
	ID         int64     `json:"id"`
	PlayedAt   time.Time `json:"played_at"`
	ListenedMS int64     `json:"listened_ms"`
	SourceKind *string   `json:"source_kind,omitempty"`
	SourceID   *string   `json:"source_id,omitempty"`
	Track      Track     `json:"track"`
}

// HistoryQuery selects a page of history.
type HistoryQuery struct {
	From, To time.Time // zero means unbounded; To is exclusive
	Search   string    // substring of title, artist or album
	Oldest   bool      // oldest first instead of newest first
	Limit    int
	Offset   int
}

// History returns the user's plays, newest first by default. Tracks whose
// files are missing or whose roots are unavailable are included; their
// track fields say so.
func (p *Personal) History(ctx context.Context, userID int64, q HistoryQuery) ([]HistoryEntry, int64, error) {
	if q.Limit <= 0 || q.Offset < 0 {
		return nil, 0, errors.New("library: invalid history paging")
	}
	where := " WHERE h.user_id = ?"
	args := []any{userID}
	if !q.From.IsZero() {
		where += " AND h.played_at >= ?"
		args = append(args, q.From.Unix())
	}
	if !q.To.IsZero() {
		where += " AND h.played_at < ?"
		args = append(args, q.To.Unix())
	}
	if q.Search != "" {
		pat := "%" + escapeLike(q.Search) + "%"
		where += ` AND (t.title LIKE ? ESCAPE '\' OR t.artist LIKE ? ESCAPE '\' OR t.album LIKE ? ESCAPE '\')`
		args = append(args, pat, pat, pat)
	}
	from := " FROM play_history h JOIN tracks t ON t.id = h.track_id JOIN library_roots r ON r.id = t.root_id"
	var total int64
	if err := p.db.QueryRowContext(ctx, "SELECT count(*)"+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := " ORDER BY h.played_at DESC, h.id DESC"
	if q.Oldest {
		order = " ORDER BY h.played_at, h.id"
	}
	rows, err := p.db.QueryContext(ctx, "SELECT "+trackFields+", h.id, h.played_at, h.listened_ms, h.source_kind, h.source_id"+
		from+where+order+" LIMIT ? OFFSET ?", append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		var at int64
		var kind, sid sql.NullString
		t, err := scanTrack(rows, &e.ID, &at, &e.ListenedMS, &kind, &sid)
		if err != nil {
			return nil, 0, err
		}
		e.Track, e.PlayedAt, e.SourceKind, e.SourceID = t, time.Unix(at, 0).UTC(), strp(kind), strp(sid)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// MaxHistoryDelete bounds one delete request.
const MaxHistoryDelete = 1000

// DeleteHistory removes the user's plays with these IDs and returns how many
// were removed. IDs of other users' plays are ignored.
func (p *Personal) DeleteHistory(ctx context.Context, userID int64, ids []int64) (int64, error) {
	if len(ids) == 0 || len(ids) > MaxHistoryDelete {
		return 0, &HistoryInputError{fmt.Sprintf("ids must list 1 to %d plays", MaxHistoryDelete)}
	}
	marks := make([]string, len(ids))
	args := []any{userID}
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	res, err := p.db.ExecContext(ctx, "DELETE FROM play_history WHERE user_id = ? AND id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearHistory removes all of the user's plays.
func (p *Personal) ClearHistory(ctx context.Context, userID int64) (int64, error) {
	res, err := p.db.ExecContext(ctx, "DELETE FROM play_history WHERE user_id = ?", userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecentAlbum is an album the user played recently.
type RecentAlbum struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Artist         string      `json:"artist"`
	Artists        []ArtistRef `json:"artists"`
	ArtworkTrackID int64       `json:"artwork_track_id"`
	LastPlayedAt   time.Time   `json:"last_played_at"`
}

// RecentArtist is an artist the user played recently.
type RecentArtist struct {
	ArtistRef
	ArtworkTrackID int64     `json:"artwork_track_id"`
	LastPlayedAt   time.Time `json:"last_played_at"`
}

// Recent is what the user played recently.
type Recent struct {
	Tracks  []Track        `json:"tracks"`
	Albums  []RecentAlbum  `json:"albums"`
	Artists []RecentArtist `json:"artists"`
}

// recentScan bounds how many recent plays are examined for artists.
const recentScan = 500

// Recent returns the user's recently played tracks, albums and artists
// (distinct, most recent first, at most limit each), from enabled roots.
func (p *Personal) Recent(ctx context.Context, userID int64, limit int) (Recent, error) {
	out := Recent{Tracks: []Track{}, Albums: []RecentAlbum{}, Artists: []RecentArtist{}}
	rows, err := p.db.QueryContext(ctx, trackSelect+` JOIN (SELECT track_id, max(played_at) AS last FROM play_history
		WHERE user_id = ? GROUP BY track_id) h ON h.track_id = t.id WHERE r.enabled = 1
		ORDER BY h.last DESC, t.id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return Recent{}, err
	}
	if out.Tracks, err = collectTracks(rows); err != nil {
		return Recent{}, err
	}

	rows, err = p.db.QueryContext(ctx, `SELECT `+albumKeyExpr+`, min(t.album), min(coalesce(t.album_artist, t.artist, '')),
		min(t.id), max(h.played_at) AS last
		FROM play_history h JOIN tracks t ON t.id = h.track_id JOIN library_roots r ON r.id = t.root_id
		WHERE h.user_id = ? AND r.enabled = 1 AND t.album IS NOT NULL
		GROUP BY 1 ORDER BY last DESC LIMIT ?`, userID, limit)
	if err != nil {
		return Recent{}, err
	}
	for rows.Next() {
		var a RecentAlbum
		var key string
		var last int64
		if err := rows.Scan(&key, &a.Title, &a.Artist, &a.ArtworkTrackID, &last); err != nil {
			rows.Close()
			return Recent{}, err
		}
		a.ID, a.LastPlayedAt, a.Artists = EncodeID(key), time.Unix(last, 0).UTC(), artistRefs(a.Artist)
		if a.Artist == "" {
			a.Artist = "Unknown artist"
		}
		out.Albums = append(out.Albums, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Recent{}, err
	}

	rows, err = p.db.QueryContext(ctx, `SELECT t.id, t.artist, t.album_artist, h.played_at
		FROM play_history h JOIN tracks t ON t.id = h.track_id JOIN library_roots r ON r.id = t.root_id
		WHERE h.user_id = ? AND r.enabled = 1 ORDER BY h.played_at DESC, h.id DESC LIMIT ?`, userID, recentScan)
	if err != nil {
		return Recent{}, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() && len(out.Artists) < limit {
		var id, at int64
		var artist, albumArtist sql.NullString
		if err := rows.Scan(&id, &artist, &albumArtist, &at); err != nil {
			return Recent{}, err
		}
		credits := artist.String
		if credits == "" {
			credits = albumArtist.String
		}
		for _, ref := range artistRefs(credits) {
			if !seen[ref.ID] && len(out.Artists) < limit {
				seen[ref.ID] = true
				out.Artists = append(out.Artists, RecentArtist{ArtistRef: ref, ArtworkTrackID: id, LastPlayedAt: time.Unix(at, 0).UTC()})
			}
		}
	}
	return out, rows.Err()
}

// TopTracks returns the user's most played tracks (at least one play), most
// played first, optionally limited to an artist's tracks (artistID != "").
func (p *Personal) TopTracks(ctx context.Context, userID int64, artistID string, limit int) ([]Track, error) {
	where, args := "r.enabled = 1", []any{userID}
	if artistID != "" {
		key, ok := DecodeID(artistID)
		if !ok {
			return nil, ErrArtistNotFound
		}
		match, margs := artistWhere(key)
		where += " AND " + match
		args = append(args, margs...)
	}
	rows, err := p.db.QueryContext(ctx, trackSelect+` JOIN (SELECT track_id, count(*) AS n, max(played_at) AS last FROM play_history
		WHERE user_id = ? GROUP BY track_id) h ON h.track_id = t.id WHERE `+where+`
		ORDER BY h.n DESC, h.last DESC, t.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return collectTracks(rows)
}

// RecentForArtist returns the artist's tracks the user played most
// recently (distinct).
func (p *Personal) RecentForArtist(ctx context.Context, userID int64, artistID string, limit int) ([]Track, error) {
	key, ok := DecodeID(artistID)
	if !ok {
		return nil, ErrArtistNotFound
	}
	match, margs := artistWhere(key)
	rows, err := p.db.QueryContext(ctx, trackSelect+` JOIN (SELECT track_id, max(played_at) AS last FROM play_history
		WHERE user_id = ? GROUP BY track_id) h ON h.track_id = t.id WHERE r.enabled = 1 AND `+match+`
		ORDER BY h.last DESC, t.id LIMIT ?`, append(append([]any{userID}, margs...), limit)...)
	if err != nil {
		return nil, err
	}
	return collectTracks(rows)
}

// ListeningMS returns the user's total listened time, optionally for one
// artist's tracks.
func (p *Personal) ListeningMS(ctx context.Context, userID int64, artistID string) (int64, error) {
	where, args := "h.user_id = ?", []any{userID}
	if artistID != "" {
		key, ok := DecodeID(artistID)
		if !ok {
			return 0, ErrArtistNotFound
		}
		match, margs := artistWhere(key)
		where += " AND " + match
		args = append(args, margs...)
	}
	var total int64
	err := p.db.QueryRowContext(ctx, `SELECT coalesce(sum(h.listened_ms), 0) FROM play_history h
		JOIN tracks t ON t.id = h.track_id WHERE `+where, args...).Scan(&total)
	return total, err
}
