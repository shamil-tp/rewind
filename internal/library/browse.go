// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Albums and artists are not stored: they are derived from track tags, so
// they always agree with the library and metadata edits.
//
//   - An album is the set of tracks with the same album title and album
//     artist (the artist when no album artist is tagged), compared
//     case-insensitively for ASCII letters, as SQLite's lower() does. Tracks
//     without an album title belong to no album.
//   - An artist is a name credited in a track's artist or album artist field.
//     Several artists in one field are separated by "; " (that is how
//     the scanner joins multi-value tags), so "A; B" credits A and B.
//
// IDs are the grouping keys encoded with base64url: stable across scans and
// restarts, and changed only when the tags that define the group change.

// albumKeyExpr must stay identical to the expression of the tracks_album_key
// index (migration 0006), apart from the table alias, so SQLite can use it.
// Callers must also require "t.album IS NOT NULL" (the index is partial).
const albumKeyExpr = "(lower(coalesce(t.album_artist, t.artist, '')) || char(31) || lower(t.album))"

// unknownArtistID is the artist of tracks that credit no artist at all.
const unknownArtistID = "~"

// EncodeID turns a grouping key into an ID for URLs.
func EncodeID(key string) string {
	if key == "" {
		return unknownArtistID
	}
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// DecodeID reverses EncodeID. ok is false for anything EncodeID cannot
// have produced.
func DecodeID(id string) (key string, ok bool) {
	if id == unknownArtistID {
		return "", true
	}
	if id == "" || len(id) > 4096 {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(b) == 0 || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

// asciiLower lower-cases ASCII letters only, like SQLite's lower().
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// SplitArtists splits a credit field into names.
func SplitArtists(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "; ") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// artistMatch selects tracks crediting an artist; bind artistPattern(key)
// twice. The unknown artist (key "") is handled by artistWhere.
const artistMatch = `((char(31) || replace(lower(t.artist), '; ', char(31)) || char(31)) LIKE ? ESCAPE '\'
	OR (char(31) || replace(lower(t.album_artist), '; ', char(31)) || char(31)) LIKE ? ESCAPE '\')`

func artistPattern(key string) string { return "%\x1f" + escapeLike(key) + "\x1f%" }

// artistWhere returns the condition and arguments selecting an artist's
// tracks.
func artistWhere(key string) (string, []any) {
	if key == "" {
		return "(t.artist IS NULL AND t.album_artist IS NULL)", nil
	}
	p := artistPattern(key)
	return artistMatch, []any{p, p}
}

// ArtistRef links to an artist.
type ArtistRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func artistRefs(field string) []ArtistRef {
	out := []ArtistRef{}
	for _, n := range SplitArtists(field) {
		out = append(out, ArtistRef{ID: EncodeID(asciiLower(n)), Name: n})
	}
	return out
}

// Album is the API view of an album.
type Album struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Artist is the album artist (or the artist when none is tagged);
	// Artists links to each credited name.
	Artist           string      `json:"artist"`
	Artists          []ArtistRef `json:"artists"`
	Year             *int64      `json:"year,omitempty"`
	Genre            *string     `json:"genre,omitempty"`
	TrackCount       int64       `json:"track_count"`
	DiscCount        int64       `json:"disc_count"`
	DurationMS       int64       `json:"duration_ms"`
	UnavailableCount int64       `json:"unavailable_count"`
	// ArtworkTrackID is a track whose artwork represents the album.
	ArtworkTrackID int64     `json:"artwork_track_id"`
	AddedAt        time.Time `json:"added_at"`
}

// AlbumQuery selects a page of albums.
type AlbumQuery struct {
	Search string // substring of the album title or artist
	Sort   string // title (default), artist, year, added
	Desc   bool
	Limit  int
	Offset int
}

var albumSorts = map[string][]string{
	"title":  {"title COLLATE NOCASE", "artist COLLATE NOCASE", "id"},
	"artist": {"artist COLLATE NOCASE", "year IS NULL", "year", "title COLLATE NOCASE", "id"},
	"year":   {"year IS NULL", "year", "artist COLLATE NOCASE", "title COLLATE NOCASE", "id"},
	"added":  {"added", "id"},
}

// IsAlbumSort reports whether key is an accepted album sort key.
func IsAlbumSort(key string) bool { _, ok := albumSorts[key]; return ok }

// albumAggregate selects one row per album; callers add WHERE conditions on
// t and r. Columns: id, title, artist, year, genre, tracks, discs, duration,
// unavailable, artwork track, added.
const albumAggregate = `SELECT ` + albumKeyExpr + ` AS id, min(t.album) AS title,
	min(coalesce(t.album_artist, t.artist, '')) AS artist, max(t.year) AS year, min(t.genre) AS genre,
	count(*) AS tracks, count(DISTINCT coalesce(t.disc_number, 1)) AS discs, coalesce(sum(t.duration_ms), 0) AS duration,
	coalesce(sum(NOT ` + trackAvailable + `), 0) AS unavailable, min(t.id) AS artwork, min(t.first_seen_at) AS added
	FROM tracks t JOIN library_roots r ON r.id = t.root_id`

func scanAlbum(row rowScanner) (Album, error) {
	var (
		a     Album
		key   string
		year  sql.NullInt64
		genre sql.NullString
		added int64
	)
	if err := row.Scan(&key, &a.Title, &a.Artist, &year, &genre, &a.TrackCount, &a.DiscCount, &a.DurationMS,
		&a.UnavailableCount, &a.ArtworkTrackID, &added); err != nil {
		return Album{}, err
	}
	a.ID = EncodeID(key)
	a.Year, a.Genre = intp(year), strp(genre)
	a.AddedAt = time.Unix(added, 0).UTC()
	a.Artists = artistRefs(a.Artist)
	if a.Artist == "" {
		a.Artist = "Unknown artist"
	}
	return a, nil
}

// Albums returns a page of albums with present tracks in enabled roots.
func (c *Catalog) Albums(ctx context.Context, q AlbumQuery) ([]Album, int64, error) {
	terms, ok := albumSorts[q.Sort]
	if q.Sort == "" {
		terms, ok = albumSorts["title"], true
	}
	if !ok || q.Limit <= 0 || q.Offset < 0 {
		return nil, 0, errors.New("library: invalid album query")
	}
	where := "r.enabled = 1 AND t.status = 'present' AND t.album IS NOT NULL"
	var args []any
	if q.Search != "" {
		p := "%" + escapeLike(q.Search) + "%"
		where += ` AND (t.album LIKE ? ESCAPE '\' OR t.album_artist LIKE ? ESCAPE '\' OR t.artist LIKE ? ESCAPE '\')`
		args = append(args, p, p, p)
	}
	dir := " ASC"
	if q.Desc {
		dir = " DESC"
	}
	order := make([]string, len(terms))
	for i, t := range terms {
		if strings.HasSuffix(t, " IS NULL") {
			order[i] = t
		} else {
			order[i] = t + dir
		}
	}
	inner := albumAggregate + " WHERE " + where + " GROUP BY 1"
	var total int64
	if err := c.db.QueryRowContext(ctx, "SELECT count(*) FROM ("+inner+")", args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.db.QueryContext(ctx, "SELECT * FROM ("+inner+") ORDER BY "+strings.Join(order, ", ")+" LIMIT ? OFFSET ?",
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// AlbumDetail is an album with its tracks in album order.
type AlbumDetail struct {
	Album
	// Version identifies the album's tracks and their revisions, for edits.
	Version string `json:"version"`
	// Folders are the directories holding the album's files, relative to
	// their library root and prefixed with the root's display name.
	Folders []string `json:"folders"`
	Tracks  []Track  `json:"tracks"`
}

// albumOrder sorts an album's tracks deterministically.
const albumOrder = ` ORDER BY t.disc_number IS NULL, t.disc_number, t.track_number IS NULL, t.track_number,
	t.title COLLATE NOCASE, t.id`

// Album returns one album, including tracks whose files are missing, from
// enabled roots.
func (c *Catalog) Album(ctx context.Context, id string) (AlbumDetail, error) {
	key, ok := DecodeID(id)
	if !ok || key == "" {
		return AlbumDetail{}, ErrAlbumNotFound
	}
	cond := " WHERE r.enabled = 1 AND t.album IS NOT NULL AND " + albumKeyExpr + " = ?"
	a, err := scanAlbum(c.db.QueryRowContext(ctx, albumAggregate+cond+" GROUP BY 1", key))
	if errors.Is(err, sql.ErrNoRows) {
		return AlbumDetail{}, ErrAlbumNotFound
	}
	if err != nil {
		return AlbumDetail{}, err
	}
	d := AlbumDetail{Album: a, Folders: []string{}, Tracks: []Track{}}
	rows, err := c.db.QueryContext(ctx, trackSelect+cond+albumOrder, key)
	if err != nil {
		return AlbumDetail{}, err
	}
	defer rows.Close()
	folders := map[string]bool{}
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return AlbumDetail{}, err
		}
		d.Tracks = append(d.Tracks, t)
		dir := t.rootName
		if i := strings.LastIndex(t.RelativePath, "/"); i >= 0 {
			dir += "/" + t.RelativePath[:i]
		}
		if !folders[dir] {
			folders[dir] = true
			d.Folders = append(d.Folders, dir)
		}
	}
	if err := rows.Err(); err != nil {
		return AlbumDetail{}, err
	}
	// The representative artwork is the first available track in album order.
	for _, t := range d.Tracks {
		if t.Available {
			d.ArtworkTrackID = t.ID
			break
		}
	}
	d.Version, err = c.groupVersion(ctx, "t.album IS NOT NULL AND "+albumKeyExpr+" = ?", key)
	return d, err
}

func (c *Catalog) groupVersion(ctx context.Context, where string, args ...any) (string, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT t.id, t.revision FROM tracks t WHERE "+where+" ORDER BY t.id", args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids, revs []int64
	for rows.Next() {
		var id, rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			return "", err
		}
		ids, revs = append(ids, id), append(revs, rev)
	}
	return GroupVersion(ids, revs), rows.Err()
}

// Artist is the API view of an artist.
type Artist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	TrackCount     int64  `json:"track_count"`
	AlbumCount     int64  `json:"album_count"`
	DurationMS     int64  `json:"duration_ms"`
	ArtworkTrackID int64  `json:"artwork_track_id"`
}

// ArtistQuery selects a page of artists.
type ArtistQuery struct {
	Search string
	Sort   string // name (default), tracks, albums
	Desc   bool
	Limit  int
	Offset int
}

// IsArtistSort reports whether key is an accepted artist sort key.
func IsArtistSort(key string) bool { return key == "name" || key == "tracks" || key == "albums" }

type artistAgg struct {
	Artist
	albums map[string]bool
}

// Artists returns a page of artists credited by present tracks in enabled
// roots. Credits are split in Go (SQLite cannot split "; " lists), from one
// row per distinct (artist, album artist, album) combination.
func (c *Catalog) Artists(ctx context.Context, q ArtistQuery) ([]Artist, int64, error) {
	if q.Limit <= 0 || q.Offset < 0 || (q.Sort != "" && !IsArtistSort(q.Sort)) {
		return nil, 0, errors.New("library: invalid artist query")
	}
	rows, err := c.db.QueryContext(ctx, `SELECT t.artist, t.album_artist,
		CASE WHEN t.album IS NOT NULL THEN `+albumKeyExpr+` END, count(*), coalesce(sum(t.duration_ms), 0), min(t.id)
		FROM tracks t JOIN library_roots r ON r.id = t.root_id
		WHERE r.enabled = 1 AND t.status = 'present' GROUP BY 1, 2, 3`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	byKey := map[string]*artistAgg{}
	for rows.Next() {
		var artist, albumArtist, album sql.NullString
		var count, duration, first int64
		if err := rows.Scan(&artist, &albumArtist, &album, &count, &duration, &first); err != nil {
			return nil, 0, err
		}
		names := map[string]string{}
		for _, n := range append(SplitArtists(artist.String), SplitArtists(albumArtist.String)...) {
			if _, ok := names[asciiLower(n)]; !ok {
				names[asciiLower(n)] = n
			}
		}
		if len(names) == 0 {
			names[""] = "Unknown artist"
		}
		for key, name := range names {
			a := byKey[key]
			if a == nil {
				a = &artistAgg{Artist: Artist{ID: EncodeID(key), Name: name, ArtworkTrackID: first}, albums: map[string]bool{}}
				byKey[key] = a
			}
			a.TrackCount += count
			a.DurationMS += duration
			if first < a.ArtworkTrackID {
				a.ArtworkTrackID = first
			}
			if album.Valid {
				a.albums[album.String] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	search := asciiLower(q.Search)
	var list []Artist
	for _, a := range byKey {
		if search != "" && !strings.Contains(asciiLower(a.Name), search) {
			continue
		}
		a.AlbumCount = int64(len(a.albums))
		list = append(list, a.Artist)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		less := func() bool {
			switch q.Sort {
			case "tracks":
				if a.TrackCount != b.TrackCount {
					return a.TrackCount < b.TrackCount
				}
			case "albums":
				if a.AlbumCount != b.AlbumCount {
					return a.AlbumCount < b.AlbumCount
				}
			}
			la, lb := asciiLower(a.Name), asciiLower(b.Name)
			if la != lb {
				return la < lb
			}
			return a.ID < b.ID
		}()
		if q.Desc {
			return !less
		}
		return less
	})
	total := int64(len(list))
	if q.Offset >= len(list) {
		return []Artist{}, total, nil
	}
	end := q.Offset + q.Limit
	if end > len(list) {
		end = len(list)
	}
	return list[q.Offset:end], total, nil
}

// MaxArtistTracks bounds the tracks listed on an artist page.
const MaxArtistTracks = 2000

// ArtistDetail is an artist with albums and tracks.
type ArtistDetail struct {
	Artist
	Version string `json:"version"`
	// Albums are albums whose album artist credits the artist; AppearsOn
	// are other albums with tracks crediting it.
	Albums    []Album `json:"albums"`
	AppearsOn []Album `json:"appears_on"`
	// Tracks are all tracks crediting the artist (at most MaxArtistTracks),
	// ordered by album year, album and position.
	Tracks          []Track `json:"tracks"`
	TracksTruncated bool    `json:"tracks_truncated"`
}

// Artist returns one artist from enabled roots, including tracks whose
// files are missing.
func (c *Catalog) Artist(ctx context.Context, id string) (ArtistDetail, error) {
	key, ok := DecodeID(id)
	if !ok {
		return ArtistDetail{}, ErrArtistNotFound
	}
	match, args := artistWhere(key)
	rows, err := c.db.QueryContext(ctx, trackSelect+" WHERE r.enabled = 1 AND "+match+
		` ORDER BY t.year IS NULL, t.year, t.album IS NULL, t.album COLLATE NOCASE, t.disc_number IS NULL, t.disc_number,
		t.track_number IS NULL, t.track_number, t.title COLLATE NOCASE, t.id LIMIT ?`, append(args, MaxArtistTracks+1)...)
	if err != nil {
		return ArtistDetail{}, err
	}
	defer rows.Close()
	d := ArtistDetail{Albums: []Album{}, AppearsOn: []Album{}, Tracks: []Track{}}
	type group struct {
		album *Album
		main  bool
		discs map[int64]bool
	}
	groups := map[string]*group{}
	var order []string
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return ArtistDetail{}, err
		}
		if len(d.Tracks) == MaxArtistTracks {
			d.TracksTruncated = true
			break
		}
		d.Tracks = append(d.Tracks, t)
		if d.Name == "" {
			d.Name = "Unknown artist"
			for _, n := range append(SplitArtists(deref(t.Artist)), SplitArtists(deref(t.AlbumArtist))...) {
				if asciiLower(n) == key {
					d.Name = n
					break
				}
			}
		}
		if t.Status == "present" {
			d.TrackCount++
			if t.DurationMS != nil {
				d.DurationMS += *t.DurationMS
			}
		}
		if d.ArtworkTrackID == 0 || (t.Available && d.ArtworkTrackID > t.ID) {
			d.ArtworkTrackID = t.ID
		}
		if t.AlbumID == nil {
			continue
		}
		g := groups[*t.AlbumID]
		if g == nil {
			albumArtist := deref(t.AlbumArtist)
			if albumArtist == "" {
				albumArtist = deref(t.Artist)
			}
			main := false
			for _, n := range SplitArtists(albumArtist) {
				if asciiLower(n) == key {
					main = true
				}
			}
			name := albumArtist
			if name == "" {
				name = "Unknown artist"
			}
			g = &group{album: &Album{ID: *t.AlbumID, Title: deref(t.Album), Artist: name, Artists: artistRefs(albumArtist),
				Year: t.Year, Genre: t.Genre, ArtworkTrackID: t.ID, AddedAt: t.AddedAt}, main: main, discs: map[int64]bool{}}
			groups[*t.AlbumID] = g
			order = append(order, *t.AlbumID)
		}
		a := g.album
		// The title is chosen like the album list's min(album), so both
		// pages show the same spelling when tracks differ in case.
		if title := deref(t.Album); title < a.Title {
			a.Title = title
		}
		a.TrackCount++
		if t.DurationMS != nil {
			a.DurationMS += *t.DurationMS
		}
		if !t.Available {
			a.UnavailableCount++
		}
		disc := int64(1)
		if t.DiscNumber != nil {
			disc = *t.DiscNumber
		}
		g.discs[disc] = true
		a.DiscCount = int64(len(g.discs))
		if t.Year != nil && (a.Year == nil || *t.Year > *a.Year) {
			a.Year = t.Year
		}
	}
	if err := rows.Err(); err != nil {
		return ArtistDetail{}, err
	}
	if len(d.Tracks) == 0 {
		return ArtistDetail{}, ErrArtistNotFound
	}
	d.ID = EncodeID(key)
	for _, k := range order {
		g := groups[k]
		if g.main {
			d.Albums = append(d.Albums, *g.album)
		} else {
			d.AppearsOn = append(d.AppearsOn, *g.album)
		}
	}
	d.AlbumCount = int64(len(d.Albums))
	d.Version, err = c.groupVersion(ctx, match, args...)
	return d, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
