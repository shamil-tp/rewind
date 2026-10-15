-- SPDX-License-Identifier: Apache-2.0
-- Listening experience: favorites, listening history, the persisted play
-- queue, database-backed metadata edits, artwork cache records, playlist
-- artwork, and two more tag fields. Nothing existing is changed or removed.

-- Tags read since this version. metadata_version tells the scanner which
-- files were read by an older extractor; it re-reads those once, even when
-- the file is unchanged. revision increases with every metadata change
-- (scan or edit) and lets the editor detect concurrent changes.
ALTER TABLE tracks ADD COLUMN composer TEXT;
ALTER TABLE tracks ADD COLUMN lyrics TEXT;
ALTER TABLE tracks ADD COLUMN metadata_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

-- Albums are groups of tracks with the same album title and album artist
-- (or artist, when no album artist is tagged). The expression must match
-- library.albumKeyExpr exactly so SQLite can use the index.
CREATE INDEX tracks_album_key ON tracks (
    (lower(coalesce(album_artist, artist, '')) || char(31) || lower(album))
) WHERE album IS NOT NULL;

-- Metadata edits made in Rewind. They are stored in the database only and
-- never written to the music files. fields holds the edited values (JSON
-- object, a null value means "cleared"); original holds the values read from
-- the file for those fields, so an edit can be reverted. The scanner keeps
-- original up to date and re-applies fields after reading a file.
CREATE TABLE track_edits (
    track_id   INTEGER PRIMARY KEY REFERENCES tracks (id) ON DELETE CASCADE,
    fields     TEXT    NOT NULL CHECK (length(fields) BETWEEN 2 AND 65536),
    original   TEXT    NOT NULL CHECK (length(original) BETWEEN 2 AND 65536),
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE favorites (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    track_id   INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, track_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX favorites_track ON favorites (track_id);

-- One row per qualified play (the track played past the user's threshold).
-- play_key is chosen by the client for one playback of one track, so a
-- retried request can never count twice. Tracks are never deleted when their
-- files disappear (they are marked missing), so history survives.
CREATE TABLE play_history (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    track_id    INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    played_at   INTEGER NOT NULL,
    listened_ms INTEGER NOT NULL CHECK (listened_ms >= 0),
    -- Where playback started: library, album, artist, playlist, history or
    -- queue, and an identifier within it (e.g. a playlist ID).
    source_kind TEXT    CHECK (source_kind IS NULL OR length(source_kind) BETWEEN 1 AND 16),
    source_id   TEXT    CHECK (source_id IS NULL OR length(source_id) BETWEEN 1 AND 1024),
    play_key    TEXT    NOT NULL CHECK (length(play_key) BETWEEN 8 AND 64),
    UNIQUE (user_id, play_key)
) STRICT;

CREATE INDEX play_history_user_time ON play_history (user_id, played_at);
CREATE INDEX play_history_user_track ON play_history (user_id, track_id, played_at);
CREATE INDEX play_history_track ON play_history (track_id);

-- The play queue and playback settings of a user, as a validated JSON
-- document (internal/playback). revision is chosen by the client and only
-- grows, so a late, older write cannot replace a newer queue.
CREATE TABLE playback_state (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    state      TEXT    NOT NULL CHECK (length(state) BETWEEN 2 AND 262144),
    revision   INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- Cached artwork: which image a track has, read from its file or a cover
-- image next to it, keyed to the file's size and modification time. The
-- image files live in <data dir>/artwork, named by content hash, so tracks
-- of one album share them.
CREATE TABLE track_artwork (
    track_id      INTEGER PRIMARY KEY REFERENCES tracks (id) ON DELETE CASCADE,
    file_size     INTEGER NOT NULL,
    file_mtime_ns INTEGER NOT NULL,
    status        TEXT    NOT NULL CHECK (status IN ('ok', 'none')),
    hash          TEXT    CHECK (hash IS NULL OR length(hash) = 32),
    source        TEXT    CHECK (source IS NULL OR source IN ('embedded', 'sidecar')),
    checked_at    INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX track_artwork_hash ON track_artwork (hash);

-- A playlist's artwork: the artwork of a chosen track. NULL uses the first
-- track's.
ALTER TABLE playlists ADD COLUMN artwork_track_id INTEGER REFERENCES tracks (id) ON DELETE SET NULL;
