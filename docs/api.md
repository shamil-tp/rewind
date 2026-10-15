# Rewind HTTP API

Status: **Phase 10.** This page documents only the endpoints that exist:
health, authentication, the read-only music-library API with a rescan
trigger, audio streaming, playlists, preferences, themes and read-only
plugin status.

## Conventions

- All paths are under `/api` and are unversioned. Unknown `/api/*` paths
  return a JSON `404`, never HTML.
- Query parameters are validated strictly. Unknown, repeated, out-of-range
  or non-UTF-8 parameters return `400 invalid_parameter`, with a message
  naming the parameter.
- Timestamps are RFC 3339 strings in UTC. Optional values that are unknown
  (such as a track without an artist tag) are **omitted** from the JSON
  rather than sent as `null` or an empty string.
- Request bodies must be JSON (`Content-Type: application/json`), contain one
  object, and include no unknown fields. JSON bodies are limited to 16 KiB,
  except a playlist reorder (256 KiB), and all bodies to 1 MiB.
- Successful responses are JSON objects. `204 No Content` has no body.
- Errors always have this shape. `code` is stable and meant for programs;
  `message` is meant for people:

  ```json
  { "error": { "code": "invalid_credentials", "message": "invalid username or password" } }
  ```

- Authentication uses an HttpOnly session cookie set by the server
  (`rewind_session`, or `__Host-rewind_session` when
  `REWIND_COOKIE_SECURE=true`). Clients never handle the token directly. No
  token is ever accepted in a URL.
- Responses from `/api/auth/*` carry `Cache-Control: no-store`.
- **Cross-origin protection:** requests using methods other than GET, HEAD or
  OPTIONS are rejected with `403 cross_origin` when the browser marks them as
  cross-site (`Sec-Fetch-Site`), or when `Origin` does not match `Host`.
- The server only answers requests whose `Host` is a loopback name or
  address, an IP address Rewind explicitly listens on, or a name in
  `REWIND_TRUSTED_HOSTS` (`403 forbidden_host` otherwise; see
  [deployment.md](deployment.md#network-access)). Forwarding headers such as
  `X-Forwarded-Host` are ignored.
- No CORS headers are sent. Every endpoint except `health` and
  `auth/status`, `auth/setup` and `auth/login` requires a session,
  whichever network the request comes from.

### Common errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_request` | malformed JSON, unknown fields, trailing data |
| 400 | `invalid_parameter` | invalid query parameter or path ID |
| 401 | `unauthenticated` | no valid session (missing, expired, revoked) |
| 403 | `cross_origin` | cross-origin unsafe request (CSRF protection) |
| 403 | `forbidden_host` | `Host` header is not loopback, a listened-on IP, or a trusted host |
| 404 | `not_found` | unknown API path, or a track or playlist that does not exist (or belongs to another user) |
| 405 | `method_not_allowed` | wrong method; `Allow` header lists the right ones |
| 413 | `body_too_large` | body over the limit |
| 415 | `unsupported_media_type` | body is not `application/json` |
| 422 | `validation_failed` | well-formed JSON with an invalid value (the message says which) |
| 429 | `rate_limited` | too many authentication attempts; see `Retry-After` |
| 500 | `internal_error` | server failure; details are only in the server log |

## Endpoints

### `GET /api/health`

No authentication. Reports that the process is serving HTTP. It does not
check the database.

`200` → `{"status": "ok", "name": "rewind", "version": "…"}`

### `GET /api/auth/status`

No authentication. Never names the account.

`200` → `{"setup_required": true|false, "authenticated": true|false}`

### `POST /api/auth/setup`

Creates the owner account. Available only while no account exists, only from
a client connecting from a loopback IP address (`127.0.0.0/8` or `::1`, checked
on the TCP connection; forwarding headers are ignored) that also uses a
loopback `Host` (such as `localhost:8090`), and only with the one-time setup
code. Setup is therefore never possible through `tailscale serve`, a LAN
address or a trusted host name. Rate-limited. On success, the new owner is signed in.

```json
{ "setup_code": "ABCD-EFGH-IJKL-MNOP", "username": "owner", "password": "…" }
```

The setup code ignores case, spaces and dashes.

| Status | Result |
| --- | --- |
| 201 | `{"user": {"id": 1, "username": "owner"}}` plus session cookie |
| 403 `setup_not_local` | client is not on a loopback address, or `Host` is not a loopback name |
| 403 `invalid_setup_code` | wrong or missing code |
| 409 `setup_complete` | an account already exists |
| 422 `validation_failed` | username or password breaks the rules (`message` says which) |
| 503 `setup_unavailable` | no code is loaded; restart Rewind |

Username: 1–64 characters from `A–Z a–z 0–9 . _ -`, unique regardless of
case. Password: 12–256 characters (Unicode characters, not bytes), and not
equal to the username.

### `POST /api/auth/login`

Rate-limited. Signs in and sets a new session cookie. If the request already
carries a session cookie, that session is revoked first (session rotation).

```json
{ "username": "owner", "password": "…" }
```

The username is case-insensitive.

| Status | Result |
| --- | --- |
| 200 | `{"user": {"id": 1, "username": "owner"}}` plus session cookie |
| 401 `invalid_credentials` | unknown user or wrong password (identical responses) |

### `POST /api/auth/logout`

Revokes the current session on the server and clears the cookie. Succeeds
even without a session.

`204`

### `GET /api/auth/me`

Requires a session.

`200` → `{"user": {"id": 1, "username": "owner"}}`

### `POST /api/auth/change-password`

Requires a session. Rate-limited.

```json
{ "current_password": "…", "new_password": "…" }
```

On success, **every** session of the account is revoked, including those on
other devices, and the caller gets a new session cookie.

| Status | Result |
| --- | --- |
| 200 | `{"user": {…}}` plus new session cookie |
| 403 `invalid_current_password` | current password is wrong |
| 422 `validation_failed` | new password breaks the rules or equals the current one |

## Music library

All library endpoints **require a session** (`401 unauthenticated`
otherwise) and send `Cache-Control: no-store`.

**Authorization:** Rewind has a single account, the owner created at setup,
so any valid session is the owner. That session is therefore allowed to read
the library and to trigger rescans. There are no other roles yet, so no
library request currently returns `403` for authorization. `403` responses
come only from the CSRF (`cross_origin`) and Host (`forbidden_host`) checks.

Library roots are configured only through `REWIND_LIBRARY_ROOTS`; the API
cannot add, change or remove them. No endpoint accepts a filesystem path, and
responses contain only paths **relative to a root**, never absolute paths.
Stored error messages are cleaned of root paths.

### `GET /api/library`

Library overview. Takes no query parameters.

```json
{
  "tracks": 1234,
  "available_tracks": 1200,
  "missing_tracks": 3,
  "artists": 85,
  "albums": 140,
  "total_duration_ms": 312000000,
  "roots": {"total": 2, "available": 1, "unavailable": 1, "pending": 0, "disabled": 0},
  "last_successful_scan_at": "2026-10-11T18:00:00Z",
  "scan": {
    "running": true,
    "active": [{"root_id": 1, "scan_id": 7, "started_at": "2026-10-11T18:05:00Z", "counts": {"files_found": 120}}],
    "queued": [2]
  }
}
```

(`counts` contains the same ten counters as `last_scan.counts` below;
shortened here.)

| Field | Meaning |
| --- | --- |
| `tracks` | tracks whose files were found, in enabled roots, **including roots that are currently unavailable** (an unplugged drive is not an empty library) |
| `available_tracks` | of those, tracks on roots that are currently available |
| `missing_tracks` | tracks whose files were not found by a complete scan |
| `artists`, `albums` | distinct, case-insensitive; tracks without the tag are not counted |
| `roots` | enabled roots by status (`total` = enabled), plus `disabled` (no longer configured, tracks kept) |
| `last_successful_scan_at` | finish time of the latest `completed` scan (omitted if none) |
| `scan` | live scanner state: scans in progress (`active`) and roots waiting their turn (`queued`) |

### `GET /api/library/roots`

All registered roots. Takes no query parameters.

```json
{
  "items": [
    {
      "id": 1,
      "name": "MUSICSTICK",
      "enabled": true,
      "status": "unavailable",
      "status_message": "cannot access directory: does not exist",
      "track_count": 1200,
      "missing_count": 3,
      "scanning": false,
      "queued": false,
      "last_scan": {
        "id": 7,
        "status": "failed",
        "started_at": "2026-10-11T18:00:00Z",
        "finished_at": "2026-10-11T18:00:01Z",
        "counts": {"files_found": 0, "files_skipped": 0, "files_failed": 0, "dirs_failed": 0,
                   "tracks_added": 0, "tracks_updated": 0, "tracks_unchanged": 0,
                   "tracks_renamed": 0, "tracks_missing": 0, "tracks_restored": 0},
        "error": "library root unavailable: cannot access directory: does not exist; no tracks were marked missing"
      }
    }
  ]
}
```

- `name` is the last element of the configured path (for example
  `/media/you/MUSICSTICK` becomes `MUSICSTICK`); the path itself is not
  returned.
- `status` is `pending`, `available` or `unavailable`.
- `last_scan.status` is `running`, `completed`, `partial`, `failed` or
  `interrupted`; see [library.md](library.md#reading-scan-results).
  `last_scan` is omitted for a root that has never been scanned.
- `track_count` counts tracks whose files were found. These tracks are kept
  while the root is unavailable.

### `GET /api/tracks`

One page of tracks. By default this means tracks whose files were found
(`status=present`) in enabled roots, sorted by title.

| Parameter | Default | Values |
| --- | --- | --- |
| `page` | `1` | 1–1,000,000 |
| `limit` | `50` (or the maximum, if lower) | 1 to `REWIND_API_MAX_PAGE_SIZE` (default 200) |
| `q` | | up to 200 characters; case-insensitive substring of title, artist, album or album artist (`%` and `_` match literally) |
| `artist`, `album`, `album_artist`, `genre` | | exact match, case-insensitive; up to 512 characters |
| `root` | | root ID |
| `format` | | `mp3`, `flac`, `m4a`, `ogg`, `wav` |
| `year` | | 1000–9999 |
| `status` | `present` | `present`, `missing` (files not found), `all` |
| `sort` | `title` | `title`, `artist`, `album`, `album_artist`, `year`, `duration`, `added` |
| `order` | `asc` | `asc`, `desc` |

Sorting details:

- `artist`, `album` and `album_artist` sort by that field, then album, disc,
  track number and title, so albums come out in track order.
- Tracks without the sort field always come last, in either direction.
- Ties are broken by track ID, so pages are stable.
- `added` sorts by when Rewind first indexed the file.
- Case-insensitive matching and sorting apply to ASCII letters only.

```json
{
  "items": [
    {
      "id": 42,
      "root_id": 1,
      "title": "Opening",
      "artist": "Band",
      "album": "Record",
      "year": 2001,
      "track_number": 1,
      "duration_ms": 213000,
      "format": "flac",
      "size_bytes": 23456789,
      "relative_path": "Band/Record/01.flac",
      "status": "present",
      "available": true,
      "metadata_status": "ok",
      "added_at": "2026-10-11T17:00:00Z",
      "updated_at": "2026-10-11T17:00:00Z"
    }
  ],
  "pagination": {"page": 1, "limit": 50, "total": 1, "total_pages": 1}
}
```

Track fields:

| Field | Presence | Meaning |
| --- | --- | --- |
| `id` | always | stable track ID; kept across rescans and unique renames |
| `root_id`, `title`, `format`, `size_bytes`, `relative_path`, `status`, `available`, `metadata_status`, `added_at`, `updated_at` | always | `title` falls back to the file name; `available` is true when the file was found **and** its root is enabled and currently available |
| `artist`, `album`, `album_artist`, `genre`, `release_date`, `year`, `track_number`, `disc_number`, `duration_ms`, `codec` | if known | from the file's tags and audio stream |
| `metadata_error` | if metadata failed | short reason, with root paths removed |
| `missing_since` | if `status` is `missing` | when the file was first found missing |

A `page` beyond the last page returns an empty `items` list with the correct
`total`. Tracks of disabled roots are not listed.

### `GET /api/tracks/{id}`

One track, in the same shape, as `{"track": {...}}`. This also returns
tracks of disabled roots and missing tracks (with `available: false`).

| Status | Result |
| --- | --- |
| 200 | `{"track": {...}}` |
| 400 `invalid_parameter` | the ID is not a positive integer |
| 404 `not_found` | no such track |

### `POST /api/library/scan`

Starts a background rescan with the library scanner and returns immediately.
Optional body:

```json
{ "root_ids": [1, 2] }
```

An empty body (no `Content-Type` needed) or an empty `root_ids` list scans
every enabled root. Roots are scanned one after another.

| Status | Result |
| --- | --- |
| 202 | `{"started": [1, 2], "already_running": []}`: these roots were queued; roots already scanning or queued are listed in `already_running` and are not scanned twice |
| 400 `invalid_parameter` / `invalid_request` | invalid IDs (more than 100, or non-positive), malformed JSON, unknown fields |
| 404 `root_not_found` | a listed root does not exist (nothing is queued) |
| 409 `root_disabled` | a listed root is no longer configured (nothing is queued) |
| 409 `scan_in_progress` | every requested root is already scanning or queued |
| 409 `no_library_roots` | `REWIND_LIBRARY_ROOTS` lists no usable root |
| 503 `shutting_down` | the server is stopping |

Scans follow the library rules in [library.md](library.md):

- An unavailable root's scan is recorded as `failed` and its tracks are kept.
- Missing files are reconciled only after a complete traversal.
- Shutdown interrupts a running scan safely.

Poll `GET /api/library` (the `scan` object) or `GET /api/library/roots`
(`scanning`, `queued`, `last_scan`) to follow progress. The scan ID is
created when each root's scan begins, so it appears there, not in the 202
response.

### `GET|HEAD /api/tracks/{id}/stream`

Streams the audio file of an indexed track. It requires a session (`401`
otherwise); browsers send the session cookie automatically, because the
player requests this URL from the same origin. The track is identified only
by its numeric ID; no request parameter can name a file, and the endpoint
takes no query parameters.

**Responses:**

| Status | When | Notes |
| --- | --- | --- |
| 200 | no `Range` header | whole file; `Content-Length`, `Accept-Ranges: bytes` |
| 206 | valid `Range` | `Content-Range: bytes <start>-<end>/<size>` and the partial `Content-Length`; several ranges give `multipart/byteranges` |
| 416 | range starts beyond the end of the file | `Content-Range: bytes */<size>` |
| 416 | malformed `Range` (for example `bytes=abc` or another unit) | short plain-text body; Go's `http.ServeContent` behaviour |
| 304 / 412 | conditional requests (`If-None-Match`, `If-Modified-Since`, …) | standard semantics |
| 400 `invalid_parameter` | the ID is not a positive integer, or a query parameter is present | |
| 404 `not_found` | no such track | |
| 404 `file_unavailable` | the indexed file is gone, is not a regular file, is a symbolic link, or its stored path is unsafe | the reason is logged on the server without paths |
| 409 `root_disabled` | the track's root is no longer in `REWIND_LIBRARY_ROOTS` | |
| 503 `root_unavailable` | the track's root cannot be opened (drive unplugged or unmounted) | |

Supported range forms include `bytes=0-1023`, `bytes=1024-` (to the end),
`bytes=-1024` (the last 1024 bytes), and ranges that extend past the end of
the file, which are clipped. A `Range` on an **empty file** is ignored and
answered with `200` and an empty body (also `ServeContent` behaviour).

**Headers:**

- `Content-Type` comes from the format recorded when the file was indexed
  (from its extension):

  | Format | Content-Type |
  | --- | --- |
  | MP3 | `audio/mpeg` |
  | FLAC | `audio/flac` |
  | M4A | `audio/mp4` |
  | Ogg | `audio/ogg` |
  | WAV | `audio/wav` |
  | anything else | `application/octet-stream` |

  The client's `Accept` header has no influence.
- `ETag` is derived from the track ID, size and modification time, so a
  changed file invalidates `If-Range`. `Last-Modified` is also sent.
- `Cache-Control: private, no-store`.
- The usual security headers (`X-Content-Type-Options: nosniff`, CSP, …)
  are sent as well.

**Seeking.** The browser's `<audio>` element requests `bytes=0-` first.
When the user seeks past what it has buffered, it sends a new request such
as `bytes=23986176-` and receives `206`, so only the needed part of the file
is transferred. No audio is transcoded; the file is sent as stored.

**Playback support depends on the browser.** The server sends any indexed
format, but the browser must be able to decode it. For example, ALAC inside
`.m4a` is not supported by every browser. When a browser cannot play a file,
the player says so; it checks the stream with a `HEAD` request to tell
decoding problems apart from missing files, unplugged drives and expired
sessions.

## Playlists

Every playlist endpoint **requires a session**, sends `Cache-Control:
no-store`, and passes the cross-origin check for changes. Playlists belong to
the signed-in user: every query is scoped to the session's user ID, and a
playlist of another user answers exactly like one that does not exist
(`404 not_found`). Today the owner is the only account.

A playlist lists **indexed tracks by ID**; it never holds paths or audio, and
playing it uses the normal streaming endpoint. Removing a track from a
playlist, or deleting a playlist, never touches the library or any file.

**Limits:** names are 1–100 characters (trimmed, no control characters) and
unique per user ignoring ASCII case. Descriptions are optional, up to 500
characters, and may contain line breaks. A user can have 1,000 playlists, and
a playlist 5,000 tracks.

**Order and duplicates:** tracks are added at the end. A track can appear in
a playlist **only once**; adding it again returns `409 already_in_playlist`.
Removing a track keeps the order of the others. `position` in responses is
always 1, 2, 3, … in playlist order. The order changes only through
`PUT …/tracks`, which replaces it as a whole.

**Unavailable tracks stay in playlists.** An unplugged drive, a folder
removed from `REWIND_LIBRARY_ROOTS`, or a file that a scan no longer finds
does not remove anything: the entry keeps its place, and its `track` has
`"available": false` (and `"status": "missing"` for a missing file). When the
file or drive comes back, the same track ID is available again. If a scan
detects a rename, the entry keeps pointing at the renamed track. See
[library.md](library.md#disconnected-drives-and-missing-files).

**Playlist object:**

```json
{
  "id": 3,
  "name": "Road trip",
  "description": "For the car",
  "track_count": 12,
  "unavailable_count": 1,
  "duration_ms": 2745000,
  "created_at": "2026-10-12T08:00:00Z",
  "updated_at": "2026-10-12T09:30:00Z"
}
```

`description` is omitted when empty. `duration_ms` sums the known durations.
`updated_at` changes when the name, the description or the tracks change.

| Status | `code` | When |
| --- | --- | --- |
| 404 | `not_found` | playlist does not exist or is not yours |
| 404 | `track_not_found` | `track_id` is not an indexed track |
| 404 | `not_in_playlist` | removing a track that is not in the playlist |
| 409 | `name_taken` | another of your playlists has this name |
| 409 | `already_in_playlist` | the track is already in the playlist |
| 409 | `playlist_full` | 5,000 tracks already |
| 409 | `too_many_playlists` | 1,000 playlists already |
| 409 | `order_mismatch` | a reorder did not list exactly the current tracks; reload and retry |
| 422 | `validation_failed` | invalid name, description or IDs in the body |

### `GET /api/playlists`

Your playlists sorted by name, without their tracks:
`{"items": [Playlist, …]}`. One query computes all the counts.

### `POST /api/playlists`

Body: `{"name": "Road trip", "description": "For the car"}` (`description`
optional). Returns `201 {"playlist": Playlist}`.

### `GET /api/playlists/{id}`

The playlist and all its entries in order (no paging; at most 5,000):

```json
{
  "playlist": {
    "id": 3, "name": "Road trip", "track_count": 2, "unavailable_count": 1, "…": "…",
    "entries": [
      { "position": 1, "added_at": "2026-10-12T08:01:00Z", "track": { "id": 42, "title": "…", "available": true, "…": "…" } },
      { "position": 2, "added_at": "2026-10-12T08:02:00Z", "track": { "id": 7, "title": "…", "available": false, "status": "missing", "…": "…" } }
    ]
  }
}
```

Each `track` has the same fields as in `GET /api/tracks`.

### `PATCH /api/playlists/{id}`

Body: `{"name": "…"}`, `{"description": "…"}` or both. An empty description
removes it. At least one field is required. Returns `{"playlist": Playlist}`.

### `DELETE /api/playlists/{id}`

Deletes the playlist and its entries in one statement. `204`.

### `POST /api/playlists/{id}/tracks`

Body: `{"track_id": 42}`. Appends the track. Any indexed track can be added,
including one that is unavailable right now. Returns
`201 {"entry": {"position": 13, "added_at": "…", "track": Track}}`.

### `PUT /api/playlists/{id}/tracks`

Body: `{"track_ids": [7, 42, 13]}`: the **complete** new order, each current
track exactly once. Anything else (a missing, extra, repeated or unknown ID,
for example because the playlist changed in another tab) returns `409
order_mismatch` and changes nothing. The order is rewritten in one
transaction. Returns the playlist like `GET /api/playlists/{id}`.

### `DELETE /api/playlists/{id}/tracks/{trackId}`

Removes one track from the playlist. `204`.

## Preferences

### `GET /api/preferences`

Requires a session; `Cache-Control: no-store`.

```json
{
  "preferences": { "library_sort": "added", "autoplay_next": true, "theme": "light", "accent_color": "",
                   "primary_color": "", "secondary_color": "" },
  "defaults": { "library_sort": "artist", "autoplay_next": true, "theme": "rewind-dark", "accent_color": "",
                "primary_color": "", "secondary_color": "" }
}
```

`preferences` always contains every key: saved values where valid, defaults
elsewhere. A stored value that is no longer valid (for example after an
upgrade) is ignored in favour of the default.

| Key | Values | Default | Effect in the web UI |
| --- | --- | --- | --- |
| `library_sort` | `artist`, `album`, `title`, `added`, `year`, `duration` | `artist` | Initial sort of the library. `added` means newest first and `duration` longest first; the others are ascending. |
| `autoplay_next` | `true`, `false` | `true` | When a playlist track ends, play the next available one. |
| `theme` | The ID of an available theme (bundled or installed) | `rewind-dark` | The active theme. Prefer `PUT /api/themes/active`, which also checks that the theme is enabled and records it for "return to the previous theme". A saved ID that is no longer available reads as `rewind-dark`. |
| `accent_color` | `""` or `#rrggbb` (six **lower-case** hex digits) | `""` | Earlier custom colors, kept for compatibility: these three apply only to themes the user has not customized (reported as their `legacy` customization, see [Themes](#themes)). Custom accent (progress, playing markers, focus ring); `""` uses the theme's. The server checks only the format; the web UI also requires 4.5:1 contrast with the theme in use and ignores an accent that falls short. |
| `primary_color` | `""` or `#rrggbb` | `""` | Custom primary color (main buttons, links, selected navigation). `""` uses a custom `accent_color` if one is set, otherwise the theme's. Same checks as `accent_color`. |
| `secondary_color` | `""` or `#rrggbb` | `""` | Custom secondary color (secondary buttons, supporting surfaces). `""` uses the theme's. The server checks only the format. |

### `PATCH /api/preferences`

Body: an object with one or more keys from the table, for example
`{"autoplay_next": false}`. All values are checked first. An unknown key, a
wrong type, `null` or a value not in the list returns `422 validation_failed`
and saves nothing. On success, the changes are saved in one transaction, and
the response is the same as for `GET`. Preferences are per user and never
contain secrets.

## Themes

Themes are declarative sets of design-token values (see
[themes.md](themes.md)). Every route requires a session and returns
`Cache-Control: no-store`. Every successful response, including changes,
returns the **complete theme state** below, so the client always has one
consistent copy.

There are deliberately **no routes to install, upload, update or remove
themes**: the administrator installs them in `REWIND_THEME_DIR` and restarts
Rewind. No route reads the theme directory or returns a file path.

### `GET /api/themes`

```json
{
  "theme_api": 1,
  "directory_configured": true,
  "active": "light",
  "default_theme": "rewind-dark",
  "items": [
    {
      "id": "light", "name": "Rewind Light", "version": "1.0.0",
      "description": "Near-white paper background, …", "author": "Rewind contributors",
      "scheme": "light", "source": "bundled", "state": "available",
      "enabled": true, "active": true,
      "tokens": { "color.background": "#f5f5f0", "radius.button": 10, "font.family": "system", "…": "…" },
      "customizable": ["color.background", "color.surface", "…"],
      "customization": { "color.accent": "#0055aa" },
      "customization_source": "saved"
    },
    { "id": "broken", "source": "installed", "state": "invalid", "error": "theme.json is missing",
      "enabled": false, "active": false, "customization": {}, "customization_source": "none" }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `theme_api` | The token contract this server provides. |
| `directory_configured` | Whether `REWIND_THEME_DIR` is set. The path is never sent. |
| `active` | The theme shown: always available and enabled. When the saved theme is not (removed, invalid, disabled), this is `default_theme` and `notice` explains why. |
| `notice` | Optional text written by Rewind. |
| `items[].source` | `bundled` or `installed`. Bundled first, then installed by directory name. |
| `items[].state` | `available`, `invalid` (rejected; `error` says why) or `incompatible` (written for another `theme_api`). Only available themes have `tokens` and `customizable`. |
| `items[].tokens` | The theme's own value for **every** token (omitted optional tokens filled in). |
| `items[].customizable` | Tokens the user may change in this theme. |
| `items[].customization` | The user's values for this theme: valid, allowed values that differ from the theme's. The web UI also drops colors that would be unreadable and says so. |
| `items[].customization_source` | `saved` (the user saved values), `legacy` (never customized, so the earlier `primary_color`/`secondary_color`/`accent_color` preferences apply; an unset primary follows the accent), or `none`. |

`name`, `description`, `author` and `homepage` come from the manifest and are
untrusted: render them as plain text.

### `PUT /api/themes/active`

Body: `{"id": "light"}`. Makes an available, enabled theme active, records
when it was applied, and saves it as the `theme` preference, all in one
transaction.

| Status | Code | When |
| --- | --- | --- |
| `200` | | The new state. |
| `404` | `theme_not_found` | Unknown ID, or the theme is invalid or incompatible. |
| `409` | `theme_disabled` | The theme is disabled; enable it first. |
| `422` | `validation_failed` | `id` is missing. |

### `PUT /api/themes/{id}/enabled`

Body: `{"enabled": false}`. Enables or disables a theme for the user.
Disabling the **active** theme makes the most recently applied theme that is
still available and enabled active, or else the default, in the same
transaction. The default theme cannot be disabled (`422 validation_failed`).
Unknown or unavailable themes return `404 theme_not_found`.

### `PUT /api/themes/{id}/customization`

Body: `{"tokens": {"color.accent": "#ff8800", "radius.button": 4}}`. Replaces
the user's customization of that theme. Every value is checked first:

- the token must exist and be in the theme's `customizable` list;
- colors must be lower-case `#rrggbb`, choices one of the listed values, and
  numbers within the range and step.

Any failure returns `422 validation_failed` naming the token, and saves
nothing. Values equal to the theme's own are dropped. `{"tokens": {}}`
resets the theme to its defaults, which also stops the legacy colors from
applying to it. Other themes' customizations are never changed. The server
checks format and range. The web UI also checks contrast before saving and
ignores unreadable colors when showing a theme. Customizing a disabled theme
is allowed.

## Plugins

### `GET /api/plugins`

Requires a session; `Cache-Control: no-store`. Read-only status of the
plugin system (see [plugins.md](plugins.md)). It runs no plugin code and
changes nothing; there are no endpoints to install, enable, disable or run
plugins.

```json
{
  "configured": true,
  "api_version": 1,
  "items": [
    {
      "id": "missing-tracks",
      "name": "Missing tracks warning",
      "version": "1.0.0",
      "description": "Logs a warning when a library scan marks tracks as missing.",
      "author": "Rewind contributors",
      "capabilities": ["events.library_scan"],
      "state": "running"
    },
    { "id": "old-plugin", "capabilities": [], "state": "invalid", "error": "unsupported manifest schema 2; this version of Rewind reads schema 1" }
  ]
}
```

- `configured` is `false` when `REWIND_PLUGIN_DIR` is not set; `items` is
  then empty.
- `state` is one of `running`, `disabled` (installed, not enabled), `failed`,
  `invalid`, `incompatible`, `unavailable` (not compiled into this build) and
  `missing` (enabled, no manifest installed). Items are sorted by `id`.
- `name`, `description`, `author` and `homepage` come from the manifest and
  are untrusted text: display them as text only. Fields are omitted when the
  manifest does not provide them or was rejected.
- `error` is a short summary written by Rewind. A plugin's own error
  messages, file system paths and configuration values never appear here;
  they are in the server log.

### Trying the API with curl

These examples use the default loopback address, so run them on the server. The cookie jar
keeps the HttpOnly session cookie. curl sends no `Origin` or
`Sec-Fetch-Site` header, so the cross-origin check treats it as a non-browser
client and allows its POSTs.

To keep your password out of shell history, put the login body in a file
only you can read:

```sh
umask 077
printf '{"username":"%s","password":"%s"}\n' 'owner' 'REPLACE-WITH-YOUR-PASSWORD' > ~/rewind-login.json
```

Then:

```sh
B=http://127.0.0.1:8090
jar=$(mktemp)

curl -sS -c "$jar" -H 'Content-Type: application/json' -d @"$HOME/rewind-login.json" "$B/api/auth/login"

curl -sS -b "$jar" "$B/api/library"
curl -sS -b "$jar" "$B/api/library/roots"
curl -sS -b "$jar" "$B/api/tracks?limit=20&sort=artist"
curl -sS -b "$jar" "$B/api/tracks?q=beatles&format=flac"
curl -sS -b "$jar" "$B/api/tracks/42"

# Stream a track: headers only, the whole file, or part of it.
curl -sS -b "$jar" -I "$B/api/tracks/42/stream"
curl -sS -b "$jar" -o track.flac "$B/api/tracks/42/stream"
curl -sS -b "$jar" -H 'Range: bytes=0-1023' -o first-1k.bin -D - "$B/api/tracks/42/stream"
curl -sS -b "$jar" -H 'Range: bytes=-1024' -o last-1k.bin -D - "$B/api/tracks/42/stream"

# Rescan everything, or only some roots:
curl -sS -b "$jar" -X POST "$B/api/library/scan"
curl -sS -b "$jar" -H 'Content-Type: application/json' -d '{"root_ids":[1]}' "$B/api/library/scan"

# Playlists: create one, add two tracks, swap them, look, delete.
J='Content-Type: application/json'
curl -sS -b "$jar" -H "$J" -d '{"name":"Test"}' "$B/api/playlists"   # note the returned id, here 1
curl -sS -b "$jar" -H "$J" -d '{"track_id":42}' "$B/api/playlists/1/tracks"
curl -sS -b "$jar" -H "$J" -d '{"track_id":7}' "$B/api/playlists/1/tracks"
curl -sS -b "$jar" -H "$J" -X PUT -d '{"track_ids":[7,42]}' "$B/api/playlists/1/tracks"
curl -sS -b "$jar" "$B/api/playlists/1"
curl -sS -b "$jar" -X DELETE "$B/api/playlists/1/tracks/42"
curl -sS -b "$jar" -X DELETE "$B/api/playlists/1"

# Preferences:
curl -sS -b "$jar" "$B/api/preferences"
curl -sS -b "$jar" -H "$J" -X PATCH -d '{"library_sort":"added"}' "$B/api/preferences"

curl -sS -b "$jar" -X POST "$B/api/auth/logout"
rm -f "$jar"
```
