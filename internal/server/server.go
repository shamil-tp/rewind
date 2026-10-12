// SPDX-License-Identifier: Apache-2.0

// Package server contains Rewind's HTTP routes, middleware, and server
// lifecycle.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/config"
	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/plugin"
	"github.com/shamil-tp/rewind/internal/prefs"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
	// shutdownTimeout bounds how long in-flight requests may take to finish
	// after a termination signal before connections are closed forcibly.
	shutdownTimeout = 10 * time.Second
)

// Options configures the HTTP handler.
type Options struct {
	// Frontend holds the files served at "/". It must contain index.html.
	Frontend fs.FS
	// Version is reported by the health endpoint.
	Version string
	// Auth provides accounts and sessions.
	Auth *auth.Service
	// SetupCode is the one-time first-run code; nil when setup was already
	// complete at startup.
	SetupCode *auth.SetupCode
	// Limiter rate-limits authentication attempts.
	Limiter *auth.Limiter
	// CookieSecure marks the session cookie Secure (HTTPS deployments).
	CookieSecure bool
	Logger       *slog.Logger
	// Catalog answers library queries; Scanner runs rescans.
	Catalog *library.Catalog
	Scanner *library.Scanner
	// Playlists and Preferences store per-user data.
	Playlists   *library.Playlists
	Preferences *prefs.Store
	// MaxPageSize caps the limit parameter of paginated endpoints
	// (default config.DefaultMaxPageSize).
	MaxPageSize int
	// TrustedHosts are the Host header values accepted besides loopback names
	// (config.Config.AllowedHosts). Empty means loopback names only.
	TrustedHosts []string
	// Plugins reports plugin status; nil means no plugin system.
	Plugins *plugin.Manager
}

// New returns the root handler: API routes, the frontend, and the security
// middleware wrapped around both.
func New(opts Options) (http.Handler, error) {
	return newHandler(opts, nil)
}

// newHandler builds the handler. extra, used only by tests, may register
// additional routes and receives the authentication middleware.
func newHandler(opts Options, extra func(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler)) (http.Handler, error) {
	if opts.Auth == nil || opts.Limiter == nil || opts.Logger == nil || opts.Catalog == nil || opts.Scanner == nil ||
		opts.Playlists == nil || opts.Preferences == nil {
		return nil, errors.New("server: Auth, Limiter, Logger, Catalog, Scanner, Playlists and Preferences are required")
	}
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = config.DefaultMaxPageSize
	}
	frontend, err := newFrontendHandler(opts.Frontend)
	if err != nil {
		return nil, err
	}
	a := &authAPI{svc: opts.Auth, setupCode: opts.SetupCode, limiter: opts.Limiter, secure: opts.CookieSecure, logger: opts.Logger}

	mux := http.NewServeMux()
	mux.Handle("/api/health", allowMethods(healthHandler(opts.Version), http.MethodGet, http.MethodHead))

	mux.Handle("/api/auth/status", noStore(allowMethods(http.HandlerFunc(a.status), http.MethodGet, http.MethodHead)))
	mux.Handle("/api/auth/setup", noStore(allowMethods(http.HandlerFunc(a.setup), http.MethodPost)))
	mux.Handle("/api/auth/login", noStore(allowMethods(http.HandlerFunc(a.login), http.MethodPost)))
	mux.Handle("/api/auth/logout", noStore(allowMethods(http.HandlerFunc(a.logout), http.MethodPost)))
	mux.Handle("/api/auth/me", noStore(allowMethods(a.requireAuth(http.HandlerFunc(a.me)), http.MethodGet, http.MethodHead)))
	mux.Handle("/api/auth/change-password", noStore(allowMethods(a.requireAuth(http.HandlerFunc(a.changePassword)), http.MethodPost)))

	// Library API: every route requires a session. The only account is the
	// owner, so the session is also the authorization for rescans.
	l := &libraryAPI{catalog: opts.Catalog, scanner: opts.Scanner, maxPageSize: opts.MaxPageSize, logger: opts.Logger}
	protected := func(h http.HandlerFunc, methods ...string) http.Handler {
		return noStore(allowMethods(a.requireAuth(h), methods...))
	}
	mux.Handle("/api/library", protected(l.summary, http.MethodGet, http.MethodHead))
	mux.Handle("/api/library/roots", protected(l.roots, http.MethodGet, http.MethodHead))
	mux.Handle("/api/library/scan", protected(l.scan, http.MethodPost))
	mux.Handle("/api/tracks", protected(l.tracks, http.MethodGet, http.MethodHead))
	mux.Handle("/api/tracks/{id}", protected(l.track, http.MethodGet, http.MethodHead))
	// Audio responses manage their own caching headers, so no noStore here.
	mux.Handle("/api/tracks/{id}/stream", allowMethods(a.requireAuth(http.HandlerFunc(l.stream)), http.MethodGet, http.MethodHead))

	// Playlists and preferences belong to the signed-in user.
	p := &playlistAPI{playlists: opts.Playlists, prefs: opts.Preferences, logger: opts.Logger}
	mux.Handle("/api/playlists", protected(p.collection, http.MethodGet, http.MethodHead, http.MethodPost))
	mux.Handle("/api/playlists/{id}", protected(p.item, http.MethodGet, http.MethodHead, http.MethodPatch, http.MethodDelete))
	mux.Handle("/api/playlists/{id}/tracks", protected(p.tracks, http.MethodPost, http.MethodPut))
	mux.Handle("/api/playlists/{id}/tracks/{trackId}", protected(p.entry, http.MethodDelete))
	mux.Handle("/api/preferences", protected(p.preferences, http.MethodGet, http.MethodHead, http.MethodPatch))

	// Plugin status: read-only. Plugins cannot register routes.
	mux.Handle("/api/plugins", protected(pluginStatus(opts.Plugins), http.MethodGet, http.MethodHead))

	if extra != nil {
		extra(mux, a.requireAuth)
	}

	// Unknown API paths get a JSON 404, never the frontend's index.html.
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/api", apiNotFound)
	mux.Handle("/", frontend)

	return boundWrites(securityHeaders(requireTrustedHost(opts.TrustedHosts, opts.Logger, crossOriginProtection(limitRequestBody(mux))))), nil
}

// Listen opens a listener for every address (already validated by the config
// package) and verifies each bound socket: unless allowRemote is true it must
// be a loopback address. This guards against host names such as "localhost"
// resolving to a non-loopback address. If any address fails, the listeners
// opened so far are closed and nothing is served.
func Listen(addrs []string, allowRemote bool) ([]net.Listener, error) {
	if len(addrs) == 0 {
		return nil, errors.New("no listen address configured")
	}
	var lns []net.Listener
	fail := func(err error) ([]net.Listener, error) {
		for _, ln := range lns {
			ln.Close()
		}
		return nil, err
	}
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fail(fmt.Errorf("cannot listen on %s: %w (is another process using this port, or is the address not "+
				"assigned to this machine yet? set REWIND_ADDR to change it)", addr, err))
		}
		lns = append(lns, ln)
		if err := checkBound(ln.Addr(), allowRemote); err != nil {
			return fail(err)
		}
	}
	return lns, nil
}

// checkBound verifies the address a listener actually bound to.
func checkBound(a net.Addr, allowRemote bool) error {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("refusing to serve on %s: not a TCP address", a)
	}
	if !allowRemote && !tcp.IP.IsLoopback() {
		return fmt.Errorf("refusing to serve on %s: it is not a loopback address and REWIND_REMOTE_ACCESS is not enabled", a)
	}
	return nil
}

// Serve runs one HTTP server on all listeners until ctx is cancelled, then
// shuts it down gracefully, giving in-flight requests up to shutdownTimeout to
// finish. If any listener fails, the server stops and the error is returned.
func Serve(ctx context.Context, lns []net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		// The read deadline stays in force while a handler runs, so net/http
		// cancels r.Context() readTimeout after a request started, even while
		// its response is still being written. Long audio streams must not
		// depend on the request context after they open the file.
		ReadTimeout: readTimeout,
		// No global WriteTimeout: it would cut off long audio streams. Write
		// deadlines are set per response instead (deadlines.go).
		IdleTimeout:    idleTimeout,
		MaxHeaderBytes: maxHeaderBytes,
		ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, len(lns))
	for _, ln := range lns {
		go func() { serveErr <- srv.Serve(ln) }()
		logger.Info("server listening", "addr", "http://"+ln.Addr().String())
	}
	pending := len(lns)

	select {
	case err := <-serveErr:
		pending--
		srv.Close()
		for ; pending > 0; pending-- {
			<-serveErr
		}
		return fmt.Errorf("http server stopped unexpectedly: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
		return fmt.Errorf("graceful shutdown did not finish within %s: %w", shutdownTimeout, err)
	}
	for ; pending > 0; pending-- {
		if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}
	logger.Info("server stopped")
	return nil
}
