# Rewind Architecture

Status: **Phase 12, plus the specification's "Phase 2" listening experience
(§5f).** Sections marked *implemented* describe working code. All
other sections describe intended behavior and are updated as each phase lands.

## 1. Goals and constraints

- One process and one SQLite file. No Redis, brokers, search servers, or
  external services.
- Primary target is Linux (Ubuntu Server / Debian) on amd64. The design stays
  portable, and linux/arm64 is a later target.
- Hardware is assumed to be modest, but no particular specification is
  assumed. Exact hardware is collected from the owner when a phase needs it
  (for example, measuring resource use in Phase 10).
- Music comes from a USB drive that the operating system has already mounted.
  Rewind never mounts, formats, or writes to it.
- Clients use the LAN or Tailscale. Rewind is never exposed publicly by
  default.

## 2. System overview

```
 Browser / PWA ──HTTP(S)──▶ rewind (single Go binary)
                              ├─ embedded frontend (Svelte build)
                              ├─ JSON API   /api/health, /api/auth/*, /api/library*, /api/tracks*
                              ├─ audio      /stream/{trackId}
                              ├─ scanner    (background goroutine, on demand)
                              └─ SQLite     $REWIND_DATA_DIR/rewind.db
                                   │
                    read-only ─────┴──▶ /mnt/usb-music (OS-mounted USB)
```

Remote access is provided by the deployment environment (Tailscale on the
host). Rewind contains no VPN or Tailscale code.

API paths (*implemented*):

- All endpoints are under `/api/` and are **unversioned**: `/api/health`,
  `/api/auth/*`, `/api/library*` and `/api/tracks*`.
- The Phase 0 plan put feature endpoints under `/api/v1/`. Phase 4 followed
  the convention already implemented by `/api/auth/*`, as the owner's Phase 4
  specification requested.
- A version prefix can be introduced later, alongside the old paths, if an
  incompatible change is ever needed.
- Any unknown `/api/*` path returns a JSON 404, never the frontend.
- Errors share one shape: `{"error": {"code": "...", "message": "..."}}`.
- Endpoints are documented in [api.md](api.md).

## 3. Backend layout (Go)

*Implemented* (Phases 1–3):

```
cmd/rewind/          entry point: config, logging, data dir, database, setup
                     code, library roots + background startup scan, server
                     start, signal-driven graceful shutdown (10 s limit),
                     scan stop, database close
internal/config/     REWIND_* environment variables -> validated Config
internal/db/         SQLite open (0600 file, pragmas, single connection),
                     migration runner, transaction helper
  migrations/        numbered .sql files, embedded with go:embed
internal/auth/       Argon2id password hashing, account setup, login,
                     persistent sessions, setup code, rate limiter
internal/metadata/   read-only tag/property reader wrapping go-taglib
internal/library/    library roots (SyncRoots), scanner (walker, bounded
                     readers, batched writer), reconciliation, scan history,
                     background scan queue (Start/Queued/Close), the
                     read-only Catalog used by the library API, the
                     per-user Playlists store, albums and artists (browse),
                     metadata edits, favorites and history (personal),
                     the saved queue (playback) and the artwork cache (§5f)
internal/prefs/      per-user preferences: allowlisted keys, defaults, storage
internal/plugin/     plugin API (Plugin, Host, ScanEvent), compile-time registry,
                     manifest validation, discovery, lifecycle manager
plugins/             compiled-in plugins; cmd/rewind/plugins.go imports them
internal/testaudio/  test-only generator of tiny silent audio files
internal/server/     ServeMux routes, JSON API helpers, auth handlers and
                     middleware, frontend handler, middleware (security
                     headers, Host check, CSRF, body limit), loopback
                     listener, server lifecycle
web/                 Svelte + TypeScript + Vite project, plus Go package `web`:
  embed_placeholder.go  default build: embeds web/placeholder (says "not built")
  embed_dist.go         -tags rewind_embed: embeds web/dist (compile fails if absent)
scripts/build.sh     production build: npm ci + vite build + verify + go build
```

Further packages are created only when their phase needs them.

Frontend embedding: the default build embeds a placeholder, so `go build`,
`go test` and `go vet` work on a clean checkout without Node.js. The real
frontend is embedded only with the `rewind_embed` build tag, which
`scripts/build.sh` sets after checking that `web/dist/index.html` exists. The
server logs a warning whenever it serves the placeholder.

Standard library first: `net/http` with Go 1.22+ routing patterns,
`http.CrossOriginProtection` (Go 1.25+, CSRF), `log/slog`, `embed`,
`http.ServeContent`, and `os.Root` (confined file access, for streaming).

## 4. Dependencies and license review

Rewind's own code is licensed under **Apache-2.0**. Licenses were checked
against official repositories and the npm registry during Phase 0. Transitive
licenses are re-verified from downloaded module sources when each dependency is
actually added.

### Backend (runtime, compiled into the binary)

| Module | License | Purpose | Status |
| --- | --- | --- | --- |
| `modernc.org/sqlite` v1.60.1 | BSD-3-Clause (bundled SQLite: public domain; sqlite-vec: MIT) | Pure-Go SQLite driver, no CGO | **Added in Phase 2** |
| indirect: `modernc.org/libc` v1.77.1 (BSD-3; includes musl, MIT), `modernc.org/mathutil`, `modernc.org/memory` (BSD-3), `golang.org/x/sys` (BSD-3), `google/uuid` (BSD-3), `remyoudompheng/bigfft` (BSD-3), `dustin/go-humanize` (MIT), `mattn/go-isatty` (MIT), `ncruces/go-strftime` (MIT) | as listed; verified from the downloaded LICENSE files | Driver internals | Added in Phase 2 |
| `golang.org/x/crypto` v0.58.0 | BSD-3-Clause | Argon2id password hashing (`argon2` package); maintained by the Go team | **Added in Phase 2** |
| `go.senan.xyz/taglib` v0.14.0 | **LGPL-2.1** (TagLib 2.1.1 inside, LGPL-2.1/MPL-1.1) | Tags, duration and codec for MP3/FLAC/M4A/Ogg/WAV; TagLib compiled to WebAssembly, no CGO | **Added in Phase 3** (owner decision) |
| `github.com/tetratelabs/wazero` (pseudo-version pinned by go-taglib) | Apache-2.0 (NOTICE: "wazero, Copyright 2020-2023 wazero authors") | WebAssembly runtime used by taglib. It compiles the module to machine code and caches it in `os.TempDir()`, which Rewind points at a private directory (security-audit.md, F8) | Added in Phase 3 (indirect) |

All Go dependencies except go-taglib are permissively licensed
(BSD-2/BSD-3/MIT/Apache-2.0, public domain). go-taglib is LGPL-2.1; see the
release obligations below.

### Frontend (build-time only, nothing runs on the server)

Added in Phase 1 as devDependencies, at exact versions, locked in
`web/package-lock.json`:

| Package | Version | License | Why |
| --- | --- | --- | --- |
| `svelte` | 5.57.2 | MIT | UI framework; its runtime is compiled into the bundle |
| `vite` | 8.3.4 | MIT | dev server with `/api` proxy, production bundler |
| `@sveltejs/vite-plugin-svelte` | 7.3.1 | MIT | compiles `.svelte` files in Vite |
| `typescript` | 6.0.3 | Apache-2.0 | type checking; held at 6.0.x because `svelte-check` supports only TypeScript 5–6 |
| `svelte-check` | 4.7.6 | MIT | type and template checks for `.svelte` files |
| `@types/node` | 24.19.2 | MIT | types for `vite.config.ts` (`process.cwd()`) |
| `vitest` | 5.0.3 | MIT | test runner (Phase 2: setup and sign-in flow tests) |
| `@testing-library/svelte` | 5.4.2 | MIT | renders components and queries them the way users do |
| `jsdom` | 30.1.2 | MIT | browser-like DOM for tests |

Not added:
- `@tsconfig/svelte`: its options are written directly into the tsconfig.
- `@testing-library/user-event` and `jest-dom` matchers: plain assertions
  suffice.
- A linter or formatter, UI kits, icon packages, and fonts (system font
  stacks only).

The compiled bundle contains Svelte runtime code (MIT), which needs attribution
in release notices.

### Compatibility findings

- **BSD, MIT, and Apache-2.0** dependencies are compatible with an Apache-2.0
  project. Their obligations are to preserve copyright and license notices and,
  for Apache-2.0 components with a NOTICE file (wazero), to include that NOTICE
  text in distributions.
- **LGPL-2.1 (go-taglib)** does not prevent Rewind from being Apache-2.0. It
  only affects how **binaries** that include it are distributed. Rewind's
  source stays Apache-2.0. Go links statically, so a release binary is treated
  conservatively as a "work that uses the Library" (LGPL-2.1 §6). To comply,
  each binary or container release must:
  1. include the LGPL-2.1 text and a notice that the binary contains
     go-taglib/TagLib;
  2. provide the corresponding source from the same place as the binary.
     Planned: a release source archive with vendored modules
     (`go mod vendor`), **plus the TagLib 2.1.1 source**. Verified in Phase 3:
     the go-taglib module download contains the wrapper (`taglib.go`,
     `taglib.cpp`), the build scripts and the prebuilt `taglib.wasm`, but
     **not** TagLib's C++ sources, which are a git submodule in go-taglib's
     repository. `go mod vendor` alone is therefore not enough;
  3. let users rebuild with a modified go-taglib. Rewind's open source plus
     `go mod edit -replace` satisfies this, and the steps will be documented;
  4. keep any patches to go-taglib under LGPL-2.1.

  Apache-2.0 permits modification and reverse engineering, which LGPL-2.1 §6
  requires of the combined work's terms. TagLib itself is dual-licensed
  LGPL-2.1 / MPL-1.1, but the go-taglib wrapper is LGPL-2.1 only, so LGPL-2.1
  governs. (This is an engineering review, not legal advice.)
- **Alternative if a fully permissive binary is wanted:** `dhowden/tag`
  (BSD-2-Clause). It covers tags for MP3/M4A/Ogg/FLAC but **not WAV and not
  duration**, so Rewind would need its own duration parsers for each format.
  Its last commit was in July 2024. This choice is made at the start of Phase 2.

### Deliberately not used

Router libraries, ORMs, migration frameworks, config/dotenv libraries,
SvelteKit, Workbox/vite-plugin-pwa, UI kits, and ffmpeg/transcoding.

## 5. Data model

*Implemented*: migration `0001_auth` (`schema_migrations`, `users`,
`sessions`), migration `0002_library` (`library_roots`, `tracks`, `scans`),
migration `0003_playlists` (`playlists`, `playlist_tracks`) and migration
`0004_preferences` (`user_preferences`). See §5a, §5b, §5d and §8.

- `users` (*implemented*): id, username (UNIQUE, `COLLATE NOCASE`, ASCII
  only), password_hash (Argon2id PHC string), created_at, updated_at,
  password_changed_at.
- `sessions` (*implemented*): id, token_hash (SHA-256, UNIQUE, 32 bytes),
  user_id (`REFERENCES users ON DELETE CASCADE`), created_at, last_seen_at,
  expires_at. Indexed on user_id and expires_at.
- `schema_migrations` (*implemented*): version, name, applied_at.

- `library_roots` (*implemented*): id, configured_path (UNIQUE), path
  (canonical, UNIQUE), enabled, status (`pending`/`available`/`unavailable`),
  status_message, created_at, last_scan_id.
- `tracks` (*implemented*): id, root_id (`REFERENCES library_roots`, no
  cascade), rel_path (`UNIQUE (root_id, rel_path)`), file_size,
  file_mtime_ns, format, codec, title, artist, album, album_artist, genre,
  release_date, year, track_number, disc_number, duration_ms,
  metadata_status (`ok`/`no_tags`/`error`), metadata_error, status
  (`present`/`missing`), missing_since, first_seen_at, updated_at. Indexed
  on (root_id, status) and (root_id, file_size).
- `scans` (*implemented*): id, root_id, started_at, finished_at, status
  (`running`/`completed`/`partial`/`failed`/`interrupted`), file and track
  counters, error (bounded summary).

**Not normalized (Phase 3 decision).** Artist, album and album artist are
text columns on `tracks`; the Phase 0 plan had separate `artists`/`albums`
tables. Separate tables would add upserts and orphan cleanup before any
query needs them. Phase 4 measured the library API's queries before
deciding on indexes; see §5c.

- `playlists` (*implemented*, Phase 8): id, user_id (`REFERENCES users ON
  DELETE CASCADE`), name, description, created_at, updated_at. Unique index
  on (user_id, name `COLLATE NOCASE`).
- `playlist_tracks` (*implemented*, Phase 8, `WITHOUT ROWID`): playlist_id
  (`ON DELETE CASCADE`), track_id (`REFERENCES tracks ON DELETE CASCADE`),
  position (≥ 0), added_at. Primary key (playlist_id, track_id), so a track
  appears once per playlist; `UNIQUE (playlist_id, position)`; index on
  track_id.
- `user_preferences` (*implemented*, Phase 8, `WITHOUT ROWID`): user_id
  (`ON DELETE CASCADE`), key, value (JSON text, at most 1 KiB), updated_at.
  Primary key (user_id, key).

The Phase 0 plan had `playlist_entries` with a nullable track_id (`ON DELETE
SET NULL`) and title/artist snapshots, to survive deleted tracks. That turned
out unnecessary: the scanner never deletes a track that a user could have
seen go missing (see §5d), so the simpler schema was used.

## 5b. Music library and scanning (*implemented*, Phase 3)

User-facing details are in [library.md](library.md). Design summary:

- **Roots** come from `REWIND_LIBRARY_ROOTS` (owner decision: an environment
  variable plus a startup scan; there is no HTTP endpoint).
  - `SyncRoots` runs at every start. New paths must be existing, readable
    directories.
  - Symlinks in the configured path are resolved once into the canonical
    `path`.
  - Duplicate and overlapping (nested) roots are rejected, so a file belongs
    to at most one root.
  - Known roots stay registered while unavailable. Unlisted roots are
    disabled, never deleted.
- **Read-only by construction.** The scanner only lists directories, calls
  `Lstat`, and passes paths to `internal/metadata`. go-taglib's read
  functions mount only the file's directory, **read-only**, in the WebAssembly
  sandbox, and Rewind never calls its write functions. Tests check bytes,
  modification times and modes of a read-only library before and after
  scanning.
- **Traversal.** `filepath.WalkDir` from the canonical root:
  - symlinks are never followed (no escapes, no loops);
  - hidden entries and `$RECYCLE.BIN`, `System Volume Information` and
    `lost+found` are skipped;
  - supported formats are recognized by extension (`internal/metadata.Formats`).
- **Pipeline.** One walker, `REWIND_SCAN_WORKERS` readers (1–8, default 2),
  and one writer. Channels are bounded (4 × workers). The writer commits up
  to 200 changes per transaction, or after 1 s, so no transaction spans file
  I/O and the single connection is free between batches.
- **Incremental.** The root's index is loaded into memory once per scan. A
  file with the same size and modification time (ns) is skipped without a
  database write. Files whose metadata previously failed are retried.
- **Identity.** Track identity is (root_id, rel_path). Updates keep the ID.
  After a complete walk, each disappeared track is either merged with a new
  file that has the same size, modification time and duration, if the match
  is unique on both sides (a rename: the old ID is kept and the new row
  removed), or marked `missing`. Files are never hashed.
- **Safety.** Missing files are reconciled only after a traversal with no
  errors. In all of these cases no track is marked missing:
  - root unavailable or vanished;
  - unreadable sub-folders (`partial`);
  - interrupted scan;
  - root suddenly empty while tracks are indexed (probable unmounted mount
    point).

  Nothing is deleted automatically.
- **Concurrency.** An in-memory per-root lock means a second `Scan` of the
  same root returns `ErrScanRunning`. `ScanAll` scans roots one after
  another. Scans left `running` by a crash are marked `interrupted` at the
  next start.
- **Observability.**
  - `Scanner.Running()` reports live atomic counters without database writes.
  - The `scans` table keeps the history, and `library_roots.last_scan_id`
    points to the latest scan.
  - Each scan logs one summary line, plus at most 10 per-file warnings.
- **Startup and shutdown.** The startup scan runs in a goroutine after the
  listener opens. Shutdown cancels it; already-read results are committed,
  the scan is recorded as `interrupted`, and the database is closed only
  after the scan goroutine has returned.

## 5a. Database (*implemented*)

**Driver.** `modernc.org/sqlite`, pure Go (builds with `CGO_ENABLED=0`).

**Location.** `$REWIND_DATA_DIR/rewind.db`. `REWIND_DATA_DIR` defaults to
`./data`, resolved to an absolute path at startup and logged. The data
directory never holds music. On startup:

- a missing data directory is created with mode 0700;
- an existing directory is used as-is, and its permissions are never changed;
- a path that is not a directory stops startup with an error;
- a missing database file is pre-created with mode 0600, because it holds
  password and session hashes. Existing files keep their permissions.

**Connection settings,** applied by the driver to every connection:

- `foreign_keys=ON` (checked at startup) and `busy_timeout=5000` ms;
- `journal_mode=DELETE` and `synchronous=FULL`;
- `_txlock=immediate`: every transaction takes the write lock at `BEGIN`,
  so read-then-write sequences such as first-run setup cannot interleave;
- the pool is limited to **one connection**. All access is serialized, so
  there are no concurrent writes and no `SQLITE_BUSY` between Rewind's own
  connections. The write load (logins, session updates at most once per
  minute per session) is tiny. Re-evaluated in Phase 3: scans write in
  short batches (at most 200 rows per transaction, no file I/O inside) on
  this single connection, so other requests wait for at most one batch. WAL
  is not needed.

**Journal mode decision.** Rewind uses SQLite's default rollback journal
(`DELETE`), not WAL:

- At rest the database is a single file, so a backup is one file.
- WAL adds `-wal`/`-shm` files that must be copied together with the
  database, and WAL is unsafe on network filesystems.
- Current load does not need WAL's read/write concurrency.

**Migrations.** Numbered files `internal/db/migrations/NNNN_name.sql` are
embedded in the binary and applied in order at startup:

- each migration runs in its own transaction together with its
  `schema_migrations` row, so it applies completely or not at all;
- applied migrations are skipped, which makes repeated starts idempotent;
- startup is **refused, without modifying the database**, if:
  - the database records a migration this build does not know (for example,
    it was created by a newer Rewind version);
  - a recorded migration name differs from this build's;
  - the file is not a valid SQLite database.
- Rewind never deletes, resets or recreates a database to recover from an
  error.
- Released migration files are never edited. Schema changes always go in a
  new file.

**Shutdown.** The database is closed after the HTTP server has finished
graceful shutdown.

### Backup and restore

The database holds the owner account (password hash), sessions, the library
index, playlists and preferences. It never holds music, so a backup is small
and does not need the music drive. Playlists refer to tracks by their IDs in
this database: restore the database as a whole, not the playlist tables
alone into a different database.
**Protect backups like passwords:** they contain the password hash and
session hashes.

Backup, either way:

- **Stop Rewind, then copy `rewind.db`.** After a clean shutdown there is no
  journal file. If a `rewind.db-journal` file exists, a write was interrupted:
  start and stop Rewind once so SQLite can roll it back, then copy.
- **While Rewind runs,** use SQLite's online backup, which produces a
  consistent copy:

  ```sh
  (umask 077 && sqlite3 /var/lib/rewind/rewind.db ".backup '/backups/rewind-$(date +%F).db'")
  ```

  `VACUUM INTO '/backups/rewind.db'` also works. `umask 077` makes the copy
  mode 0600; without it, sqlite3 creates it world-readable (0644 with the
  usual umask). A plain `cp` of a running
  database can capture a half-written state and is not recommended.

Restore:

1. Stop Rewind.
2. Replace `rewind.db` with the backup. Delete any `rewind.db-journal` left
   from the old file: a journal must never be paired with a different
   database.
3. Make sure the file is owned by the service user, with mode 0600.
4. Start Rewind. Migrations newer than the backup are applied automatically.
   A backup from a *newer* Rewind version is refused.

Sessions that existed when the backup was taken become valid again after a
restore. To revoke them, change the password, or run
`sqlite3 rewind.db "DELETE FROM sessions;"` while Rewind is stopped.

### Password recovery

There is no email reset, because Rewind has no email infrastructure and
needs none. Someone with access to the server's files can set a new
password. The new hash is made by a throwaway Rewind instance, so no hashing
tool is needed, and playlists and preferences are kept (verified in Phase 11
on Linux):

1. Stop Rewind and back up `rewind.db` (see above).
2. Start a temporary instance with an empty data directory, on another
   loopback port and without library roots, as the service user:

   ```sh
   umask 077; mkdir /tmp/rewind-reset
   REWIND_DATA_DIR=/tmp/rewind-reset REWIND_ADDR=127.0.0.1:18090 /opt/rewind/current/rewind
   ```

   Complete its setup with **the same username** and the new password, in a
   browser on the server (`http://127.0.0.1:18090`) or with `curl` and the
   code in `/tmp/rewind-reset/setup-code`. Then stop it.
3. Copy the new hash into the real database and revoke all sessions:

   ```sh
   sqlite3 /var/lib/rewind/rewind.db "ATTACH '/tmp/rewind-reset/rewind.db' AS reset;
     UPDATE users SET password_hash = (SELECT password_hash FROM reset.users),
       updated_at = unixepoch(), password_changed_at = unixepoch();
     DELETE FROM sessions;"
   rm -rf /tmp/rewind-reset
   ```

4. Start Rewind and sign in with the new password.

This requires the same access as reading the setup code, so it does not
weaken the setup model. **Do not** use `DELETE FROM users` to reset the
account: the foreign-key cascade also deletes every playlist and preference
of that user.

## 5c. Library API (*implemented*, Phase 4)

- **Routes** are in `internal/server/library.go`. Every library route is
  wrapped in the Phase 2 `requireAuth` middleware and `no-store`;
  state-changing requests also pass the existing CSRF protection.
- **Authorization:** with a single owner account, an authenticated session
  is the owner. There is no role column; adding accounts would require one.
- **Queries** run in `library.Catalog`, using the Phase 3 tables unchanged:
  - all values are bound parameters;
  - `sort` only selects one of seven fixed ORDER BY lists, so user input is
    never an SQL identifier;
  - `LIKE` wildcards in search text are escaped;
  - paging uses `LIMIT`/`OFFSET` in SQLite, with a `count(*)` for the total;
  - track rows come from one JOIN with their root, and the root list uses one
    query with correlated counts and its latest scan. No per-item queries.
- **Paths:** responses contain relative paths and a root display name only.
  Stored messages are cleaned of the root's absolute paths (`Sanitize`).
- **Rescans** use `Scanner.Start`, which queues roots and scans them one after
  another in a background goroutine.
  - A root that is running or queued is busy for both `Start` and the blocking
    `Scan`, so the same root is never scanned twice at once.
  - `Scanner.Close` cancels and waits; `main` calls it before closing the
    database. The startup scan uses the same queue.
- **Index decision (measured, not assumed).** On 100,000 synthetic tracks
  (fast desktop, WSL, not target hardware):

  | Query | Time |
  | --- | --- |
  | Count with default filters | 4 ms |
  | First page sorted by title | 15 ms |
  | Search with count | 23 ms |
  | Exact artist filter | 12 ms |
  | Overview aggregates | 80 ms |
  | Page 1,000 sorted by artist (worst case) | 167 ms |

  No new index was added. Revisit with measurements on the owner's hardware
  (Phase 10).

## 5d. Playlists and preferences (*implemented*, Phase 8)

- **Code:** `library.Playlists` (`internal/library/playlists.go`) and
  `prefs.Store` (`internal/prefs`). Routes are in
  `internal/server/playlists.go`, behind `requireAuth`, `no-store` and the
  CSRF check. The API is documented in [api.md](api.md#playlists).
- **Ownership:** every playlist and preference row has a user_id, and every
  query filters on the session's user. Another user's playlist answers `404`,
  like a missing one. Rewind has one account today; the scoping costs nothing
  and keeps a later multi-account change from leaking data.
- **Ordering:** `position` is a sort key, unique per playlist but not
  necessarily contiguous. Appending uses `max(position) + 1`; removing leaves
  a gap. The API reports 1-based positions computed from the order. A reorder
  takes the complete new list, checks that it matches the current tracks
  exactly, then deletes and re-inserts the entries in one transaction. A
  partial failure rolls back to the old order (tested with an injected
  failure).
- **Transactions:** every multi-step change (create with limit and name
  checks, add, remove, rename, reorder) runs in one transaction. The
  connection's `BEGIN IMMEDIATE` holds the write lock, so the check-then-write
  steps cannot race.
- **Tracks that disappear:** tracks are never deleted when files go missing,
  roots become unavailable or roots are removed from the configuration (they
  are marked missing or unavailable, §5b). Playlist entries therefore stay,
  and `available` tells the UI what can be played. The only row the scanner
  deletes is the duplicate created when it merges a rename. That row was
  visible during the scan, so `mergeRename` first moves any playlist entries
  from it to the kept track ID (`UPDATE OR IGNORE`). If a playlist already
  holds the kept ID, the duplicate entry goes with the deleted row. `ON DELETE
  CASCADE` on track_id is only a backstop for that case.
- **Queries:** the playlist list is one aggregate query. A playlist's entries
  are one JOIN, read in the same transaction as its summary. The track
  columns and the availability expression are shared with `Catalog`, so
  playlist entries and `/api/tracks` describe tracks identically.
- **Limits:** 1,000 playlists per user and 5,000 tracks per playlist keep the
  unpaginated detail response and the reorder request bounded (256 KiB body
  limit for reorders).
- **Preferences** are an allowlist in code: each key has a default and a
  validator. Reads skip unknown keys and invalid stored values, so a row from
  another version cannot break the UI. Writes validate every key and value
  first, store canonical JSON, and save all changes in one transaction.

## 5e. Plugins (*implemented*, Phase 10)

Details, the security model and the contributor guide are in
[plugins.md](plugins.md).

- **Compile-time registration only.** Plugins are Go packages in this module
  that call `plugin.Register` from `init()`, included by blank imports in
  `cmd/rewind/plugins.go`. Go's `plugin` package was rejected: it does not
  support Windows and needs identical toolchains and dependency versions. No
  WebAssembly runtime, RPC or process supervisor was added.
- **Manifests are the administrator's authorization.** A plugin runs only if
  its manifest is in `REWIND_PLUGIN_DIR/<id>/plugin.json` with the compiled
  ID and version, and its ID is in `REWIND_PLUGINS`. Manifests are JSON
  (standard library, strict decoding), schema 1, at most 16 KiB, read through
  `os.Root`; links and junctions are rejected. Compatibility is a plugin API
  version (`rewind_api`), not a release version.
- **One extension point,** `events.library_scan`. The scanner got an optional
  `OnFinished` callback; `main` converts the result to a path-free
  `ScanEvent` and calls `Manager.PublishScan`, which never blocks. Each
  plugin has its own goroutine and a 16-event queue; handlers run with a
  deadline and panic recovery. Plugins get no database, routes or file
  access, so the core keeps authentication and authorization.
- **Lifecycle.** `Manager.Start` runs once, after the library is ready and
  before serving: plugins are initialized one at a time in ID order with a
  deadline; failures are recorded and contained. Shutdown order: HTTP
  server, scanner, plugins (handlers stopped, then `Close` in reverse order
  with deadlines), database.
- **Status.** `GET /api/plugins` (session required, read-only) and a
  read-only Settings section. Errors shown there are Rewind's own summaries;
  plugin error text stays in the log.
- **Config.** `REWIND_PLUGIN_DIR` (absolute, may not overlap the data
  directory or library roots) and `REWIND_PLUGINS`; both validated at startup.

## 5f. Listening experience (*implemented*, product specification "Phase 2")

The product specification in the repository root (`ARCHITECTURE.md`) groups
features into its own phases. Its "Phase 2 — Complete listening experience"
was implemented after CLAUDE.md Phase 12; this section describes the result.

### Audit findings that shaped the design

- `ARCHITECTURE.md` (repository root) is a **product specification**, not a
  description of the code; this file (`docs/architecture.md`) is the
  implementation architecture. They are kept separate on purpose; the root
  file's "Implementation status" note points here.
- The specification's "Phase 1" assumes a queue, favorites and
  album/artist/folder browsing. Before this work Rewind had none of them:
  only a track list, playlists played as a fixed sequence by `Shell`, and a
  player component that owned the `<audio>` element. Queue, favorites,
  albums and artists were therefore built as part of this phase; **folder
  browsing remains deferred**.
- Metadata was stored only in SQLite; the extractor is strictly read-only.
  Rewind never wrote tags, and still does not (see "Metadata editing").
- There was no artwork at all ("no artwork API yet"), and the scanner's
  rename merge only moved playlist entries; it now also moves favorites,
  history and re-applies metadata edits.

### Backend

| File | Responsibility |
| --- | --- |
| `internal/library/browse.go` | Albums and artists derived from tags (no tables): grouping keys, base64url IDs, album/artist lists and details |
| `internal/library/edits.go` | Database-backed metadata edits: validation, optimistic concurrency (`tracks.revision`), revert, album and artist bulk edits, `ReapplyEdits` for the scanner |
| `internal/library/personal.go` | Favorites, listening history, play counts, recent and top tracks, `Annotate` (per-user fields on any track list) |
| `internal/library/playback.go` | The saved play queue and settings (`playback_state`) |
| `internal/library/artwork.go` | Artwork extraction (embedded or sidecar), validation, renditions, cache records and cleanup |
| `internal/library/playlists.go` | Adds bulk add, duplicate and artwork choice |
| `internal/metadata` | Adds `Composer`, `Lyrics`, `ReadArtwork` (read-only) and `Version` |
| `internal/server/listening.go` | HTTP handlers for all of the above; all behind `requireAuth` and the existing CSRF protection, scoped to the signed-in user |

**Migration `0006_listening`** (additive; nothing changed or removed):
`tracks.composer`, `tracks.lyrics`, `tracks.metadata_version`,
`tracks.revision`; expression index `tracks_album_key`; tables
`track_edits`, `favorites`, `play_history`, `playback_state`,
`track_artwork`; `playlists.artwork_track_id`. Tested by an upgrade test that
starts from 0005 with data (`internal/db`).

**Albums and artists.** An album groups tracks by
`lower(coalesce(album_artist, artist, '')) || char(31) || lower(album)`, the
same rule the library summary already used for its album count. The
expression is indexed (partial index on `album IS NOT NULL`; a test checks
the query plan uses it). Artists split credit fields on `"; "`, the
separator the scanner uses for multi-value tags; artist lists aggregate one
row per distinct (artist, album artist, album) combination in Go. IDs are
base64url-encoded grouping keys: stable, and changing only when the tags
do. Decision: no `albums`/`artists` tables, because derived groups can never
drift from tracks or edits, and the library is small enough that grouping
queries are cheap. Compilations without an album artist split per artist;
the album editor (set the album artist) fixes that.

**Metadata editing (database-only).** Rewind never writes tags into files.
An edit overwrites the column in `tracks` (so every query, search and
grouping uses it with no extra cost) and is recorded in `track_edits`
(`fields`: edited values; `original`: the file's values for those fields).
When the scanner re-reads a changed file it writes the fresh tag values,
then `ReapplyEdits` stores them as the new originals and applies the edits
again; the rename merge does the same. A change back to the file's value
drops the edit; revert restores originals per field. Every metadata write
increments `tracks.revision`; edits must send the revision they read
(`409 edit_conflict` otherwise). Album edits and artist renames apply the
same edit machinery to every track of the group in one transaction, guarded
by a version hash over the group's IDs and revisions. Embedded tag writing
is deliberately not implemented; it would be a separate, explicit
capability.

`metadata.Version` (now 1) records which extractor read a file; files read
by an older version are re-read once on the next scan even if unchanged, so
existing libraries get composer and lyrics without a full manual rescan.

**Listening history.** `play_history` stores one row per **qualified** play
(only those are sent), referencing `tracks` (no copied track data). A random
client-generated `play_key`, unique per user, makes retries idempotent. Play
counts and last-played times are aggregates over the history (indexed by
user, track and time), so they always agree with it. Tracks are never
deleted when files disappear (they are marked `missing`), so history
survives; the UI distinguishes "File missing" (`status: missing`) from
"Unavailable" (drive unplugged or root disabled). Preferences add
`play_threshold_percent` (10–100, default 50; capped at four minutes) and
`record_history` (privacy mode, enforced by the server).

**Playback state.** `playback_state` holds one validated JSON document per
user: queue (track IDs, duplicates allowed), the pre-shuffle order, index,
position, shuffle, repeat, speed and context. Writes carry a client revision
(milliseconds since the epoch, always increasing); the server keeps the
document only if the revision is newer (`409 stale_state` otherwise), so a
late request never overwrites a newer queue. Volume and mute are per device
(`localStorage` key `rewind.player`), not per user.

**Artwork.** On first request for a track: open the file through
`OpenTrack` (same containment rules as streaming), read its embedded picture
with go-taglib's read-only reader, or else a sidecar cover in the same
folder through `os.Root`. Images are validated before decoding (format
JPEG/PNG/GIF, ≤16 MiB, ≤12,000 px per side, ≤25 Mpx), then scaled with a
bounded box filter (standard library only, no image dependency) to 320 px
and 1000 px JPEGs in `<data dir>/artwork/<hash>-<size>.jpg`. The hash is of
the source image, so an album's tracks share files. `track_artwork` records
the result (including "none") against the file size and mtime stored by the
scanner; extraction runs at most twice concurrently and once per track at a
time. Responses are `private, max-age=86400` with an ETag. When a drive is
unplugged, the last rendition is served. `Cleanup` (run in the background at
startup) removes renditions no track refers to and leftover temporary files,
and only touches file names the cache creates. The cache is regenerable and
need not be backed up.

### Frontend

**One playback engine** (`web/src/services/player.svelte.ts`): a class with
Svelte 5 `$state` fields and a single `<audio>` element created once
(`attach`, from `Shell`) and appended to `<body>`, outside every component.
All views (mini player, Now Playing, queue panel, track lists, keyboard
shortcuts, browser media controls) read the same state and call the same
actions, so navigation never touches playback. Listeners are added once
and removed on `detach` (sign-out).

- Each load gets a token; asynchronous work (error probes, network retries,
  restore) checks it, so a late result never affects a newer track.
- Errors are explained with a `HEAD` probe of the stream (401 sign-in, 404
  missing file, 409 unconfigured folder, 503 unplugged drive, 200 codec not
  supported). An unreachable server triggers up to five retries, on the
  `online` event or with back-off, resuming at the same position. A track
  that fails while advancing on its own is skipped after a short notice.
- Seeking uses the browser's Range requests; nothing is downloaded into
  JavaScript.
- The queue is saved (debounced 0.8 s, plus the position every 15 s, on
  pause and with `keepalive` on `pagehide`) and restored at sign-in **without
  starting audio** ("Press play to resume").
- History: listened time counts only small forward steps while playing
  (seeks do not count). At the threshold, one `POST /api/history` is sent
  with a new random play key per playback (repeat-one replays get a new
  key); failures retry with the same key.
- Media Session metadata and actions (lock screen, headset keys) where the
  browser supports them.

**Queue rules** (`services/queue.ts`, pure functions, unit-tested):

- Entries wrap tracks with a unique key, so a track can be queued twice and
  keeps its identity while moving. Moving entries never changes which entry
  is current.
- *Play now* inserts after the current entry and plays it; *Play next*
  inserts after the current entry; *Add to queue* appends. Choosing a track
  in a list (album, artist, playlist, library or search results, history)
  replaces the queue with that list, starting at the track.
- Next skips unavailable tracks; at the end, repeat all wraps, otherwise
  playback stops ("End of the queue"). Repeat one replays only when a track
  ends on its own. Previous restarts the track after 3 s, otherwise goes to
  the previous available track (wrapping with repeat all).
- Removing the current entry plays the following one (or the new last one),
  if it was playing.
- Shuffle shuffles only upcoming entries and keeps the original order;
  turning it off restores that order around the current entry. Tracks added
  while shuffled keep their place in the play order and are inserted after
  the current track (play next) or at the end (add to queue) of the
  original order. "Shuffle" buttons start a list in random order.
- The preference "Continue playing" (`autoplay_next`) decides whether the
  next track starts when one ends.

**Components.**

| Component | Responsibility |
| --- | --- |
| `Player` | Mini player and Now Playing screen (artwork, favorite, shuffle/repeat, volume/mute, speed, ±10 s, remaining time, lyrics, track information, album/artist links, track menu, focus trap, swipe gestures) |
| `QueuePanel` | Current, up next (drag and drop plus ↑/↓), played; remove, clear upcoming, clear with confirmation, save as playlist |
| `TrackMenu` | Play now/next, add to queue, add to playlist, favorite, go to album/artist, edit details, view-specific extras; keyboard, right-click, Shift+F10; bottom sheet on phones |
| `TrackList` | Shared track rows (album, artist, history, favorites, top tracks): current marker in text, disc headings, favorites, selection |
| `BrowseGrid` | Album and artist grids with search, sort and paging |
| `AlbumView`, `ArtistView` | Detail pages with actions, album editor, artist rename |
| `HistoryView`, `FavoritesView` | History (recent, most played, filtered list, bulk actions) and favorites |
| `MetadataEditor` | Track details dialog (database-only notice, validation, unsaved state, per-field revert, conflict handling) |
| `ShortcutsDialog`, `services/shortcuts.ts` | Shortcut reference and the single global key handler |
| `Artwork`, `Icon` | Shared artwork (lazy, square, placeholder and fallback) and icon set |
| `services/listening.svelte.ts` | Cross-view consistency: favorite overrides, `libraryVersion` and `historyVersion` counters that views watch to reload, one live region for announcements |
| `services/ui.svelte.ts` | Overlays (queue, Now Playing, editor, shortcuts) and focus return |

Routes (hash router in `Shell`): `#/library`, `#/albums[/{id}]`,
`#/artists[/{id}]`, `#/playlists[/{id}]`, `#/favorites`, `#/history`,
`#/settings`, `#/status`. Views stay mounted while hidden, so lists keep
their state.

**Keyboard shortcuts** (one `window` listener in `Shell`): Space play/pause,
←/→ seek 10 s, Shift+←/→ previous/next, `-`/`+` volume, M mute, S shuffle,
R repeat, F favorite, Q queue, N Now Playing, `/` search, `?` reference,
Esc closes the topmost overlay. They never fire while typing (text fields,
selects, ranges), never use Ctrl/Alt/Meta combinations, keep Space and
Enter on focused buttons, and only Escape works while a modal dialog is open.

**Responsive layout.** Desktop: sidebar, main content, floating mini player,
queue as a side panel. Tablets (≤64 rem): the sidebar can collapse to icons
(remembered per device); the mini player drops shuffle, repeat and the
volume slider. Phones (≤48 rem): bottom navigation (Library, Albums,
Artists, Playlists, More), a compact mini player above it, full-screen Now
Playing, menus as bottom sheets, safe-area insets, 44 px touch targets, no
hover-only actions.

### Testing

`go test ./...` (library: albums/artists, index use, edits across rescans
and renames, history idempotency, playback state validation and staleness,
artwork embedded/sidecar/cache/cleanup/limits; server: auth and CSRF on
every new route, API flows; db: 0006 upgrade) and `npm test` in `web/`
(queue rules, engine transitions, repeat/shuffle, history recording,
persistence and restore, components, and Shell integration flows: album
playback across navigation, restoring the queue, shortcuts, queue
reordering, metadata editing). A manual smoke test against the real binary
is described in the Phase 2 report.

### Known limitations and deferred features

- Folder browsing, smart playlists, statistics pages, lyrics editing UI
  beyond the metadata editor, custom artwork uploads, crossfade/gapless,
  and an output-device selector are not implemented.
- Album grouping depends on tags; compilations need an album artist.
- Artist lists are aggregated in Go from one row per distinct credit
  combination; very large libraries (hundreds of thousands of tracks) have
  not been measured.
- Embedded tags are never written; edits live in Rewind's database (back it
  up to keep them).
- Playback state is per user, not per device: two devices playing at once
  save over each other (the newest save wins).
- Visual layout was verified by component tests and the production build,
  not yet by a browser-automation run on real devices.

## 6. Audio streaming (*implemented*, Phase 5)

- **Route:** `GET|HEAD /api/tracks/{id}/stream` (`internal/server/library.go`),
  behind the Phase 2 `requireAuth`, the Host check and the security headers.
  It changes no state, so the CSRF check does not apply. No CORS headers are
  sent, and cookie settings are unchanged; same-origin `<audio>` requests send
  the `SameSite=Strict` cookie.
- **File resolution** happens in `Catalog.OpenTrack` (`internal/library/stream.go`):
  1. The track ID selects the database record (root path, relative path,
     format) and the root must be enabled. Request input never names a file.
  2. The stored relative path must satisfy `filepath.IsLocal`: not absolute,
     no `..`, not empty, no Windows reserved names. A tampered record such as
     `../x` or `/etc/passwd` is refused before any filesystem access.
  3. The root directory is opened as an `os.Root` (Go 1.24+). Every open goes
     through it, so a path or symbolic link that resolves outside the root
     fails. On Linux this is enforced component by component, so a race that
     swaps in a symlink between checks cannot escape.
  4. Symbolic links are refused outright (`Lstat`), matching the scanner,
     which never indexes them. `os.Root` alone would follow relative links
     that stay inside the root. Since Phase 11 the opened descriptor must
     also be the same file as the `Lstat` result (`os.SameFile`), so a link
     or another file swapped in between the check and the open is refused
     (tested with a hook in that window). Symbolic links in *directory*
     components of a stored path are still followed while they stay inside
     the root; the scanner never records such paths.
  5. On Unix the file is opened with `O_NONBLOCK`, so a FIFO swapped in for a
     track cannot hang the request. The opened descriptor must be a regular
     file (`fstat`), which also rules out devices and directories.
  6. Errors are classified without paths: missing or unsafe (`404
     file_unavailable`), root not openable (`503 root_unavailable`), root
     disabled (`409`). A database record alone is never treated as proof
     that a file is inside its root.
- **Serving:**
  - `http.ServeContent` provides Range/206, multiple ranges, 416, HEAD,
    `If-Range` and conditional requests. It copies through a small buffer,
    so memory does not depend on file size.
  - `Content-Type` comes from the indexed format; there is no sniffing
    (`nosniff`).
  - The file is closed when the handler returns, including when the client
    disconnects (a test confirms descriptors are not leaked on Linux).
- **No server-side playback state, no transcoding, no rescans, no writes.**
  The 10-second graceful-shutdown limit can cut off a long stream that is
  still downloading at shutdown; browsers retry with a Range request.
- **Platform notes:** `os.Root` also works on Windows (development), but the
  FIFO protection and the descriptor-leak test are Unix-only, and symlink
  tests need symlink permission. All streaming tests run without skips on
  Linux.

## 7. Frontend and PWA (Phases 5–6)

- Plain Svelte 5 + TypeScript + Vite, without SvelteKit. Navigation is simple
  state or hash based, with no router dependency.
- *Implemented:* the Vite dev server proxies `/api` to the Go server
  (`REWIND_DEV_API_TARGET`, default `http://127.0.0.1:8090`). `/stream` is
  added together with the streaming endpoint.
- *Implemented:* in production, `web/dist` is embedded with `go:embed`:
  - existing files are served by `http.FileServerFS` with pinned content types;
  - `assets/*` is cached as `immutable`;
  - other extension-less paths fall back to `index.html`;
  - missing files with an extension return 404.
- *Implemented:* design tokens are CSS custom properties (`--rw-*`) in
  `web/src/app.css`. The themed subset (colors, type, shape, surfaces) is
  the theme system's only output to the page (see "theme and style system"
  below).
- *Implemented (Phase 6): main UI.* Once signed in, `App` renders
  `Shell`:
  - **Navigation:** a sidebar on desktop and a top bar on narrow screens.
    The hash routes are `#/library` (default), `#/playlists`,
    `#/playlists/{id}`, `#/settings` and `#/status` (Phase 8 added the
    playlist and settings routes), with no router dependency. The current page is marked with `aria-current` plus a
    visible bar or underline, not colour alone. On a route change, focus moves
    to the page heading. A skip link is provided.
  - **Views stay mounted** while hidden (`hidden`), so switching keeps search
    text, loaded pages and scroll position. The single `Player` lives in the
    shell, outside the views, so navigation never interrupts playback.
  - **`LibraryView`** reads `GET /api/tracks`:
    - search is debounced (300 ms, or immediate on Enter), and an
      `AbortController` plus a request counter discard stale responses;
    - sorting uses the API's sort keys only (artist, album, title, recently
      added, year, longest);
    - "Load more" pages through the results (50 per page), keyed by track ID;
    - wide screens show columns, narrow screens two-line rows;
    - unknown tags show a dash;
    - the current track is labelled "Playing" or "Paused" in text;
    - unavailable tracks are disabled;
    - there are distinct empty, no-results, loading and error-with-retry
      states.
  - **`StatusView`** reads `GET /api/library` and `GET /api/library/roots`:
    - it shows real figures, each root's availability (unavailable roots say
      their tracks are kept), and the latest scan per root, including errors;
    - **Rescan library** calls `POST /api/library/scan` and is disabled while
      a request is in flight or the server reports a scan running or queued;
    - it polls the overview every 2 s only while a scan is running, shows the
      server's live counts (files checked) rather than a made-up percentage,
      and reports "Scan finished" only when the server no longer reports a
      running scan. The library list then reloads.
  - Any `401` returns to the sign-in screen with "Your session has expired".
  - **The design** stays a music player, not a dashboard: no cards grid, no
    artwork at the time (artwork was added later, §5f), restrained
    accent, and system fonts.
- *Implemented (Phase 5, extended in Phase 6):*
  - `Player` is now a fixed bottom bar. It respects
    `env(safe-area-inset-bottom)`, and the content reserves space so the bar
    never hides the last rows.
  - `Player` uses one native `<audio>` element for its whole lifetime.
    Selecting a track sets `src` to `/api/tracks/{id}/stream` (built from the
    numeric ID only) and calls `play()`, handling a rejected promise such as
    an autoplay block.
  - Controls: play/pause, elapsed and total time, a seek slider (keyboard
    operable), and loading and error states. Errors are explained with a
    `HEAD` probe: expired session, missing file, unplugged drive, or a format
    the browser cannot decode.
  - Clearing or replacing the track pauses and removes the old source. No
    audio is downloaded into JavaScript; there are no Blob or base64 URLs.
  - A play queue and artist/album pages came later (§5f).
- *Implemented (Phase 8): playlists and settings.*
  - `PlaylistsView` lists and creates playlists. `PlaylistDetail` plays,
    renames (name and description), deletes (after an inline confirmation)
    and reorders with ↑/↓ buttons (no drag and drop), and it removes tracks.
    Each change runs one at a time and reports its result in a live region.
    A `409 order_mismatch` reloads the playlist. Unavailable entries are
    shown and disabled. Views reload their data each time they are shown.
  - The library has a `+` button on each row that opens an inline panel to
    add the track to a playlist. Adding never starts playback.
  - **Playlist playback** reuses the single `Player`. `Shell` keeps the
    playlist's tracks as they were when Play was pressed and the current
    index. Previous/next and the end of a track step to the nearest
    *available* track (`services/sequence.ts`), never wrapping around. The
    end of a track advances only when `autoplay_next` is on. Choosing a
    track in the library leaves the playlist. This is a sequence, not a
    queue: there is no "play next" or shuffle. (Superseded by the play
    queue in §5f: playlists now play through the shared queue.)
  - `Shell` loads `GET /api/preferences` once and renders the library after
    it answers, so the library loads once, in the saved order. If the request
    fails, defaults are used and `SettingsView` shows the error with a retry
    and disables saving. `SettingsView` sends only changed keys and reports
    a failed save as "Not saved", keeping the unsaved values. Preferences
    live on the server. (Phase 9 added one browser-side cache, the theme
    hint; see below.)
- *Implemented (Phase 9): themes.* Details and contributor rules are in
  [themes.md](themes.md).
  - `services/themes.ts` defines the built-in themes (Rewind Dark, Light,
    OLED Black) as complete sets of the eight color tokens. It also has the
    WCAG contrast function, the custom-accent check and `applyTheme`, which
    sets the tokens, `color-scheme` and the `theme-color` meta tag on
    `<html>` through `style.setProperty`. No stylesheet is generated or
    loaded, so the Content-Security-Policy is unchanged.
  - Selection is the `theme` preference and the custom accent is
    `accent_color`, both stored in the existing `user_preferences` table
    through the existing API. No migration and no new endpoint. The server
    validates against an allowlist and a strict `#rrggbb` pattern; the UI
    checks contrast.
  - `Shell` applies the saved theme once preferences load. `SettingsView`
    previews a choice immediately, saves it, and reverts on failure.
    Switching only changes custom properties, so the views, the player and
    the `<audio>` element are untouched (tested while playing).
  - `main.ts` applies the theme remembered in `localStorage` (`rewind.theme`:
    theme ID and accent only) before mounting, so a reload does not flash
    the default colors. The server's value always wins. This is the only
    thing Rewind stores in web storage; the test that guarded "no web
    storage at all" now checks that nothing else, and no credential, is
    stored.
  - CLAUDE.md's Phase 8 also mentions custom themes loaded from a documented
    location. Phase 9 left that out; the theme and style system (below)
    adds it as administrator-installed, data-only manifests.
- *Implemented (UI enhancement after Phase 12): rounded neo-brutalism, Now
  Playing, three custom colors.* No backend change except two preference
  keys; no new dependency; no migration.
  - **Tokens:** 2px borders and shared radius tokens (`--rw-radius-xs` to
    `--rw-radius-xl`, `--rw-radius-art`) in `app.css`. Four color tokens
    were added: `primary`, `on-primary`, `secondary`, `on-secondary`. The
    accent keeps the highlight role. See [themes.md](themes.md#design-tokens).
  - **Colors:** `primary_color` and `secondary_color` join `accent_color` in
    `user_preferences` (same `""`/`#rrggbb` validator, no migration).
    `resolveTheme` lets an unchosen primary follow a custom accent, so saved
    accents look unchanged. The `rewind.theme` hint now holds the theme ID and
    all three colors, and old hints are still read. Settings edits all three
    with synced pickers and hex fields, previews them by setting the tokens on
    a preview element, and saves the changed ones in one `PATCH`.
  - **Now Playing** lives inside `Player`, as a second view of the same
    state: one `<audio>` element, and the same `playing`, `current` and
    `duration` values and handlers. Opening and closing changes only an
    `expanded` flag, so playback, position and the source are untouched
    (tested). While it is open, the mini bar and Shell's rail and main
    content are `inert`, focus is kept inside, Escape closes it and focus
    returns to the mini player. Pull-up and pull-down gestures count only
    when they start on the mini player's track info or the screen's header,
    never on the seek bar. Previous and next appear only while a playlist
    plays. (§5f replaced this: the player is now a view of the shared
    playback engine, with artwork, shuffle, repeat, volume and speed.)
- *Implemented (after Phase 12): theme and style system.* Details, the
  manifest schema and the token contract are in [themes.md](themes.md).
  - **Model.** A theme is a declarative manifest (`theme.json`, schema 1,
    `theme_api` 1): metadata plus values for a fixed token contract of 29
    tokens (colors, typography, borders and radii, shadow, spacing), and the
    list of tokens users may customize. It contains no code, CSS, markup,
    assets or URLs. Themes are deliberately **not** plugins: they are
    untrusted data with no capabilities, and the plugin system is unchanged.
  - **Backend: `internal/theme`.** `tokens.go` is the contract (kinds,
    ranges, defaults equal to Rewind Dark and `app.css`) and the WCAG
    contrast checks. `manifest.go` is a strict parser: unknown fields,
    duplicate keys, unknown or invalid tokens and unreadable color pairs are
    rejected, and a different `theme_api` is reported as incompatible.
    `registry.go` loads the bundled manifests (`go:embed`, a broken one
    stops startup) and, if `REWIND_THEME_DIR` is set, discovers
    `<id>/theme.json` through `os.Root` with the plugin discovery's
    protections. It is read once at startup and never changes. `store.go`
    holds per-user state.
  - **Storage.** The active theme stays in the `theme` preference (prefs now
    validates it against the registry through `prefs.WithThemes`).
    Migration `0005_themes` adds `theme_settings` (user, theme, enabled,
    customization JSON, last applied time). Applying, and disabling the
    active theme, write the preference and `theme_settings` in one
    transaction (`prefs.Store.UpdateTx`). The three earlier custom-color
    preferences are not migrated: a theme without a saved customization
    reports them as its `legacy` customization, so nothing changes for
    existing users and downgrades keep working.
  - **API.** `GET /api/themes`, `PUT /api/themes/active`,
    `PUT /api/themes/{id}/enabled` and `PUT /api/themes/{id}/customization`,
    all behind `requireAuth` and the existing cross-origin protection, each
    returning the full state. There are no install, upload or file endpoints.
  - **Frontend.** `services/theme-tokens.json` mirrors the contract (a Go
    golden test keeps it identical). `services/themes.ts` validates values,
    resolves a theme plus a customization (unreadable colors are ignored and
    reported), and turns tokens into the `--rw-*` properties with its own
    code (`cssVariables`), set through `style.setProperty`, so the CSP is
    unchanged. `Shell` holds the single theme state from the server and one
    effect applies the shown theme: a preview from Settings while Settings is
    open, otherwise the active theme. `ThemeSettings` (cards, apply, preview,
    enable/disable) and `ThemeEditor` (generated controls, live preview,
    contrast blocking, Save / Save and apply / Save only / Reset / Cancel
    with a discard confirmation) live in Settings. `ThemePreview` draws the
    main screens with the shared `app.css` classes. The `rewind.theme` hint
    now holds the token values last shown (version 2).
  - **New tokens** in `app.css`: `--rw-radius-button`, `--rw-radius-input`,
    `--rw-border-style`, `--rw-line-height`, `--rw-heading-scale` and
    `--rw-space-panel`, used by buttons, inputs, structural borders, body
    text, titles and panels. No new dependency.
- *Implemented (Phase 7): installable PWA.*
  - `public/manifest.webmanifest`: `standalone`, start URL and scope `/`, dark
    background and theme colour, icons at 192 and 512 px plus a maskable
    512 px icon and a 180 px Apple touch icon. The icons are rendered from
    `favicon.svg` by `scripts/render-icons.mjs` (original Rewind artwork) and
    committed.
  - **Service worker** (`web/sw/service-worker.js`), hand-written, no
    Workbox or PWA plugin. A small build plugin (`web/sw/plugin.ts`) writes
    `dist/sw.js` with the list of files to cache and a version hash of their
    contents. The policy:
    - it caches **only the versioned shell**: the content-hashed files in
      `/assets/`, `offline.html` and `offline.css`, the manifest and the
      icons. They are cached once at install, with `cache: 'reload'`;
      nothing is ever added to the cache at runtime;
    - `/api` requests (sign-in, session, library data and audio streams),
      requests with a `Range` header, non-GET requests and other origins
      are **not intercepted at all**, so they behave exactly as without a
      service worker. Music, cookies and private API responses are never
      cached;
    - page navigations always go to the network, so the server stays the
      authority and `index.html` is never served stale. Only when the
      network fails is the cached offline page shown. It explains that
      music needs the server and offers "Try again";
    - on activation, caches from older versions (`rewind-shell-*`) are
      deleted. A new version takes over at once (`skipWaiting`), which is
      safe because every asset name is content-hashed and nothing is
      lazily loaded.
  - The worker is registered only in production builds and only in a secure
    context. A failed registration is logged to the console and never
    affects the player. Media Session (lock-screen controls) is not
    implemented yet.
- **Secure context:** service workers and PWA install require HTTPS or
  localhost. Over plain `http://<lan-ip>` Rewind plays music but cannot be
  installed and has no offline page. The documented HTTPS route is
  `tailscale serve` (see [deployment.md](deployment.md#network-access)).
  LAN-only HTTPS needs a reverse proxy with locally trusted certificates,
  which Rewind does not configure.

## 8. Security model

Principles:

- Being on the LAN or tailnet does not make a request trustworthy. Tailscale
  limits who can reach the server, but it is **not** a substitute for
  protecting administrative operations.
- Single-owner design: one owner account. There is no public sign-up, no
  registration endpoint, and no multi-user roles in the initial release.
- Remote access (LAN, Tailscale) is opt-in (Phase 7). It is off by default
  and every step fails closed.

### Network foundation (*implemented*, Phase 1; opt-in remote access, Phase 7)

**By default Rewind is reachable from the server only.** Remote access needs
explicit configuration:

1. **Listen addresses.** `REWIND_ADDR` is a comma-separated list of
   `host:port` addresses (at most 8), default `127.0.0.1:8090`.
   - Loopback addresses (`127.x.x.x`, `::1`, `localhost`) are always
     accepted.
   - A **non-loopback IP address** (a LAN address such as `192.168.1.20`,
     or a Tailscale address such as `100.x.y.z`) is accepted only with
     `REWIND_REMOTE_ACCESS=true`.
   - **All interfaces** (`0.0.0.0`, `[::]` or `:port`) additionally needs
     `REWIND_LISTEN_ALL_INTERFACES=true`, because it also exposes Rewind on
     any public interface the server has. A specific address is
     recommended.
   - Host names other than `localhost`, interface zones and multicast
     addresses are always rejected: a name could resolve to an unexpected
     interface.
   - Any invalid entry stops startup with an error that names the setting
     to change. Rewind never falls back to another address.
2. **Bound-socket check.** After it starts listening, the server checks
   every socket. Without `REWIND_REMOTE_ACCESS=true`, each must be on a
   loopback address, so `localhost` resolving elsewhere cannot bypass rule
   1. If any address fails to bind (for example a Tailscale IP before
   `tailscaled` is up), the others are closed and Rewind exits instead of
   serving partially. If a listener fails later, the server stops.
3. **Host-header validation.** Requests are accepted only if their `Host` is:
   - a loopback name or address;
   - an IP address Rewind explicitly listens on (not a wildcard); or
   - a name or address listed in `REWIND_TRUSTED_HOSTS`. Matching is exact
     and case-insensitive, ignoring a port and a trailing dot. Wildcards,
     schemes, ports and paths are rejected in the setting, and subdomains
     are never implied.

   Anything else gets `403 forbidden_host`. The rejected name is logged
   (truncated) so the owner can add it. This blocks DNS-rebinding attacks.
   `X-Forwarded-Host`, `Forwarded`, `X-Forwarded-For` and `X-Real-IP` are
   never consulted.
4. **Startup log.** Rewind logs which addresses other devices can reach and
   which host names it accepts. It warns when remote access is on, louder
   when it listens on all interfaces, and when remote clients use plain
   HTTP. It never opens firewall ports, configures port forwarding, or
   talks to Tailscale.
4. **Security headers** on every response:
   - a same-origin `Content-Security-Policy` (no inline scripts or styles,
     `frame-ancestors 'none'`);
   - `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`;
   - `Referrer-Policy: no-referrer` and `Cross-Origin-Opener-Policy:
     same-origin`;
   - a restrictive `Permissions-Policy`.
5. **Request limits:**
   - request bodies are capped at 1 MiB (`413 body_too_large`) and headers at
     64 KiB;
   - `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s;
   - there is no global `WriteTimeout`, so future audio streams are not cut
     off.
6. **No CORS headers.** Request headers and bodies are not logged.

### Authentication (*implemented*, Phase 2)

Endpoints are documented in [api.md](api.md).

**First-run setup** (owner decision: browser setup gated by a one-time code):

- While no account exists, each start generates an 80-bit random code. It is
  written as `XXXX-XXXX-XXXX-XXXX` to `$REWIND_DATA_DIR/setup-code`, a fresh
  file with mode 0600. Only the file's **path** is logged, never the code.
- `POST /api/auth/setup` succeeds only if every one of these holds:
  1. no account exists;
  2. the TCP peer is a loopback IP **and** the `Host` header is a loopback
     name or address (Phase 7). `X-Forwarded-For` and similar headers are
     ignored;
  3. the code matches (constant-time comparison);
  4. the request passes the CSRF and Host checks;
  5. the request is within the rate limit.
- A local reverse proxy such as `tailscale serve` connects from
  `127.0.0.1` but forwards the tailnet `Host`, so the Host requirement keeps
  setup unavailable through it. The setup code is a second, independent
  barrier.
- The account check and insert run in one write-locked transaction, so
  concurrent attempts create exactly one account; the rest get `409`.
- After success the code is invalidated and its file deleted, and the owner
  is signed in. Later starts remove any leftover code file.
- No default credentials exist and there is no registration endpoint.
  Setup cannot create a second account.

**Passwords:**

- Hashed with **Argon2id** (`golang.org/x/crypto/argon2`), at OWASP's
  recommended minimum: memory 19 MiB, 2 iterations, 1 lane, a 16-byte random
  salt per hash, and a 32-byte key.
- Stored as a PHC string (`$argon2id$v=19$m=19456,t=2,p=1$salt$key`), so the
  parameters can be raised later. Hashes with outdated parameters are
  re-hashed on the next successful login.
- Verification uses `crypto/subtle.ConstantTimeCompare`.
- When reading a stored hash, parameters are bounds-checked, so a tampered
  database cannot trigger huge memory use.
- At most **two** hashes are computed at once, which caps memory on modest
  hardware.
- Rules: 12–256 characters (counted as Unicode characters), valid UTF-8, and
  not equal to the username. There are no composition rules; passphrases are
  encouraged.
- Usernames: 1–64 characters from `A–Z a–z 0–9 . _ -`, case-insensitive.
- Passwords and hashes are never logged or returned by the API. The frontend
  never stores them, and tests check this.

**Sessions:**

- The token is 32 bytes from `crypto/rand`, encoded as base64url (43
  characters). Only its SHA-256 is stored in `sessions.token_hash`, so a
  leaked database does not reveal usable tokens.
- Lifetime: an absolute limit (`REWIND_SESSION_LIFETIME`, default 30 days)
  plus an idle limit (`REWIND_SESSION_IDLE_TIMEOUT`, default 7 days). Both are
  enforced on the server for every request.
- `last_seen_at` is written at most once per minute.
- Expired or idle sessions are deleted when they are presented. All expired
  rows are purged at each login. There is no background worker.
- **Rotation:** login revokes the session cookie the request carried (if
  any) and issues a new token. Setup signs in through the same path.
- **Logout** deletes the session on the server and clears the cookie.
- **Password change** requires the current password. In one transaction it
  updates the hash, deletes **all** of the account's sessions (every device),
  and issues a new session to the caller.
- **Middleware:** `requireAuth` validates the cookie, puts the user in the
  request context, and returns a JSON `401 unauthenticated` otherwise. It
  never redirects. An invalid cookie is cleared. All future protected routes
  use it. Session tokens never appear in logs, URLs or response bodies.
- **Unknown users:** a login with an unknown username runs a full Argon2id
  verification against a dummy hash. The response is byte-identical to a
  wrong-password response, so neither content nor timing reveals whether the
  account exists.

**Cookie:**

- `rewind_session`, with `HttpOnly`, `SameSite=Strict`, `Path=/`, no
  `Domain`, and `Max-Age` and `Expires` set to the session's absolute expiry.
- `Secure` is controlled by `REWIND_COOKIE_SECURE`. The default is `false`,
  because plain HTTP (loopback, or a LAN address) would drop a `Secure`
  cookie. Use `true` whenever remote clients connect over HTTPS, for example
  through `tailscale serve`. Chromium and Firefox also accept `Secure`
  cookies on `http://localhost` and `http://127.0.0.1`, so local access keeps
  working there.
- Remote access never weakens the cookie: it is always `HttpOnly` and
  `SameSite=Strict`, never has a `Domain`, and Rewind does not switch
  `Secure` off on its own.
- With `REWIND_COOKIE_SECURE=true` the cookie becomes
  `__Host-rewind_session`. Browsers then enforce Secure, `Path=/` and no
  Domain.
- `X-Forwarded-Proto` is not trusted to switch this automatically.

**CSRF:**

- Layer 1: `SameSite=Strict` cookies are not sent on cross-site requests.
- Layer 2: the standard library's `http.CrossOriginProtection` wraps every
  route. It rejects any non-GET/HEAD/OPTIONS request that the browser marks
  `Sec-Fetch-Site: cross-site` or `same-site`, or whose `Origin` host does
  not match `Host`, with `403 cross_origin`.
  - This covers authenticated actions and the unauthenticated login and
    setup (login CSRF).
  - Requests carrying neither header come from non-browser clients, which
    cannot be used for CSRF, and are allowed.
- Layer 3: state-changing endpoints accept only `application/json` bodies.
  A cross-origin page cannot send that without a CORS preflight, and Rewind
  answers no preflight.
- GET requests never change state.
- No CORS headers are sent, and no origins are trusted. A trusted host name
  is not a trusted origin: a page on one trusted name cannot post to Rewind
  under another (tested).
- CSRF tokens were judged unnecessary given these layers.

**Login abuse controls:**

- An in-memory token bucket guards `setup`, `login` and `change-password`:
  - per client IP: a burst of 5, then 1 attempt per 12 s;
  - all clients together: a burst of 20, then 1 per 3 s.
- Exceeding a limit returns `429 rate_limited` with `Retry-After`. The
  password is not checked while a client is limited.
- The bucket table holds at most 4096 entries; fully refilled entries are
  evicted first.
- There is **no account lockout.** Attempts resume as soon as tokens refill,
  so an attacker cannot lock the owner out permanently.
- Limitations:
  - state resets on restart;
  - the client IP comes from the TCP connection. With the default
    loopback-only setup, every client shares one bucket, so a local process that
    keeps failing can delay the owner's sign-in by up to a few seconds per
    attempt, but cannot block it permanently;
  - behind a local reverse proxy such as `tailscale serve`, all proxied
    clients share the address `127.0.0.1`, so the per-client limit acts as
    one shared limit. There is deliberately no trusted-proxy setting:
    forwarding headers are never trusted. A tailnet member who keeps
    failing to sign in can therefore slow down other sign-ins, but cannot
    block them permanently. Direct LAN or Tailscale-IP connections are
    limited per device.

**Request validation:**

- JSON bodies must be `application/json`, contain a single object, and
  include no unknown fields.
- Auth bodies are capped at 16 KiB, and all bodies at 1 MiB.
- Internal errors return a generic `500 internal_error`. Database errors
  appear only in the server log.

**Filesystem (planned, for streaming):** files are reached only through
database IDs plus `os.Root`. API responses do not include absolute paths.

### Remote access (*implemented*, Phase 7)

The earlier requirements for enabling LAN or Tailscale access are met:

- Remote listening is opt-in (`REWIND_REMOTE_ACCESS`, plus
  `REWIND_LISTEN_ALL_INTERFACES` for wildcards). Trusted host names are
  explicit (`REWIND_TRUSTED_HOSTS`), with no wildcards.
- Setup stays restricted to the server itself: a loopback peer, a loopback
  Host, and the setup code. Complete setup before enabling remote access.
- Every library, stream and admin route requires a session, whatever
  network the request comes from. Tests send requests from LAN and
  Tailscale peer addresses, through trusted host names, with spoofed
  forwarding headers, and with mismatched origins.
- The recommended remote path is HTTPS through `tailscale serve`, with
  `REWIND_COOKIE_SECURE=true`. Direct HTTP to a Tailscale IP is encrypted by
  WireGuard. Plain-HTTP LAN access is supported but sends the password and
  session cookie unencrypted over the LAN, and Rewind warns about this at
  startup.
- Tests bind only to loopback addresses. The non-loopback path is covered
  by configuration tests and by verifying the bound-socket check with
  synthetic addresses. Real LAN and tailnet use was **not** tested by the
  project's automated tests.

Phase 10 still performs the full endpoint-by-endpoint review.

## 9. Deployment model

- One binary, run by systemd as an unprivileged user (no root, no privileged
  container), with configuration from environment variables
  (`EnvironmentFile=`). *Implemented in Phase 12:* `release/rewind.service`
  (hardened unit) and `scripts/install.sh`. The installer keeps versioned
  releases in `/opt/rewind/releases/<version>` with a `current` symlink,
  the same layout `deploy.yml` uses. It verifies `SHA256SUMS` and test-starts
  a new binary on a spare loopback port before switching. It backs up the
  database before upgrades and rollbacks, and never restores a database or
  overwrites configuration. See [install.md](install.md) and
  [operations.md](operations.md).
- `REWIND_DATA_DIR` (recommended: `/var/lib/rewind`, owned by the service
  user) holds the SQLite database and, before first setup, the setup code.
  Backups copy the database (see §5a), not the music.
- The USB drive is mounted by the OS, preferably read-only and readable by the
  service user. Exact mount instructions are documented later, never automated.
- **Local machine only by default.** Remote access is opt-in (Phase 7, §8).
  The supported layouts, all documented in
  [deployment.md](deployment.md#network-access):
  - Tailscale with HTTPS (recommended): Rewind stays on loopback,
    `tailscale serve` proxies the tailnet name to it, and that name is
    listed in `REWIND_TRUSTED_HOSTS`.
  - Tailscale IP: Rewind also listens on the server's `100.x.y.z` address.
  - LAN: Rewind also listens on the server's LAN address (plain HTTP).
  - Rewind configures none of the networking itself: no firewall rules, no
    port forwarding, no Tailscale API calls, and no public reverse proxy.
- Builds: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, with linux/arm64 later.
  Development may happen on Windows, while CI and the reference environment
  are Linux.

## 10. Phase plan (updated after Phase 0–2 decisions)

| Phase | Change from CLAUDE.md |
| --- | --- |
| 0 | Adds a license/dependency review and these docs. |
| 1 | Server and frontend foundation without a database. Security foundation only: loopback-only binds (non-loopback rejected, no override), bound-socket check, Host validation, security headers, body and header limits. No authentication. |
| 2 | SQLite, versioned migrations, and database-backed authentication: browser first-run setup gated by a one-time code and loopback peers, Argon2id, persistent hashed sessions with absolute and idle expiry, rotation, logout, password change, CSRF protection, login rate limiting. **Still loopback-only.** No `hash-password` command: the setup code replaced it. |
| 3 | USB storage and music library scanner (CLAUDE.md "Phase 2" scope): `REWIND_LIBRARY_ROOTS` plus a background startup scan, go-taglib metadata, migration `0002_library`, incremental scanning, rename detection, safe handling of disconnected drives. No library API or UI yet. Still loopback-only. |
| CI/CD | GitHub Actions: `ci.yml` (checks, embedded build, smoke test, license gate), `release.yml` (`v*` tags → Linux amd64 release with notices, LGPL sources, SHA256SUMS), `deploy.yml` (manual deploy with approval: ephemeral Tailscale node via OIDC, SSH over the tailnet only, health check, automatic rollback). See [deployment.md](deployment.md). |
| 4 | Authenticated music-library API (CLAUDE.md "Phase 3" scope): overview, roots, paginated/searchable tracks, track detail, background rescan via the existing scanner (`Scanner.Start`). No schema change. Still loopback-only. |
| 5 | Audio streaming (`/api/tracks/{id}/stream`, `os.Root` containment, Range via `http.ServeContent`) and a minimal browser player (native `<audio>`, track list with search). Still loopback-only. |
| all later | Every new endpoint is built behind `requireAuth`. |
| 6 | Documents the HTTPS requirement for PWA install (`tailscale serve`). |
| 7 | Installable PWA (manifest, original icons, conservative hand-written service worker caching only the versioned shell, offline page) and opt-in network access: multiple `REWIND_ADDR` entries, `REWIND_REMOTE_ACCESS`, `REWIND_LISTEN_ALL_INTERFACES`, `REWIND_TRUSTED_HOSTS` Host allowlist, setup limited to a loopback peer and Host, startup exposure warnings. No Tailscale dependency. |
| 8 | Playlists and preferences (CLAUDE.md "Phase 7" scope, without artwork caching): migrations `0003_playlists` and `0004_preferences`, per-user playlist and preference APIs, playlist UI, sequential playlist playback with previous/next in the existing player, and a settings view (`library_sort`, `autoplay_next`). The scanner's rename merge moves playlist entries to the kept track ID. No change to networking, sessions or the service worker. |
| 9 | Themes (CLAUDE.md "Phase 8" scope, built-in only): Rewind Dark (unchanged default), Light and OLED Black; `theme` and `accent_color` preferences in the existing table and API; theme picker and custom accent in Settings with WCAG contrast checks; theme hint in `localStorage` to avoid a flash at startup. No theme files, uploads or CSS editing. |
| 10 | Plugin architecture (CLAUDE.md "Phase 9" scope): compiled-in, administrator-enabled plugins with JSON manifests, discovery through `os.Root`, a lifecycle manager with deadlines and failure containment, one extension point (`events.library_scan`), read-only `GET /api/plugins` and a Settings status section, and the `missing-tracks` example plugin. No runtime loading, no install/enable endpoints. |
| 11 | Security, reliability and performance audit (CLAUDE.md "Phase 10" scope), documented in [security-audit.md](security-audit.md): Range zero-suffix fix, stream-open race check (`os.SameFile`), non-destructive password recovery, private deploy backups, two test-reliability fixes, measurements on a development machine. No new features, dependencies or schema changes. |
| later | Resource measurement on the owner's actual hardware. |
| 12 | Packaging and release readiness (CLAUDE.md "Phase 11" scope). Release packaging was implemented earlier (see "CI/CD" above). Phase 12 adds: the hardened systemd unit and server configuration template in `release/`, `install.sh` (install, upgrade, rollback, status), an end-to-end installation test run in CI (`install-test.sh`), `release-check.sh`, the installation, configuration and operations guides, SECURITY.md, CONTRIBUTING.md, CHANGELOG.md and issue templates. It also fixes security-audit F8: taglib's compiled-code cache is moved out of the shared `/tmp`. No new features, no schema change and no new dependency. |
| UI | Theme and style system: declarative theme manifests (theme API 1) validated on the server, bundled Rewind Dark/Light/OLED, optional administrator theme directory (`REWIND_THEME_DIR`), migration `0005_themes` (per-theme customization, enabled flag), theme API endpoints, Settings theme cards and per-theme editor with live preview. Earlier custom colors kept as "legacy" customization. No upload/download, no theme code, no new dependency. |
| Spec Phase 2 | Listening experience (§5f): shared playback engine with a persisted per-user queue, shuffle/repeat/volume/speed, Now Playing and mini player, albums and artists derived from tags, artwork extraction and cache, favorites, listening history with idempotent qualified plays and play counts, database-only metadata editor (edits survive rescans), playlist duplicate/bulk add/artwork/drag and drop, keyboard shortcuts, responsive navigation. Migration `0006_listening`. No new dependency. |

### Phase 1 acceptance criteria (as amended)

- The Go server starts on a loopback address and stops cleanly on SIGINT and
  SIGTERM.
- `GET /api/health` returns `200 {"status":"ok","name":"rewind","version":...}`.
- Invalid configuration, **including any non-loopback bind address**, stops
  startup with a clear error. Tests prove that non-loopback binds are rejected
  and loopback binds work.
- Requests with a non-loopback `Host` header are rejected.
- The Svelte frontend runs in development mode, passes type checks, and builds.
- The backend compiles before the frontend is built. Production builds embed
  and serve the real frontend, and API routes never return frontend HTML.
- LAN and Tailscale access are **not** expected to work in Phase 1.

### Phase 2 acceptance criteria

- SQLite initializes in `REWIND_DATA_DIR`, and migrations apply reliably and
  idempotently. Failures never reset the database.
- First-run setup requires a loopback peer and the one-time code, creates
  exactly one account, and cannot be repeated.
- Passwords are stored only as Argon2id hashes. Session tokens are stored
  only as SHA-256 hashes.
- Sessions persist across restarts, expire on the server, rotate on login,
  and are revoked by logout and password change.
- The cookie is `HttpOnly`, `SameSite=Strict` and `Path=/`, with a bounded
  `Max-Age`, and `Secure` when configured.
- Protected routes reject unauthenticated requests with a JSON 401.
  Cross-origin state changes are rejected, and login attempts are
  rate-limited without lockout.
- The application remains loopback-only. Phase 1 behavior is intact.

### Phase 3 acceptance criteria

- Roots are configured through `REWIND_LIBRARY_ROOTS`, validated,
  deduplicated, and never deleted automatically.
- Supported files are discovered recursively. Metadata, including duration,
  is stored, with a documented file-name fallback.
- Unsupported, malformed, unreadable and vanishing files never abort a scan.
- Rescans create no duplicates, skip unchanged files, reprocess changed
  files, and keep track IDs (including unique renames).
- Unavailable, vanished, empty (unmounted) and partially readable roots
  never cause tracks to be marked missing. Interrupted scans are recorded.
- Source files are never modified (tested on read-only fixtures).
- Readers and channels are bounded. Same-root scans are mutually exclusive.
- Upgrading a Phase 2 database keeps the auth data. Phase 1/2 behavior is
  intact.

## 11. Deferred decisions

Root relocation (keeping track IDs when a drive's mount path changes),
purging missing tracks and disabled roots, indexes for the library API
(only if measurements on the target hardware call for them), an owner/role
column if more accounts are ever added, a trusted reverse-proxy model
(deliberately not implemented in Phase 7: forwarding headers are ignored),
WAL mode (only if contention is measured), custom artwork uploads,
folder browsing, smart playlists, Media Session lock-screen controls, hot reload of the theme directory, plugin trust model, a container image (Phase 12
chose a native systemd installation; no image is published), Debian/Ubuntu
packages (`.deb`), signed releases, and arm64 builds (after amd64 is
stable, and only once tested).
