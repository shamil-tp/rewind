-- SPDX-License-Identifier: Apache-2.0
-- User preferences: one row per user and key; keys without a row use the
-- application's default. Keys and values are validated by the application
-- against an allowlist (internal/prefs); values are JSON.

CREATE TABLE user_preferences (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        TEXT    NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
    value      TEXT    NOT NULL CHECK (length(value) BETWEEN 1 AND 1024),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
) STRICT, WITHOUT ROWID;
