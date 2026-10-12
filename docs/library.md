# Music library and scanning

Rewind indexes music folders into its database. The indexed library can be
browsed, searched, played and rescanned in the web interface and through
the authenticated API ([api.md](api.md#music-library)). For a server
installation with systemd, start with [install.md](install.md#4-add-your-music).

## Configure a library root

A *library root* is a folder Rewind is allowed to read music from. List one
or more absolute paths in `REWIND_LIBRARY_ROOTS`, separated by the operating
system's path-list separator: `:` on Linux and macOS, `;` on Windows.

```sh
REWIND_LIBRARY_ROOTS=/media/alex/MUSICSTICK
REWIND_LIBRARY_ROOTS=/media/alex/MUSICSTICK:/srv/music
```

At every start, Rewind:

1. Registers each listed folder and gives it a stable ID.
   - **New** roots must exist and be readable at that moment.
   - Roots that were registered before are **kept** while unavailable (for
     example, a pendrive that is not plugged in) and marked `unavailable`.
2. Rejects, with a logged reason:
   - relative paths;
   - missing or unreadable new folders;
   - files;
   - duplicates, including two spellings or symlinks of the same folder;
   - folders that are inside, or contain, another listed root.
3. Disables roots that are no longer listed. They are **not deleted**, and
   their indexed tracks are kept. Listing the path again re-enables the same
   root.
4. Scans all enabled roots in the background, one after another, unless
   `REWIND_SCAN_ON_STARTUP=false`. The web interface keeps working during a
   scan.

**To rescan**, use **Rescan library** on the Status page of the web
interface, restart Rewind, or call `POST /api/library/scan` (see
[api.md](api.md#post-apilibraryscan)). The request returns immediately and
the scan runs in the background, using the same rules and the same
one-scan-per-root limit as the startup scan.

| Variable | Default | Meaning |
| --- | --- | --- |
| `REWIND_LIBRARY_ROOTS` | *(empty)* | absolute folders to index |
| `REWIND_SCAN_ON_STARTUP` | `true` | scan all roots at startup |
| `REWIND_SCAN_WORKERS` | `2` | files read in parallel during a scan (1–8); more rarely helps on USB drives |

### Find your pendrive's mount path

Rewind never mounts drives. Plug the pendrive in and let the operating system
mount it, then find where it is mounted:

```sh
lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINTS   # mounted partitions and their folders
findmnt -t vfat,exfat,ntfs3,ext4              # mounts of common pendrive filesystems
```

On desktops the path is often `/media/<user>/<LABEL>` or
`/run/media/<user>/<LABEL>`. Headless servers often need an `/etc/fstab`
entry (see below). Use whatever path your system shows, and do not assume
`/media` or `/mnt`.

If the mount path changes (a different label, or another mount point),
Rewind sees a *different* root. Update `REWIND_LIBRARY_ROOTS`. The old root
is then disabled and its tracks are kept but not used, and the new path is
indexed as a new root. Moving a root to a new path while keeping its track
IDs is not supported yet.

### Permissions

Rewind only needs to **read** the music. Recommended:

- Run Rewind as an unprivileged user. Never run it as root.
- Mount the pendrive **read-only** and readable by that user. FAT32 and exFAT
  have no Unix permissions, so the mount options decide access. For example,
  in `/etc/fstab`:

  ```
  UUID=XXXX-XXXX  /srv/usb-music  exfat  ro,nofail,uid=rewind,gid=rewind,umask=0222  0  0
  ```

  `nofail` lets the server boot without the stick. Find the UUID with
  `lsblk -f`.
- On ext4 or other Unix filesystems, the folders need `r-x` and the files
  `r--` for Rewind's user.
- With the provided systemd unit (`rewind.service`), the whole file system
  is read-only for Rewind, except its data directory. Reading music works
  anywhere the `rewind` user has permission. Home directories are often
  `0700` or `0750`, though, so a folder under `/home` is usually not
  readable by `rewind`. Mount points such as `/srv/usb-music` or `/media/…`
  avoid this.

Unreadable sub-folders make a scan *partial* (see below). Unreadable files
are indexed with an error and retried on the next scan.

## What gets indexed

Supported formats, checked by file extension:

| Format | Extensions | Notes |
| --- | --- | --- |
| MP3 | `.mp3` | ID3v1/ID3v2 tags; duration may be approximate for VBR files without a Xing/VBRI header |
| FLAC | `.flac` | Vorbis comments |
| AAC / ALAC in MP4 | `.m4a` | iTunes-style tags; the codec (`aac` or `alac`) is recorded |
| Ogg | `.ogg`, `.oga` | Vorbis comments; the codec inside is recorded |
| WAV | `.wav` | RIFF INFO or ID3 tags, if present |

Metadata is read with TagLib (via `go.senan.xyz/taglib`). Rewind's tests cover
all five formats with generated files.

Supported metadata does **not** mean every browser can play the file. For
example, ALAC inside `.m4a` is not supported by every browser. Rewind streams
the original file and does not transcode, so a browser that cannot decode a
file shows a playback error for it.

Other files are skipped and counted, including `.opus`, `.wma`, raw `.aac`,
images and text. Also skipped:

- hidden files and folders (names starting with `.`, including macOS `._*`
  files);
- `$RECYCLE.BIN`, `System Volume Information` and `lost+found`;
- symbolic links of any kind.

Stored per track:
- title, artist, album, album artist, genre, raw date and year, track and
  disc number, duration, format, codec;
- file size, modification time, and the path relative to the root.

### Missing or bad metadata

- **Tags are the primary source.** If there is no title tag, the title is the
  file name without its extension (`07 - My Song.mp3` becomes
  `07 - My Song`), and the track is marked `no_tags`.
- **Nothing is guessed.** Folder names are never used as artist or album, and
  numbers are never parsed out of file names. Unknown fields stay empty.
- Multiple values for one tag are joined with `; `. Values are cleaned of
  control characters and truncated at 512 characters.
- **Corrupt, truncated or unreadable files** do not stop a scan. They are
  indexed with status `error`, a short reason (for example `not a valid or
  supported audio file` or `cannot open file: permission denied`) and the
  file-name title, and they are read again on every scan.
- Rewind **never writes** fallback metadata, or anything else, into the files.

## How scanning works

1. Check that the root folder exists and can be listed. If not, the scan
   fails and nothing else happens.
2. Load the root's existing index (paths, sizes and modification times) into
   memory.
3. Walk the folder tree. A file whose size and modification time match the
   index is **unchanged** and is not opened at all.
4. New and changed files go to a small pool of readers (`REWIND_SCAN_WORKERS`).
   Results are written to the database in batches of up to 200, so no
   database transaction is open while files are read.
5. After a **complete** walk, files that were indexed but not found are
   handled: either matched to a renamed file, or marked `missing`.
6. Record the result: one log line per scan, plus a row in the `scans` table.

**Track identity** is the root plus the file's path relative to the root.
Rescanning never creates duplicates, and a track keeps its ID while its file
stays at the same path, even if the tags change.

- **Renames and moves within a root** keep the track ID when the moved file has
  the same size, modification time and duration as exactly one disappeared
  track. Moving or renaming on the same filesystem normally keeps the
  modification time. If the match is ambiguous (for example, identical
  copies), Rewind does not guess: the old track becomes `missing` and the file
  is added as a new track.
- **Moving a file to a different root** marks it missing in the old root and
  adds it as a new track in the new root.
- The same physical file cannot be in two roots, because roots may not overlap.
  Separate copies on different drives are separate tracks.
- Files are never hashed. Detecting duplicate songs by title, artist or
  duration is out of scope.

Only one scan per root runs at a time. Shutting Rewind down stops a running
scan, saves the work already done, and records the scan as `interrupted`.

## Disconnected drives and missing files

Rewind is designed so that unplugging a pendrive never loses the library:

| Situation | Result | Tracks |
| --- | --- | --- |
| Root folder missing or unreadable at scan time | scan `failed`, root `unavailable` | untouched |
| Root folder exists but has **no audio files**, while tracks are indexed (typical of an unmounted mount point) | scan `failed` ("the drive may not be mounted"), root `unavailable` | untouched |
| Drive removed **during** a scan | scan `failed` | untouched (changes already read are kept) |
| Some sub-folders unreadable | scan `partial` | none marked missing |
| Rewind shut down during a scan | scan `interrupted` | none marked missing |
| Complete scan, some files gone | scan `completed` | gone files marked `missing` with a timestamp |
| A missing file comes back unchanged | `completed` | marked `present` again with the **same ID** |

Rewind never deletes tracks or roots automatically. Missing tracks stay
missing until their files return. A future phase will add a way to remove
them deliberately.

**Playlists keep their tracks in every case above.** Entries for missing
files or unplugged drives stay in place and are shown as unavailable; they
play again, in the same position, once the files are back. When a scan
detects a rename, the playlist follows the renamed file.

If a *different* drive is mounted at the same path, its files are added and
the previous drive's tracks are marked missing. They come back when the
original drive returns.

## Reading scan results

Each scan logs one summary line:

```
level=INFO msg="library scan finished" root_id=1 scan_id=7 status=completed duration=1.02s
  found=3000 added=12 updated=1 unchanged=2987 renamed=0 missing=0 restored=0 skipped=41 failed=2 dirs_failed=0
```

| Status | Meaning | What to do |
| --- | --- | --- |
| `completed` | The whole root was read. `failed=N` counts files with unreadable metadata. | Check those files; they are retried next scan. |
| `partial` | Some folders could not be read (`dirs_failed`); the error names the first one. | Fix permissions, then rescan. |
| `failed` | The root was unavailable, vanished during the scan, or looked unmounted. | Check that the drive is plugged in and mounted, then rescan. |
| `interrupted` | Rewind stopped during the scan, or crashed (recorded at the next start). | Rescan. |

"Rescan" means `POST /api/library/scan` or restarting Rewind. The latest
result per root is also available from `GET /api/library/roots`.

Up to 10 unreadable files per scan are logged individually. The scan history
is in the database:

```sh
sqlite3 /var/lib/rewind/rewind.db \
  "SELECT id, root_id, status, files_found, tracks_added, files_failed, error FROM scans ORDER BY id DESC LIMIT 5;"
```

## Resource use

- One walker goroutine, `REWIND_SCAN_WORKERS` readers (default 2) and one
  database writer. No goroutine per file, and no permanent background worker
  or file watcher.
- Audio files are not loaded into memory. TagLib reads only the parts it
  needs.
- Unchanged files cost a directory entry and a `stat`, with no database
  write. Progress is kept in memory; the database is written per batch and
  at the end.
- One measurement, on a fast desktop in WSL on a local disk, **not** a USB
  drive, with 3,000 generated tagged files (47 MB):
  - first scan: about 1.0 s, peak memory about 51 MiB;
  - rescan with nothing changed: 11 ms;
  - database: 632 KB.

  Expect real pendrives on modest hardware to be much slower, because USB
  I/O dominates. Measurements on the owner's target hardware are still to
  be made (see [security-audit.md](security-audit.md#4-measurements)).

## Running the scanner tests

```sh
go test ./internal/metadata ./internal/library ./internal/db
```

The tests generate tiny silent MP3, FLAC, M4A, Ogg and WAV files at run time
(`internal/testaudio`), so the repository contains no audio fixtures. Tests
for permission errors and symlinks are skipped on Windows and run on Linux.

## Limitations

- Rescans happen at startup or when requested through the API. There is no
  file watcher and no automatic detection of plugged-in drives.
- The web interface has a simple track list and player only; browsing by
  artist or album comes later.
- Changing a root's path creates a new root (old tracks kept but disabled).
- Missing tracks are never purged automatically.
- Files with unreadable metadata are re-read on every scan.
- Rename detection relies on unchanged size, modification time and duration.
  Copying files to a new location (which changes the modification time)
  creates new tracks.
- Filenames that are not valid UTF-8 are skipped and counted as failed.
- Only the five formats above are indexed.
