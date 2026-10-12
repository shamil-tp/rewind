# Security and Reliability Audit (Phase 11)

Date: 2026-10-12. Scope: the complete repository as of this audit (server,
scanner, streaming, SQLite, authentication, plugins, frontend and service
worker, CI/CD and release scripts). This phase covers CLAUDE.md's "Phase 10"
scope; this project's phase numbers are one ahead (see
[architecture.md](architecture.md#10-phase-plan-updated-after-phase-02-decisions)).

Environment: a Windows 11 development machine, plus WSL2 Ubuntu (Linux 6.6,
i7-12650H, 7.6 GiB) for every Linux-specific test. Linux test binaries were
cross-compiled with `GOOS=linux CGO_ENABLED=0 go test -c` and run in WSL. This
is **not** the owner's target hardware, and no real USB drive, LAN or
tailnet was used.

## 1. Findings

Severity reflects practical exploitability for a single-owner server on a
private network.

| ID | Severity | Type | Finding | Status |
| --- | --- | --- | --- | --- |
| F1 | Medium | Data integrity, confirmed | The documented password recovery (`DELETE FROM users`) silently deleted every playlist and preference through `ON DELETE CASCADE` | Fixed (docs) |
| F2 | Low | Confidentiality, confirmed | Database backups made by `scripts/deploy-remote.sh` and by the documented `sqlite3 .backup` command were created world-readable (0644) | Fixed |
| F3 | Low | HTTP correctness, confirmed | `Range: bytes=-  0` and `bytes=-+0` got `206` with the invalid `Content-Range: bytes 54-53/54`; the committed fuzz corpus made `go test ./internal/server` fail | Fixed |
| F4 | Low | Defense in depth, confirmed | Race between the symlink check and the open in `OpenTrack`: a relative symlink (or another file) swapped in during that window was served, though always from inside the same root | Fixed |
| F5 | — | Test reliability, confirmed | `TestSlowAndFailingHandlersDoNotBlock` failed intermittently on Linux (most runs with `-count=10`) | Fixed |
| F6 | — | CI performance, confirmed | Two stalled-reader tests took 67 s each on Linux, adding more than 2 minutes to every Linux test run | Fixed |
| F7 | — | Repository hygiene | Empty `.github/workflows/releases.yml`; untracked, un-ignored `pid` file; `google/uuid` approved in `release/third-party-modules.txt` but no longer linked | Reported, not changed |

### F1: password recovery deleted playlists and preferences

- **Evidence:** `playlists.user_id` and `user_preferences.user_id` are
  `REFERENCES users ON DELETE CASCADE` (migrations 0003 and 0004).
  `architecture.md` itself said the procedure would be revisited once
  playlists existed. That never happened.
- **Fix:** a new procedure in architecture.md, "Password recovery". A
  throwaway Rewind instance with an empty data directory creates the new
  Argon2id hash. `ATTACH` copies it into the real database and deletes all
  sessions. No tool, endpoint or code was added.
- **Verified** end to end on Linux with the real binary:
  - the old password is refused (`401`);
  - the old session cookie is refused (`401`);
  - the new password works (`200`);
  - the playlist and the `theme` preference are unchanged.

  Python's `sqlite3` module stood in for the `sqlite3` CLI, which is not
  installed in the test environment.

### F2: world-readable backups

- **Evidence:** `sqlite3 .backup` creates its output file through the process
  umask. With umask 022 the file was 0644, and with umask 077 it was 0600.
  This was checked in WSL with the same SQLite library through Python.
- **Impact:** a backup contains the Argon2id password hash and the SHA-256
  hashes of session tokens. The token hashes cannot be used as cookies, so
  the exposure is an offline password-guessing target for local users of the
  server.
- **Fix:**
  - `deploy-remote.sh` runs the backup under `umask 077`;
  - deployment.md creates the backup directory with mode 0700;
  - the architecture.md backup command uses `(umask 077 && sqlite3 …)`.
- The deploy script itself was syntax-checked (`sh -n`) but not run against
  a server.

### F3: zero-length suffix ranges

- **Cause:** `dropZeroSuffixRanges` recognized only the exact spelling `-0`.
  `net/http` trims spaces and tabs inside each range spec and parses the
  length with `strconv.ParseInt`, so `- 0`, `-\t0`, `-+0` and `-00` reached
  `http.ServeContent`, which answers them with an invalid `Content-Range`.
- **Fix:** `zeroSuffixRange` (`internal/server/library.go`) parses each spec
  exactly as `net/http` does.
- **Tests:**
  - `TestStreamUnsatisfiableAndMalformedRanges` covers seven spellings, plus
    a zero suffix combined with a valid range;
  - new fuzz seeds were added;
  - `FuzzStreamRange` ran for 90 s (527,520 executions) without a failure.
- **Real binary:** `bytes=-  0` and `bytes=-+0` now return `416` with
  `Content-Range: bytes */<size>`.

### F4: check-then-open race when streaming

- **Cause:** `os.Root` confines every open to the root, but it follows
  relative symbolic links that stay inside it. `OpenTrack` refused links with
  `Lstat` and then opened the path. A link swapped in between those two
  calls was therefore followed. Exploiting this needs write access to the
  music drive, and it can never reach a file outside the root.
- **Fix:** the opened descriptor must be the file that `Lstat` saw
  (`os.SameFile`).
- **Tests** (`internal/library/stream_test.go`) use a test-only hook in that
  window:
  - **without the fix**, both race tests fail on Linux ("opened the
    swapped-in link target");
  - **with the fix**, both pass.

  On Windows the symlink test is skipped, because creating symlinks needs a
  privilege.
- **Remaining, by design:** symbolic links in *directory* components of a
  stored path are followed while they stay inside the root. This was
  verified: a link that leaves the root fails with "path escapes from
  parent". The scanner never records such paths.

### F5: flaky plugin test

- **Cause:** the test closed the manager once the fast plugin had its
  events. `Manager.Close` drops undelivered events (as documented), so the
  slow or panicking plugin sometimes never received one.
- **Fix:** the test waits until every worker has taken an event, and now
  also checks that `Close` returns promptly.
- **Result:** the test passed 300 times in a row on Linux and 100 times on
  Windows. Product code is unchanged.

### F6: slow stalled-reader tests on Linux

- **Cause:** instrumentation of `idleWriter` showed that the server behaves
  correctly. The stalled chunk failed with `i/o timeout` after the 300 ms
  idle deadline, and the handler returned and closed the file. The client
  side was slow: it set `SO_RCVBUF=4096`, which is below the 64 KiB loopback
  segment size on Linux, so the kernel delivered the already-sent 2.6 MB
  through window probes for about 66 s.
- **Fix:** the tests use a 128 KiB receive buffer. A stalled stream is still
  cut off: 2.7 MB of 16 MB delivered.
- **Result:** the full Linux test run went from more than 3 minutes to
  15 seconds.

### F7: hygiene (left for the owner)

- **`.github/workflows/releases.yml`** is a 0-byte file. GitHub reports
  empty workflow files as invalid. `release.yml` is the real release
  workflow. Recommendation: delete it.
- **`pid`** (contents `757`) is untracked and not ignored, so `git add .`
  would commit it. Recommendation: delete it.
- **`google/uuid`** is approved in `release/third-party-modules.txt` but is
  no longer linked; `scripts/third-party.sh` reports this itself.
  Recommendation: remove the entry.

## 2. Informational and speculative (not changed)

- **Request context of long streams.** `ReadTimeout` (30 s) keeps the read
  deadline in force while a handler runs, so `net/http` cancels
  `r.Context()` 30 s after a stream starts. This was verified in the Go
  1.27.2 source. It has no effect today, because the copy never consults
  the context. A comment in `server.go` warns future changes.
- **Metadata reads open files by path.** A FIFO swapped in during a scan
  would block one reader until the 2-minute extract timeout. The reader
  then moves on, but the blocked goroutine cannot be reclaimed. Abandoned
  TagLib reads (timeouts) keep their goroutine and WebAssembly memory. The
  timed-out set is capped at 1,024 files, after which a hanging file is
  retried on each rescan. Both require a crafted file on the music drive.
  Neither was reproduced.
- **Tampered password hashes.** Bounds accept up to 1 GiB of Argon2 memory,
  with 2 hashes at once. This is only reachable with write access to the
  database, which is already full compromise.
- **Panics.** `net/http` recovers handler panics and closes the connection
  without a JSON body. A transaction open in that handler is rolled back
  when the request context is cancelled. `db.InTx` does not `defer
  Rollback`; the scanner (which uses `context.WithoutCancel`) has no recover
  at all, so a scanner panic stops the process.
- **Log volume.** Every request with an untrusted `Host` logs one warning,
  truncated to 100 characters and quoted by slog, so a client that can reach
  the port can grow the log. There is no log rate limit.
- **Health endpoint.** `/api/health` is unauthenticated and reports the
  version. This is intentional, and the deploy health check uses it.

## 3. Audit checklist: verified without findings

Each item was checked against the code. Black-box checks used the embedded
Linux binary on loopback, unless noted.

- **Network:**
  - default bind is `127.0.0.1` only (`ss -ltnp`);
  - non-loopback and wildcard addresses need explicit flags, and every bound
    socket is checked;
  - `Host: evil.example` gets `403`;
  - forwarding headers are never read;
  - there are no CORS headers;
  - a cross-site `POST` gets `403`;
  - a request whose headers stall for 12 s is closed by
    `ReadHeaderTimeout`;
  - `SIGTERM` exits with status 0.
- **Authentication:**
  - Argon2id (19 MiB, t=2, p=1), with bounds-checked parsing of stored
    hashes and at most 2 concurrent hashes;
  - unknown users get a dummy verification;
  - 256-bit session tokens, stored as SHA-256 hashes, with absolute and idle
    expiry checked on every request;
  - login rotates the session, logout revokes it (the old cookie gets
    `401`), and a password change revokes all sessions;
  - the cookie is `HttpOnly`, `SameSite=Strict`, with no `Domain`;
  - setup requires a loopback peer and Host, the code, and a write-locked
    transaction;
  - the setup-code file is 0600 and is deleted after setup (checked);
  - the rate limiter is bounded (4,096 keys);
  - neither the code nor the password appears in the log (checked).
- **Filesystem:**
  - the scanner never follows symlinks and skips non-regular files;
  - an unavailable, vanished, unmounted-looking or partially readable root
    never marks tracks missing (re-checked: drive renamed away, rescan
    `failed`, the track stays `present`, stream `503 root_unavailable`);
  - source files were unchanged (SHA-256 before and after);
  - same-root scans are mutually exclusive;
  - streaming goes through `os.Root`, `filepath.IsLocal`, `O_NONBLOCK` and a
    regular-file `fstat`;
  - FIFO and descriptor-leak tests pass on Linux.
- **Streaming:** full, single, multiple, suffix and open-ended ranges; 416;
  empty files; `HEAD`; conditional requests; per-chunk write deadlines;
  `Cache-Control: private, no-store`; content type taken from the indexed
  format.
- **SQLite:**
  - every user value is a bound parameter;
  - sort keys come from a fixed map;
  - `LIKE` is escaped;
  - foreign keys are enforced and checked at startup;
  - multi-step writes run in `BEGIN IMMEDIATE` transactions on a single
    connection;
  - a playlist reorder is all-or-nothing (existing injected-failure test);
  - migrations are transactional and refuse unknown versions;
  - there is no automatic reset;
  - the database file is 0600.
- **Plugins:**
  - compile-time registration only;
  - manifests are strict, bounded and read through `os.Root`, and links are
    refused;
  - the plugin's ID and version must match the manifest;
  - deadlines and panic recovery apply;
  - plugins get no routes, database or file access;
  - the docs do not claim a sandbox.
- **Frontend:**
  - no `{@html}`/`innerHTML`, `eval` or dynamic URLs from metadata;
  - the only web-storage item is the theme hint;
  - the service worker never intercepts `/api`, `Range` or non-GET requests,
    and caches only the content-hashed shell.
- **Supply chain:**
  - GitHub Actions are pinned to full commit SHAs, with `contents: read` by
    default;
  - only the release publish job has `contents: write`, and only the deploy
    job has `id-token: write`;
  - CI uses no secrets, and deploy secrets are scoped to steps behind the
    `production` environment;
  - release archives are built from an allowlist and scanned for `.env`
    files, databases, keys and audio.

## 4. Measurements

Taken on the development machine described above (Linux in WSL2, temporary
ext4 directories), so they are **not** representative of a modest server
reading from USB.

| Workload | Result |
| --- | --- |
| Real scan, 2,000 generated files (5 formats, real TagLib), 2 workers | 690 ms; unchanged rescan 7 ms |
| Same, 8 workers | 400 ms; unchanged rescan 8 ms |
| Heap in use during those scans | 2.2 MiB before, 14 MiB peak; Go runtime total 45 MiB |
| Goroutines / open descriptors before and after scans | 3 → 3, 8 → 8 (no leaks) |
| 100,000 tracks: first page sorted by artist | 35 ms (median) |
| 100,000 tracks: search with count | 67 ms |
| 1,000 playlists listed with counts | 26 ms |
| 5,000-track playlist: get / reverse reorder | 29 ms / 28 ms |
| Track-page reads during a 1,500-file scan | median 1.4 ms, p95 5.8 ms, max 8.3 ms |

- **Streaming memory and descriptors:** the existing tests
  `TestStreamingMemoryIsBounded` and `TestDisconnectReleasesFileAndGoroutines`
  pass on Linux.
- **Query plans:** use the existing indexes (`perf_test.go` output).
- **Changes:** nothing justified a new index or a change to scanning.

Commands:

- `REWIND_PERF=1 go test ./internal/library -run 'TestPerformanceReport|TestScanContentionReport' -v`
- The real-scan measurement used a temporary test that is not part of the
  repository.

## 5. Tests executed

| Check | Result |
| --- | --- |
| `gofmt -l`, `go vet ./...`, `go vet -tags rewind_embed ./...` | clean |
| `go mod verify`, `go mod tidy -diff` | clean |
| `go test -race -count=1 ./...` (Windows, CGO) | all packages pass |
| Linux test binaries for all 12 packages (WSL), 3 consecutive rounds plus one verbose run | all pass (259 top-level tests). The only skips are the two opt-in perf reports (`REWIND_PERF=1`), which were run separately. Symlink, FIFO and descriptor tests run and pass |
| `go test -tags rewind_embed ./web/ ./internal/server/` | pass |
| `FuzzStreamRange` 90 s | pass, 527,520 executions |
| `npm run check` (svelte-check + tsc) | 0 errors, 0 warnings |
| `npm test` | 17 files, 182 tests pass |
| `npm run build` | pass |
| `npm audit` | 0 vulnerabilities |
| `scripts/smoke-test.sh` on the embedded Linux binary (WSL) | pass |
| `scripts/third-party.sh` | pass (12 Go modules, 2 npm packages, taglib.wasm components) |
| Black-box checks of the Linux binary (section 3) and the password-recovery procedure (F1) | pass |

The frontend has no lint script; `npm run check` is its static check, as in
CI.

## 6. Not run, and manual verification still needed

- **`govulncheck`** is not installed and was not downloaded during this
  audit. Run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` before
  a release.
- **Not run:**
  - `scripts/package-release.sh`, which needs a committed, clean tree;
  - `scripts/lgpl-sources.sh`, which downloads sources;
  - the deploy workflow.

  License compliance for the LGPL go-taglib binary is not claimed beyond
  what `third-party.sh` checks.
- **Real LAN, Tailscale and `tailscale serve`** access, with
  `REWIND_COOKIE_SECURE=true`.
- **A real USB drive:** unplugging during playback and during a scan
  (simulated here by renaming the root), read-only mounts, and exFAT/NTFS
  behaviour.
- **Resource use on the owner's hardware,** and with a real library over
  USB.
- **PWA behaviour in real browsers** (install, offline page, cache update).
  These were checked only through unit tests of the service worker and the
  build plugin.

## 7. Operational recommendations

- Keep the default loopback binding. Prefer `tailscale serve` with
  `REWIND_COOKIE_SECURE=true` for remote access.
- Keep backups and the backup directory private (0600 / 0700). Test a
  restore after upgrades.
- Use the non-destructive password recovery in architecture.md; never
  `DELETE FROM users`.
- Delete `releases.yml` and `pid` (F7) before the next commit.
