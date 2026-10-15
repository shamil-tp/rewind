# Rewind

Rewind is a lightweight, self-hosted music server for music you own. Plug a
USB drive full of music into your home server and point Rewind at it. You can
then listen in a browser on the server, on your home network, or over your
private Tailscale network. It needs no accounts with anyone, no cloud
service and no telemetry.

> **Status: pre-release.** No version has been published yet. The features
> below are implemented and tested. The first release is being prepared;
> see [CHANGELOG.md](CHANGELOG.md).

## Features

- **Your files stay where they are.** Rewind indexes MP3, FLAC, M4A (AAC or
  ALAC), Ogg and WAV files from one or more folders, such as a mounted
  pendrive. It reads tags with TagLib and never writes to the music.
  Rescans are incremental. Unplugging the drive keeps the library and
  playlists.
- **Web player:** browse, search and sort the library, and stream with
  seeking (HTTP Range), from a floating mini player that opens a full Now
  Playing screen. The browser plays the original files; nothing is
  transcoded.
- **Listening:** one persistent player across every page with a play queue
  (play now, play next, add to queue, drag to reorder), shuffle, repeat,
  volume and speed; the queue is restored after a refresh. Album and artist
  pages, artwork from your files, favorites, listening history with play
  counts, and keyboard shortcuts. Works on phones, tablets and desktops.
- **Playlists:** create, edit, reorder (drag and drop or buttons),
  duplicate, choose artwork, play or shuffle, add to the queue.
- **Metadata editor:** fix titles, artists, albums, numbers, years, genres,
  composers and lyrics. Edits are stored in Rewind's database; your music
  files are never modified.
- **Themes and style:** Rewind Dark (rounded neo-brutalist, the default),
  Rewind Light and Rewind OLED. Customize colors, fonts, borders, corner
  radii, shadows and spacing per theme with a live preview and contrast
  checks. Administrators can add declarative (data-only) themes.
- **Installable web app (PWA)** over HTTPS or on localhost. Only the
  interface is cached: music, library data and sign-in are never stored on
  the device.
- **Private by default:** listens on `127.0.0.1` only, with one owner
  account and no default password. LAN and Tailscale access must be turned
  on explicitly.
- **Small:** one Go binary with the web interface built in, and one SQLite
  file. No Node.js, database server or Docker on the server.
- **Optional plugins**, compiled in, which the administrator must enable
  ([docs/plugins.md](docs/plugins.md)).

**Not available yet:** artist and album pages, a separate play queue,
lock-screen media controls, artwork display, offline downloads, more than
one account, and arm64 builds.

## Supported platforms

- **Server:** Linux x86-64 (amd64) with systemd. The intended range is
  Ubuntu Server 24.04 and 26.04 LTS, and Debian 12 and 13. The installer has
  been tested on Ubuntu 26.04. Other platforms are for development only.
  Details: [docs/install.md](docs/install.md#supported-platforms).
- **Browsers:** Chrome and Edge 111+, Firefox 114+, Safari 16.4+ (iOS
  16.4+). Codec support is the browser's: MP3, AAC, FLAC and WAV are widely
  supported, while Ogg Vorbis and ALAC are not supported everywhere.

## Quick start

On an Ubuntu Server (24.04 or 26.04 LTS, x86-64), download the installer,
verify it, and run the wizard:

```sh
VERSION=vX.Y.Z    # a release from the releases page
base="https://github.com/shamil-tp/rewind/releases/download/$VERSION"
curl -fLO "$base/install.sh"
curl -fLO "$base/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing      # must say OK
sudo bash install.sh
```

The wizard is published with releases after v1.2.0. The `install.sh` of
v1.2.0 and earlier has only the scripted commands (see below).

The wizard checks the server, downloads and verifies the release, asks who
may connect (this server only, Tailscale, or your LAN) and where your music
is, shows a summary, and installs nothing before you confirm. It creates a
`rewind` service user, starts Rewind at boot, and checks its health.

Then:

1. **Create the owner account.** Setup only works from the server itself.
   From your computer, open a tunnel (`ssh -L 8090:127.0.0.1:8090
   you@server`), browse to http://localhost:8090, and enter the code from
   `sudo cat /var/lib/rewind/setup-code`.
2. **Later changes:** `sudo rewind-ctl` shows the status and offers repair,
   update, settings (music folders, network access) and uninstall.
3. **Optional: use it from other devices** with `tailscale serve` (HTTPS,
   recommended) or on your LAN. See
   [Network access](docs/deployment.md#network-access).

The full guide, including the scripted installation
(`sudo sh install.sh install ARCHIVE`) and a manual one without the script,
is [docs/install.md](docs/install.md).

## Security defaults

- Reachable only from the server until you configure LAN or Tailscale
  access. Rewind never opens firewall ports and is never exposed to the
  internet.
- Every device signs in. Being on the same network grants nothing.
- Passwords are stored as Argon2id hashes, sessions are server-side, and
  cookies are `HttpOnly` and `SameSite=Strict`. Sign-in is rate-limited.
- Files are served only through the database record of an indexed track,
  only from inside its configured folder, and never through symbolic links.
- The service runs as an unprivileged user, with the file system read-only
  except for its data directory.

Plain HTTP on a LAN sends the password unencrypted; prefer HTTPS through
`tailscale serve`. See [SECURITY.md](SECURITY.md) for the security model
and how to report vulnerabilities.

## Operating it

- **Logs and status:** `journalctl -u rewind`, `sudo rewind-ctl --status`.
- **Settings, repair, uninstall:** `sudo rewind-ctl --reconfigure`,
  `--repair`, `--uninstall`. Uninstalling keeps your database unless you
  choose a purge and type `PURGE`; it never touches your music.
- **Backup:** the database `/var/lib/rewind/rewind.db` is the only state.
  Back it up with `sqlite3 .backup`, or by copying it while Rewind is
  stopped. Music is not part of the backup.
- **Upgrade:** `sudo rewind-ctl --update` (or
  `sudo sh install.sh upgrade <archive>`) verifies the archive, test-starts
  it, backs up the database, switches, and health-checks.
- **Rollback:** `sudo sh install.sh rollback <version>`. A newer release
  may have migrated the database; then restore the backup made before the
  upgrade.

Everything is described in [docs/operations.md](docs/operations.md),
including troubleshooting, restoring a backup and password recovery.

## Documentation

| Document | Contents |
| --- | --- |
| [install.md](docs/install.md) | installation wizard, first run, USB music and permissions, rewind-ctl, uninstalling |
| [configuration.md](docs/configuration.md) | every setting |
| [operations.md](docs/operations.md) | service, logs, backup and restore, upgrade and rollback, troubleshooting |
| [library.md](docs/library.md) | music folders, mount paths, permissions, scanning, disconnected drives |
| [deployment.md](docs/deployment.md) | network access (LAN, Tailscale, PWA), CI, releases, SSH deployment |
| [api.md](docs/api.md) | HTTP API |
| [themes.md](docs/themes.md), [plugins.md](docs/plugins.md) | themes and plugins |
| [architecture.md](docs/architecture.md) | design, data model, security model, decisions |
| [security-audit.md](docs/security-audit.md) | the latest security and reliability audit |
| [development.md](docs/development.md) | building from source, development mode, tests |

## Building from source

You need Go (the version in `go.mod`), and Node.js 24 with npm for the web
interface:

```sh
sh scripts/build.sh && ./bin/rewind        # production binary on http://127.0.0.1:8090
go run ./cmd/rewind                        # development backend
cd web && npm ci && npm run dev            # development frontend on http://localhost:5173
```

See [docs/development.md](docs/development.md).

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).
Rewind deliberately stays small: please check the project principles before
proposing large features.

## License

Rewind is licensed under the [Apache License 2.0](LICENSE).

Release binaries also contain third-party code under its own licenses,
listed in `THIRD_PARTY_NOTICES.md` and `LICENSES/` inside each release
archive. This includes `go.senan.xyz/taglib` and TagLib under the GNU LGPL
2.1. Their complete corresponding source is published with every release
(`rewind-<version>-lgpl-sources.tar.gz`). The license review is in
[docs/architecture.md §4](docs/architecture.md#4-dependencies-and-license-review).
