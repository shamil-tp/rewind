# Installing Rewind on a Linux server

This guide installs a prebuilt Rewind release as a systemd service. The
server needs **no Node.js, Go, database server or Docker**: a release is one
self-contained binary with the web interface built in.

- [Supported platforms](#supported-platforms)
- [1. Download and verify](#1-download-and-verify)
- [2. Install](#2-install)
- [3. Create the owner account](#3-create-the-owner-account)
- [4. Add your music](#4-add-your-music)
- [5. Use Rewind from other devices](#5-use-rewind-from-other-devices)
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
| Ubuntu Server 24.04 LTS and 26.04 LTS, Debian 12 and 13 | intended range |
| Linux arm64 (for example Raspberry Pi), 32-bit systems | **not supported yet**: no arm64 build is published or tested |
| Windows and macOS | development only; no release builds |
| Docker or other containers | no official image |

What has actually been tested is listed in the
[pre-release record](deployment.md#release-validation). In short:
`scripts/install-test.sh`, which installs, upgrades and rolls back a real
systemd service, passed on Ubuntu 26.04 (WSL2, systemd 259). The other
distributions above are expected to work, because the binary is static and
the unit uses systemd options available since systemd 245, but they have
not been tested yet. CI runs the same test on Ubuntu 24.04 once the
workflows run on GitHub.

**Browsers.** The interface is built for current browsers: Chrome and Edge
111 or newer, Firefox 114 or newer, and Safari 16.4 or newer (iOS and iPadOS
16.4 or newer). Whether a file plays depends on the browser's own codec
support. MP3, AAC (`.m4a`), FLAC and WAV are widely supported. Ogg Vorbis
and ALAC (`.m4a`) are not supported in every browser, Safari in particular.
Rewind does not transcode. The player shows an error for a file the browser
cannot play. Browser compatibility has not been tested systematically.

## Requirements

- Root access (`sudo`).
- `curl`, `tar`, `gzip` and `sha256sum`. They are installed by default on
  Ubuntu Server and Debian.
- Your music on a drive or folder the server can mount and read. Rewind
  never mounts, formats or writes to it.
- About 16 MB of disk for each installed release (the binary is about
  15 MB). The database is small: about 0.6 MB for 3,000 tracks in a test.

## 1. Download and verify

Pick a version from the
[releases page](https://github.com/shamil-tp/rewind/releases). Download the
Linux archive, the installer and the checksum file into an empty directory:

```sh
VERSION=v0.1.0      # the release you want
mkdir rewind-download && cd rewind-download
base="https://github.com/shamil-tp/rewind/releases/download/$VERSION"
curl -fLO "$base/rewind-$VERSION-linux-amd64.tar.gz"
curl -fLO "$base/install.sh"
curl -fLO "$base/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
```

Both lines must end in `OK`. If one says `FAILED`, delete the files and
download them again. Do not install them.

`SHA256SUMS` comes from the same release page, so it detects damaged
downloads, not a compromised GitHub account. Releases are not signed yet.
Read `install.sh` before running it as root. It is a short shell script.

## 2. Install

```sh
sudo sh install.sh install rewind-$VERSION-linux-amd64.tar.gz
```

The installer:

1. checks the archive against `SHA256SUMS` again, and extracts nothing
   before that check passes;
2. creates the system user `rewind`. It has no password and no login shell;
3. creates these directories:

   | Path | Contents | Owner, mode |
   | --- | --- | --- |
   | `/opt/rewind/releases/<version>/` | the binary, license files and docs of each installed version | root, 0755 |
   | `/opt/rewind/current` | symlink to the active release | root |
   | `/etc/rewind/rewind.env` | configuration (never overwritten later) | root:rewind, 0640 |
   | `/var/lib/rewind/` | database and the one-time setup code (never touched by the installer) | rewind, 0700 |
   | `/var/backups/rewind/` | database copies made before upgrades and rollbacks | root, 0700 |
   | `/etc/systemd/system/rewind.service` | the systemd unit (never overwritten later) | root, 0644 |

4. **test-starts the new binary** as the `rewind` user, on a spare loopback
   port (18090–18099) with an empty temporary data directory. It checks
   that the binary reports the expected version and serves the web
   interface, and stops it again;
5. activates the release, then enables and starts the service, and checks
   `http://127.0.0.1:8090/api/health`.

It does not open firewall ports, change network settings or install
packages. Rewind starts **reachable from this server only**
(`127.0.0.1:8090`).

Check it:

```sh
sudo sh install.sh status
systemctl status rewind
journalctl -u rewind -n 30
```

## 3. Create the owner account

Rewind has one account, the owner, and no default password. While no
account exists, Rewind writes a one-time setup code to
`/var/lib/rewind/setup-code`:

```sh
sudo cat /var/lib/rewind/setup-code
```

For security, the setup page accepts requests **only from the server
itself**, through a loopback address. On a headless server, use an SSH
tunnel from your computer:

```sh
ssh -L 8090:127.0.0.1:8090 you@your-server
```

Leave that session open, then open **http://localhost:8090** in your
computer's browser. Enter the setup code, choose a username and a password
of at least 12 characters, and select **Create account**. The code file is
deleted once the account exists.

- **Lost the code, or it is refused?** Restart Rewind
  (`sudo systemctl restart rewind`). Every start without an account writes
  a new code.
- **Forgot the password later?** See
  [Password recovery](architecture.md#password-recovery). It keeps your
  playlists.

## 4. Add your music

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

   Use `vfat` for FAT32. For ext4 or other Linux filesystems, the `rewind`
   user needs read access to the files (`r`) and folders (`r-x`). Details,
   and why Rewind never mounts drives itself, are in
   [library.md](library.md#find-your-pendrives-mount-path).
2. Add the folder to `/etc/rewind/rewind.env`. Separate several folders with
   `:`:

   ```sh
   REWIND_LIBRARY_ROOTS=/srv/usb-music
   ```

3. Restart: `sudo systemctl restart rewind`. Rewind scans in the
   background. The **Status** page shows the progress and has a **Rescan
   library** button for later changes.

Rewind indexes MP3, FLAC, M4A (AAC or ALAC), Ogg (`.ogg`, `.oga`) and WAV
files, using their tags. Files without tags are listed by file name. The
service can read the music but never write to it: `rewind.service` makes
the whole file system read-only for Rewind, except `/var/lib/rewind`.

**Drive unplugged?** The library and playlists are kept, and the tracks are
shown as unavailable. Plug the drive in again, mount it, and press **Rescan
library**. A folder must exist the **first** time it is configured. Plug the
drive in before you add it to `REWIND_LIBRARY_ROOTS`, or restart Rewind
after plugging it in.

**Drive mounted late at boot?** With `nofail`, the system may start Rewind
before the drive is mounted. The startup scan then reports the folder as
unavailable, and a rescan fixes it. On systemd 256 or newer (Ubuntu 26.04,
Debian 13), you can make Rewind wait for the mount without depending on it:

```sh
sudo systemctl edit rewind
# add:
#   [Unit]
#   WantsMountsFor=/srv/usb-music
```

## 5. Use Rewind from other devices

Rewind stays reachable from the server only until you choose one of the
layouts in [deployment.md, "Network access"](deployment.md#network-access):

- **Tailscale with HTTPS (recommended).** `tailscale serve` proxies your
  tailnet name to Rewind, which stays on loopback. The app can be installed
  (PWA) on phones and computers in your tailnet.
- **Tailscale IP or home network (LAN).** Rewind also listens on that
  address over plain HTTP. On a LAN, your password crosses the network
  unencrypted, and the app cannot be installed.

Every device still signs in with your password. Rewind never opens firewall
ports or exposes itself to the internet.

## Installing without the script

These steps do the same as `install.sh`, by hand. Run them in the directory
where you verified the download:

```sh
VERSION=v0.1.0
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
writes only there. Do not run Rewind as root.

## Uninstalling

Nothing below is done automatically. Decide whether you want to keep the
data (your account, playlists and preferences):

```sh
sudo systemctl disable --now rewind
sudo rm /etc/systemd/system/rewind.service
sudo rm -rf /etc/systemd/system/rewind.service.d   # only if you made drop-ins
sudo systemctl daemon-reload
sudo rm -rf /opt/rewind                            # the program
# Optional, permanent: configuration, data, backups and the user
sudo rm -rf /etc/rewind /var/lib/rewind /var/backups/rewind
sudo userdel rewind
```

Your music is never stored by Rewind and is not affected.

## Troubleshooting the installation

| Message or symptom | Cause and fix |
| --- | --- |
| `checksum mismatch` | The download is damaged or is not the published file. Delete it and download it again. |
| `... is not listed in SHA256SUMS` | The archive and `SHA256SUMS` are from different releases. |
| `this release is built for x86-64` | The server is arm64 or 32-bit; no release exists for it yet. |
| `systemd is not running` | For example, a container without systemd. Run the binary yourself, as in [development.md](development.md#running-the-production-binary). |
| `Rewind is already installed` | Use `sudo sh install.sh upgrade ARCHIVE` (see [operations.md](operations.md#upgrading)). |
| `... is owned by ..., not rewind` | An existing `/var/lib/rewind` belongs to another user. Check what it contains before you change its owner. |
| `release ... failed its test start` | The new binary did not start or reported the wrong version. The output is shown, and nothing was changed. |
| `no free loopback port between 18090 and 18099` | Other programs use all of these ports. Free one for the test start. |
| `Rewind did not become healthy` | See `journalctl -u rewind -n 50`. Usually a configuration error, which names the setting. Fix `/etc/rewind/rewind.env`, then `sudo systemctl restart rewind`. |
| `cannot listen on 127.0.0.1:8090` in the log | Another program uses port 8090. Set another port in `REWIND_ADDR`. |
| A drive mounted while Rewind runs is not found by **Rescan library** | Restart Rewind (`sudo systemctl restart rewind`). The service sees later mounts through systemd's mount propagation, which some environments (containers, WSL) do not provide. |
| Browser cannot connect from another computer | Expected: Rewind listens on the server only. Use the SSH tunnel for setup and see [step 5](#5-use-rewind-from-other-devices). |
| `setup is only available from the server itself` | Open Rewind through `http://localhost:8090` on the server or through the SSH tunnel, not through a LAN or Tailscale address. |
