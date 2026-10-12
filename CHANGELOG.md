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

### Security

- The metadata library's cache of compiled code is kept in a private
  temporary directory created at startup, not in the shared `/tmp`
  (security-audit.md, F8).
