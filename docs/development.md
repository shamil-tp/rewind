# Developing Rewind

This page is for working on Rewind's code. To install a release on a
server, see [install.md](install.md).

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | 1.26 or newer (see `go.mod`) | backend |
| Node.js | 24 LTS recommended (`^20.19`, `^22.12` or `>=24` supported) | frontend only |
| npm | bundled with Node.js | frontend dependencies (do not use Yarn or pnpm) |
| Git | any recent version | |

Not required: a C compiler (builds use `CGO_ENABLED=0`), Docker, a database
server, or Tailscale.

The backend can be built and tested without Node.js. Node.js is only needed to
work on or build the frontend.

Development works on Linux, macOS and Windows. The reference environment and CI
are Linux on amd64. Shell examples use POSIX `sh`. On Windows, use Git Bash or
the PowerShell equivalents shown.

## Configuration

Rewind reads environment variables only and **does not load `.env` files
itself**. All settings are optional; they are listed in
[configuration.md](configuration.md). `.env.example` contains every setting
with its default.

Invalid values stop startup with a message naming every problem.

**Loopback by default.** Without extra settings Rewind accepts only
`127.0.0.1:<port>`, `[::1]:<port>` or `localhost:<port>`, checks the bound
socket, and rejects requests whose `Host` header is not a loopback name.
Access from other devices is opt-in; see
[deployment.md, "Network access"](deployment.md#network-access). Development
needs none of it.

**Cookies in development.** Keep `REWIND_COOKIE_SECURE=false` for
`http://localhost`. With `true`, browsers drop the cookie over plain HTTP and
you cannot stay signed in.

To use a file of settings, copy the template and load it into your shell:

```sh
cp .env.example .env              # .env is git-ignored
set -a; . ./.env; set +a          # bash / zsh
```

```powershell
# Windows PowerShell
Get-Content .env | Where-Object { $_ -match '^\s*[^#].*=' } | ForEach-Object {
  $k, $v = $_ -split '=', 2; Set-Item "env:$($k.Trim())" $v.Trim()
}
```

## Development mode

Development uses two processes: the Go server for the API, and the Vite dev
server for the frontend with hot reload.

**Terminal 1: backend**

```sh
go run ./cmd/rewind
```

Without the production build tag, the Go server serves a small **placeholder
page** at `/` that says the frontend is not built in, and logs a warning. The
placeholder is not the application.

**Terminal 2: frontend**

```sh
cd web
npm ci            # first time, or after package-lock.json changes
npm run dev
```

Open the address Vite prints, usually http://localhost:5173. Vite forwards
`/api/*` requests to the Go server at `http://127.0.0.1:8090`. The session
cookie is set for the Vite origin, so sign-in works through the proxy. If you
changed `REWIND_ADDR`, point Vite at it with `REWIND_DEV_API_TARGET`, for
example `REWIND_DEV_API_TARGET=http://127.0.0.1:9000 npm run dev`, or put the
variable in `web/.env.local`.

**First run in development.** On first start the backend creates `./data/`,
the database, and `./data/setup-code`. Enter that code on the setup screen.
To start over, stop the backend and delete `./data/`. That deletes the
account and all sessions.

## Checks

```sh
# Backend
gofmt -l .               # must print nothing
go vet ./...
go test ./...
go test -race ./...      # needs CGO and a C compiler; CI runs this on Linux

# Frontend
cd web
npm ci
npm run check            # svelte-check (warnings fail) + TypeScript for vite.config.ts
npm test                 # Vitest + Testing Library in jsdom (no backend needed; fetch is mocked)
npm run build
```

Backend tests use temporary SQLite files and need no running server. Scanner
and metadata tests generate tiny silent audio files at run time
(`internal/testaudio`), so no audio fixtures are committed. Permission and
symlink tests are skipped on Windows; run them on Linux.

To try the scanner in development, point it at a **copy** of some music (or a
read-only mount):

```sh
REWIND_LIBRARY_ROOTS="$HOME/music-sample" go run ./cmd/rewind
```

There is no frontend linter yet; `npm run check` is the frontend's static
check.

## Production build

```sh
scripts/build.sh
```

The script:

1. runs `npm ci` and `npm run build` in `web/`;
2. fails if `web/dist/index.html` was not produced;
3. compiles `bin/rewind` (`bin/rewind.exe` on Windows) with `CGO_ENABLED=0`,
   `-trimpath`, and the `rewind_embed` build tag, which embeds `web/dist` in
   the binary.

Cross-compile by setting `GOOS`/`GOARCH`, for example
`GOOS=linux GOARCH=amd64 scripts/build.sh`. Set `VERSION` to override the
version string, which defaults to `git describe`.

Equivalent manual steps (for example in PowerShell):

```powershell
cd web; npm ci; npm run build; cd ..
$env:CGO_ENABLED = "0"
go build -tags rewind_embed -trimpath -ldflags "-s -w -X main.version=dev" -o bin/rewind.exe ./cmd/rewind
```

Building with `-tags rewind_embed` while `web/dist` is missing fails at compile
time (`pattern all:dist: no matching files found`). A production binary cannot
silently ship without the frontend.

## Running the production binary

```sh
./bin/rewind                          # http://127.0.0.1:8090
REWIND_ADDR=127.0.0.1:9000 ./bin/rewind
```

One process serves the frontend and the API:

- `/api/*` routes go to the API. Unknown API paths return a JSON 404 and never
  the frontend HTML.
- Files in the build (for example `/assets/index-<hash>.js`) are served with
  explicit content types. Hashed assets are cached as immutable.
- Other extension-less paths return `index.html` for client-side navigation.
  Missing files with an extension return 404.

Stop the server with Ctrl+C or SIGTERM. In-flight requests get up to 10 seconds
to finish.

## Development vs production

| | Development | Production |
| --- | --- | --- |
| Frontend served by | Vite dev server (`:5173`, hot reload) | Go binary (embedded `web/dist`) |
| Go server at `/` | placeholder page | real frontend |
| Build tag | none | `rewind_embed` |
| Processes | two | one |
| Node.js at runtime | yes | no |

## Project layout

```
cmd/rewind/          entry point: config, logging, database, setup code, server, signals
internal/config/     environment-variable configuration and validation
internal/db/         SQLite open/close, connection settings, migration runner
  migrations/        numbered .sql files (never edit one that has been released)
internal/auth/       password hashing, sessions, setup code, rate limiter
internal/metadata/   read-only tag reader (wraps go-taglib)
internal/library/    library roots, the scanner, the catalog and playlists
internal/prefs/      per-user preferences (allowlist, defaults, storage)
internal/plugin/     plugin API, manifest validation, discovery and lifecycle
plugins/             compiled-in plugins (missingtracks: the example)
internal/testaudio/  test-only audio file generator
internal/server/     routes, auth handlers, frontend handler, middleware, lifecycle
web/                 Svelte + TypeScript + Vite frontend (npm project)
  embed_*.go         Go package that embeds the placeholder or web/dist
  placeholder/       page served by non-production builds
  src/app.css        design tokens (CSS custom properties) and base styles
  src/App.svelte     auth state machine: loading, setup, sign-in, signed-in shell
  src/components/    Shell (navigation, routes, player, preferences, themes,
                     playlist playback), LibraryView, AddToPlaylist,
                     PlaylistsView, PlaylistDetail, SettingsView,
                     ThemeSettings, ThemeEditor, ThemePreview, StatusView,
                     Player, SetupForm, LoginForm, ServerStatus
  src/services/      API client, client-side validation, library sort choices,
                     playlist playback order, themes (see docs/themes.md);
                     theme-tokens.json mirrors the server's token contract
  src/**/*.test.ts   frontend tests
scripts/build.sh     production build
```

## Frontend structure

| Route | View | API used |
| --- | --- | --- |
| `#/library` (default) | `LibraryView`: tracks, search, sort, paging; `+` opens `AddToPlaylist` | `GET /api/tracks`, `GET /api/playlists`, `POST /api/playlists/{id}/tracks` |
| `#/playlists` | `PlaylistsView`: list, create | `GET /api/playlists`, `POST /api/playlists` |
| `#/playlists/{id}` | `PlaylistDetail`: play, rename, delete, reorder, remove | `GET`, `PATCH`, `DELETE /api/playlists/{id}`, `PUT /api/playlists/{id}/tracks`, `DELETE /api/playlists/{id}/tracks/{trackId}` |
| `#/settings` | `SettingsView`: `ThemeSettings` (theme cards, preview, apply, enable/disable) and `ThemeEditor` (per-theme customization with `ThemePreview`), library and playback preferences, `PluginStatus` (read-only) | `GET /api/themes` (loaded by `Shell`), `PUT /api/themes/active`, `PUT /api/themes/{id}/enabled`, `PUT /api/themes/{id}/customization`, `PATCH /api/preferences` (loaded by `Shell` with `GET /api/preferences`), `GET /api/plugins` |
| `#/status` | `StatusView`: overview, music folders, rescan | `GET /api/library`, `GET /api/library/roots`, `POST /api/library/scan` |
| (always, once a track is chosen) | `Player`: the floating mini player, with previous/next while a playlist plays. Clicking or pulling up its track info opens the Now Playing screen, a modal view of the same `<audio>` element and state (Escape or the collapse button closes it; the rest of the page is `inert` meanwhile) | `GET /api/tracks/{id}/stream` (via `<audio>`), `HEAD` on errors |

- Routing uses the URL hash, so the Go server's `index.html` fallback is not
  needed for these routes and reloading a route works in development and
  production.
- All API calls go through `src/services/api.ts` (typed responses, one error
  type). Components never call `fetch` directly, except the media element
  streaming audio.
- Before sign-in, `App` shows the setup or sign-in forms; the shell appears
  only once the server confirms a session.

To work on the UI with real data, run the backend with a music folder (see
[library.md](library.md)) and the Vite dev server as described above. Sign in
at http://localhost:5173. Frontend tests (`npm test`) mock the network with
`src/test/fetch.ts` and the media element with small stubs, because jsdom has
no audio engine; they never touch a real library.

### Testing playlists locally

Automated tests use temporary databases and generated audio:

```sh
go test ./internal/library -run 'Playlist|MergeRename'   # store: order, limits, rollback, unplugged drives
go test ./internal/prefs                                  # preference validation and defaults
go test ./internal/server -run 'Playlist|Preferences'    # HTTP: auth, CSRF, ownership, validation
cd web && npx vitest run src/components/Playlist src/components/SettingsView.test.ts
```

By hand: run the backend with a small test folder in
`REWIND_LIBRARY_ROOTS` and sign in. Create a playlist, add tracks with `+` in
the Library, reorder them, and press Play; the bar shows "Name · n of N"
with previous/next. To see unavailable tracks, rename the test folder, press
**Rescan library** on Status, and open the playlist again: the entries stay,
marked unavailable. Rename the folder back and rescan to restore them.
[api.md](api.md#trying-the-api-with-curl) has curl examples for the API.

## PWA and service worker

- `web/public/manifest.webmanifest`, `offline.html`, `offline.css` and the
  icons in `web/public/icons/` are copied into the build as they are.
- The service worker source is `web/sw/service-worker.js`. In production
  builds `web/sw/plugin.ts` writes `dist/sw.js`, filling in the list of
  files to cache and a version hash. The development server has no
  service worker, and `src/main.ts` registers it only in production builds.
- To try it, build (`sh scripts/build.sh`), run the binary, and open
  `http://localhost:8090` (service workers need HTTPS or localhost). In
  Chromium DevTools, **Application → Service workers** and **Cache
  storage** show the worker and the single `rewind-shell-<version>` cache.
  **Unregister** there, or clear site data, to start fresh.
- Tests: `web/sw/service-worker.test.ts` runs the generated worker against a
  simulated cache and network (what is cached, what is never intercepted,
  offline fallback, old-cache cleanup). `web/sw/pwa-files.test.ts` checks the
  manifest, icon sizes and the offline page.
- Icons are rendered from `web/public/favicon.svg` with
  `node scripts/render-icons.mjs` (needs a local Chrome, Chromium or Edge).
  Re-run it only when the artwork changes, and commit the PNGs.

## CI, releases and deployment

GitHub Actions runs `.github/workflows/ci.yml` on pull requests and pushes to
`main`. Releases are built from `v*` tags by `release.yml`, and `deploy.yml`
deploys manually over SSH. Shared steps live in `scripts/`:

| Script | Purpose |
| --- | --- |
| `build.sh` | production build: frontend, then CGO-free Go binary with it embedded |
| `smoke-test.sh` | starts a built binary on loopback and checks health, embedded frontend, assets, and clean shutdown |
| `third-party.sh` | license gate: fails on unreviewed or license-less dependencies; collects notices |
| `lgpl-sources.sh` | LGPL corresponding-source bundle (go-taglib, TagLib, utfcpp) |
| `package-release.sh` | release archives and `SHA256SUMS` (run on Linux) |
| `install.sh` | published installer: install, upgrade, rollback and status on a systemd server ([install.md](install.md)) |
| `install-test.sh` | end-to-end test of `install.sh` as root on a disposable systemd machine (CI) |
| `release-check.sh` | all pre-release checks in one run, on Linux ([deployment.md](deployment.md#release-validation)) |
| `deploy-remote.sh` | server-side half of `deploy.yml` (streamed over SSH, never installed) |

Invoke them with `sh scripts/<name>.sh`. See [deployment.md](deployment.md).

## Conventions

- Formatting: `gofmt` for Go. No frontend formatter is configured yet.
- New source files start with `// SPDX-License-Identifier: Apache-2.0`
  (Go and shell).
- Tests must not need network access or external services.
- Never commit `.env`, databases, `data/`, `bin/`, `web/dist/` or
  `web/node_modules/`.
- Every new dependency needs a stated reason in
  [architecture.md §4](architecture.md).
