# Operating Rewind

These instructions assume the layout created by `install.sh`
([install.md](install.md)):

| Path | Contents |
| --- | --- |
| `/opt/rewind/releases/<version>/` | installed versions, kept until you remove them |
| `/opt/rewind/current` | symlink to the active release |
| `/etc/rewind/rewind.env` | configuration ([configuration.md](configuration.md)) |
| `/var/lib/rewind/rewind.db` | the database: account, sessions, library index, playlists, preferences |
| `/var/backups/rewind/` | database copies made by `install.sh` |

Releases, configuration and data are kept separate. Replacing or removing a
release never touches the database, the configuration or the music.

## Service

```sh
sudo systemctl status rewind      # state, recent log lines
sudo systemctl stop rewind
sudo systemctl start rewind
sudo systemctl restart rewind     # after changing /etc/rewind/rewind.env
sudo systemctl disable rewind     # do not start at boot
sudo sh install.sh status         # active release, installed releases, health
```

Rewind stops gracefully on SIGTERM. In-flight requests get up to 10 seconds,
a running scan is stopped and recorded as `interrupted`, and the database is
closed. The next start continues normally. With the default `Restart=on-failure`, systemd
restarts Rewind 5 seconds after a crash. After 5 failed starts within 2
minutes, it gives up and leaves the unit `failed`. That usually means a
configuration error. After fixing it, start Rewind again with
`sudo systemctl reset-failed rewind && sudo systemctl start rewind`.

Change the unit with `sudo systemctl edit rewind` (a drop-in in
`/etc/systemd/system/rewind.service.d/`). `install.sh` never overwrites
`rewind.service`. When a new release ships a changed unit, the upgrade
prints a `diff` command, so you can compare and adopt the changes yourself.

## Logs and health

```sh
journalctl -u rewind -f                 # follow
journalctl -u rewind -n 100 --no-pager  # last 100 lines
journalctl -u rewind -b                 # since boot
curl -fsS http://127.0.0.1:8090/api/health
# {"status":"ok","name":"rewind","version":"v0.1.0"}
```

`/api/health` needs no sign-in and shows only the status, name and version.
Library and scan state are on the **Status** page, or in `GET /api/library`
and `GET /api/library/roots` with a session ([api.md](api.md)). Logs never
contain passwords, session tokens or the setup code. They do contain music
folder names and the names of unreadable files, so share them with care.

## When Rewind does not start

Run `journalctl -u rewind -n 50` first. Rewind writes one clear error and
exits.

| Log message | Cause and fix |
| --- | --- |
| `invalid configuration:` followed by settings | Fix the named settings in `/etc/rewind/rewind.env` ([configuration.md](configuration.md)). |
| `cannot listen on ...` | The port is in use, or a LAN/Tailscale address is not assigned yet. |
| `refusing to serve on ...` | A non-loopback address without `REWIND_REMOTE_ACCESS=true`. |
| `permission denied` for the data directory | `/var/lib/rewind` must be owned by `rewind`. A data directory elsewhere also needs `ReadWritePaths=` in a drop-in. |
| `database has migration N (...) that this version of Rewind does not know` | The database was migrated by a newer Rewind. Start that newer release again, or restore a backup from before the upgrade ([Rollback](#rollback)). |
| `cannot open database ...` or `file is not a database` | The file is damaged, unreadable for `rewind`, or not a database. Check the owner and mode (rewind, 0600), or restore a backup. Rewind never deletes or recreates a database itself. |
| `status=203/EXEC` in `systemctl status` | `/opt/rewind/current/rewind` is missing or not executable. Check the symlink with `ls -l /opt/rewind/current`. |
| `library root not used` (Rewind still starts) | A newly listed music folder does not exist, is unreadable, or overlaps another one; the `reason` says which. Mount it or fix the path, then restart. |
| `library root unavailable; its tracks are kept` | A known folder is not mounted or not readable. Mount it, then rescan. |

## Backup

The database is the only thing to back up. It does not contain music, and
it is small. **Treat backups like passwords:** they contain the password
hash and session hashes.

**While Rewind runs** (online backup, consistent). This needs the `sqlite3`
tool (`sudo apt install sqlite3`):

```sh
sudo install -d -m 0700 /var/backups/rewind
sudo sh -c "umask 077 && sqlite3 /var/lib/rewind/rewind.db \".backup '/var/backups/rewind/rewind-\$(date +%F).db'\""
```

**Without sqlite3,** stop Rewind first, then copy the file:

```sh
sudo systemctl stop rewind
sudo ls /var/lib/rewind/            # there must be no rewind.db-journal
sudo install -m 0600 /var/lib/rewind/rewind.db /var/backups/rewind/rewind-$(date +%F).db
sudo systemctl start rewind
```

If a `rewind.db-journal` file exists after a stop, a write was interrupted.
Start and stop Rewind once, so SQLite rolls it back, then copy. Do not copy a
running database with `cp`: the copy can be inconsistent. Rewind uses
SQLite's rollback journal (not WAL), so a stopped database is exactly one
file.

`install.sh upgrade` and `rollback` stop the service, copy the database to
`/var/backups/rewind/rewind-<UTC time>-before-<version>.db` and verify the
copy before switching. Old backups are never deleted. Remove them yourself.
Copy backups to another disk as well: a copy on the same disk does not
survive a disk failure.

Also keep a copy of `/etc/rewind/rewind.env` if you changed it. It contains
no secrets.

## Restore

1. Stop Rewind: `sudo systemctl stop rewind`.
2. Put the backup in place, owned by `rewind` with mode 0600, and remove any
   journal of the old file. A journal must never be paired with another
   database:

   ```sh
   sudo install -m 0600 -o rewind -g rewind /var/backups/rewind/BACKUP.db /var/lib/rewind/rewind.db
   sudo rm -f /var/lib/rewind/rewind.db-journal
   ```

3. Start Rewind: `sudo systemctl start rewind`. If the backup is older than
   the running release, Rewind applies the newer migrations to it. A backup
   from a **newer** release is refused. Run that release instead.

Sessions that existed when the backup was taken become valid again. To end
them, change the password after the restore. Playlists refer to tracks by
their IDs in the same database, so always restore the whole file.

Neither `install.sh` nor the deploy workflow restores a database
automatically.

## Upgrading

1. Read the release notes and `CHANGELOG.md` for the new version.
2. Download and verify the new archive, `install.sh` and `SHA256SUMS`, as in
   [install.md, step 1](install.md#1-download-and-verify).
3. Upgrade:

   ```sh
   sudo sh install.sh upgrade rewind-$VERSION-linux-amd64.tar.gz
   ```

The installer verifies the checksum, then installs the release to
`/opt/rewind/releases/<version>/` and test-starts it on a spare port with an
empty temporary data directory. Only if that passes, it:

1. stops Rewind and backs up the database;
2. points `/opt/rewind/current` at the new release, starts Rewind, and waits
   up to 30 seconds for `/api/health` to report the new version;
3. if the new release does not become healthy, points `current` back at the
   previous release and restarts it. It does **not** restore the database.
   If the new release already migrated the database, the previous release
   refuses it, and the installer prints the restore commands. Run them
   yourself.

The configuration, data and unit are not changed, and old releases are
kept. Database migrations run automatically at the first start of a newer
release.

## Rollback

```sh
sudo sh install.sh status                   # lists installed releases
sudo sh install.sh rollback v0.1.0          # the release directory name
```

The rollback also stops Rewind, backs up the database, switches and
health-checks. **A newer release may have migrated the database**, and
migrations are not reversible. An older release then refuses to start with
a "newer version" error. The installer switches back, and you choose:

- stay on the newer release, or
- restore the backup made **before the upgrade** (see [Restore](#restore)),
  then roll back. Changes made since that backup (new playlists, password
  changes) are lost.

By hand, without the installer:

```sh
sudo systemctl stop rewind
sudo ln -sfn releases/v0.1.0 /opt/rewind/.current-new
sudo mv -T /opt/rewind/.current-new /opt/rewind/current
sudo systemctl start rewind
curl -fsS http://127.0.0.1:8090/api/health
```

Remove old releases you no longer need with
`sudo rm -rf /opt/rewind/releases/<version>`, but never the one `current`
points to.

## Disconnected music drives

Unplugging a drive never deletes library records or playlists. Its tracks
are shown as unavailable and fail to play until the drive returns. After
plugging it in and mounting it at the **same path**, press **Rescan
library**. A different mount path counts as a new library folder: update
`REWIND_LIBRARY_ROOTS`. Details are in
[library.md](library.md#disconnected-drives-and-missing-files).

## Forgotten password, lost setup code

- **Setup code:** while no account exists, every start writes a new code.
  Run `sudo systemctl restart rewind`, then `sudo cat /var/lib/rewind/setup-code`.
- **Password:** follow [Password recovery](architecture.md#password-recovery).
  It replaces the password hash and keeps playlists and preferences. Never
  delete the user row, because that also deletes the user's playlists.

## Moving to another server

Install the same release on the new server. Stop Rewind on both, then copy
`rewind.db` (mode 0600, owner `rewind`) and `rewind.env`. Mount the music at
the **same paths**, so tracks and playlists keep their IDs. Then start
Rewind.
