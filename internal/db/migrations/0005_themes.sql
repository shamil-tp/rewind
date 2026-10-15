-- SPDX-License-Identifier: Apache-2.0
-- Per-user, per-theme settings (internal/theme). Theme definitions are not
-- stored here: they come from the binary (bundled) or the administrator's
-- theme directory. A theme without a row is enabled and has never been
-- customized. Rows of a theme that is later removed are kept, so its
-- settings come back if it is installed again.
--
-- The active theme stays in user_preferences (key "theme").

CREATE TABLE theme_settings (
    user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    theme_id        TEXT    NOT NULL CHECK (length(theme_id) BETWEEN 1 AND 40),
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    -- JSON object of validated token values; NULL means never customized
    -- (the legacy custom colors from user_preferences then apply).
    tokens          TEXT    CHECK (tokens IS NULL OR length(tokens) BETWEEN 2 AND 4096),
    -- Unix time this theme was last applied; used to return to the previous
    -- theme when the active one is disabled.
    last_applied_at INTEGER,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (user_id, theme_id)
) STRICT, WITHOUT ROWID;
