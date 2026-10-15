# Changelog

Notable changes to Rewind. Versions follow [Semantic Versioning](https://semver.org/).
Before 1.0, a minor version (0.x.0) may contain breaking changes, and these
are listed here.

Add new entries under **Unreleased**. When a release is tagged, rename that
heading to the version and date, and start a new, empty **Unreleased**
section.

## Unreleased

The first public release. Nothing has been released yet.

### Added

- **Listening experience:** a single playback engine shared by every page,
  so navigation never interrupts playback; a play queue with play now, play
  next, add to queue, reordering (drag and drop or buttons), removing,
  clearing and saving as a playlist; shuffle, repeat off/all/one, volume,
  mute, speed (0.5–2×), seeking by ±10 s and remaining time; recovery from
  short network drops; the queue and settings are saved on the server and
  restored after a refresh without autoplay. Mini player and Now Playing
  screen with artwork, favorites, lyrics and track information.
- **Albums and artists:** grids with search and sort; album pages with disc
  grouping, missing-details notes and album editing; artist pages with
  albums by year, appearances, all tracks, your top and recent tracks and
  rename.
- **Artwork:** read from embedded pictures or a cover image in the album
  folder, validated, cached as small and large JPEGs in the data directory.
- **Favorites and listening history:** favorite any track; plays count once
  you have listened past a configurable threshold (exactly once per
  playback); recently played tracks, albums and artists; most played; a
  searchable history with date filters, bulk removal and a privacy mode.
- **Metadata editor:** database-only edits of title, artists, album, track
  and disc numbers, year, genre, composer and lyrics, which survive rescans
  and can be reverted to the file's values; album-wide edits and artist
  renames. Music files are never written.
- **Playlists:** duplicate, add many tracks at once, choose artwork, shuffle,
  add to the queue, drag-and-drop reordering.
- **Keyboard shortcuts** (press `?`), a collapsible sidebar on tablets and
  bottom navigation on phones.
- Database migration `0006_listening` (additive). New endpoints under
  `/api/albums`, `/api/artists`, `/api/favorites`, `/api/history`,
  `/api/playback`, `/api/tracks/{id}/artwork`, `/api/tracks/{id}/metadata`.

- **Server:** a single Go binary with the web interface built in, and a
  SQLite database. It is configured with environment variables, and listens
  on loopback only by default.
- **Account:** one owner account, created on first run with a one-time setup
  code that can only be used from the server itself. Passwords are hashed
  with Argon2id, and sessions persist across restarts. Includes sign-in
  rate limiting and cross-origin protection.
- **Music library:** indexes MP3, FLAC, M4A (AAC or ALAC), Ogg and WAV files
  from one or more folders, such as a mounted USB drive. Scans are
  incremental, rescans create no duplicates, and renamed files are detected.
  Library records survive an unplugged drive.
- **Library API and web player:** browse, search and sort tracks, and stream
  audio with HTTP Range support for seeking. No transcoding.
- **Playlists and preferences:** create, rename, reorder and delete
  playlists, and play them in order with previous and next.
- **Themes:** Rewind Dark (the default), Light, OLED Black, and custom
  primary, secondary and accent colors with contrast checks and a live
  preview. A saved accent color keeps its look: an unchosen primary follows
  it.
- **Theme and style system:** themes are declarative, versioned manifests
  (`theme.json`, theme API 1) validated on the server, including WCAG
  contrast. The bundled themes are now named Rewind Light and Rewind OLED;
  their IDs are unchanged. **Settings → Theme and style** lists themes with
  generated previews and compatibility status. It can apply, preview,
  enable and disable them, and opens a per-theme editor for colors,
  typography, borders, corner radii, shadows and spacing, with a live
  preview, Save, Save and apply, Reset and Cancel. Customizations are stored
  per theme in a new `theme_settings` table (migration `0005_themes`). The
  earlier custom colors are kept and still apply to themes that were never
  customized. New endpoints: `GET /api/themes`, `PUT /api/themes/active`,
  `PUT /api/themes/{id}/enabled`, `PUT /api/themes/{id}/customization`.
  The optional `REWIND_THEME_DIR` lets the administrator install more
  themes. There is no upload or download, and themes never contain code.
- **Rounded neo-brutalist design:** 2px borders and shared corner-radius
  tokens for buttons, inputs, panels, the player and artwork.
- **Now Playing screen:** opens from the floating mini player (click, tap or
  pull up), with large artwork placeholder, track details, seeking and the
  same controls, sharing the mini player's audio element and state.
- **Installable web app (PWA)** over HTTPS or on localhost. Only the
  interface is cached; music, account data and API responses never are.
- **Opt-in network access:** HTTPS through `tailscale serve` (recommended),
  or plain HTTP on a Tailscale IP or a LAN address. Only the host names you
  configure are accepted.
- **Plugins:** an optional, trusted, compiled-in plugin system with one
  extension point, and the example plugin `missing-tracks`.
- **Releases:** a Linux amd64 archive with license notices, the LGPL
  corresponding source, `SHA256SUMS`, a systemd unit, and `install.sh` for
  installation, upgrades with a database backup, and rollback.

- **Installation wizard:** `sudo bash install.sh` checks the server (Ubuntu
  24.04 and 26.04 LTS, amd64, systemd, tools, disk space), offers to install
  only missing packages, downloads the latest or a chosen release (or uses a
  local archive) and verifies it against `SHA256SUMS`, then asks for network
  access (this server only, Tailscale HTTPS, Tailscale IP or LAN IP; never
  all interfaces or public addresses), music folders (with access checks as
  the service user and an optional, explicit group membership), waiting for
  music drives at boot, and other settings. It shows a summary before
  changing anything, reports each step, and reports success only after the
  health check passes. The scripted `install`, `upgrade`, `rollback` and
  `status` commands still work, also with `sh`.
- **`rewind-ctl`:** the installer installs itself as
  `/usr/local/sbin/rewind-ctl` for `--status`, `--check`, `--update`,
  `--reconfigure` (configuration backed up first, previous settings restored
  if Rewind becomes unhealthy) and `--repair` (missing parts, modes, checksum
  of the active binary).
- **Uninstaller:** `uninstall.sh` and `rewind-ctl --uninstall` remove the
  application only, the application and configuration, or (after typing
  `PURGE`) also the database and service user; backups only on a separate
  confirmation. Only the fixed installation paths and the files Rewind
  creates are removed, never through symlinks, never anything that existed
  before the installation (recorded in `/etc/rewind/install-manifest`), and
  never music. Release assets now include `uninstall.sh`.

### Security

- The metadata library's cache of compiled code is kept in a private
  temporary directory created at startup, not in the shared `/tmp`
  (security-audit.md, F8).
