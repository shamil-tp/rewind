// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates Rewind's configuration from environment
// variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Defaults used when the corresponding variable is not set.
const (
	DefaultAddr               = "127.0.0.1:8090"
	DefaultDataDir            = "data"
	DefaultSessionLifetime    = 30 * 24 * time.Hour
	DefaultSessionIdleTimeout = 7 * 24 * time.Hour
	DefaultScanWorkers        = 2
	maxScanWorkers            = 8
	DefaultMaxPageSize        = 200
	minMaxPageSize            = 10
	maxMaxPageSize            = 1000
)

// Bounds for session durations.
const (
	minSessionDuration = 5 * time.Minute
	maxSessionLifetime = 365 * 24 * time.Hour
)

// Config is the validated application configuration.
type Config struct {
	// Addrs are the host:port addresses the HTTP server listens on. By
	// default a single loopback address. Non-loopback addresses require
	// RemoteAccess, and wildcard addresses also ListenAllInterfaces (see
	// network.go and docs/deployment.md, "Network access").
	Addrs []string
	// RemoteAccess permits non-loopback listen addresses (REWIND_REMOTE_ACCESS).
	RemoteAccess bool
	// ListenAllInterfaces permits 0.0.0.0, [::] or an empty host
	// (REWIND_LISTEN_ALL_INTERFACES); it has no effect without RemoteAccess.
	ListenAllInterfaces bool
	// TrustedHosts are extra Host header values accepted besides loopback
	// names and the IP addresses Rewind listens on (REWIND_TRUSTED_HOSTS).
	TrustedHosts []string
	// LogLevel is the minimum level written to the log.
	LogLevel slog.Level
	// LogJSON selects JSON log output instead of text.
	LogJSON bool
	// DataDir is the absolute path of the application-data directory, which
	// holds the SQLite database. It never contains music.
	DataDir string
	// SessionLifetime is the absolute lifetime of a login session.
	SessionLifetime time.Duration
	// SessionIdleTimeout ends a session that has not been used for this long.
	SessionIdleTimeout time.Duration
	// CookieSecure marks the session cookie Secure (HTTPS only).
	CookieSecure bool
	// LibraryRoots are the absolute music directories to index.
	LibraryRoots []string
	// ScanOnStartup scans all library roots in the background at startup.
	ScanOnStartup bool
	// ScanWorkers is the number of concurrent metadata readers during a scan.
	ScanWorkers int
	// MaxPageSize caps the `limit` parameter of paginated API endpoints.
	MaxPageSize int
	// PluginDir holds installed plugin manifests (REWIND_PLUGIN_DIR); empty
	// turns plugins off. Plugins lists the enabled plugin IDs
	// (REWIND_PLUGINS). See docs/plugins.md.
	PluginDir string
	Plugins   []string
}

// Load reads the configuration through getenv (normally os.Getenv). All
// problems are reported together so they can be fixed in one pass.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		LogLevel:           slog.LevelInfo,
		DataDir:            DefaultDataDir,
		SessionLifetime:    DefaultSessionLifetime,
		SessionIdleTimeout: DefaultSessionIdleTimeout,
		ScanOnStartup:      true,
		ScanWorkers:        DefaultScanWorkers,
		MaxPageSize:        DefaultMaxPageSize,
	}
	var errs []error

	for _, f := range []struct {
		name string
		dst  *bool
	}{
		{"REWIND_REMOTE_ACCESS", &cfg.RemoteAccess},
		{"REWIND_LISTEN_ALL_INTERFACES", &cfg.ListenAllInterfaces},
	} {
		name, dst := f.name, f.dst
		if v := strings.TrimSpace(getenv(name)); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s=%q is not valid; use true or false", name, v))
			} else {
				*dst = b
			}
		}
	}
	addrValue := DefaultAddr
	if v := strings.TrimSpace(getenv("REWIND_ADDR")); v != "" {
		addrValue = v
	}
	addrs, addrErrs := parseAddrs(addrValue, cfg.RemoteAccess, cfg.ListenAllInterfaces)
	cfg.Addrs = addrs
	errs = append(errs, addrErrs...)

	hosts, hostErrs := parseTrustedHosts(getenv("REWIND_TRUSTED_HOSTS"))
	cfg.TrustedHosts = hosts
	errs = append(errs, hostErrs...)

	if v := strings.TrimSpace(getenv("REWIND_LOG_LEVEL")); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			errs = append(errs, fmt.Errorf("REWIND_LOG_LEVEL=%q is not valid; use debug, info, warn or error", v))
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_LOG_FORMAT")); v != "" {
		switch strings.ToLower(v) {
		case "text":
			cfg.LogJSON = false
		case "json":
			cfg.LogJSON = true
		default:
			errs = append(errs, fmt.Errorf("REWIND_LOG_FORMAT=%q is not valid; use text or json", v))
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_DATA_DIR")); v != "" {
		cfg.DataDir = v
	}
	if abs, err := filepath.Abs(cfg.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("REWIND_DATA_DIR=%q: cannot resolve path: %v", cfg.DataDir, err))
	} else {
		cfg.DataDir = abs
	}

	if v := strings.TrimSpace(getenv("REWIND_SESSION_LIFETIME")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minSessionDuration || d > maxSessionLifetime {
			errs = append(errs, fmt.Errorf("REWIND_SESSION_LIFETIME=%q is not valid; use a duration from 5m to 8760h, for example 720h", v))
		} else {
			cfg.SessionLifetime = d
		}
	}
	if v := strings.TrimSpace(getenv("REWIND_SESSION_IDLE_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minSessionDuration {
			errs = append(errs, fmt.Errorf("REWIND_SESSION_IDLE_TIMEOUT=%q is not valid; use a duration of at least 5m, for example 168h", v))
		} else {
			cfg.SessionIdleTimeout = d
		}
	}
	if cfg.SessionIdleTimeout > cfg.SessionLifetime {
		cfg.SessionIdleTimeout = cfg.SessionLifetime
	}

	if v := strings.TrimSpace(getenv("REWIND_COOKIE_SECURE")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("REWIND_COOKIE_SECURE=%q is not valid; use true or false", v))
		} else {
			cfg.CookieSecure = b
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_LIBRARY_ROOTS")); v != "" {
		for _, p := range filepath.SplitList(v) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				errs = append(errs, fmt.Errorf("REWIND_LIBRARY_ROOTS: %q is not an absolute path", p))
				continue
			}
			cfg.LibraryRoots = append(cfg.LibraryRoots, filepath.Clean(p))
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_SCAN_ON_STARTUP")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("REWIND_SCAN_ON_STARTUP=%q is not valid; use true or false", v))
		} else {
			cfg.ScanOnStartup = b
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_SCAN_WORKERS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxScanWorkers {
			errs = append(errs, fmt.Errorf("REWIND_SCAN_WORKERS=%q is not valid; use a number from 1 to %d", v, maxScanWorkers))
		} else {
			cfg.ScanWorkers = n
		}
	}

	if v := strings.TrimSpace(getenv("REWIND_API_MAX_PAGE_SIZE")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minMaxPageSize || n > maxMaxPageSize {
			errs = append(errs, fmt.Errorf("REWIND_API_MAX_PAGE_SIZE=%q is not valid; use a number from %d to %d", v, minMaxPageSize, maxMaxPageSize))
		} else {
			cfg.MaxPageSize = n
		}
	}

	errs = append(errs, parsePlugins(getenv, &cfg)...)

	return cfg, errors.Join(errs...)
}
