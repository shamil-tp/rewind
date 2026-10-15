// SPDX-License-Identifier: Apache-2.0

package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/shamil-tp/rewind/internal/metadata"
)

// Scan status values, stored in scans.status.
const (
	ScanRunning     = "running"
	ScanCompleted   = "completed"
	ScanPartial     = "partial"
	ScanFailed      = "failed"
	ScanInterrupted = "interrupted"
)

// Errors returned by Scan.
var (
	ErrScanRunning  = errors.New("a scan of this library root is already running")
	ErrRootDisabled = errors.New("library root is disabled")
	ErrRootNotFound = errors.New("library root not found")
	// ErrNoRoots is returned by Start when no enabled library root exists.
	ErrNoRoots = errors.New("no library roots are configured")
	// ErrScannerClosed is returned by Start after Close.
	ErrScannerClosed = errors.New("the scanner is shutting down")
)

const (
	// DefaultWorkers is the default number of concurrent metadata readers.
	// USB drives are I/O-bound; more readers rarely help.
	DefaultWorkers = 2
	// MaxWorkers caps the configurable reader count.
	MaxWorkers = 8
	// writeBatch is the number of track changes committed per transaction.
	writeBatch = 200
	// flushInterval commits a partial batch after this long, so progress is
	// saved during slow scans without a write per file.
	flushInterval = time.Second
	// maxLoggedFileErrors limits per-file warnings in the log per scan.
	maxLoggedFileErrors = 10
	// maxErrorSummary bounds scans.error.
	maxErrorSummary = 1000
)

// Counts are the per-scan counters, persisted in the scans table.
type Counts struct {
	FilesFound, FilesSkipped, FilesFailed, DirsFailed          int
	TracksAdded, TracksUpdated, TracksUnchanged, TracksRenamed int
	TracksMissing, TracksRestored                              int
}

// Result describes a finished (or currently running) scan.
type Result struct {
	ScanID     int64
	RootID     int64
	Status     string
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	Counts     Counts
	Error      string
}

// Options configures a Scanner.
type Options struct {
	DB      *sql.DB
	Logger  *slog.Logger
	Workers int // concurrent metadata readers; 0 means DefaultWorkers
	// Extract reads metadata; defaults to metadata.Extract. Tests override it.
	Extract func(path string) metadata.Metadata
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
	// ExtractTimeout bounds reading one file's metadata; 0 means
	// DefaultExtractTimeout.
	ExtractTimeout time.Duration
	// OnFinished, if set, is called after each scan's result is recorded. It
	// runs on the scanning goroutine, so it must return quickly (Rewind uses
	// it to queue plugin events without blocking).
	OnFinished func(Result)
}

// Scanner indexes library roots. A root is scanned by at most one scan at a
// time, whether it was requested with Scan (blocking) or Start (background);
// a root that is running or queued is reported as busy. ScanAll and Start
// scan roots one after another.
type Scanner struct {
	db      *sql.DB
	logger  *slog.Logger
	workers int
	extract func(string) metadata.Metadata
	now     func() time.Time
	done    func(Result)

	// extractTimeout bounds one metadata read. timedOut remembers files
	// whose read overran (by path, size and modification time), so a file
	// that hangs TagLib is not retried on every rescan.
	extractTimeout time.Duration
	timedOutMu     sync.Mutex
	timedOut       map[timedOutKey]bool

	mu      sync.Mutex
	running map[int64]*progress
	queued  map[int64]bool // accepted by Start, not yet running
	closed  bool

	// Background scans started with Start run under bgCtx; Close cancels it
	// and waits for them.
	bgCtx    context.Context
	bgCancel context.CancelFunc
	bg       sync.WaitGroup
}

// NewScanner returns a Scanner.
func NewScanner(opts Options) (*Scanner, error) {
	if opts.DB == nil || opts.Logger == nil {
		return nil, errors.New("library: DB and Logger are required")
	}
	if opts.Workers <= 0 {
		opts.Workers = DefaultWorkers
	}
	if opts.Workers > MaxWorkers {
		opts.Workers = MaxWorkers
	}
	if opts.Extract == nil {
		opts.Extract = metadata.Extract
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ExtractTimeout <= 0 {
		opts.ExtractTimeout = DefaultExtractTimeout
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	return &Scanner{
		db: opts.DB, logger: opts.Logger, workers: opts.Workers, extract: opts.Extract, now: opts.Now, done: opts.OnFinished,
		extractTimeout: opts.ExtractTimeout, timedOut: map[timedOutKey]bool{},
		running: map[int64]*progress{}, queued: map[int64]bool{},
		bgCtx: bgCtx, bgCancel: bgCancel,
	}, nil
}

// StartResult reports which roots a Start call queued and which were already
// being scanned (or queued) and were therefore left alone.
type StartResult struct {
	Started []int64
	Busy    []int64
}

// Start scans roots in the background, one after another, and returns
// immediately. With no IDs it scans every enabled root. It validates all
// IDs before queueing any: an unknown ID returns ErrRootNotFound and a
// disabled root returns ErrRootDisabled. A root that is already running or
// queued is reported in Busy and not scanned twice. Scans of unavailable
// roots still run and are recorded as failed without touching their tracks.
func (s *Scanner) Start(rootIDs ...int64) (StartResult, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return StartResult{}, ErrScannerClosed
	}
	roots, err := loadRoots(s.bgCtx, s.db)
	if s.bgCtx.Err() != nil {
		return StartResult{}, ErrScannerClosed
	}
	if err != nil {
		return StartResult{}, err
	}
	byID := map[int64]Root{}
	for _, r := range roots {
		byID[r.ID] = r
	}
	var targets []int64
	if len(rootIDs) == 0 {
		for _, r := range roots {
			if r.Enabled {
				targets = append(targets, r.ID)
			}
		}
		if len(targets) == 0 {
			return StartResult{}, ErrNoRoots
		}
	} else {
		seen := map[int64]bool{}
		for _, id := range rootIDs {
			r, ok := byID[id]
			switch {
			case !ok:
				return StartResult{}, ErrRootNotFound
			case !r.Enabled:
				return StartResult{}, ErrRootDisabled
			case !seen[id]:
				seen[id] = true
				targets = append(targets, id)
			}
		}
	}

	var res StartResult
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return StartResult{}, ErrScannerClosed
	}
	for _, id := range targets {
		if _, running := s.running[id]; running || s.queued[id] {
			res.Busy = append(res.Busy, id)
			continue
		}
		s.queued[id] = true
		res.Started = append(res.Started, id)
	}
	if len(res.Started) > 0 {
		s.bg.Add(1)
	}
	s.mu.Unlock()

	if len(res.Started) > 0 {
		ids := append([]int64(nil), res.Started...)
		go func() {
			defer s.bg.Done()
			for _, id := range ids {
				if s.bgCtx.Err() == nil {
					if _, err := s.scan(s.bgCtx, id, true); err != nil {
						s.logger.Error("library scan not started", "root_id", id, "err", err)
					}
				}
				s.mu.Lock()
				delete(s.queued, id) // no-op unless the scan never began
				s.mu.Unlock()
			}
		}()
	}
	return res, nil
}

// Queued returns the roots accepted by Start that have not started yet.
func (s *Scanner) Queued() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.queued))
	for id := range s.queued {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Close stops background scans (they are recorded as interrupted) and waits
// for them to finish. Call it before closing the database.
func (s *Scanner) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.bgCancel()
	s.bg.Wait()
}

// progress holds live counters for a running scan. Counters are atomic so
// Running can read them while the scan updates them; nothing is written to
// the database until the scan ends.
type progress struct {
	scanID  int64
	rootID  int64
	started time.Time

	filesFound, filesSkipped, filesFailed, dirsFailed     atomic.Int64
	added, updated, unchanged, renamed, missing, restored atomic.Int64
}

func (p *progress) counts() Counts {
	return Counts{
		FilesFound: int(p.filesFound.Load()), FilesSkipped: int(p.filesSkipped.Load()),
		FilesFailed: int(p.filesFailed.Load()), DirsFailed: int(p.dirsFailed.Load()),
		TracksAdded: int(p.added.Load()), TracksUpdated: int(p.updated.Load()),
		TracksUnchanged: int(p.unchanged.Load()), TracksRenamed: int(p.renamed.Load()),
		TracksMissing: int(p.missing.Load()), TracksRestored: int(p.restored.Load()),
	}
}

// Running returns a snapshot of the scans currently in progress.
func (s *Scanner) Running() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Result, 0, len(s.running))
	for _, p := range s.running {
		out = append(out, Result{ScanID: p.scanID, RootID: p.rootID, Status: ScanRunning, StartedAt: p.started, Counts: p.counts()})
	}
	return out
}

// ScanAll scans every enabled root, one at a time, until ctx is cancelled.
func (s *Scanner) ScanAll(ctx context.Context) []Result {
	roots, err := loadRoots(ctx, s.db)
	if err != nil {
		s.logger.Error("loading library roots", "err", err)
		return nil
	}
	var results []Result
	for _, r := range roots {
		if !r.Enabled {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		res, err := s.Scan(ctx, r.ID)
		if err != nil {
			s.logger.Error("library scan not started", "root_id", r.ID, "err", err)
			continue
		}
		results = append(results, res)
	}
	return results
}

// Scan indexes one root. It returns ErrScanRunning if that root is already
// being scanned. A scan that cannot traverse the root never marks tracks
// missing; see docs/architecture.md, "Music library".
func (s *Scanner) Scan(ctx context.Context, rootID int64) (Result, error) {
	return s.scan(ctx, rootID, false)
}

// scan runs one scan. fromQueue is true when Start queued this root, so its
// own queue reservation does not count as busy.
func (s *Scanner) scan(ctx context.Context, rootID int64, fromQueue bool) (Result, error) {
	root, err := loadRoot(ctx, s.db, rootID)
	if err != nil {
		return Result{}, err
	}
	if !root.Enabled {
		return Result{}, ErrRootDisabled
	}

	start := s.now()
	s.mu.Lock()
	if _, busy := s.running[rootID]; busy || (s.queued[rootID] && !fromQueue) {
		s.mu.Unlock()
		return Result{}, ErrScanRunning
	}
	delete(s.queued, rootID)
	p := &progress{rootID: rootID, started: start}
	s.running[rootID] = p
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, rootID)
		s.mu.Unlock()
	}()

	res, err := s.db.ExecContext(ctx, "INSERT INTO scans (root_id, started_at, status) VALUES (?, ?, ?)",
		rootID, start.Unix(), ScanRunning)
	if err != nil {
		return Result{}, fmt.Errorf("recording scan start: %w", err)
	}
	p.scanID, _ = res.LastInsertId()
	s.logger.Info("library scan started", "root_id", rootID, "scan_id", p.scanID, "path", root.Path)

	run := &scanRun{s: s, root: root, p: p}
	status, summary, rootStatus, rootMsg := run.execute(ctx)

	result := Result{
		ScanID: p.scanID, RootID: rootID, Status: status, StartedAt: start, FinishedAt: s.now(),
		Counts: p.counts(), Error: truncateRunes(summary, maxErrorSummary),
	}
	// Record the outcome even if ctx was cancelled (interrupted scans).
	if err := s.finish(context.WithoutCancel(ctx), result, rootStatus, rootMsg); err != nil {
		return result, fmt.Errorf("recording scan result: %w", err)
	}
	c := result.Counts
	s.logger.Info("library scan finished", "root_id", rootID, "scan_id", p.scanID, "status", status,
		"duration", result.FinishedAt.Sub(start).Round(time.Millisecond).String(),
		"found", c.FilesFound, "added", c.TracksAdded, "updated", c.TracksUpdated, "unchanged", c.TracksUnchanged,
		"renamed", c.TracksRenamed, "missing", c.TracksMissing, "restored", c.TracksRestored,
		"skipped", c.FilesSkipped, "failed", c.FilesFailed, "dirs_failed", c.DirsFailed, "error", result.Error)
	if s.done != nil {
		s.done(result)
	}
	return result, nil
}

func (s *Scanner) finish(ctx context.Context, r Result, rootStatus, rootMsg string) error {
	c := r.Counts
	_, err := s.db.ExecContext(ctx, `UPDATE scans SET finished_at = ?, status = ?,
		files_found = ?, files_skipped = ?, files_failed = ?, dirs_failed = ?,
		tracks_added = ?, tracks_updated = ?, tracks_unchanged = ?, tracks_renamed = ?,
		tracks_missing = ?, tracks_restored = ?, error = ? WHERE id = ?`,
		r.FinishedAt.Unix(), r.Status, c.FilesFound, c.FilesSkipped, c.FilesFailed, c.DirsFailed,
		c.TracksAdded, c.TracksUpdated, c.TracksUnchanged, c.TracksRenamed, c.TracksMissing, c.TracksRestored,
		nullIfEmpty(r.Error), r.ScanID)
	if err != nil {
		return err
	}
	if rootStatus == "" { // interrupted: leave the root's status as it was
		_, err = s.db.ExecContext(ctx, "UPDATE library_roots SET last_scan_id = ? WHERE id = ?", r.ScanID, r.RootID)
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE library_roots SET last_scan_id = ?, status = ?, status_message = ? WHERE id = ?",
		r.ScanID, rootStatus, nullIfEmpty(rootMsg), r.RootID)
	return err
}

// RecoverInterruptedScans marks scans left 'running' by a previous process
// (for example after a crash or power loss) as interrupted. Call it at
// startup before scanning.
func RecoverInterruptedScans(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE scans SET status = ?, finished_at = ?,
		error = 'Rewind stopped before the scan finished' WHERE status = ?`, ScanInterrupted, now.Unix(), ScanRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LastScan returns the most recent scan of a root, if any.
func LastScan(ctx context.Context, db *sql.DB, rootID int64) (Result, bool, error) {
	var r Result
	var started int64
	var finished sql.NullInt64
	var errText sql.NullString
	c := &r.Counts
	err := db.QueryRowContext(ctx, `SELECT id, root_id, status, started_at, finished_at,
		files_found, files_skipped, files_failed, dirs_failed, tracks_added, tracks_updated,
		tracks_unchanged, tracks_renamed, tracks_missing, tracks_restored, error
		FROM scans WHERE root_id = ? ORDER BY id DESC LIMIT 1`, rootID).Scan(
		&r.ScanID, &r.RootID, &r.Status, &started, &finished,
		&c.FilesFound, &c.FilesSkipped, &c.FilesFailed, &c.DirsFailed, &c.TracksAdded, &c.TracksUpdated,
		&c.TracksUnchanged, &c.TracksRenamed, &c.TracksMissing, &c.TracksRestored, &errText)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	r.StartedAt = time.Unix(started, 0)
	if finished.Valid {
		r.FinishedAt = time.Unix(finished.Int64, 0)
	}
	r.Error = errText.String
	return r, true, nil
}

func loadRoot(ctx context.Context, db *sql.DB, id int64) (Root, error) {
	var r Root
	err := db.QueryRowContext(ctx,
		"SELECT id, configured_path, path, enabled, status, coalesce(status_message, '') FROM library_roots WHERE id = ?", id).
		Scan(&r.ID, &r.ConfiguredPath, &r.Path, &r.Enabled, &r.Status, &r.StatusMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Root{}, ErrRootNotFound
	}
	return r, err
}

// ---------------------------------------------------------------------------
// A single scan run.

// indexed is the stored state of a track, loaded once at the start of a scan.
type indexed struct {
	id             int64
	size, mtime    int64
	status         string
	metadataStatus string
	durationMS     sql.NullInt64
	version        int // metadata.Version of the extractor that read it
}

type job struct {
	rel, abs    string
	size, mtime int64
	old         *indexed // nil for a new file
	restoreOnly bool     // unchanged file that was marked missing
}

type outcome struct {
	job
	meta     metadata.Metadata
	vanished bool // the file disappeared before it could be read
}

// newTrack records a track inserted during this scan, for rename matching.
type newTrack struct {
	id          int64
	size, mtime int64
	durationMS  sql.NullInt64
}

type scanRun struct {
	s    *Scanner
	root Root
	p    *progress

	index      map[string]*indexed
	seen       map[string]struct{} // written only by the walker
	vanished   map[string]struct{} // written only by the writer
	added      []newTrack
	traversal  []string     // first few traversal errors
	fileErrors atomic.Int64 // file errors seen (walker and writer both log)
	rootErr    error
}

// execute runs the scan and returns its status, an error summary, and the
// root status to record ("" to leave it unchanged).
func (r *scanRun) execute(ctx context.Context) (status, summary, rootStatus, rootMsg string) {
	if err := checkReadableDir(r.root.Path); err != nil {
		msg := "library root unavailable: " + err.Error()
		return ScanFailed, msg, RootUnavailable, err.Error()
	}

	var err error
	r.index, err = r.loadIndex(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ScanInterrupted, "interrupted before the scan started", "", ""
		}
		return ScanFailed, "reading the index: " + err.Error(), "", ""
	}
	presentBefore := 0
	for _, t := range r.index {
		if t.status == "present" {
			presentBefore++
		}
	}
	r.seen = map[string]struct{}{}
	r.vanished = map[string]struct{}{}

	writeErr := r.pipeline(ctx)

	switch {
	case ctx.Err() != nil:
		return ScanInterrupted, "scan interrupted by shutdown; no tracks were marked missing", "", ""
	case writeErr != nil:
		return ScanFailed, "writing to the database: " + writeErr.Error(), "", ""
	case r.rootErr != nil:
		msg := "library root became unreadable: " + describeFSError(r.rootErr)
		return ScanFailed, msg + "; no tracks were marked missing", RootUnavailable, msg
	}
	if err := checkReadableDir(r.root.Path); err != nil {
		msg := "library root became unavailable during the scan: " + err.Error()
		return ScanFailed, msg + "; no tracks were marked missing", RootUnavailable, msg
	}
	if len(r.traversal) > 0 {
		msg := fmt.Sprintf("%d director%s could not be read (first: %s); no tracks were marked missing",
			r.p.dirsFailed.Load(), plural(r.p.dirsFailed.Load(), "y", "ies"), r.traversal[0])
		return ScanPartial, msg, RootAvailable, msg
	}
	if r.p.filesFound.Load() == 0 && presentBefore > 0 {
		// An unmounted mount point is usually an empty directory. Never treat
		// that as "every track was deleted".
		msg := fmt.Sprintf("no audio files found, but %d tracks are indexed for this root; "+
			"the drive may not be mounted. No tracks were marked missing", presentBefore)
		return ScanFailed, msg, RootUnavailable, msg
	}

	if err := r.reconcile(ctx); err != nil {
		if ctx.Err() != nil {
			return ScanInterrupted, "scan interrupted during reconciliation", "", ""
		}
		return ScanFailed, "reconciling missing files: " + err.Error(), "", ""
	}
	if n := r.p.filesFailed.Load(); n > 0 {
		summary = fmt.Sprintf("metadata could not be read from %d file%s", n, plural(n, "", "s"))
	}
	return ScanCompleted, summary, RootAvailable, ""
}

func (r *scanRun) loadIndex(ctx context.Context) (map[string]*indexed, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, rel_path, file_size, file_mtime_ns, status, metadata_status, duration_ms,
		metadata_version FROM tracks WHERE root_id = ?`, r.root.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]*indexed{}
	for rows.Next() {
		var rel string
		t := &indexed{}
		if err := rows.Scan(&t.id, &rel, &t.size, &t.mtime, &t.status, &t.metadataStatus, &t.durationMS, &t.version); err != nil {
			return nil, err
		}
		idx[rel] = t
	}
	return idx, rows.Err()
}

// pipeline runs the walker, a bounded pool of metadata readers, and a single
// database writer. Only the writer touches the database, in short batched
// transactions; no transaction is open while files are read.
func (r *scanRun) pipeline(ctx context.Context) error {
	jobs := make(chan job, r.s.workers*4)
	outcomes := make(chan outcome, r.s.workers*4)

	go func() {
		defer close(jobs)
		r.walk(ctx, jobs)
	}()

	var wg sync.WaitGroup
	for i := 0; i < r.s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue // drain
				}
				o := outcome{job: j}
				if !j.restoreOnly {
					meta, ok := r.extractBounded(ctx, j)
					if !ok {
						continue // cancelled: the scan is being interrupted
					}
					o.meta = meta
					if o.meta.Status == metadata.StatusError {
						if _, err := os.Lstat(j.abs); errors.Is(err, fs.ErrNotExist) {
							o.vanished = true
						}
					}
				}
				outcomes <- o
			}
		}()
	}
	go func() {
		wg.Wait()
		close(outcomes)
	}()

	return r.write(ctx, outcomes)
}

// DefaultExtractTimeout bounds reading one file's metadata. TagLib normally
// needs milliseconds, even on slow USB drives; this only catches a file that
// makes it hang.
const DefaultExtractTimeout = 2 * time.Minute

// cancelGrace is how long an interrupted scan waits for a read in progress.
var cancelGrace = 5 * time.Second

// maxTimedOut bounds how many timed-out files are remembered.
const maxTimedOut = 1024

type timedOutKey struct {
	path        string
	size, mtime int64
}

// extractBounded reads a file's metadata with a deadline. TagLib runs in
// WebAssembly and cannot be interrupted, so a read that overruns is
// abandoned: the file is recorded as unreadable, the worker moves on, and the
// file is not read again until it changes or Rewind restarts. ok is false
// when ctx was cancelled (the outcome must then be dropped).
func (r *scanRun) extractBounded(ctx context.Context, j job) (meta metadata.Metadata, ok bool) {
	key := timedOutKey{path: j.abs, size: j.size, mtime: j.mtime}
	r.s.timedOutMu.Lock()
	skip := r.s.timedOut[key]
	r.s.timedOutMu.Unlock()
	if skip {
		return timedOutMetadata(j.abs, "reading metadata timed out earlier; skipped until the file changes or Rewind restarts"), true
	}

	done := make(chan metadata.Metadata, 1) // buffered: an abandoned read can still finish
	go func() { done <- r.s.extract(j.abs) }()
	timer := time.NewTimer(r.s.extractTimeout)
	defer timer.Stop()
	select {
	case m := <-done:
		return m, true
	case <-ctx.Done():
		// Let a normal read finish (milliseconds), so no file is still being
		// read when shutdown continues, but never wait long for a stuck one.
		select {
		case <-done:
		case <-time.After(cancelGrace):
		}
		return metadata.Metadata{}, false
	case <-timer.C:
		r.s.timedOutMu.Lock()
		if len(r.s.timedOut) < maxTimedOut {
			r.s.timedOut[key] = true
		}
		r.s.timedOutMu.Unlock()
		r.s.logger.Warn("reading a file's metadata took too long; it is indexed without tags and skipped until it changes",
			"root_id", r.root.ID, "file", j.rel, "timeout", r.s.extractTimeout.String())
		return timedOutMetadata(j.abs, "reading metadata timed out"), true
	}
}

func timedOutMetadata(path, msg string) metadata.Metadata {
	return metadata.Metadata{
		Format: metadata.FormatOf(path), Title: metadata.FallbackTitle(path),
		Status: metadata.StatusError, Error: msg,
	}
}

// walk enumerates the root. Symlinks are never followed, hidden entries and
// common system folders are skipped, and errors below the root are recorded
// so the scan is reported as partial.
func (r *scanRun) walk(ctx context.Context, jobs chan<- job) {
	_ = filepath.WalkDir(r.root.Path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p == r.root.Path {
				r.rootErr = err
				return err
			}
			r.p.dirsFailed.Add(1)
			if len(r.traversal) < 5 {
				r.traversal = append(r.traversal, r.relOrBase(p)+": "+describeFSError(err))
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == r.root.Path {
			return nil
		}
		name := d.Name()
		if ignoredName(name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			r.p.filesSkipped.Add(1)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			r.p.filesSkipped.Add(1)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || metadata.FormatOf(name) == "" {
			r.p.filesSkipped.Add(1)
			return nil
		}

		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed between listing and stat: simply not present
		}
		if err != nil {
			r.p.dirsFailed.Add(1)
			if len(r.traversal) < 5 {
				r.traversal = append(r.traversal, r.relOrBase(p)+": "+describeFSError(err))
			}
			return nil
		}
		rel, err := filepath.Rel(r.root.Path, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !utf8.ValidString(rel) {
			r.p.filesFailed.Add(1)
			r.logFileError(rel, "file name is not valid UTF-8; skipped")
			return nil
		}

		r.p.filesFound.Add(1)
		r.seen[rel] = struct{}{}
		size, mtime := info.Size(), info.ModTime().UnixNano()
		old := r.index[rel]
		// Files read by an older extractor are read again once, so new
		// fields (such as composer and lyrics) are filled in.
		if old != nil && old.size == size && old.mtime == mtime && old.metadataStatus != string(metadata.StatusError) &&
			old.version >= metadata.Version {
			if old.status == "present" {
				r.p.unchanged.Add(1)
				return nil
			}
			return send(ctx, jobs, job{rel: rel, abs: p, size: size, mtime: mtime, old: old, restoreOnly: true})
		}
		return send(ctx, jobs, job{rel: rel, abs: p, size: size, mtime: mtime, old: old})
	})
}

func send(ctx context.Context, jobs chan<- job, j job) error {
	select {
	case jobs <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// write commits outcomes in batches.
func (r *scanRun) write(ctx context.Context, outcomes <-chan outcome) error {
	var batch []outcome
	var firstErr error
	timer := time.NewTimer(flushInterval)
	defer timer.Stop()
	flush := func() {
		if len(batch) == 0 || firstErr != nil {
			batch = batch[:0]
			return
		}
		// Use a context that survives shutdown so already-read results are
		// saved; the batch is small, so this finishes quickly.
		if err := r.commit(context.WithoutCancel(ctx), batch); err != nil {
			firstErr = err
		}
		batch = batch[:0]
	}
	for {
		select {
		case o, ok := <-outcomes:
			if !ok {
				flush()
				return firstErr
			}
			if o.vanished {
				r.vanished[o.rel] = struct{}{}
				continue
			}
			batch = append(batch, o)
			if len(batch) >= writeBatch {
				flush()
			}
		case <-timer.C:
			flush()
			timer.Reset(flushInterval)
		}
	}
}

const trackColumns = `rel_path, file_size, file_mtime_ns, format, codec, title, artist, album, album_artist,
	genre, release_date, year, track_number, disc_number, duration_ms, metadata_status, metadata_error,
	composer, lyrics, metadata_version`

// trackColumnCount is the number of columns in trackColumns.
const trackColumnCount = 20

func (r *scanRun) commit(ctx context.Context, batch []outcome) error {
	now := r.s.now().Unix()
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var added []newTrack
	var counts struct{ added, updated, restored, failed int64 }
	for _, o := range batch {
		if o.restoreOnly {
			if _, err := tx.ExecContext(ctx,
				"UPDATE tracks SET status = 'present', missing_since = NULL, updated_at = ? WHERE id = ?", now, o.old.id); err != nil {
				return err
			}
			counts.restored++
			continue
		}
		m := o.meta
		if m.Status == metadata.StatusError {
			counts.failed++
			r.logFileError(o.rel, m.Error)
		}
		vals := []any{o.rel, o.size, o.mtime, m.Format, nullIfEmpty(m.Codec), m.Title, nullIfEmpty(m.Artist),
			nullIfEmpty(m.Album), nullIfEmpty(m.AlbumArtist), nullIfEmpty(m.Genre), nullIfEmpty(m.Date),
			m.Year, m.TrackNumber, m.DiscNumber, m.DurationMS, string(m.Status), nullIfEmpty(m.Error),
			nullIfEmpty(m.Composer), nullIfEmpty(m.Lyrics), metadata.Version}
		if o.old != nil {
			_, err := tx.ExecContext(ctx, `UPDATE tracks SET (`+trackColumns+`, status, missing_since, updated_at, revision)
				= (`+placeholders(trackColumnCount)+`, 'present', NULL, ?, revision + 1) WHERE id = ?`,
				append(vals, now, o.old.id)...)
			if err != nil {
				return err
			}
			// Metadata edits made in Rewind win over the file's tags.
			if err := ReapplyEdits(ctx, tx, o.old.id, now); err != nil {
				return err
			}
			counts.updated++
			if o.old.status == "missing" {
				counts.restored++
			}
			continue
		}
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO tracks (root_id, `+trackColumns+`, status, first_seen_at, updated_at)
			VALUES (?, `+placeholders(trackColumnCount)+`, 'present', ?, ?) RETURNING id`,
			append(append([]any{r.root.ID}, vals...), now, now)...).Scan(&id)
		if err != nil {
			return err
		}
		added = append(added, newTrack{id: id, size: o.size, mtime: o.mtime, durationMS: nullInt(m.DurationMS)})
		counts.added++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.added = append(r.added, added...)
	r.p.added.Add(counts.added)
	r.p.updated.Add(counts.updated)
	r.p.restored.Add(counts.restored)
	r.p.filesFailed.Add(counts.failed)
	return nil
}

// reconcile runs after a complete traversal. Indexed tracks that were not
// seen are either matched to a newly added file (a rename: the old track ID
// is kept) or marked missing. Only the merged duplicate rows of renames are
// deleted (see mergeRename).
func (r *scanRun) reconcile(ctx context.Context) error {
	var gone []*indexed
	for rel, t := range r.index {
		_, seen := r.seen[rel]
		_, vanished := r.vanished[rel]
		if t.status == "present" && (!seen || vanished) {
			gone = append(gone, t)
		}
	}
	if len(gone) == 0 {
		return nil
	}

	// Rename detection: a disappeared track and a new file match if size,
	// modification time and duration are equal, and the match is unique on
	// both sides. Renames and moves within a filesystem keep the
	// modification time; ambiguous cases are left as missing + added.
	type key struct {
		size, mtime int64
		dur         sql.NullInt64
	}
	goneBy := map[key][]*indexed{}
	for _, t := range gone {
		k := key{t.size, t.mtime, t.durationMS}
		goneBy[k] = append(goneBy[k], t)
	}
	addedBy := map[key][]newTrack{}
	for _, n := range r.added {
		k := key{n.size, n.mtime, n.durationMS}
		addedBy[k] = append(addedBy[k], n)
	}
	renamedOld := map[int64]bool{}
	now := r.s.now().Unix()

	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var renamed, missing int64
	for k, olds := range goneBy {
		news := addedBy[k]
		if len(olds) != 1 || len(news) != 1 {
			continue
		}
		if err := mergeRename(ctx, tx, olds[0].id, news[0].id, now); err != nil {
			return err
		}
		renamedOld[olds[0].id] = true
		renamed++
	}
	for _, t := range gone {
		if renamedOld[t.id] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE tracks SET status = 'missing', missing_since = ?, updated_at = ? WHERE id = ?", now, now, t.id); err != nil {
			return err
		}
		missing++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.p.renamed.Add(renamed)
	r.p.added.Add(-renamed)
	r.p.missing.Add(missing)
	return nil
}

// mergeRename moves the new row's location and metadata onto the old row
// (keeping the old ID) and removes the new row. The new row was visible
// during the scan, so it may already be in a playlist: those entries move to
// the old ID, keeping their positions. Where a playlist already holds the old
// ID, the duplicate entry is removed with the new row (foreign-key cascade).
//
// Favorites and listening history recorded for the new row move to the old
// ID as well, and metadata edits of the old row are applied again on top of
// the file's tags.
func mergeRename(ctx context.Context, tx *sql.Tx, oldID, newID, now int64) error {
	vals := make([]any, trackColumnCount)
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := tx.QueryRowContext(ctx, "SELECT "+trackColumns+" FROM tracks WHERE id = ?", newID).Scan(ptrs...); err != nil {
		return err
	}
	for _, q := range []string{
		"UPDATE OR IGNORE playlist_tracks SET track_id = ? WHERE track_id = ?",
		"UPDATE OR IGNORE favorites SET track_id = ? WHERE track_id = ?",
		"UPDATE play_history SET track_id = ? WHERE track_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, oldID, newID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tracks WHERE id = ?", newID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tracks SET (`+trackColumns+`, status, missing_since, updated_at, revision)
		= (`+placeholders(trackColumnCount)+`, 'present', NULL, ?, revision + 1) WHERE id = ?`,
		append(vals, now, oldID)...); err != nil {
		return err
	}
	return ReapplyEdits(ctx, tx, oldID, now)
}

// placeholders returns n comma-separated "?".
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func (r *scanRun) logFileError(rel, msg string) {
	switch n := r.fileErrors.Add(1); {
	case n <= maxLoggedFileErrors:
		r.s.logger.Warn("could not read music file metadata", "root_id", r.root.ID, "file", rel, "reason", msg)
	case n == maxLoggedFileErrors+1:
		r.s.logger.Warn("further metadata errors in this scan are not logged individually; see the scan summary", "root_id", r.root.ID)
	}
}

func (r *scanRun) relOrBase(p string) string {
	if rel, err := filepath.Rel(r.root.Path, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(p)
}

// ignoredName reports entries that are never part of a music library:
// hidden files and folders (including macOS "._" resource forks), and common
// operating-system folders found on removable drives.
func ignoredName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "$recycle.bin", "system volume information", "lost+found":
		return true
	}
	return false
}

func describeFSError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return truncateRunes(pe.Err.Error(), 200)
	}
	return truncateRunes(err.Error(), 200)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
