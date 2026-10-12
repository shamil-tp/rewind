-- SPDX-License-Identifier: Apache-2.0
-- Playlists: named, ordered lists of indexed tracks, owned by a user.
-- Timestamps are Unix seconds (UTC).

CREATE TABLE playlists (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Trimmed, 1-100 characters (validated by the application).
    name        TEXT    NOT NULL,
    description TEXT,
    created_at  INTEGER NOT NULL,
    -- Changes when the name, description or the list of tracks changes.
    updated_at  INTEGER NOT NULL
) STRICT;

-- Names are unique per user, ignoring ASCII case.
CREATE UNIQUE INDEX playlists_user_name ON playlists (user_id, name COLLATE NOCASE);

CREATE TABLE playlist_tracks (
    playlist_id INTEGER NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    -- Tracks are never deleted when files go missing or a drive is unplugged
    -- (they are marked missing or unavailable), so membership survives. The
    -- scanner moves entries to the surviving ID when it merges a rename; the
    -- cascade only covers rows the scanner deletes after that.
    track_id    INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    -- Sort key within the playlist. Unique, not necessarily contiguous:
    -- removing a track leaves a gap. The API reports 1-based positions.
    position    INTEGER NOT NULL CHECK (position >= 0),
    added_at    INTEGER NOT NULL,
    PRIMARY KEY (playlist_id, track_id),
    UNIQUE (playlist_id, position)
) STRICT, WITHOUT ROWID;

-- Lookups by track: the foreign-key cascade and the scanner's rename merge.
CREATE INDEX playlist_tracks_track ON playlist_tracks (track_id);
