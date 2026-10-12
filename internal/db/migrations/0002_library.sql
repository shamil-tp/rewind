-- SPDX-License-Identifier: Apache-2.0
-- Music library: configured roots, indexed tracks, and scan history.
-- Timestamps are Unix seconds (UTC) unless the column name says otherwise.

CREATE TABLE library_roots (
    id              INTEGER PRIMARY KEY,
    -- Path as written in REWIND_LIBRARY_ROOTS (cleaned, absolute). Used to
    -- recognize the root while its directory is unavailable.
    configured_path TEXT    NOT NULL UNIQUE,
    -- Canonical path (symlinks resolved) from the last successful
    -- registration. Scans traverse this path.
    path            TEXT    NOT NULL UNIQUE,
    -- 1 while the root is listed in REWIND_LIBRARY_ROOTS. Removed roots are
    -- disabled, never deleted, so their tracks are preserved.
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    status          TEXT    NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'available', 'unavailable')),
    status_message  TEXT,
    created_at      INTEGER NOT NULL,
    last_scan_id    INTEGER
) STRICT;

CREATE TABLE scans (
    id               INTEGER PRIMARY KEY,
    root_id          INTEGER NOT NULL REFERENCES library_roots (id),
    started_at       INTEGER NOT NULL,
    finished_at      INTEGER,
    -- completed:   the whole root was traversed; missing files reconciled.
    -- partial:     some directories could not be read; nothing marked missing.
    -- failed:      the root was unavailable, vanished, or looked unmounted.
    -- interrupted: stopped by shutdown; nothing marked missing.
    -- running:     in progress (or the process died; see docs).
    status           TEXT    NOT NULL
                             CHECK (status IN ('running', 'completed', 'partial', 'failed', 'interrupted')),
    files_found      INTEGER NOT NULL DEFAULT 0, -- supported audio files seen
    files_skipped    INTEGER NOT NULL DEFAULT 0, -- unsupported, hidden, symlinks
    files_failed     INTEGER NOT NULL DEFAULT 0, -- metadata could not be read
    dirs_failed      INTEGER NOT NULL DEFAULT 0, -- directories that could not be read
    tracks_added     INTEGER NOT NULL DEFAULT 0,
    tracks_updated   INTEGER NOT NULL DEFAULT 0,
    tracks_unchanged INTEGER NOT NULL DEFAULT 0,
    tracks_renamed   INTEGER NOT NULL DEFAULT 0,
    tracks_missing   INTEGER NOT NULL DEFAULT 0, -- newly marked missing
    tracks_restored  INTEGER NOT NULL DEFAULT 0, -- missing tracks found again
    error            TEXT                        -- short summary, bounded length
) STRICT;

CREATE INDEX scans_root_id ON scans (root_id, id);

CREATE TABLE tracks (
    id              INTEGER PRIMARY KEY,
    root_id         INTEGER NOT NULL REFERENCES library_roots (id),
    -- Path relative to the root, '/'-separated. (root_id, rel_path) is the
    -- track's logical identity.
    rel_path        TEXT    NOT NULL,
    file_size       INTEGER NOT NULL,
    file_mtime_ns   INTEGER NOT NULL,
    format          TEXT    NOT NULL, -- mp3, flac, m4a, ogg, wav (from the extension)
    codec           TEXT,             -- from the audio stream, e.g. aac, alac, vorbis
    title           TEXT    NOT NULL, -- tag, or file name without extension
    artist          TEXT,
    album           TEXT,
    album_artist    TEXT,
    genre           TEXT,
    release_date    TEXT,             -- raw date tag
    year            INTEGER,
    track_number    INTEGER,
    disc_number     INTEGER,
    duration_ms     INTEGER,
    metadata_status TEXT    NOT NULL CHECK (metadata_status IN ('ok', 'no_tags', 'error')),
    metadata_error  TEXT,
    status          TEXT    NOT NULL DEFAULT 'present' CHECK (status IN ('present', 'missing')),
    missing_since   INTEGER,
    first_seen_at   INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    UNIQUE (root_id, rel_path)
) STRICT;

CREATE INDEX tracks_root_status ON tracks (root_id, status);
CREATE INDEX tracks_root_size ON tracks (root_id, file_size);
