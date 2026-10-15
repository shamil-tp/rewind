// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// Catalog answers read-only questions about the indexed library. It reads
// the tables the scanner maintains and never touches the filesystem.
type Catalog struct {
	db *sql.DB
}

// NewCatalog returns a Catalog over the application database.
func NewCatalog(db *sql.DB) *Catalog { return &Catalog{db: db} }

// ErrTrackNotFound is returned by Track for an unknown ID.
var ErrTrackNotFound = errors.New("track not found")

// Track is the API view of an indexed track. Optional metadata is nil when
// the file has no such tag. Filesystem paths are relative to the track's
// root; absolute paths are never included.
type Track struct {
	ID             int64      `json:"id"`
	RootID         int64      `json:"root_id"`
	Title          string     `json:"title"`
	Artist         *string    `json:"artist,omitempty"`
	Album          *string    `json:"album,omitempty"`
	AlbumArtist    *string    `json:"album_artist,omitempty"`
	Genre          *string    `json:"genre,omitempty"`
	ReleaseDate    *string    `json:"release_date,omitempty"`
	Year           *int64     `json:"year,omitempty"`
	TrackNumber    *int64     `json:"track_number,omitempty"`
	DiscNumber     *int64     `json:"disc_number,omitempty"`
	DurationMS     *int64     `json:"duration_ms,omitempty"`
	Format         string     `json:"format"`
	Codec          *string    `json:"codec,omitempty"`
	SizeBytes      int64      `json:"size_bytes"`
	RelativePath   string     `json:"relative_path"`
	Status         string     `json:"status"`    // present or missing
	Available      bool       `json:"available"` // present, and its root is enabled and available
	MetadataStatus string     `json:"metadata_status"`
	MetadataError  *string    `json:"metadata_error,omitempty"`
	MissingSince   *time.Time `json:"missing_since,omitempty"`
	AddedAt        time.Time  `json:"added_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	Composer *string `json:"composer,omitempty"`
	// HasLyrics says whether lyrics are stored; Lyrics is filled in only by
	// Track (single-track reads), to keep lists small.
	HasLyrics bool    `json:"has_lyrics"`
	Lyrics    *string `json:"lyrics,omitempty"`
	// Revision changes with every metadata change (scan or edit).
	Revision int64 `json:"revision"`
	// AlbumID is the album's ID (see browse.go); nil without an album tag.
	AlbumID *string `json:"album_id,omitempty"`
	// Artists links to each artist credited by the artist field (or the
	// album artist field when the artist is not tagged).
	Artists []ArtistRef `json:"artists"`

	// Per-user values, filled in by Annotate.
	Favorite     bool       `json:"favorite"`
	PlayCount    int64      `json:"play_count"`
	LastPlayedAt *time.Time `json:"last_played_at,omitempty"`

	rootName string // display name of the root, for folder names
}

// Sort keys accepted by TrackQuery.Sort, mapped to fixed ORDER BY terms. User
// input only selects a key; it is never placed in SQL.
var trackSorts = map[string][]string{
	"title":        {"t.title COLLATE NOCASE", "t.id"},
	"artist":       {"t.artist IS NULL", "t.artist COLLATE NOCASE", "t.album COLLATE NOCASE", "t.disc_number", "t.track_number", "t.title COLLATE NOCASE", "t.id"},
	"album":        {"t.album IS NULL", "t.album COLLATE NOCASE", "t.disc_number", "t.track_number", "t.title COLLATE NOCASE", "t.id"},
	"album_artist": {"t.album_artist IS NULL", "t.album_artist COLLATE NOCASE", "t.album COLLATE NOCASE", "t.disc_number", "t.track_number", "t.id"},
	"year":         {"t.year IS NULL", "t.year", "t.album COLLATE NOCASE", "t.disc_number", "t.track_number", "t.id"},
	"duration":     {"t.duration_ms IS NULL", "t.duration_ms", "t.id"},
	"added":        {"t.first_seen_at", "t.id"},
}

// SortKeys lists the accepted sort keys.
func SortKeys() []string {
	return []string{"title", "artist", "album", "album_artist", "year", "duration", "added"}
}

// IsSortKey reports whether key is an accepted sort key.
func IsSortKey(key string) bool { _, ok := trackSorts[key]; return ok }

// TrackQuery selects a page of tracks. The zero value of each filter means
// "no filter". Callers validate input; Catalog only builds parameterized SQL.
type TrackQuery struct {
	Search      string // substring of title, artist, album or album artist
	RootID      int64
	Artist      string // exact, case-insensitive
	Album       string
	AlbumArtist string
	Genre       string
	Format      string
	Year        int64
	Status      string // "present" (default), "missing" or "all"
	Sort        string // a key of trackSorts; default "title"
	Desc        bool
	Limit       int
	Offset      int
}

// TrackPage is one page of results and the total number of matches.
type TrackPage struct {
	Tracks []Track
	Total  int64
}

// trackFields are the columns scanTrack reads, from tracks t joined with
// library_roots r.
const trackFields = `t.id, t.root_id, t.title, t.artist, t.album, t.album_artist, t.genre, t.release_date,
	t.year, t.track_number, t.disc_number, t.duration_ms, t.format, t.codec, t.file_size, t.rel_path,
	t.status, ` + trackAvailable + `,
	t.metadata_status, t.metadata_error, t.missing_since, t.first_seen_at, t.updated_at,
	r.path, r.configured_path, t.composer, t.lyrics IS NOT NULL, t.revision,
	CASE WHEN t.album IS NOT NULL THEN ` + albumKeyExpr + ` END`

// trackAvailable is true when a track can be streamed: its file was found by
// the last scan and its root is configured and mounted.
const trackAvailable = `(t.status = 'present' AND r.enabled = 1 AND r.status = 'available')`

const trackSelect = "SELECT " + trackFields + " FROM tracks t JOIN library_roots r ON r.id = t.root_id"

// Tracks returns one page of tracks from enabled roots. Filtering, ordering,
// and paging all happen in SQLite.
func (c *Catalog) Tracks(ctx context.Context, q TrackQuery) (TrackPage, error) {
	where := []string{"r.enabled = 1"}
	var args []any
	switch q.Status {
	case "", "present":
		where = append(where, "t.status = 'present'")
	case "missing":
		where = append(where, "t.status = 'missing'")
	case "all":
	default:
		return TrackPage{}, errors.New("library: invalid status filter")
	}
	if q.Search != "" {
		pattern := "%" + escapeLike(q.Search) + "%"
		where = append(where, `(t.title LIKE ? ESCAPE '\' OR t.artist LIKE ? ESCAPE '\' OR t.album LIKE ? ESCAPE '\' OR t.album_artist LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern, pattern)
	}
	exact := []struct {
		col, val string
	}{{"t.artist", q.Artist}, {"t.album", q.Album}, {"t.album_artist", q.AlbumArtist}, {"t.genre", q.Genre}}
	for _, f := range exact {
		if f.val != "" {
			where = append(where, f.col+" = ? COLLATE NOCASE")
			args = append(args, f.val)
		}
	}
	if q.Format != "" {
		where = append(where, "t.format = ?")
		args = append(args, q.Format)
	}
	if q.RootID != 0 {
		where = append(where, "t.root_id = ?")
		args = append(args, q.RootID)
	}
	if q.Year != 0 {
		where = append(where, "t.year = ?")
		args = append(args, q.Year)
	}
	terms, ok := trackSorts[q.Sort]
	if q.Sort == "" {
		terms, ok = trackSorts["title"], true
	}
	if !ok {
		return TrackPage{}, errors.New("library: invalid sort key")
	}
	if q.Limit <= 0 || q.Offset < 0 {
		return TrackPage{}, errors.New("library: invalid paging")
	}
	dir := " ASC"
	if q.Desc {
		dir = " DESC"
	}
	order := make([]string, len(terms))
	for i, t := range terms {
		if strings.HasSuffix(t, " IS NULL") {
			order[i] = t // tracks without the value always sort last
		} else {
			order[i] = t + dir
		}
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	var page TrackPage
	if err := c.db.QueryRowContext(ctx,
		"SELECT count(*) FROM tracks t JOIN library_roots r ON r.id = t.root_id"+cond, args...).Scan(&page.Total); err != nil {
		return TrackPage{}, err
	}
	rows, err := c.db.QueryContext(ctx, trackSelect+cond+" ORDER BY "+strings.Join(order, ", ")+" LIMIT ? OFFSET ?",
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return TrackPage{}, err
	}
	defer rows.Close()
	page.Tracks = []Track{}
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return TrackPage{}, err
		}
		page.Tracks = append(page.Tracks, t)
	}
	return page, rows.Err()
}

// Track returns one track by ID, from any root (including disabled ones),
// with its lyrics.
func (c *Catalog) Track(ctx context.Context, id int64) (Track, error) {
	var lyrics sql.NullString
	t, err := scanTrack(c.db.QueryRowContext(ctx,
		"SELECT "+trackFields+", t.lyrics FROM tracks t JOIN library_roots r ON r.id = t.root_id WHERE t.id = ?", id), &lyrics)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, ErrTrackNotFound
	}
	t.Lyrics = strp(lyrics)
	return t, err
}

type rowScanner interface{ Scan(dest ...any) error }

// scanTrack reads a row selected with trackFields. extra receives any
// columns selected after them.
func scanTrack(row rowScanner, extra ...any) (Track, error) {
	var (
		t                                                         Track
		artist, album, albumArtist, genre, date, codec, metaError sql.NullString
		composer, albumKey                                        sql.NullString
		year, trackNo, discNo, duration, missingSince             sql.NullInt64
		added, updated                                            int64
		rootPath, rootConfigured                                  string
	)
	err := row.Scan(append([]any{&t.ID, &t.RootID, &t.Title, &artist, &album, &albumArtist, &genre, &date,
		&year, &trackNo, &discNo, &duration, &t.Format, &codec, &t.SizeBytes, &t.RelativePath,
		&t.Status, &t.Available, &t.MetadataStatus, &metaError, &missingSince, &added, &updated,
		&rootPath, &rootConfigured, &composer, &t.HasLyrics, &t.Revision, &albumKey}, extra...)...)
	if err != nil {
		return Track{}, err
	}
	t.Composer = strp(composer)
	if albumKey.Valid {
		id := EncodeID(albumKey.String)
		t.AlbumID = &id
	}
	credits := artist.String
	if credits == "" {
		credits = albumArtist.String
	}
	t.Artists = artistRefs(credits)
	t.rootName = DisplayName(rootConfigured)
	t.Artist, t.Album, t.AlbumArtist = strp(artist), strp(album), strp(albumArtist)
	t.Genre, t.ReleaseDate, t.Codec = strp(genre), strp(date), strp(codec)
	t.Year, t.TrackNumber, t.DiscNumber, t.DurationMS = intp(year), intp(trackNo), intp(discNo), intp(duration)
	if metaError.Valid {
		msg := Sanitize(metaError.String, rootPath, rootConfigured)
		t.MetadataError = &msg
	}
	if missingSince.Valid {
		ts := time.Unix(missingSince.Int64, 0).UTC()
		t.MissingSince = &ts
	}
	t.AddedAt = time.Unix(added, 0).UTC()
	t.UpdatedAt = time.Unix(updated, 0).UTC()
	return t, nil
}

// RootStatus is the API view of a library root. It carries a display name
// (the last element of the configured path) instead of the path itself.
type RootStatus struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	Enabled       bool        `json:"enabled"`
	Status        string      `json:"status"` // pending, available or unavailable
	StatusMessage *string     `json:"status_message,omitempty"`
	TrackCount    int64       `json:"track_count"`   // present tracks
	MissingCount  int64       `json:"missing_count"` // tracks whose files were not found
	Scanning      bool        `json:"scanning"`      // filled in by the caller
	Queued        bool        `json:"queued"`        // filled in by the caller
	LastScan      *ScanReport `json:"last_scan,omitempty"`
}

// ScanReport is the API view of a scan.
type ScanReport struct {
	ID         int64      `json:"id"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Counts     ScanCounts `json:"counts"`
	Error      *string    `json:"error,omitempty"`
}

// ScanCounts mirrors Counts with JSON names.
type ScanCounts struct {
	FilesFound      int `json:"files_found"`
	FilesSkipped    int `json:"files_skipped"`
	FilesFailed     int `json:"files_failed"`
	DirsFailed      int `json:"dirs_failed"`
	TracksAdded     int `json:"tracks_added"`
	TracksUpdated   int `json:"tracks_updated"`
	TracksUnchanged int `json:"tracks_unchanged"`
	TracksRenamed   int `json:"tracks_renamed"`
	TracksMissing   int `json:"tracks_missing"`
	TracksRestored  int `json:"tracks_restored"`
}

// CountsJSON converts scanner counters to their API form.
func CountsJSON(c Counts) ScanCounts {
	return ScanCounts{c.FilesFound, c.FilesSkipped, c.FilesFailed, c.DirsFailed, c.TracksAdded,
		c.TracksUpdated, c.TracksUnchanged, c.TracksRenamed, c.TracksMissing, c.TracksRestored}
}

// Roots returns every registered root with track counts and its latest scan,
// in two queries.
func (c *Catalog) Roots(ctx context.Context) ([]RootStatus, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT r.id, r.configured_path, r.path, r.enabled, r.status, r.status_message,
		(SELECT count(*) FROM tracks t WHERE t.root_id = r.id AND t.status = 'present'),
		(SELECT count(*) FROM tracks t WHERE t.root_id = r.id AND t.status = 'missing'),
		s.id, s.status, s.started_at, s.finished_at,
		coalesce(s.files_found, 0), coalesce(s.files_skipped, 0), coalesce(s.files_failed, 0), coalesce(s.dirs_failed, 0),
		coalesce(s.tracks_added, 0), coalesce(s.tracks_updated, 0), coalesce(s.tracks_unchanged, 0),
		coalesce(s.tracks_renamed, 0), coalesce(s.tracks_missing, 0), coalesce(s.tracks_restored, 0), s.error
		FROM library_roots r
		LEFT JOIN scans s ON s.id = (SELECT max(id) FROM scans WHERE root_id = r.id)
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RootStatus{}
	for rows.Next() {
		var (
			r                     RootStatus
			configured, path      string
			msg, scanStatus, sErr sql.NullString
			scanID, started, fin  sql.NullInt64
			sc                    ScanCounts
		)
		if err := rows.Scan(&r.ID, &configured, &path, &r.Enabled, &r.Status, &msg, &r.TrackCount, &r.MissingCount,
			&scanID, &scanStatus, &started, &fin, &sc.FilesFound, &sc.FilesSkipped, &sc.FilesFailed, &sc.DirsFailed,
			&sc.TracksAdded, &sc.TracksUpdated, &sc.TracksUnchanged, &sc.TracksRenamed, &sc.TracksMissing,
			&sc.TracksRestored, &sErr); err != nil {
			return nil, err
		}
		r.Name = DisplayName(configured)
		if msg.Valid {
			m := Sanitize(msg.String, path, configured)
			r.StatusMessage = &m
		}
		if scanID.Valid {
			rep := &ScanReport{ID: scanID.Int64, Status: scanStatus.String, StartedAt: time.Unix(started.Int64, 0).UTC(), Counts: sc}
			if fin.Valid {
				f := time.Unix(fin.Int64, 0).UTC()
				rep.FinishedAt = &f
			}
			if sErr.Valid {
				e := Sanitize(sErr.String, path, configured)
				rep.Error = &e
			}
			r.LastScan = rep
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Summary is the library overview.
type Summary struct {
	Tracks             int64      `json:"tracks"`           // present tracks in enabled roots
	AvailableTracks    int64      `json:"available_tracks"` // of those, in roots that are currently available
	MissingTracks      int64      `json:"missing_tracks"`   // files not found by the last complete scans
	Artists            int64      `json:"artists"`
	Albums             int64      `json:"albums"`
	TotalDurationMS    int64      `json:"total_duration_ms"`
	Roots              RootCounts `json:"roots"`
	LastSuccessfulScan *time.Time `json:"last_successful_scan_at,omitempty"`
}

// RootCounts summarizes root availability. Unavailable roots keep their
// tracks; they are counted, not treated as empty.
type RootCounts struct {
	Total       int64 `json:"total"` // enabled roots
	Available   int64 `json:"available"`
	Unavailable int64 `json:"unavailable"`
	Pending     int64 `json:"pending"`
	Disabled    int64 `json:"disabled"`
}

// Summary computes the overview with three aggregate queries.
func (c *Catalog) Summary(ctx context.Context) (Summary, error) {
	var s Summary
	err := c.db.QueryRowContext(ctx, `SELECT
		count(*),
		coalesce(sum(r.status = 'available'), 0),
		count(DISTINCT lower(t.artist)),
		count(DISTINCT lower(coalesce(t.album_artist, t.artist, '')) || char(31) || lower(t.album)),
		coalesce(sum(t.duration_ms), 0)
		FROM tracks t JOIN library_roots r ON r.id = t.root_id
		WHERE r.enabled = 1 AND t.status = 'present'`).
		Scan(&s.Tracks, &s.AvailableTracks, &s.Artists, &s.Albums, &s.TotalDurationMS)
	if err != nil {
		return Summary{}, err
	}
	if err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM tracks t JOIN library_roots r ON r.id = t.root_id
		WHERE r.enabled = 1 AND t.status = 'missing'`).Scan(&s.MissingTracks); err != nil {
		return Summary{}, err
	}
	var last sql.NullInt64
	err = c.db.QueryRowContext(ctx, `SELECT
		coalesce(sum(enabled = 1), 0),
		coalesce(sum(enabled = 1 AND status = 'available'), 0),
		coalesce(sum(enabled = 1 AND status = 'unavailable'), 0),
		coalesce(sum(enabled = 1 AND status = 'pending'), 0),
		coalesce(sum(enabled = 0), 0),
		(SELECT max(finished_at) FROM scans WHERE status = 'completed')
		FROM library_roots`).
		Scan(&s.Roots.Total, &s.Roots.Available, &s.Roots.Unavailable, &s.Roots.Pending, &s.Roots.Disabled, &last)
	if err != nil {
		return Summary{}, err
	}
	if last.Valid {
		ts := time.Unix(last.Int64, 0).UTC()
		s.LastSuccessfulScan = &ts
	}
	return s, nil
}

// DisplayName is the last element of a configured root path, e.g.
// "MUSICSTICK" for /media/alex/MUSICSTICK.
func DisplayName(configured string) string {
	name := filepath.Base(filepath.Clean(configured))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "library"
	}
	return name
}

// Sanitize removes a root's absolute paths from a stored message, so API
// responses never reveal where a library lives on disk.
func Sanitize(msg string, paths ...string) string {
	for _, p := range paths {
		if p != "" {
			msg = strings.ReplaceAll(msg, p, "<library root>")
		}
	}
	return msg
}

// escapeLike escapes LIKE wildcards so search text matches literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func strp(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func intp(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
