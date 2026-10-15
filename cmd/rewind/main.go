// SPDX-License-Identifier: Apache-2.0

// Command rewind runs the Rewind music server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/config"
	"github.com/shamil-tp/rewind/internal/db"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/metadata"
	"github.com/shamil-tp/rewind/internal/plugin"
	"github.com/shamil-tp/rewind/internal/prefs"
	"github.com/shamil-tp/rewind/internal/server"
	"github.com/shamil-tp/rewind/internal/theme"
	"github.com/shamil-tp/rewind/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// pluginShutdownTimeout bounds how long stopping plugins may take.
const pluginShutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "rewind: %v\n", err)
		os.Exit(1)
	}
}

// run wires the application together and blocks until ctx is cancelled or the
// server fails.
func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger := newLogger(cfg, logOut)
	logger.Info("starting rewind", "version", version, "frontend_embedded", web.Embedded, "data_dir", cfg.DataDir)

	// Before anything reads metadata: the metadata library keeps compiled
	// code in the temporary directory, which must not be a shared one.
	// Removed after the scanner has stopped (defers run in reverse).
	cleanupTemp, err := metadata.UsePrivateTempDir()
	if err != nil {
		return err
	}
	defer cleanupTemp()

	created, err := db.EnsureDir(cfg.DataDir)
	if err != nil {
		return err
	}
	if created {
		logger.Info("created data directory", "path", cfg.DataDir)
	}
	conn, err := db.Open(ctx, filepath.Join(cfg.DataDir, db.FileName), logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("closing database", "err", err)
		} else {
			logger.Info("database closed")
		}
	}()

	authSvc, err := auth.NewService(ctx, auth.Options{
		DB: conn, Lifetime: cfg.SessionLifetime, IdleTimeout: cfg.SessionIdleTimeout, Logger: logger,
	})
	if err != nil {
		return err
	}
	setupCode, err := prepareSetup(ctx, authSvc, cfg.DataDir, logger)
	if err != nil {
		return err
	}

	// Plugins: compiled in, started only if installed and enabled. Closed
	// after the scanner (defers run in reverse), so no event arrives late.
	plugins := plugin.New(plugin.Options{
		Dir: cfg.PluginDir, Enabled: cfg.Plugins, Registry: plugin.Builtin(), Logger: logger,
	})
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), pluginShutdownTimeout)
		defer cancel()
		plugins.Close(cctx)
	}()

	scanner, err := prepareLibrary(ctx, conn, cfg, logger, plugins)
	if err != nil {
		return err
	}
	// Runs before the deferred database close: stops background scans
	// (recorded as interrupted) and waits for them.
	defer scanner.Close()

	// Themes: bundled ones plus any installed by the administrator. Read once;
	// installed themes that fail validation are logged and listed, never
	// fatal.
	themes, err := theme.Load(cfg.ThemeDir, logger)
	if err != nil {
		return err
	}
	preferences := prefs.NewStore(conn, prefs.WithThemes(themes.Has))

	frontend, err := web.Frontend()
	if err != nil {
		return fmt.Errorf("loading frontend: %w", err)
	}
	handler, err := server.New(server.Options{
		Frontend: frontend, Version: version,
		Auth: authSvc, SetupCode: setupCode, Limiter: auth.NewLimiter(),
		CookieSecure: cfg.CookieSecure, Logger: logger,
		Catalog: library.NewCatalog(conn), Scanner: scanner, MaxPageSize: cfg.MaxPageSize,
		Playlists: library.NewPlaylists(conn), Preferences: preferences,
		Themes:       theme.NewStore(conn, themes, preferences),
		TrustedHosts: cfg.AllowedHosts(), Plugins: plugins,
	})
	if err != nil {
		return err
	}

	lns, err := server.Listen(cfg.Addrs, cfg.RemoteAccess)
	if err != nil {
		return err
	}
	logNetworkExposure(cfg, logger)
	if err := plugins.Start(ctx); err != nil {
		return err
	}
	if !web.Embedded {
		logger.Warn("serving the placeholder page instead of the frontend; build with scripts/build.sh for production")
	}

	// The startup scan runs in the background while the server is up, through
	// the same scanner queue as API-triggered rescans.
	if cfg.ScanOnStartup && len(cfg.LibraryRoots) > 0 {
		if _, err := scanner.Start(); err != nil && !errors.Is(err, library.ErrNoRoots) {
			logger.Error("startup scan not started", "err", err)
		}
	}

	return server.Serve(ctx, lns, handler, logger)
}

// logNetworkExposure states plainly at startup which addresses other devices
// can reach and which Host names are accepted, and warns about weaker setups.
func logNetworkExposure(cfg config.Config, logger *slog.Logger) {
	if hosts := cfg.AllowedHosts(); len(hosts) > 0 {
		logger.Info("accepting requests for these host names besides localhost", "hosts", strings.Join(hosts, ","))
	}
	exposed := cfg.ExposedAddrs()
	if len(exposed) == 0 {
		logger.Info("listening on loopback only; other devices cannot connect directly (see \"Network access\" in docs/deployment.md)")
		return
	}
	logger.Warn("remote access is enabled: other devices that can reach these addresses can open Rewind's sign-in page; "+
		"Rewind still requires a password", "addrs", strings.Join(exposed, ","))
	if cfg.ListensOnAllInterfaces() {
		logger.Warn("listening on ALL network interfaces, including any public ones; make sure a firewall blocks the port " +
			"from untrusted networks, or listen on a specific LAN or Tailscale address instead")
	}
	if !cfg.CookieSecure {
		logger.Warn("serving remote clients over plain HTTP: passwords and session cookies cross the network unencrypted " +
			"unless the network itself is encrypted (Tailscale is); prefer HTTPS with \"tailscale serve\" and REWIND_COOKIE_SECURE=true")
	}
}

// prepareLibrary registers the configured library roots, recovers scans left
// running by a previous process, and returns the scanner.
func prepareLibrary(ctx context.Context, conn *sql.DB, cfg config.Config, logger *slog.Logger, plugins *plugin.Manager) (*library.Scanner, error) {
	if n, err := library.RecoverInterruptedScans(ctx, conn, time.Now()); err != nil {
		return nil, fmt.Errorf("recovering interrupted scans: %w", err)
	} else if n > 0 {
		logger.Warn("marked scans left running by a previous process as interrupted", "count", n)
	}
	roots, problems, err := library.SyncRoots(ctx, conn, cfg.LibraryRoots, logger, time.Now())
	if err != nil {
		return nil, fmt.Errorf("registering library roots: %w", err)
	}
	if len(cfg.LibraryRoots) == 0 {
		logger.Info("no library roots configured; set REWIND_LIBRARY_ROOTS to index music")
	} else {
		logger.Info("library roots ready", "enabled", len(roots), "rejected", len(problems))
	}
	return library.NewScanner(library.Options{
		DB: conn, Logger: logger, Workers: cfg.ScanWorkers,
		OnFinished: func(r library.Result) { plugins.PublishScan(scanEvent(r)) },
	})
}

// scanEvent is the plugin view of a scan: IDs and counts only. The error
// summary is left out because it may contain file system paths.
func scanEvent(r library.Result) plugin.ScanEvent {
	c := r.Counts
	return plugin.ScanEvent{
		RootID: r.RootID, ScanID: r.ScanID, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		FilesFound: c.FilesFound, FilesFailed: c.FilesFailed, TracksAdded: c.TracksAdded, TracksUpdated: c.TracksUpdated,
		TracksRenamed: c.TracksRenamed, TracksMissing: c.TracksMissing, TracksRestored: c.TracksRestored,
	}
}

// prepareSetup creates the one-time setup code while no account exists, and
// removes any leftover code file otherwise.
func prepareSetup(ctx context.Context, svc *auth.Service, dataDir string, logger *slog.Logger) (*auth.SetupCode, error) {
	required, err := svc.SetupRequired(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading setup state: %w", err)
	}
	if !required {
		if err := auth.RemoveSetupCodeFile(dataDir); err != nil {
			logger.Warn("could not remove leftover setup code file", "err", err)
		}
		return nil, nil
	}
	code, err := auth.NewSetupCode(dataDir)
	if err != nil {
		return nil, err
	}
	logger.Warn("initial setup required: open Rewind in a browser on this machine and enter the setup code stored in the file",
		"file", code.Path())
	return code, nil
}

func newLogger(cfg config.Config, out io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(out, opts))
	}
	return slog.New(slog.NewTextHandler(out, opts))
}
