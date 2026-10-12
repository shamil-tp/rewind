// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Errors returned by OpenTrack. None of them reveal filesystem paths.
var (
	// ErrRootUnavailable means the track's library root cannot be opened
	// (for example, the drive is unplugged).
	ErrRootUnavailable = errors.New("the library root is unavailable")
	// ErrFileUnavailable means the indexed file is missing, is not a regular
	// file, is a symbolic link, or its stored path is not a safe relative path.
	ErrFileUnavailable = errors.New("the track's file is not available")
)

// contentTypes maps Rewind's format names (from the file extension at index
// time) to the Content-Type sent when streaming. The client never chooses it.
var contentTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"flac": "audio/flac",
	"m4a":  "audio/mp4",
	"ogg":  "audio/ogg",
	"wav":  "audio/wav",
}

// ContentType returns the streaming Content-Type for a format name, falling
// back to application/octet-stream.
func ContentType(format string) string {
	if ct, ok := contentTypes[format]; ok {
		return ct
	}
	return "application/octet-stream"
}

// beforeOpenHook, set only by tests, runs between the symlink check and the
// open, to simulate the file being replaced in that window.
var beforeOpenHook func(rootPath, name string)

// TrackFile is an opened, verified track file. The caller must Close it.
type TrackFile struct {
	*os.File
	TrackID     int64
	Size        int64
	ModTime     time.Time
	ContentType string
}

// OpenTrack opens the file of an indexed track for reading. The file is
// reached only through the track's database record, and only inside its
// configured root:
//
//   - the stored relative path must be a local path (filepath.IsLocal: not
//     absolute, no "..", no empty or reserved names);
//   - the file is opened through os.Root, which refuses any path or symbolic
//     link that resolves outside the root, even if the filesystem changes
//     between checks;
//   - symbolic links are refused outright (the scanner never indexes them),
//     and the opened descriptor must be the file that was checked, so a link
//     or file swapped in between the check and the open is refused too;
//   - on Unix the file is opened non-blocking, so a FIFO cannot hang the
//     request, and only regular files are accepted (checked on the opened
//     descriptor, not on the path).
//
// Disabled roots (no longer configured) are refused. It never writes.
func (c *Catalog) OpenTrack(ctx context.Context, id int64) (*TrackFile, error) {
	var (
		rel, format, rootPath string
		enabled               bool
	)
	err := c.db.QueryRowContext(ctx, `SELECT t.rel_path, t.format, r.path, r.enabled
		FROM tracks t JOIN library_roots r ON r.id = t.root_id WHERE t.id = ?`, id).
		Scan(&rel, &format, &rootPath, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTrackNotFound
	}
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrRootDisabled
	}

	name := filepath.FromSlash(rel)
	if !filepath.IsLocal(name) {
		return nil, fmt.Errorf("%w: stored path is not a safe relative path", ErrFileUnavailable)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, ErrRootUnavailable
	}
	defer root.Close() // the opened file stays valid after the Root is closed

	linfo, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFileUnavailable, describeFSError(err))
	} else if linfo.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: symbolic links are not served", ErrFileUnavailable)
	}
	if beforeOpenHook != nil {
		beforeOpenHook(rootPath, name)
	}

	f, err := root.OpenFile(name, openFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFileUnavailable, describeFSError(err))
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%w: not a regular file", ErrFileUnavailable)
	}
	// os.Root follows symbolic links that stay inside the root, so a link
	// swapped in after the Lstat above would be opened. The descriptor must
	// be the file that Lstat saw.
	if !os.SameFile(linfo, info) {
		f.Close()
		return nil, fmt.Errorf("%w: the file changed while it was being opened", ErrFileUnavailable)
	}
	return &TrackFile{File: f, TrackID: id, Size: info.Size(), ModTime: info.ModTime(), ContentType: ContentType(format)}, nil
}
