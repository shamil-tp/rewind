// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The play queue and playback settings are kept per user on the server, so
// they survive refreshes, browser restarts and switching devices. The web
// player owns the live state and saves it here; this store only validates
// and keeps the latest copy.

// MaxQueueLength bounds the queue.
const MaxQueueLength = 10000

// PlaybackContext says where the queue came from.
type PlaybackContext struct {
	Kind string `json:"kind"` // a key of SourceKinds
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// PlaybackState is the saved queue and playback settings.
type PlaybackState struct {
	// Queue is the play order (track IDs; a track may appear more than once).
	Queue []int64 `json:"queue"`
	// Original is the order before shuffling (the same entries), present only
	// while shuffle is on, so turning shuffle off restores it.
	Original   []int64          `json:"original,omitempty"`
	Index      int              `json:"index"` // current entry, -1 for none
	PositionMS int64            `json:"position_ms"`
	Shuffle    bool             `json:"shuffle"`
	Repeat     string           `json:"repeat"` // off, all or one
	Speed      float64          `json:"speed"`  // 0.5 to 2
	Context    *PlaybackContext `json:"context,omitempty"`
}

// PlaybackInputError reports an invalid state. Msg is meant for users.
type PlaybackInputError struct{ Msg string }

func (e *PlaybackInputError) Error() string { return e.Msg }

// ErrStaleState means a newer state was already saved.
var ErrStaleState = errors.New("a newer playback state was already saved")

// DefaultPlaybackState is the state of a user who never played anything.
func DefaultPlaybackState() PlaybackState {
	return PlaybackState{Queue: []int64{}, Index: -1, Repeat: "off", Speed: 1}
}

// Validate checks the state and normalizes the speed.
func (s *PlaybackState) Validate() error {
	bad := func(format string, args ...any) error { return &PlaybackInputError{fmt.Sprintf(format, args...)} }
	if s.Queue == nil {
		s.Queue = []int64{}
	}
	if len(s.Queue) > MaxQueueLength {
		return bad("the queue may hold at most %d tracks", MaxQueueLength)
	}
	counts := map[int64]int{}
	for _, id := range s.Queue {
		if id < 1 {
			return bad("queue must contain positive track IDs")
		}
		counts[id]++
	}
	if s.Original != nil {
		if !s.Shuffle {
			return bad("original is only kept while shuffle is on")
		}
		if len(s.Original) != len(s.Queue) {
			return bad("original must hold the same tracks as queue")
		}
		for _, id := range s.Original {
			counts[id]--
		}
		for _, n := range counts {
			if n != 0 {
				return bad("original must hold the same tracks as queue")
			}
		}
	}
	if s.Index < -1 || s.Index >= len(s.Queue) {
		return bad("index must point into the queue (or be -1)")
	}
	if len(s.Queue) > 0 && s.Index == -1 {
		s.Index = 0
	}
	if s.PositionMS < 0 || s.PositionMS > maxListenedMS {
		return bad("position_ms must be between 0 and 86400000")
	}
	switch s.Repeat {
	case "off", "all", "one":
	case "":
		s.Repeat = "off"
	default:
		return bad("repeat must be off, all or one")
	}
	if s.Speed == 0 {
		s.Speed = 1
	}
	if math.IsNaN(s.Speed) || s.Speed < 0.5 || s.Speed > 2 {
		return bad("speed must be between 0.5 and 2")
	}
	s.Speed = math.Round(s.Speed*100) / 100
	if c := s.Context; c != nil {
		if !SourceKinds[c.Kind] {
			return bad("context kind must be one of library, album, artist, playlist, history, queue, favorites")
		}
		if len(c.ID) > 1024 || utf8.RuneCountInString(c.Name) > 200 || !utf8.ValidString(c.Name) ||
			strings.IndexFunc(c.Name, unicode.IsControl) >= 0 || strings.IndexFunc(c.ID, unicode.IsControl) >= 0 {
			return bad("context id must be at most 1024 characters and name at most 200, without control characters")
		}
	}
	return nil
}

// PlaybackStore keeps users' playback state.
type PlaybackStore struct {
	db  *sql.DB
	now func() time.Time
}

// NewPlaybackStore returns a store over the application database.
func NewPlaybackStore(db *sql.DB) *PlaybackStore { return &PlaybackStore{db: db, now: time.Now} }

// SavedPlayback is a stored state with the tracks of its queue.
type SavedPlayback struct {
	State    PlaybackState `json:"state"`
	Revision int64         `json:"revision"`
	// Tracks lists the distinct tracks of the queue (in no particular
	// order), so a client can restore the queue in one request.
	Tracks []Track `json:"tracks"`
}

// Get returns the user's state. Entries whose tracks no longer exist (for
// example merged duplicates) are dropped, and the index follows its track.
func (s *PlaybackStore) Get(ctx context.Context, userID int64) (SavedPlayback, error) {
	out := SavedPlayback{State: DefaultPlaybackState(), Tracks: []Track{}}
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT state, revision FROM playback_state WHERE user_id = ?", userID).Scan(&raw, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return SavedPlayback{}, err
	}
	var st PlaybackState
	if json.Unmarshal([]byte(raw), &st) != nil || st.Validate() != nil {
		return out, nil // a damaged row is treated as no state
	}
	tracks, err := s.tracksByID(ctx, st.Queue)
	if err != nil {
		return SavedPlayback{}, err
	}
	keep := func(ids []int64) []int64 {
		kept := make([]int64, 0, len(ids))
		for _, id := range ids {
			if _, ok := tracks[id]; ok {
				kept = append(kept, id)
			}
		}
		return kept
	}
	newIndex := -1
	for i, id := range st.Queue[:max(st.Index+1, 0)] {
		if _, ok := tracks[id]; ok {
			newIndex++
		} else if i == st.Index {
			st.PositionMS = 0 // the current track is gone; its successor starts over
		}
	}
	st.Queue = keep(st.Queue)
	if st.Original != nil {
		st.Original = keep(st.Original)
	}
	switch {
	case len(st.Queue) == 0:
		st.Index, st.PositionMS = -1, 0
	case newIndex < 0:
		st.Index = 0
	case newIndex >= len(st.Queue):
		st.Index = len(st.Queue) - 1
	default:
		st.Index = newIndex
	}
	out.State = st
	for _, t := range tracks {
		out.Tracks = append(out.Tracks, t)
	}
	return out, nil
}

func (s *PlaybackStore) tracksByID(ctx context.Context, ids []int64) (map[int64]Track, error) {
	out := map[int64]Track{}
	distinct := map[int64]bool{}
	var list []int64
	for _, id := range ids {
		if !distinct[id] {
			distinct[id] = true
			list = append(list, id)
		}
	}
	for start := 0; start < len(list); start += annotateChunk {
		chunk := list[start:min(start+annotateChunk, len(list))]
		marks := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			marks[i], args[i] = "?", id
		}
		rows, err := s.db.QueryContext(ctx, trackSelect+" WHERE t.id IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		ts, err := collectTracks(rows)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			out[t.ID] = t
		}
	}
	return out, nil
}

// Put saves a state if revision is newer than the stored one; otherwise it
// returns ErrStaleState and keeps the stored state.
func (s *PlaybackStore) Put(ctx context.Context, userID, revision int64, st PlaybackState) error {
	if revision < 1 {
		return &PlaybackInputError{"revision must be a positive integer"}
	}
	if err := st.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO playback_state (user_id, state, revision, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET state = excluded.state, revision = excluded.revision, updated_at = excluded.updated_at
		WHERE excluded.revision > playback_state.revision`, userID, string(b), revision, s.now().Unix())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrStaleState
	}
	return nil
}
