// SPDX-License-Identifier: Apache-2.0

package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders for embedded and cover images
	"image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shamil-tp/rewind/internal/metadata"
)

// Artwork is extracted on first request and cached:
//
//   - The image comes from the track's file (embedded) or, failing that, from
//     a cover image next to it (cover, folder, front or album .jpg/.jpeg/.png).
//     Music files are only read, never changed.
//   - Two JPEG renditions (small, large) are written to <data dir>/artwork,
//     named by the hash of the original image, so the tracks of an album
//     share them. track_artwork records the result per track together with
//     the file's size and modification time; a changed file is read again,
//     and "no artwork" is remembered too, so files are not re-read on every
//     request.
//   - Images are checked before decoding (format, dimensions) and limited in
//     size, so a malformed or huge image cannot exhaust memory.
//   - Extraction runs at most two at a time, and at most once per track at a
//     time (concurrent requests wait for the same result).

// ArtworkSize names a rendition.
type ArtworkSize string

const (
	ArtworkSmall ArtworkSize = "small" // lists and the mini player
	ArtworkLarge ArtworkSize = "large" // detail pages and Now Playing
)

var artworkPixels = map[ArtworkSize]int{ArtworkSmall: 320, ArtworkLarge: 1000}

// Limits for source images.
const (
	maxArtworkSide   = 12000
	maxArtworkPixels = 25_000_000
)

// ErrNoArtwork means the track has no usable artwork.
var ErrNoArtwork = errors.New("no artwork")

var sidecarNames = []string{"cover", "folder", "front", "album"}
var sidecarExts = []string{".jpg", ".jpeg", ".png"}

// ArtworkCache extracts and caches artwork.
type ArtworkCache struct {
	db      *sql.DB
	catalog *Catalog
	dir     string
	sem     chan struct{}
	now     func() time.Time

	mu       sync.Mutex
	inflight map[int64]*artworkCall
}

type artworkCall struct {
	done chan struct{}
	hash string
	err  error
}

// NewArtworkCache returns a cache that stores renditions in dir (created
// with mode 0700 if missing).
func NewArtworkCache(db *sql.DB, catalog *Catalog, dir string) (*ArtworkCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the artwork cache directory: %w", err)
	}
	return &ArtworkCache{db: db, catalog: catalog, dir: dir, sem: make(chan struct{}, 2), now: time.Now,
		inflight: map[int64]*artworkCall{}}, nil
}

func (a *ArtworkCache) file(hash string, size ArtworkSize) string {
	return filepath.Join(a.dir, hash+"-"+string(size)+".jpg")
}

// Get returns the path of a track's rendition and its hash (for ETags), or
// ErrNoArtwork / ErrTrackNotFound.
func (a *ArtworkCache) Get(ctx context.Context, trackID int64, size ArtworkSize) (string, string, error) {
	if _, ok := artworkPixels[size]; !ok {
		return "", "", fmt.Errorf("library: unknown artwork size %q", size)
	}
	var (
		fileSize, mtime     int64
		cachedSize, cachedM sql.NullInt64
		status, hash        sql.NullString
	)
	err := a.db.QueryRowContext(ctx, `SELECT t.file_size, t.file_mtime_ns, a.file_size, a.file_mtime_ns, a.status, a.hash
		FROM tracks t LEFT JOIN track_artwork a ON a.track_id = t.id WHERE t.id = ?`, trackID).
		Scan(&fileSize, &mtime, &cachedSize, &cachedM, &status, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrTrackNotFound
	}
	if err != nil {
		return "", "", err
	}
	fresh := status.Valid && cachedSize.Int64 == fileSize && cachedM.Int64 == mtime
	if fresh && status.String == "none" {
		return "", "", ErrNoArtwork
	}
	if fresh && hash.Valid {
		if p := a.file(hash.String, size); fileExists(p) {
			return p, hash.String, nil
		}
	}
	h, err := a.extractOnce(ctx, trackID)
	if err != nil {
		// The drive may be unplugged: an older rendition is better than none.
		if hash.Valid && !errors.Is(err, ErrNoArtwork) {
			if p := a.file(hash.String, size); fileExists(p) {
				return p, hash.String, nil
			}
		}
		return "", "", err
	}
	return a.file(h, size), h, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// extractOnce runs extract for a track, sharing the result with concurrent
// callers for the same track.
func (a *ArtworkCache) extractOnce(ctx context.Context, trackID int64) (string, error) {
	a.mu.Lock()
	if c, ok := a.inflight[trackID]; ok {
		a.mu.Unlock()
		select {
		case <-c.done:
			return c.hash, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &artworkCall{done: make(chan struct{})}
	a.inflight[trackID] = c
	a.mu.Unlock()

	c.hash, c.err = a.extract(context.WithoutCancel(ctx), trackID)
	a.mu.Lock()
	delete(a.inflight, trackID)
	a.mu.Unlock()
	close(c.done)
	return c.hash, c.err
}

func (a *ArtworkCache) extract(ctx context.Context, trackID int64) (string, error) {
	a.sem <- struct{}{}
	defer func() { <-a.sem }()

	// OpenTrack applies the same containment checks as streaming: a local
	// path inside the configured root, no symbolic links, a regular file.
	f, err := a.catalog.OpenTrack(ctx, trackID)
	if err != nil {
		if errors.Is(err, ErrTrackNotFound) {
			return "", err
		}
		return "", fmt.Errorf("%w: %v", ErrNoArtwork, err)
	}
	f.Close()
	var rel, rootPath string
	var storedSize, storedMtime int64
	if err := a.db.QueryRowContext(ctx, `SELECT t.rel_path, r.path, t.file_size, t.file_mtime_ns FROM tracks t
		JOIN library_roots r ON r.id = t.root_id WHERE t.id = ?`, trackID).Scan(&rel, &rootPath, &storedSize, &storedMtime); err != nil {
		return "", err
	}

	data, source := []byte(nil), ""
	if img, err := metadata.ReadArtwork(filepath.Join(rootPath, filepath.FromSlash(rel))); err == nil && validImage(img) {
		data, source = img, "embedded"
	} else if img := readSidecar(rootPath, path.Dir(rel)); img != nil {
		data, source = img, "sidecar"
	}

	// The result is recorded against the size and time stored by the
	// scanner (what Get compares with), so after the next scan notices a
	// changed file, the artwork is read again.
	if data == nil {
		return "", errors.Join(ErrNoArtwork, a.record(ctx, trackID, storedSize, storedMtime, "none", "", ""))
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:16])
	if err := a.render(hash, data); err != nil {
		// Undecodable despite the checks: remember "none" until the file changes.
		return "", errors.Join(ErrNoArtwork, a.record(ctx, trackID, storedSize, storedMtime, "none", "", ""))
	}
	if err := a.record(ctx, trackID, storedSize, storedMtime, "ok", hash, source); err != nil {
		return "", err
	}
	return hash, nil
}

func (a *ArtworkCache) record(ctx context.Context, trackID, size, mtime int64, status, hash, source string) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO track_artwork (track_id, file_size, file_mtime_ns, status, hash, source, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (track_id) DO UPDATE SET file_size = excluded.file_size,
		file_mtime_ns = excluded.file_mtime_ns, status = excluded.status, hash = excluded.hash, source = excluded.source,
		checked_at = excluded.checked_at`, trackID, size, mtime, status, nullString(hash), nullString(source), a.now().Unix())
	return err
}

// validImage checks an image's format and dimensions without decoding it.
func validImage(data []byte) bool {
	if len(data) == 0 || len(data) > metadata.MaxArtworkBytes {
		return false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "gif") {
		return false
	}
	return cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= maxArtworkSide && cfg.Height <= maxArtworkSide &&
		cfg.Width*cfg.Height <= maxArtworkPixels
}

// readSidecar returns a cover image from the track's directory, read through
// os.Root (no path can leave the library root; links are not followed).
func readSidecar(rootPath, dir string) []byte {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return nil
	}
	byName := map[string]string{}
	for _, e := range entries {
		if e.Type().IsRegular() {
			byName[strings.ToLower(e.Name())] = e.Name()
		}
	}
	for _, n := range sidecarNames {
		for _, ext := range sidecarExts {
			real, ok := byName[n+ext]
			if !ok {
				continue
			}
			p := path.Join(dir, real)
			if !filepath.IsLocal(filepath.FromSlash(p)) {
				continue
			}
			info, err := root.Lstat(filepath.FromSlash(p))
			if err != nil || !info.Mode().IsRegular() || info.Size() > metadata.MaxArtworkBytes {
				continue
			}
			f, err := root.Open(filepath.FromSlash(p))
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(f, metadata.MaxArtworkBytes+1))
			f.Close()
			if err == nil && validImage(data) {
				return data
			}
		}
	}
	return nil
}

// render writes both renditions of an image unless they exist already.
func (a *ArtworkCache) render(hash string, data []byte) error {
	if fileExists(a.file(hash, ArtworkSmall)) && fileExists(a.file(hash, ArtworkLarge)) {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	for size, px := range artworkPixels {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, scale(src, px), &jpeg.Options{Quality: 85}); err != nil {
			return err
		}
		if err := writeAtomic(a.file(hash, size), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// scale fits src within max×max pixels, keeping the aspect ratio. Each
// output pixel averages up to 4×4 samples of its source area, which is
// enough for artwork and keeps the work bounded for large images.
func scale(src image.Image, max int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw > max || sh > max {
		if sw >= sh {
			dw, dh = max, maxInt(1, sh*max/sw)
		} else {
			dw, dh = maxInt(1, sw*max/sh), max
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := b.Min.Y+y*sh/dh, b.Min.Y+maxInt((y+1)*sh/dh, y*sh/dh+1)
		for x := 0; x < dw; x++ {
			x0, x1 := b.Min.X+x*sw/dw, b.Min.X+maxInt((x+1)*sw/dw, x*sw/dw+1)
			var r, g, bl, n uint32
			stepY, stepX := maxInt((y1-y0)/4, 1), maxInt((x1-x0)/4, 1)
			for sy := y0; sy < y1; sy += stepY {
				for sx := x0; sx < x1; sx += stepX {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var renditionName = regexp.MustCompile(`^[0-9a-f]{32}-(small|large)\.jpg$`)

// Cleanup removes renditions no track refers to and leftover temporary
// files. It only touches files in the cache directory with names this cache
// creates.
func (a *ArtworkCache) Cleanup(ctx context.Context) (int, error) {
	used := map[string]bool{}
	rows, err := a.db.QueryContext(ctx, "SELECT DISTINCT hash FROM track_artwork WHERE hash IS NOT NULL")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return 0, err
		}
		used[h] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		stale := strings.HasPrefix(name, ".tmp-")
		if renditionName.MatchString(name) && !used[name[:32]] {
			stale = true
		}
		if stale && os.Remove(filepath.Join(a.dir, name)) == nil {
			removed++
		}
	}
	return removed, nil
}
