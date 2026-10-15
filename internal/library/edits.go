// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shamil-tp/rewind/internal/metadata"
)

// Metadata edits are stored in the database only. Music files are never
// written: the edited values replace the tag values in the tracks table, and
// track_edits remembers which fields were edited and what the file said, so
// the scanner can re-apply edits after it re-reads a file and an edit can be
// reverted. See docs/architecture.md, "Metadata editing".

// EditField describes one editable field.
type EditField struct {
	Name     string // API name, also the tracks column
	Kind     string // "text", "multiline" or "number"
	Min, Max int    // runes for text, value range for numbers
	Required bool   // may not be cleared (title)
}

// EditFields lists the editable fields in display order.
var EditFields = []EditField{
	{Name: "title", Kind: "text", Max: 512, Required: true},
	{Name: "artist", Kind: "text", Max: 512},
	{Name: "album_artist", Kind: "text", Max: 512},
	{Name: "album", Kind: "text", Max: 512},
	{Name: "track_number", Kind: "number", Min: 1, Max: 9999},
	{Name: "disc_number", Kind: "number", Min: 1, Max: 9999},
	{Name: "year", Kind: "number", Min: 1000, Max: 9999},
	{Name: "genre", Kind: "text", Max: 512},
	{Name: "composer", Kind: "text", Max: 512},
	{Name: "lyrics", Kind: "multiline", Max: metadata.MaxLyricsRunes},
}

var editFieldIndex = func() map[string]EditField {
	m := map[string]EditField{}
	for _, f := range EditFields {
		m[f.Name] = f
	}
	return m
}()

// AlbumEditFields may be changed for a whole album at once.
var AlbumEditFields = []string{"album", "album_artist", "year", "genre"}

// EditInputError reports an invalid edit. Msg is meant for users.
type EditInputError struct{ Msg string }

func (e *EditInputError) Error() string { return e.Msg }

// ErrEditConflict means the track (or album) changed since the client read
// it: another edit or a rescan. Nothing was saved.
var ErrEditConflict = errors.New("the metadata changed since it was loaded; reload and try again")

// ErrAlbumNotFound and ErrArtistNotFound are returned for unknown IDs.
var (
	ErrAlbumNotFound  = errors.New("album not found")
	ErrArtistNotFound = errors.New("artist not found")
)

// editValue is a validated value: a string, an int64, or nil (cleared).
type editValue = any

// normalizeEdit validates one field's JSON value.
func normalizeEdit(name string, raw json.RawMessage) (editValue, error) {
	f, ok := editFieldIndex[name]
	if !ok {
		return nil, &EditInputError{fmt.Sprintf("%q cannot be edited", truncateRunes(name, 40))}
	}
	if string(raw) == "null" {
		if f.Required {
			return nil, &EditInputError{name + " cannot be empty"}
		}
		return nil, nil
	}
	if f.Kind == "number" {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil || n < int64(f.Min) || n > int64(f.Max) {
			return nil, &EditInputError{fmt.Sprintf("%s must be a whole number from %d to %d, or null", name, f.Min, f.Max)}
		}
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, &EditInputError{name + " must be text or null"}
	}
	if f.Kind == "multiline" {
		s = metadata.CleanMultiline(s)
	} else {
		s = metadata.Clean(s)
	}
	if s == "" {
		if f.Required {
			return nil, &EditInputError{name + " cannot be empty"}
		}
		return nil, nil
	}
	if utf8.RuneCountInString(s) > f.Max {
		return nil, &EditInputError{fmt.Sprintf("%s must be at most %d characters", name, f.Max)}
	}
	return s, nil
}

// TrackMetadata is the editor's view of a track: effective values, the
// values read from the file for edited fields, and the revision to send back.
type TrackMetadata struct {
	TrackID  int64          `json:"track_id"`
	Revision int64          `json:"revision"`
	Values   map[string]any `json:"values"`
	Edited   []string       `json:"edited"`
	FromFile map[string]any `json:"from_file"`
	Updated  time.Time      `json:"updated_at"`
}

// Editor reads and writes metadata edits.
type Editor struct {
	db  *sql.DB
	now func() time.Time
}

// NewEditor returns an Editor over the application database.
func NewEditor(db *sql.DB) *Editor { return &Editor{db: db, now: time.Now} }

const editColumns = "title, artist, album_artist, album, track_number, disc_number, year, genre, composer, lyrics"

func readValues(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (map[string]any, int64, int64, error) {
	var (
		title                                        string
		artist, albumArtist, album, genre, comp, lyr sql.NullString
		trackNo, discNo, year                        sql.NullInt64
		revision, updated                            int64
	)
	err := q.QueryRowContext(ctx, "SELECT "+editColumns+", revision, updated_at FROM tracks WHERE id = ?", id).Scan(
		&title, &artist, &albumArtist, &album, &trackNo, &discNo, &year, &genre, &comp, &lyr, &revision, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, 0, ErrTrackNotFound
	}
	if err != nil {
		return nil, 0, 0, err
	}
	str := func(v sql.NullString) any {
		if !v.Valid {
			return nil
		}
		return v.String
	}
	num := func(v sql.NullInt64) any {
		if !v.Valid {
			return nil
		}
		return v.Int64
	}
	return map[string]any{
		"title": title, "artist": str(artist), "album_artist": str(albumArtist), "album": str(album),
		"track_number": num(trackNo), "disc_number": num(discNo), "year": num(year),
		"genre": str(genre), "composer": str(comp), "lyrics": str(lyr),
	}, revision, updated, nil
}

func readEdits(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (fields, original map[string]any, found bool, err error) {
	var f, o string
	err = q.QueryRowContext(ctx, "SELECT fields, original FROM track_edits WHERE track_id = ?", id).Scan(&f, &o)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{}, map[string]any{}, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	fields, original = map[string]any{}, map[string]any{}
	if err := decodeEditMap(f, fields); err != nil {
		return nil, nil, false, err
	}
	if err := decodeEditMap(o, original); err != nil {
		return nil, nil, false, err
	}
	return fields, original, true, nil
}

// decodeEditMap reads a stored JSON object, re-validating every value so a
// damaged row can never put invalid data into tracks.
func decodeEditMap(s string, dst map[string]any) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return fmt.Errorf("stored metadata edit is not valid JSON: %w", err)
	}
	for k, v := range raw {
		if _, ok := editFieldIndex[k]; !ok {
			continue
		}
		val, err := normalizeEdit(k, v)
		if err != nil {
			// Originals may legitimately be empty for required fields (no
			// title tag); keep them as null.
			val = nil
		}
		dst[k] = val
	}
	return nil
}

// Get returns a track's metadata for the editor.
func (e *Editor) Get(ctx context.Context, id int64) (TrackMetadata, error) {
	values, rev, updated, err := readValues(ctx, e.db, id)
	if err != nil {
		return TrackMetadata{}, err
	}
	fields, original, _, err := readEdits(ctx, e.db, id)
	if err != nil {
		return TrackMetadata{}, err
	}
	md := TrackMetadata{TrackID: id, Revision: rev, Values: values, Edited: sortedNames(fields), FromFile: map[string]any{},
		Updated: time.Unix(updated, 0).UTC()}
	for k := range fields {
		md.FromFile[k] = original[k]
	}
	return md, nil
}

func sortedNames(m map[string]any) []string {
	out := []string{}
	for _, f := range EditFields {
		if _, ok := m[f.Name]; ok {
			out = append(out, f.Name)
		}
	}
	return out
}

// ParseChanges validates a change set: field name → JSON value (null clears
// the field). It never touches the database.
func ParseChanges(changes map[string]json.RawMessage, allowed []string) (map[string]any, error) {
	if len(changes) == 0 {
		return nil, &EditInputError{"no changes given"}
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	names := make([]string, 0, len(changes))
	for k := range changes {
		names = append(names, k)
	}
	sort.Strings(names)
	out := map[string]any{}
	for _, k := range names {
		if allowed != nil && !ok[k] {
			return nil, &EditInputError{fmt.Sprintf("%q cannot be edited here", truncateRunes(k, 40))}
		}
		v, err := normalizeEdit(k, changes[k])
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// Update applies changes to one track if its revision is still revision.
// A change back to the file's value removes the edit for that field.
func (e *Editor) Update(ctx context.Context, id, revision int64, changes map[string]any) (TrackMetadata, error) {
	err := e.inTx(ctx, func(tx *sql.Tx) error {
		_, rev, _, err := readValues(ctx, tx, id)
		if err != nil {
			return err
		}
		if rev != revision {
			return ErrEditConflict
		}
		return applyChanges(ctx, tx, id, changes, e.now().Unix())
	})
	if err != nil {
		return TrackMetadata{}, err
	}
	return e.Get(ctx, id)
}

// Revert removes the edits of the named fields (all edited fields when
// fields is empty), restoring the values read from the file.
func (e *Editor) Revert(ctx context.Context, id, revision int64, fields []string) (TrackMetadata, error) {
	err := e.inTx(ctx, func(tx *sql.Tx) error {
		_, rev, _, err := readValues(ctx, tx, id)
		if err != nil {
			return err
		}
		if rev != revision {
			return ErrEditConflict
		}
		edits, original, found, err := readEdits(ctx, tx, id)
		if err != nil || !found {
			return err
		}
		if len(fields) == 0 {
			fields = sortedNames(edits)
		}
		changes := map[string]any{}
		for _, f := range fields {
			if _, ok := editFieldIndex[f]; !ok {
				return &EditInputError{fmt.Sprintf("%q cannot be edited", truncateRunes(f, 40))}
			}
			if _, ok := edits[f]; ok {
				v := original[f]
				if f == "title" && v == nil {
					v = edits[f] // no title tag: keep the edited title
				}
				changes[f] = v
			}
		}
		if len(changes) == 0 {
			return nil
		}
		return applyChanges(ctx, tx, id, changes, e.now().Unix())
	})
	if err != nil {
		return TrackMetadata{}, err
	}
	return e.Get(ctx, id)
}

// applyChanges writes changes to tracks and records them in track_edits.
func applyChanges(ctx context.Context, tx *sql.Tx, id int64, changes map[string]any, now int64) error {
	current, _, _, err := readValues(ctx, tx, id)
	if err != nil {
		return err
	}
	edits, original, _, err := readEdits(ctx, tx, id)
	if err != nil {
		return err
	}
	for k, v := range changes {
		if _, edited := edits[k]; !edited {
			original[k] = current[k]
		}
		if sameValue(v, original[k]) {
			delete(edits, k)
			delete(original, k)
		} else {
			edits[k] = v
		}
	}
	if err := writeColumns(ctx, tx, id, changes, now); err != nil {
		return err
	}
	return saveEdits(ctx, tx, id, edits, original, now)
}

func saveEdits(ctx context.Context, tx *sql.Tx, id int64, edits, original map[string]any, now int64) error {
	if len(edits) == 0 {
		_, err := tx.ExecContext(ctx, "DELETE FROM track_edits WHERE track_id = ?", id)
		return err
	}
	f, err := json.Marshal(edits)
	if err != nil {
		return err
	}
	o, err := json.Marshal(original)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO track_edits (track_id, fields, original, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (track_id) DO UPDATE SET fields = excluded.fields, original = excluded.original, updated_at = excluded.updated_at`,
		id, string(f), string(o), now)
	return err
}

// writeColumns sets the given columns (only names from EditFields reach the
// SQL text) and bumps the revision.
func writeColumns(ctx context.Context, tx *sql.Tx, id int64, values map[string]any, now int64) error {
	var sets []string
	var args []any
	for _, f := range EditFields {
		if v, ok := values[f.Name]; ok {
			sets = append(sets, f.Name+" = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, "UPDATE tracks SET "+strings.Join(sets, ", ")+", revision = revision + 1, updated_at = ? WHERE id = ?",
		append(args, now, id)...)
	return err
}

func sameValue(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	}
	return false
}

// ReapplyEdits is called by the scanner after it wrote fresh tag values to
// a track: the file's values become the new originals of the edited fields,
// and the edits are applied again. Tracks without edits are untouched.
func ReapplyEdits(ctx context.Context, tx *sql.Tx, id int64, now int64) error {
	edits, _, found, err := readEdits(ctx, tx, id)
	if err != nil || !found {
		return err
	}
	current, _, _, err := readValues(ctx, tx, id)
	if err != nil {
		return err
	}
	original := map[string]any{}
	for k := range edits {
		original[k] = current[k]
	}
	if err := writeColumns(ctx, tx, id, edits, now); err != nil {
		return err
	}
	return saveEdits(ctx, tx, id, edits, original, now)
}

func (e *Editor) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Album and artist edits ---

// GroupVersion identifies the state of a set of tracks (their IDs and
// revisions). An album or artist edit is accepted only if it still matches.
func GroupVersion(ids, revisions []int64) string {
	h := fnv.New64a()
	for i := range ids {
		fmt.Fprintf(h, "%d:%d;", ids[i], revisions[i])
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

func groupTracks(ctx context.Context, tx *sql.Tx, where string, args ...any) (ids, revs []int64, err error) {
	rows, err := tx.QueryContext(ctx, "SELECT t.id, t.revision FROM tracks t WHERE "+where+" ORDER BY t.id", args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			return nil, nil, err
		}
		ids, revs = append(ids, id), append(revs, rev)
	}
	return ids, revs, rows.Err()
}

// UpdateAlbum applies changes (AlbumEditFields only) to every track of an
// album, in one transaction, if the album's version still matches. It
// returns the album's new ID, which changes when the title or artist does.
func (e *Editor) UpdateAlbum(ctx context.Context, albumID, version string, changes map[string]any) (string, error) {
	key, ok := DecodeID(albumID)
	if !ok {
		return "", ErrAlbumNotFound
	}
	var newKey string
	err := e.inTx(ctx, func(tx *sql.Tx) error {
		ids, revs, err := groupTracks(ctx, tx, "t.album IS NOT NULL AND "+albumKeyExpr+" = ?", key)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return ErrAlbumNotFound
		}
		if GroupVersion(ids, revs) != version {
			return ErrEditConflict
		}
		now := e.now().Unix()
		for _, id := range ids {
			if err := applyChanges(ctx, tx, id, changes, now); err != nil {
				return err
			}
		}
		return tx.QueryRowContext(ctx, "SELECT "+albumKeyExpr+" FROM tracks t WHERE t.id = ?", ids[0]).Scan(&newKey)
	})
	if err != nil {
		return "", err
	}
	return EncodeID(newKey), nil
}

// RenameArtist replaces an artist's name in the artist and album artist
// fields of every track that credits it (other credited artists are kept),
// in one transaction, if the artist's version still matches. It returns the
// artist's new ID.
func (e *Editor) RenameArtist(ctx context.Context, artistID, version, newName string) (string, error) {
	key, ok := DecodeID(artistID)
	if !ok {
		return "", ErrArtistNotFound
	}
	if key == "" {
		return "", &EditInputError{"tracks without an artist are renamed by editing the tracks"}
	}
	name := metadata.Clean(newName)
	if name == "" || utf8.RuneCountInString(name) > 200 || strings.Contains(name, ";") {
		return "", &EditInputError{"the artist name must be 1 to 200 characters and may not contain \";\""}
	}
	err := e.inTx(ctx, func(tx *sql.Tx) error {
		match, args := artistWhere(key)
		ids, revs, err := groupTracks(ctx, tx, match, args...)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return ErrArtistNotFound
		}
		if GroupVersion(ids, revs) != version {
			return ErrEditConflict
		}
		now := e.now().Unix()
		for _, id := range ids {
			values, _, _, err := readValues(ctx, tx, id)
			if err != nil {
				return err
			}
			changes := map[string]any{}
			for _, col := range []string{"artist", "album_artist"} {
				if s, ok := values[col].(string); ok {
					if renamed, changed := renameCredit(s, key, name); changed {
						changes[col] = renamed
					}
				}
			}
			if len(changes) > 0 {
				if err := applyChanges(ctx, tx, id, changes, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return EncodeID(strings.ToLower(name)), nil
}

// renameCredit replaces the credit whose lower-case form is key in a
// "; "-separated list.
func renameCredit(s, key, name string) (string, bool) {
	parts := SplitArtists(s)
	changed := false
	for i, p := range parts {
		if asciiLower(p) == key {
			parts[i] = name
			changed = true
		}
	}
	return strings.Join(dedupe(parts), "; "), changed
}

func dedupe(parts []string) []string {
	seen := map[string]bool{}
	out := parts[:0]
	for _, p := range parts {
		k := asciiLower(p)
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}
