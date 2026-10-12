// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits keep every playlist response and reorder request bounded.
const (
	MaxPlaylistsPerUser    = 1000
	MaxPlaylistTracks      = 5000
	MaxPlaylistNameRunes   = 100
	MaxPlaylistDescription = 500
)

var (
	ErrPlaylistNotFound  = errors.New("playlist not found")
	ErrPlaylistNameTaken = errors.New("a playlist with this name already exists")
	ErrTooManyPlaylists  = fmt.Errorf("at most %d playlists are allowed", MaxPlaylistsPerUser)
	ErrPlaylistFull      = fmt.Errorf("a playlist can hold at most %d tracks", MaxPlaylistTracks)
	ErrAlreadyInPlaylist = errors.New("the track is already in this playlist")
	ErrNotInPlaylist     = errors.New("the track is not in this playlist")
	// ErrOrderMismatch means a reorder request did not list exactly the
	// playlist's current tracks, usually because it changed meanwhile.
	ErrOrderMismatch = errors.New("the new order must list every track in the playlist exactly once")
)

// PlaylistInputError reports an invalid name or description. Msg is meant
// for users.
type PlaylistInputError struct{ Msg string }

func (e *PlaylistInputError) Error() string { return e.Msg }

// Playlist is the API view of a playlist without its tracks.
type Playlist struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	TrackCount  int64   `json:"track_count"`
	// UnavailableCount counts tracks that cannot be played right now:
	// missing files, unplugged drives or roots no longer configured.
	UnavailableCount int64     `json:"unavailable_count"`
	DurationMS       int64     `json:"duration_ms"` // sum of known durations
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// PlaylistEntry is one track of a playlist. Position is 1-based and
// contiguous in API responses, whatever the stored sort keys are.
type PlaylistEntry struct {
	Position int       `json:"position"`
	AddedAt  time.Time `json:"added_at"`
	Track    Track     `json:"track"`
}

// PlaylistDetail is a playlist with all its entries in order.
type PlaylistDetail struct {
	Playlist
	Entries []PlaylistEntry `json:"entries"`
}

// PlaylistUpdate lists the fields to change; nil fields are kept. An empty
// Description removes the description.
type PlaylistUpdate struct {
	Name        *string
	Description *string
}

// Playlists stores users' playlists. Every method is scoped to a user ID: a
// playlist of another user behaves exactly like one that does not exist.
// Removing a playlist or an entry never touches tracks or music files.
type Playlists struct {
	db  *sql.DB
	now func() time.Time
}

// NewPlaylists returns a playlist store over the application database.
func NewPlaylists(db *sql.DB) *Playlists { return &Playlists{db: db, now: time.Now} }

// NormalizePlaylistName trims name and checks its length and characters.
func NormalizePlaylistName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "", &PlaylistInputError{"name must not be empty"}
	case n > MaxPlaylistNameRunes:
		return "", &PlaylistInputError{fmt.Sprintf("name must be at most %d characters", MaxPlaylistNameRunes)}
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", &PlaylistInputError{"name must not contain control characters"}
	}
	return name, nil
}

// NormalizePlaylistDescription trims a description and checks it. Line
// breaks are kept (as \n); other control characters are rejected. An empty
// result means "no description".
func NormalizePlaylistDescription(desc string) (string, error) {
	desc = strings.TrimSpace(strings.ReplaceAll(desc, "\r\n", "\n"))
	if utf8.RuneCountInString(desc) > MaxPlaylistDescription {
		return "", &PlaylistInputError{fmt.Sprintf("description must be at most %d characters", MaxPlaylistDescription)}
	}
	if strings.IndexFunc(desc, func(r rune) bool { return r != '\n' && unicode.IsControl(r) }) >= 0 {
		return "", &PlaylistInputError{"description must not contain control characters other than line breaks"}
	}
	return desc, nil
}

// playlistSummary selects Playlist fields; callers append WHERE conditions
// on p and the GROUP BY.
const playlistSummary = `SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
	count(pt.track_id),
	coalesce(sum(NOT ` + trackAvailable + `), 0),
	coalesce(sum(t.duration_ms), 0)
	FROM playlists p
	LEFT JOIN playlist_tracks pt ON pt.playlist_id = p.id
	LEFT JOIN tracks t ON t.id = pt.track_id
	LEFT JOIN library_roots r ON r.id = t.root_id`

func scanPlaylist(row rowScanner) (Playlist, error) {
	var (
		pl               Playlist
		desc             sql.NullString
		created, updated int64
	)
	if err := row.Scan(&pl.ID, &pl.Name, &desc, &created, &updated, &pl.TrackCount, &pl.UnavailableCount, &pl.DurationMS); err != nil {
		return Playlist{}, err
	}
	pl.Description = strp(desc)
	pl.CreatedAt = time.Unix(created, 0).UTC()
	pl.UpdatedAt = time.Unix(updated, 0).UTC()
	return pl, nil
}

// List returns the user's playlists, sorted by name, without their tracks.
// One query computes every playlist's counts.
func (s *Playlists) List(ctx context.Context, userID int64) ([]Playlist, error) {
	rows, err := s.db.QueryContext(ctx, playlistSummary+
		" WHERE p.user_id = ? GROUP BY p.id ORDER BY p.name COLLATE NOCASE, p.id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playlist{}
	for rows.Next() {
		pl, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pl)
	}
	return out, rows.Err()
}

func summary(ctx context.Context, q *sql.Tx, userID, id int64) (Playlist, error) {
	pl, err := scanPlaylist(q.QueryRowContext(ctx, playlistSummary+
		" WHERE p.id = ? AND p.user_id = ? GROUP BY p.id", id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrPlaylistNotFound
	}
	return pl, err
}

// Get returns a playlist and all its entries in order, read in one
// transaction so the counts match the entries.
func (s *Playlists) Get(ctx context.Context, userID, id int64) (PlaylistDetail, error) {
	var d PlaylistDetail
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		pl, err := summary(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		d.Playlist = pl
		rows, err := tx.QueryContext(ctx, "SELECT "+trackFields+`, pt.added_at
			FROM playlist_tracks pt
			JOIN tracks t ON t.id = pt.track_id
			JOIN library_roots r ON r.id = t.root_id
			WHERE pt.playlist_id = ? ORDER BY pt.position`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		d.Entries = make([]PlaylistEntry, 0, pl.TrackCount)
		for rows.Next() {
			var added int64
			t, err := scanTrack(rows, &added)
			if err != nil {
				return err
			}
			d.Entries = append(d.Entries, PlaylistEntry{Position: len(d.Entries) + 1, AddedAt: time.Unix(added, 0).UTC(), Track: t})
		}
		return rows.Err()
	})
	return d, err
}

// Create adds an empty playlist. description may be empty.
func (s *Playlists) Create(ctx context.Context, userID int64, name, description string) (Playlist, error) {
	name, err := NormalizePlaylistName(name)
	if err != nil {
		return Playlist{}, err
	}
	if description, err = NormalizePlaylistDescription(description); err != nil {
		return Playlist{}, err
	}
	var pl Playlist
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM playlists WHERE user_id = ?", userID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxPlaylistsPerUser {
			return ErrTooManyPlaylists
		}
		if err := nameFree(ctx, tx, userID, name, 0); err != nil {
			return err
		}
		now := s.now().Unix()
		res, err := tx.ExecContext(ctx,
			"INSERT INTO playlists (user_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			userID, name, nullString(description), now, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		pl, err = summary(ctx, tx, userID, id)
		return err
	})
	return pl, err
}

// Update renames a playlist and/or changes its description.
func (s *Playlists) Update(ctx context.Context, userID, id int64, u PlaylistUpdate) (Playlist, error) {
	var name, desc string
	var err error
	if u.Name != nil {
		if name, err = NormalizePlaylistName(*u.Name); err != nil {
			return Playlist{}, err
		}
	}
	if u.Description != nil {
		if desc, err = NormalizePlaylistDescription(*u.Description); err != nil {
			return Playlist{}, err
		}
	}
	var pl Playlist
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := owned(ctx, tx, userID, id); err != nil {
			return err
		}
		now := s.now().Unix()
		if u.Name != nil {
			if err := nameFree(ctx, tx, userID, name, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE playlists SET name = ?, updated_at = ? WHERE id = ?", name, now, id); err != nil {
				return err
			}
		}
		if u.Description != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE playlists SET description = ?, updated_at = ? WHERE id = ?",
				nullString(desc), now, id); err != nil {
				return err
			}
		}
		pl, err = summary(ctx, tx, userID, id)
		return err
	})
	return pl, err
}

// Delete removes a playlist and its entries (foreign-key cascade, one
// statement). Tracks and files are not affected.
func (s *Playlists) Delete(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM playlists WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrPlaylistNotFound
	}
	return nil
}

// AddTrack appends an indexed track to the end of a playlist. Any indexed
// track can be added, including one that is unavailable right now. A track
// can appear in a playlist only once.
func (s *Playlists) AddTrack(ctx context.Context, userID, playlistID, trackID int64) (PlaylistEntry, error) {
	var e PlaylistEntry
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := owned(ctx, tx, userID, playlistID); err != nil {
			return err
		}
		t, err := scanTrack(tx.QueryRowContext(ctx, trackSelect+" WHERE t.id = ?", trackID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTrackNotFound
		}
		if err != nil {
			return err
		}
		var count int
		var exists bool
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(track_id = ?), 0) > 0, coalesce(max(position) + 1, 0)
			FROM playlist_tracks WHERE playlist_id = ?`, trackID, playlistID).Scan(&count, &exists, &next); err != nil {
			return err
		}
		if exists {
			return ErrAlreadyInPlaylist
		}
		if count >= MaxPlaylistTracks {
			return ErrPlaylistFull
		}
		now := s.now().Unix()
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?)",
			playlistID, trackID, next, now); err != nil {
			return err
		}
		if err := touch(ctx, tx, playlistID, now); err != nil {
			return err
		}
		e = PlaylistEntry{Position: count + 1, AddedAt: time.Unix(now, 0).UTC(), Track: t}
		return nil
	})
	return e, err
}

// RemoveTrack removes a track from a playlist. The other entries keep their
// order. The track itself stays in the library.
func (s *Playlists) RemoveTrack(ctx context.Context, userID, playlistID, trackID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := owned(ctx, tx, userID, playlistID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM playlist_tracks WHERE playlist_id = ? AND track_id = ?", playlistID, trackID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotInPlaylist
		}
		return touch(ctx, tx, playlistID, s.now().Unix())
	})
}

// Reorder sets the playlist's order. trackIDs must list exactly the tracks
// currently in the playlist, each once; otherwise ErrOrderMismatch is
// returned and nothing changes. The entries are rewritten in one
// transaction, keeping their added_at times.
func (s *Playlists) Reorder(ctx context.Context, userID, playlistID int64, trackIDs []int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := owned(ctx, tx, userID, playlistID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT track_id, added_at FROM playlist_tracks WHERE playlist_id = ?", playlistID)
		if err != nil {
			return err
		}
		added := map[int64]int64{}
		for rows.Next() {
			var id, at int64
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return err
			}
			added[id] = at
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(trackIDs) != len(added) {
			return ErrOrderMismatch
		}
		seen := make(map[int64]bool, len(trackIDs))
		for _, id := range trackIDs {
			if _, ok := added[id]; !ok || seen[id] {
				return ErrOrderMismatch
			}
			seen[id] = true
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM playlist_tracks WHERE playlist_id = ?", playlistID); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO playlist_tracks (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, id := range trackIDs {
			if _, err := stmt.ExecContext(ctx, playlistID, id, i, added[id]); err != nil {
				return err
			}
		}
		return touch(ctx, tx, playlistID, s.now().Unix())
	})
}

func (s *Playlists) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func owned(ctx context.Context, tx *sql.Tx, userID, id int64) error {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM playlists WHERE id = ? AND user_id = ?", id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlaylistNotFound
	}
	return err
}

// nameFree checks that no other playlist of the user has this name (ASCII
// case-insensitively, like the unique index). The transaction holds the
// write lock, so the check cannot race with another insert.
func nameFree(ctx context.Context, tx *sql.Tx, userID int64, name string, except int64) error {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM playlists WHERE user_id = ? AND name = ? COLLATE NOCASE AND id != ?",
		userID, name, except).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	default:
		return ErrPlaylistNameTaken
	}
}

func touch(ctx context.Context, tx *sql.Tx, id, now int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE playlists SET updated_at = ? WHERE id = ?", now, id)
	return err
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
