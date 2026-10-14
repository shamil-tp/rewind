# Installing Rewind on a Linux server

This guide installs a prebuilt Rewind release as a systemd service. The
server needs **no Node.js, Go, database server or Docker**: a release is one
self-contained binary with the web interface built in.

The easiest way is the **installation wizard**: download `install.sh`, run
it with `sudo`, and answer its questions. It downloads and verifies the
release, creates a service user, writes the configuration, starts Rewind at
boot and checks that it is healthy. The same script, installed as
`rewind-ctl`, later repairs, updates, reconfigures and uninstalls Rewind.

- [Supported platforms](#supported-platforms)
- [Requirements](#requirements)
- [1. Download and verify the installer](#1-download-and-verify-the-installer)
- [2. Run the wizard](#2-run-the-wizard)
- [3. Create the owner account](#3-create-the-owner-account)
- [4. Add your music](#4-add-your-music)
- [5. Use Rewind from other devices](#5-use-rewind-from-other-devices)
- [Managing an installation: rewind-ctl](#managing-an-installation-rewind-ctl)
- [Installing without questions (scripted)](#installing-without-questions-scripted)
- [Installing without the script](#installing-without-the-script)
- [Uninstalling](#uninstalling)
- [Troubleshooting the installation](#troubleshooting-the-installation)

Day-to-day operation (logs, backups, upgrades, rollback) is described in
[operations.md](operations.md). Every setting is listed in
[configuration.md](configuration.md).

## Supported platforms

| | Status |
| --- | --- |
| **Linux x86-64 (amd64) with systemd** | the only release target |
| Ubuntu Server 24.04 LTS and 26.04 LTS | supported by the wizard |
| Ubuntu interim releases from 24.10, Debian 12 and 13 | expected to work; the wizard asks before continuing, because they are not tested |
| Other distributions and older Ubuntu releases | refused by the wizard (the scripted `install` command does not check the distribution) |
| Linux arm64 (for example Raspberry Pi), 32-bit systems | **not supported yet**: no arm64 build is published or tested |
| Windows and macOS | development only; no release builds |
| Docker or other containers | no official image |

What has actually been tested is listed in the
[pre-release record](deployment.md#release-validation). In short: the
wizard, `rewind-ctl` and the uninstaller (`scripts/wizard-test.sh`), and
the scripted installer (`scripts/install-test.sh`), passed on Ubuntu 26.04
(WSL2, systemd 259), including real downloads of published releases. CI
runs both tests on Ubuntu 24.04 once the workflows run on GitHub. A real
Ubuntu Server machine, a real USB drive and Debian have not been tested.

**Browsers.** The interface is built for current browsers: Chrome and Edge
111 or newer, Firefox 114 or newer, and Safari 16.4 or newer (iOS and iPadOS
16.4 or newer). Whether a file plays depends on the browser's own codec
support. MP3, AAC (`.m4a`), FLAC and WAV are widely supported. Ogg Vorbis
and ALAC (`.m4a`) are not supported in every browser, Safari in particular.
Rewind does not transcode. The player shows an error for a file the browser
cannot play. Browser compatibility has not been tested systematically.

## Requirements

- Administrator access (`sudo`).
- `bash` and the standard tools of Ubuntu Server: `curl`, `ca-certificates`,
  `tar`, `gzip`, `coreutils`, `util-linux`, `passwd`, `diffutils`,
  `findutils`, `procps`. They are installed by default. If one is missing,
  the wizard lists the packages and offers to install exactly those with
  `apt-get`. It never installs anything else, and never removes packages.
- Your music on a drive or folder the server can mount and read. Rewind
  never mounts, formats or writes to it.
- About 16 MB of disk for each installed release (the binary is about
  15 MB). The database is small: about 0.6 MB for 3,000 tracks in a test.
- Internet access to `github.com` if the wizard downloads the release. A
  release archive copied to the server works without it.

## 1. Download and verify the installer

Pick a version from the
[releases page](https://github.com/shamil-tp/rewind/releases) and download
the installer and the checksum file into an empty directory:

```sh
VERSION=vX.Y.Z      # the release you want
mkdir rewind-install && cd rewind-install
base="https://github.com/shamil-tp/rewind/releases/download/$VERSION"
curl -fLO "$base/install.sh"
curl -fLO "$base/uninstall.sh"     # optional; installed as part of rewind-ctl anyway
curl -fLO "$base/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
```

Every line must end in `OK`. If one says `FAILED`, delete the files and
download them again. Do not run them.

`SHA256SUMS` comes from the same release page, so it detects damaged
downloads, not a compromised GitHub account. Releases are not signed yet.
Read `install.sh` before running it as root.

Do not pipe the installer into a shell (`curl ... | sh`): it is meant to be
verified first, and it installs a copy of itself as `rewind-ctl`.

The wizard, `rewind-ctl` and `uninstall.sh` are published with releases
after v1.2.0. The `install.sh` of v1.2.0 and earlier has only the
[scripted commands](#installing-without-questions-scripted). The new
installer can install those older archives too (`--archive` or `--release`).

## 2. Run the wizard

```sh
sudo bash install.sh
```

Optional first: `sudo bash install.sh --check` runs the same checks without
changing anything.

The wizard changes nothing until you confirm the summary at the end. Press
Ctrl+C at any question to stop. It goes through these steps:

1. **Checks the server:** operating system, CPU architecture, systemd,
   required tools, disk space, and existing Rewind files. It stops, without
   changes, if something is unsupported. Missing packages are listed and
   installed only if you agree.
2. **Release.** The latest stable release, a specific version, or an
   archive already on the server (`rewind-<version>-linux-amd64.tar.gz` and
   its `SHA256SUMS`). Downloads use HTTPS from the GitHub releases page. The
   archive's SHA-256 must match `SHA256SUMS` before anything is extracted,
   and the archive may contain only regular files and directories under its
   own top directory: links, absolute paths and `..` are refused.
3. **Directories** are shown. They are fixed (see the table below), so
   updates, rollback and the deploy workflow always find them.
4. **Network access** (one of):

   | Choice | Settings written | Notes |
   | --- | --- | --- |
   | 1. Only this server (default) | `REWIND_ADDR=127.0.0.1:<port>` | other computers use an SSH tunnel |
   | 2. Tailscale with HTTPS | loopback `REWIND_ADDR`, `REWIND_TRUSTED_HOSTS=<name>.ts.net`, `REWIND_COOKIE_SECURE=true` | you run `tailscale serve` once yourself; the wizard prints the command |
   | 3. Tailscale IP address | `REWIND_ADDR=127.0.0.1:<port>,100.x.y.z:<port>`, `REWIND_REMOTE_ACCESS=true`, optional MagicDNS name in `REWIND_TRUSTED_HOSTS` | plain HTTP inside Tailscale's tunnel |
   | 4. Home network (LAN) IP | `REWIND_ADDR=127.0.0.1:<port>,192.168.x.y:<port>`, `REWIND_REMOTE_ACCESS=true` | plain HTTP: unencrypted on your Wi-Fi |

   The port defaults to 8090 (1024 or higher; a port another program uses
   is refused). Option 3 accepts only Tailscale addresses (100.64.0.0/10),
   option 4 only private addresses (10.x, 172.16–31.x, 192.168.x). The
   wizard never listens on `0.0.0.0` or a public address, never changes
   firewall or router settings, and never installs or configures Tailscale.
   For options 3 and 4 it warns that other devices will see the sign-in
   page and asks again. An existing configuration it cannot express (for
   example `0.0.0.0`) can be kept unchanged.
5. **Music folders**, one absolute path at a time (see
   [4. Add your music](#4-add-your-music)). The wizard checks each folder:
   it must exist (or you confirm adding it anyway), must not overlap another
   folder or Rewind's own directories, and must not contain `:` (the
   separator in `REWIND_LIBRARY_ROOTS`), quotes, backslashes, `$` or
   backticks. It then checks whether the `rewind` user can read it, and if
   not, explains why and what to do. You can also skip music for now.
6. **Drives at boot** (systemd 256 or newer, so Ubuntu 26.04 but not
   24.04): Rewind can wait for the music folders' mounts at boot
   (`WantsMountsFor=`), without depending on them.
7. **Other settings**, optional: log level, scanning at startup, scan
   workers, session lifetime and idle timeout.
8. **Review**: every value that will be written to
   `/etc/rewind/rewind.env`. Nothing has changed until you confirm.

It then installs, with a numbered progress line for each step:

1. creates the system user and group `rewind`. It has no password, no login
   shell (`/usr/sbin/nologin`) and no other groups, unless you chose to add
   it to a group for music access;
2. creates the directories;
3. **test-starts the new binary** as `rewind`, on a spare loopback port
   (18090–18099) with an empty temporary data directory: it must report the
   expected version, serve the web interface and stop cleanly;
4. writes `/etc/rewind/rewind.env` from the release's template and your
   answers (an existing file is kept, changed only where you changed
   something, and backed up first);
5. installs `rewind.service` and, if needed, the installer's drop-in;
6. installs `rewind-ctl`;
7. activates the release, runs `systemd-analyze verify`, enables the
   service at boot and starts it;
8. checks `/api/health`, then whether `rewind` can read each music folder.

It reports success only if the health check passes. At the end it shows the
version, paths, service state, listening address, music folders, how to open
Rewind and create the owner account, and the management commands.

If a step fails, the wizard names it and prints the next diagnostic
commands. Your music, database and backups are never deleted by a failed
run. Run the wizard again: it detects what is installed and offers to
repair it.

### What gets installed

| Path | Contents | Owner, mode |
| --- | --- | --- |
| `/opt/rewind/releases/<version>/` | the binary, license files and docs of each installed version | root, 0755 |
| `/opt/rewind/current` | symlink to the active release | root |
| `/etc/rewind/rewind.env` | configuration (environment variables, no secrets) | root:rewind, 0640 |
| `/etc/rewind/install-manifest` | what the installer created, so that uninstalling removes only that | root, 0644 |
| `/var/lib/rewind/` | database and the one-time setup code; never touched when installing, updating or repairing | rewind, 0700 |
| `/var/backups/rewind/` | database copies made before updates and rollbacks, configuration copies made before changes | root, 0700 |
| `/etc/systemd/system/rewind.service` | the systemd unit (never overwritten later) | root, 0644 |
| `/etc/systemd/system/rewind.service.d/50-installer.conf` | only if needed: waits for the network (LAN or Tailscale IP) and for music drives at boot | root, 0644 |
| `/usr/local/sbin/rewind-ctl` | a copy of `install.sh`, for maintenance | root, 0755 |

The service runs as `rewind`, never as root. `rewind.service` makes the
whole file system read-only for Rewind except `/var/lib/rewind`, and adds
further systemd hardening (no new privileges, private `/tmp` and devices,
no capabilities, restricted address families and namespaces). Logs go to
the systemd journal.

### Wizard settings and defaults

| Question | Default | Written to `rewind.env` |
| --- | --- | --- |
| Network access | only this server | `REWIND_ADDR`, `REWIND_REMOTE_ACCESS`, `REWIND_LISTEN_ALL_INTERFACES` (always `false`), `REWIND_TRUSTED_HOSTS`, `REWIND_COOKIE_SECURE` |
| Port | 8090 | part of `REWIND_ADDR` |
| Music folders | none | `REWIND_LIBRARY_ROOTS`, separated by `:` |
| Log level | `info` | `REWIND_LOG_LEVEL` |
| Scan at startup | yes | `REWIND_SCAN_ON_STARTUP` |
| Scan workers | 2 (1–8) | `REWIND_SCAN_WORKERS` |
| Session lifetime | `720h` (5m to 8760h) | `REWIND_SESSION_LIFETIME` |
| Session idle timeout | `168h` (at least 5m) | `REWIND_SESSION_IDLE_TIMEOUT` |

`REWIND_DATA_DIR=/var/lib/rewind` comes from the release's template and is
not asked: the unit allows writes only there. Plugins
(`REWIND_PLUGIN_DIR`, `REWIND_PLUGINS`) are configured by hand
([plugins.md](plugins.md)). Rewind has no secret settings: the owner
account is created in the browser. Lines the wizard does not manage,
including comments and your own settings, are kept as they are.

## 3. Create the owner account

Rewind has one account, the owner, and no default password. The installer
does not create it and never shows the setup code. While no account exists,
Rewind itself writes a one-time setup code to `/var/lib/rewind/setup-code`
(mode 0600) at every start:

```sh
sudo cat /var/lib/rewind/setup-code
```

For security, the setup page accepts requests **only from the server
itself**, through a loopback address, whatever network access you chose. On
a headless server, use an SSH tunnel from your computer:

```sh
ssh -L 8090:127.0.0.1:8090 you@your-server
```

Leave that session open, then open **http://localhost:8090** in your
computer's browser (use your port if you changed it). Enter the setup code,
choose a username and a password of at least 12 characters, and select
**Create account**. The code file is deleted once the account exists. Sign
out and in again once to check that the password works.

- **Lost the code, or it is refused?** Restart Rewind
  (`sudo systemctl restart rewind`). Every start without an account writes
  a new code.
- **Forgot the password later?** See
  [Password recovery](architecture.md#password-recovery). It keeps your
  playlists.

## 4. Add your music

Music folders are set in the wizard, or later with
`sudo rewind-ctl --reconfigure`. Changing them keeps your library: a folder
removed from the list is disabled, and its tracks and playlist entries are
kept.

1. Plug in the drive and find where the system mounted it. On a headless
   server, mount it yourself, preferably read-only:

   ```sh
   lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINTS
   ```

   For a drive that should be mounted at every boot, add an `/etc/fstab`
   line. For example, for an exFAT pendrive (find the UUID with `lsblk -f`):

   ```
   UUID=XXXX-XXXX  /srv/usb-music  exfat  ro,nofail,uid=rewind,gid=rewind,umask=0222  0  0
   ```

   ```sh
   sudo mkdir -p /srv/usb-music
   sudo mount /srv/usb-music
   ```

   Use `vfat` for FAT32. Details, and why Rewind never mounts drives
   itself, are in [library.md](library.md#find-your-pendrives-mount-path).
2. Add the folder: `sudo rewind-ctl --reconfigure`, keep the network
   settings, and type the path at **Folder**. (By hand: set
   `REWIND_LIBRARY_ROOTS=/srv/usb-music` in `/etc/rewind/rewind.env`,
   separating several folders with `:`, then
   `sudo systemctl restart rewind`.)
3. Rewind scans in the background. The **Status** page shows the progress
   and has a **Rescan library** button for later changes.

### Permissions

The `rewind` user needs to read the music: `r-x` on the folder and every
folder above it, and `r` on the files. The wizard checks this as `rewind`
(before that user exists, as an unprivileged user without groups, which has
the same access). If it cannot read a folder, the wizard names the folder
that blocks it, with its owner and mode, and offers:

1. **Keep it** and fix the permissions yourself. Rewind skips the folder
   until it is readable, then a rescan finds it.
2. **Do not add it.**
3. **Add `rewind` to the folder's group**, only when that group grants the
   access and is an ordinary group (ID 1000 or higher). System and
   administrative groups such as `sudo`, `adm`, `disk`, `docker` or `lxd`
   are never offered. `rewind` can then read everything that group may
   read, not only the music; the wizard says so before you choose it.

It never changes the permissions or ownership of your music, and never runs
Rewind as root to get around them. Typical fixes, all made by you:

- **FAT32, exFAT or NTFS drives** have no Unix permissions; the mount
  options decide. Mount them as in the `fstab` line above
  (`uid=rewind,gid=rewind,umask=0222`, read-only).
- **Drives mounted by a desktop** under `/media/<user>/` are often blocked
  by `/media/<user>` itself (mode 0750 with an access list for that user).
  Let `rewind` pass through that one folder without listing it:
  `sudo setfacl -m u:rewind:--x /media/<user>` (package `acl`). Or mount
  the drive elsewhere through `fstab`.
- **Folders under `/home`** are often `0700` or `0750`. A folder such as
  `/srv/music` avoids this.

**Drive unplugged?** The library and playlists are kept, and the tracks are
shown as unavailable. Plug the drive in again, mount it at the same path,
and press **Rescan library**. A folder must exist the **first** time it is
configured; otherwise Rewind starts using it after a restart once it
exists.

**Drive mounted late at boot?** With `nofail`, the system may start Rewind
before the drive is mounted. On systemd 256 or newer (Ubuntu 26.04), answer
yes to the wizard's "Wait for the music drives at boot" question. It adds
`WantsMountsFor=` to the installer's drop-in (folders whose path contains a
space are left out). Otherwise, a rescan after the drive appears fixes it.

## 5. Use Rewind from other devices

Choose the access in the wizard, or later with
`sudo rewind-ctl --reconfigure`. The layouts, and what each one protects,
are described in [deployment.md, "Network access"](deployment.md#network-access):

- **Tailscale with HTTPS (recommended).** `tailscale serve` proxies your
  tailnet name to Rewind, which stays on loopback. Run
  `sudo tailscale serve --bg http://127.0.0.1:8090` once (never
  `tailscale funnel`, which publishes to the internet). The app can be
  installed (PWA) on phones and computers in your tailnet.
- **Tailscale IP or home network (LAN).** Rewind also listens on that
  address over plain HTTP. On a LAN, your password crosses the network
  unencrypted, and the app cannot be installed. If `ufw` is active, the
  wizard prints an example rule; it never changes the firewall.

Every device still signs in with your password. Local access on the server
(`http://localhost:8090`) keeps working without Tailscale.

## Managing an installation: rewind-ctl

The installer installs itself as `/usr/local/sbin/rewind-ctl`:

| Command | What it does |
| --- | --- |
| `sudo rewind-ctl` | shows the installation and offers: status, repair, update, settings, uninstall |
| `sudo rewind-ctl --status` | active and installed releases, service state, health, addresses, music folders, owner account state |
| `sudo rewind-ctl --check` | the read-only server checks |
| `sudo rewind-ctl --update` | update to the latest or a chosen release, or `--release vX.Y.Z`, or `--archive FILE [--sums FILE]` |
| `sudo rewind-ctl --reconfigure` | change network access, music folders and other settings |
| `sudo rewind-ctl --repair` | restore missing parts, restart, check health |
| `sudo rewind-ctl --uninstall` | the uninstall wizard (below) |

Running `sudo bash install.sh` again does the same: it detects the existing
installation and shows the same menu, so it never creates a second user,
service or configuration.

- **Update** verifies the new release, test-starts it, stops Rewind, **backs
  up the database** to `/var/backups/rewind/`, switches, starts, and checks
  that the new version is healthy. If it is not, it switches back to the
  previous release (see [operations.md](operations.md#upgrading)). The
  configuration and unit are not changed, and old releases are kept. When
  the update is downloaded and that release's `install.sh` is a version of
  this wizard whose checksum matches `SHA256SUMS`, it replaces
  `rewind-ctl`; otherwise `rewind-ctl` stays as it is. Going to an older
  version asks for confirmation; rollback to an installed version is
  `sudo sh install.sh rollback`.
- **Reconfigure** shows your current settings as defaults, backs up
  `rewind.env` to `/var/backups/rewind/rewind.env.<time>-before-change`,
  shows the differences, restarts and checks health. **If Rewind is not
  healthy with the new settings, the previous configuration is restored
  and Rewind restarted.**
- **Repair** never changes configuration values or data. It recreates a
  missing service user, directory, configuration (from the active release's
  template), unit or `rewind-ctl`; resets the modes of `rewind.env` (0640),
  the data directory (0700) and the database file (0600, owner `rewind`),
  never recursively; then enables, restarts and health-checks Rewind. It
  refuses to continue if the active binary no longer matches the checksum
  recorded at installation.

## Installing without questions (scripted)

The original subcommands remain, for scripts and CI. They use default
settings (loopback only, no music folders) and the same checks:

```sh
VERSION=v1.2.0
base="https://github.com/shamil-tp/rewind/releases/download/$VERSION"
curl -fLO "$base/rewind-$VERSION-linux-amd64.tar.gz"
curl -fLO "$base/install.sh"
curl -fLO "$base/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
sudo sh install.sh install rewind-$VERSION-linux-amd64.tar.gz
```

`sh install.sh` runs the script with bash. Then set the music folders with
`sudo rewind-ctl --reconfigure`, and continue with
[step 3](#3-create-the-owner-account). `install`, `upgrade`, `rollback` and
`status` are described in [operations.md](operations.md).

The wizard also accepts its release choice on the command line:
`sudo bash install.sh --release v1.2.0` or
`sudo bash install.sh --archive rewind-v1.2.0-linux-amd64.tar.gz`.

## Installing without the script

These steps do the same as `install.sh install`, by hand. Run them in the
directory where you verified the download:

```sh
VERSION=v1.2.0
tar -xzf rewind-$VERSION-linux-amd64.tar.gz
cd rewind-$VERSION-linux-amd64

sudo useradd --system --user-group --home-dir /var/lib/rewind --no-create-home --shell /usr/sbin/nologin rewind
sudo install -d -o rewind -g rewind -m 0700 /var/lib/rewind
sudo install -d -m 0755 /opt/rewind/releases /etc/rewind
sudo install -d -m 0700 /var/backups/rewind
sudo install -d -m 0755 /opt/rewind/releases/$VERSION
sudo cp -R . /opt/rewind/releases/$VERSION/
sudo chown -R root:root /opt/rewind/releases/$VERSION
sudo ln -sfn releases/$VERSION /opt/rewind/current

sudo install -m 0640 -o root -g rewind rewind.env.example /etc/rewind/rewind.env   # skip if it exists
sudo install -m 0644 rewind.service /etc/systemd/system/rewind.service            # skip if it exists
sudo systemctl daemon-reload
sudo systemctl enable --now rewind
curl -fsS http://127.0.0.1:8090/api/health
```

Then continue with [step 3](#3-create-the-owner-account). Keep
`REWIND_DATA_DIR=/var/lib/rewind` in the configuration: the unit allows
writes only there. Do not run Rewind as root. A manual installation has no
installation record; `rewind-ctl` and the uninstaller still recognize it by
its fixed layout.

## Uninstalling

```sh
sudo rewind-ctl --uninstall
# or, from the downloaded files:
sudo bash uninstall.sh
```

`uninstall.sh` runs the installed `rewind-ctl`, or else the `install.sh` next
to it. The uninstaller first shows what it found, then offers:

| Option | Removes | Keeps |
| --- | --- | --- |
| **1. Application only** (default) | the service (stopped and disabled), `rewind.service`, the installer's drop-in, `/opt/rewind` (all releases), `rewind-ctl` | configuration, database, playlists, preferences, backups, the `rewind` user |
| **2. Application and configuration** | everything in 1, plus `/etc/rewind` (a copy of `rewind.env` is saved to `/var/backups/rewind/` first) | database, playlists, preferences, backups, the `rewind` user |
| **3. Complete purge** (destructive) | everything in 2, plus the database and setup code in `/var/lib/rewind`, the `rewind` user and group, and, only if you say so separately, the backups | anything listed below |

Before removing anything, it lists the exact paths to be removed and kept,
and asks for confirmation. A purge requires typing `PURGE` in capitals; any
other answer cancels it, and Enter alone never confirms it.

What the uninstaller never does, whichever option you choose:

- It never touches your music folders, and lists them as kept.
- It works only on the fixed paths in [What gets installed](#what-gets-installed).
  It never takes a path from the configuration, an environment variable or
  the installation record, and never deletes a directory because of its
  name.
- It never follows symlinks, and skips any of those paths that is a symlink
  or has an unexpected owner.
- It removes only entries it knows: `rewind.db` (and its journal files) and
  `setup-code` in the data directory, backups named `rewind-*.db` and
  `rewind.env.*`, release directories containing a `rewind` binary. Any
  other file is kept and reported, and its directory with it.
- It never removes a directory, file, user or group that existed before the
  installer first ran, according to `/etc/rewind/install-manifest`. Without
  that record (a manual installation, an older `install.sh`, or after option
  2), it removes the `rewind` user only if it looks like the installer's
  system account.
- It keeps the `rewind` user while files it owns remain in `/var/lib/rewind`.
- It never removes your own drop-ins (`sudo systemctl edit rewind`), system
  packages, firewall rules or Tailscale settings.
- A data directory configured outside `/var/lib/rewind` is never deleted;
  it is reported so that you can remove it yourself.

At the end, it reports exactly what was removed and what was kept. To
install again with the kept data, run the wizard: it reuses the
configuration and database, and your account and playlists stay as they
were.

By hand, the same as option 1, then the purge:

```sh
sudo systemctl disable --now rewind
sudo rm /etc/systemd/system/rewind.service
sudo rm -f /etc/systemd/system/rewind.service.d/50-installer.conf
sudo systemctl daemon-reload
sudo rm -rf /opt/rewind                            # the program
sudo rm -f /usr/local/sbin/rewind-ctl
# Optional, permanent: configuration, data, backups and the user
sudo rm -rf /etc/rewind /var/lib/rewind /var/backups/rewind
sudo userdel rewind
```

## Troubleshooting the installation

| Message or symptom | Cause and fix |
| --- | --- |
| `must run as root` | Run `sudo bash install.sh` (or `sudo rewind-ctl`). |
| `[FAIL] Operating system` | The wizard supports Ubuntu 24.04 and 26.04 LTS. On another system, install by hand at your own risk. |
| `[FAIL] CPU architecture` | The server is arm64 or 32-bit; no release exists for it yet. |
| `[FAIL] systemd` / `systemd is not running` | For example, a container without systemd. Run the binary yourself, as in [development.md](development.md#running-the-production-binary). |
| `[FAIL] Data directory ... is owned by ...` | An existing `/var/lib/rewind` belongs to another user. Check what it contains before you change its owner. |
| `missing packages: ...` | Let the wizard install them, or run `sudo apt-get install <packages>` and start again. |
| `cannot reach https://github.com/...` | No internet access. Download the archive and `SHA256SUMS` elsewhere, copy them to the server, and choose option 3 (or `--archive`). |
| `checksum mismatch` | The download is damaged or is not the published file. Delete it and download it again. |
| `... is not listed in SHA256SUMS` | The archive and `SHA256SUMS` are from different releases. |
| `release ... failed its test start` | The new binary did not start or reported the wrong version. The output is shown, and nothing was changed. |
| `no free loopback port between 18090 and 18099` | Other programs use all of these ports. Free one for the test start. |
| `Rewind did not become healthy` | See `sudo journalctl -u rewind -n 50`. Usually a configuration error, which names the setting, or a port or address that cannot be used. Fix it with `sudo rewind-ctl --reconfigure`, or in `/etc/rewind/rewind.env` followed by `sudo systemctl restart rewind`. |
| `the previous settings were restored` | A reconfiguration made Rewind unhealthy (for example an address not assigned to the server), so the wizard put the old configuration back. The journal shows why. |
| `cannot listen on 127.0.0.1:8090` in the log | Another program uses port 8090. Choose another port with `sudo rewind-ctl --reconfigure`. |
| `The 'rewind' user cannot read this folder` | See [Permissions](#permissions). |
| `checksum mismatch` during `--repair` | The installed binary changed after installation. Install a verified release with `sudo rewind-ctl --update`. |
| `refusing to use .../install-manifest` | The installation record is not a root-owned, root-only-writable file. Check who changed it; the installer does not trust it. |
| `Rewind is already installed` | From the scripted `install` command. Run the wizard (`sudo bash install.sh`) for update, repair and settings, or `sudo sh install.sh upgrade ARCHIVE`. |
| A drive mounted while Rewind runs is not found by **Rescan library** | Restart Rewind (`sudo systemctl restart rewind`). The service sees later mounts through systemd's mount propagation, which some environments (containers, WSL) do not provide. |
| Browser cannot connect from another computer | Expected with "only this server". Use the SSH tunnel for setup and see [step 5](#5-use-rewind-from-other-devices). |
| `setup is only available from the server itself` | Open Rewind through `http://localhost:8090` on the server or through the SSH tunnel, not through a LAN or Tailscale address. |
