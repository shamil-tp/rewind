-- SPDX-License-Identifier: Apache-2.0
-- Authentication: the owner account and persistent login sessions.
-- Timestamps are Unix seconds (UTC).

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    -- ASCII only (validated by the application), unique case-insensitively.
    username            TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    -- Argon2id hash in PHC string format; never the password itself.
    password_hash       TEXT    NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    password_changed_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    -- SHA-256 of the session token; the token itself is only in the cookie.
    token_hash   BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);
