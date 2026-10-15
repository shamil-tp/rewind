// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Addrs) != 1 || cfg.Addrs[0] != DefaultAddr {
		t.Errorf("Addrs = %q, want [%q]", cfg.Addrs, DefaultAddr)
	}
	if cfg.RemoteAccess || cfg.ListenAllInterfaces || len(cfg.TrustedHosts) != 0 || len(cfg.ExposedAddrs()) != 0 {
		t.Errorf("remote access is not off by default: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogJSON {
		t.Errorf("logging = (%v, json=%v), want (INFO, json=false)", cfg.LogLevel, cfg.LogJSON)
	}
}

func TestLoadAcceptsLoopbackAddrs(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8090":     "127.0.0.1:8090",
		"127.0.0.1:1":        "127.0.0.1:1",
		"127.0.0.1:65535":    "127.0.0.1:65535",
		"127.0.0.5:9000":     "127.0.0.5:9000",
		"localhost:9000":     "localhost:9000",
		"LOCALHOST:9000":     "localhost:9000",
		"[::1]:8090":         "[::1]:8090",
		"  127.0.0.1:8090  ": "127.0.0.1:8090",
	} {
		cfg, err := Load(env(map[string]string{"REWIND_ADDR": addr}))
		if err != nil {
			t.Errorf("Load(REWIND_ADDR=%q) error = %v, want nil", addr, err)
			continue
		}
		if len(cfg.Addrs) != 1 || cfg.Addrs[0] != want {
			t.Errorf("Load(REWIND_ADDR=%q).Addrs = %q, want [%q]", addr, cfg.Addrs, want)
		}
	}
}

func TestLoadRejectsNonLoopbackAddrs(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:8090",      // IPv4 wildcard
		"[::]:8090",         // IPv6 wildcard
		":8090",             // empty host = all interfaces
		"192.168.1.20:8090", // LAN
		"10.0.0.5:8090",     // LAN
		"100.64.0.1:8090",   // Tailscale CGNAT range
		"[fd7a:115c:a1e0::1]:8090",
		"example.com:8090",
	} {
		_, err := Load(env(map[string]string{"REWIND_ADDR": addr}))
		if err == nil || !strings.Contains(err.Error(), "REWIND_REMOTE_ACCESS=true") && !strings.Contains(err.Error(), "use an IP address") {
			t.Errorf("Load(REWIND_ADDR=%q) error = %v, want a remote-access-required error", addr, err)
		}
	}
}

func TestLoadRejectsMalformedAddrs(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1",       // no port
		"127.0.0.1:0",     // port 0 picks a random port
		"127.0.0.1:65536", // out of range
		"127.0.0.1:http",  // not numeric
		"not an address",
	} {
		if _, err := Load(env(map[string]string{"REWIND_ADDR": addr})); err == nil {
			t.Errorf("Load(REWIND_ADDR=%q) error = nil, want error", addr)
		}
	}
}

func TestLoadLogging(t *testing.T) {
	cfg, err := Load(env(map[string]string{"REWIND_LOG_LEVEL": "DEBUG", "REWIND_LOG_FORMAT": "json"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug || !cfg.LogJSON {
		t.Errorf("logging = (%v, json=%v), want (DEBUG, json=true)", cfg.LogLevel, cfg.LogJSON)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{
		"REWIND_ADDR":       "0.0.0.0:8090",
		"REWIND_LOG_LEVEL":  "verbose",
		"REWIND_LOG_FORMAT": "xml",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, want := range []string{"REWIND_ADDR", "REWIND_LOG_LEVEL", "REWIND_LOG_FORMAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true,
		"": false, "0.0.0.0": false, "::": false, "192.168.1.1": false, "localhost.evil.example": false,
	}
	for host, want := range cases {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestLoadDataDirAndSessionDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !filepath.IsAbs(cfg.DataDir) || filepath.Base(cfg.DataDir) != DefaultDataDir {
		t.Errorf("DataDir = %q, want absolute path ending in %q", cfg.DataDir, DefaultDataDir)
	}
	if cfg.SessionLifetime != DefaultSessionLifetime || cfg.SessionIdleTimeout != DefaultSessionIdleTimeout {
		t.Errorf("sessions = (%v, %v), want defaults", cfg.SessionLifetime, cfg.SessionIdleTimeout)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false by default (plain-HTTP loopback)")
	}
}

func TestLoadSessionAndCookieSettings(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(env(map[string]string{
		"REWIND_DATA_DIR":             dir,
		"REWIND_SESSION_LIFETIME":     "48h",
		"REWIND_SESSION_IDLE_TIMEOUT": "2h",
		"REWIND_COOKIE_SECURE":        "true",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DataDir != dir || cfg.SessionLifetime != 48*time.Hour || cfg.SessionIdleTimeout != 2*time.Hour || !cfg.CookieSecure {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadIdleTimeoutCappedAtLifetime(t *testing.T) {
	cfg, err := Load(env(map[string]string{"REWIND_SESSION_LIFETIME": "1h", "REWIND_SESSION_IDLE_TIMEOUT": "24h"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SessionIdleTimeout != time.Hour {
		t.Errorf("SessionIdleTimeout = %v, want capped to 1h", cfg.SessionIdleTimeout)
	}
}

func TestLoadRejectsInvalidSessionAndCookieSettings(t *testing.T) {
	for k, v := range map[string]string{
		"REWIND_SESSION_LIFETIME":     "1m",
		"REWIND_SESSION_IDLE_TIMEOUT": "forever",
		"REWIND_COOKIE_SECURE":        "maybe",
	} {
		_, err := Load(env(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("Load(%s=%q) error = %v, want error naming %s", k, v, err, k)
		}
	}
	if _, err := Load(env(map[string]string{"REWIND_SESSION_LIFETIME": "9000h"})); err == nil {
		t.Error("Load(REWIND_SESSION_LIFETIME=9000h) error = nil, want error")
	}
}

func TestLoadLibrarySettings(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LibraryRoots) != 0 || !cfg.ScanOnStartup || cfg.ScanWorkers != DefaultScanWorkers {
		t.Errorf("defaults = %+v", cfg)
	}

	a, b := filepath.Join(t.TempDir(), "usb"), filepath.Join(t.TempDir(), "music")
	list := a + string(filepath.ListSeparator) + " " + b + string(filepath.Separator) + string(filepath.ListSeparator)
	cfg, err = Load(env(map[string]string{
		"REWIND_LIBRARY_ROOTS":   list,
		"REWIND_SCAN_ON_STARTUP": "false",
		"REWIND_SCAN_WORKERS":    "4",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LibraryRoots) != 2 || cfg.LibraryRoots[0] != a || cfg.LibraryRoots[1] != b {
		t.Errorf("LibraryRoots = %q, want [%q %q]", cfg.LibraryRoots, a, b)
	}
	if cfg.ScanOnStartup || cfg.ScanWorkers != 4 {
		t.Errorf("scan settings = %v, %d", cfg.ScanOnStartup, cfg.ScanWorkers)
	}
}

func TestLoadRejectsInvalidLibrarySettings(t *testing.T) {
	for k, v := range map[string]string{
		"REWIND_LIBRARY_ROOTS":   "relative/music",
		"REWIND_SCAN_ON_STARTUP": "sometimes",
		"REWIND_SCAN_WORKERS":    "0",
	} {
		_, err := Load(env(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("Load(%s=%q) error = %v", k, v, err)
		}
	}
	if _, err := Load(env(map[string]string{"REWIND_SCAN_WORKERS": "9"})); err == nil {
		t.Error("REWIND_SCAN_WORKERS=9 accepted")
	}
}

func TestLoadMaxPageSize(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.MaxPageSize != DefaultMaxPageSize {
		t.Fatalf("default MaxPageSize = %d, %v", cfg.MaxPageSize, err)
	}
	cfg, err = Load(env(map[string]string{"REWIND_API_MAX_PAGE_SIZE": "500"}))
	if err != nil || cfg.MaxPageSize != 500 {
		t.Errorf("MaxPageSize = %d, %v; want 500", cfg.MaxPageSize, err)
	}
	for _, bad := range []string{"0", "9", "1001", "lots"} {
		if _, err := Load(env(map[string]string{"REWIND_API_MAX_PAGE_SIZE": bad})); err == nil || !strings.Contains(err.Error(), "REWIND_API_MAX_PAGE_SIZE") {
			t.Errorf("REWIND_API_MAX_PAGE_SIZE=%q error = %v", bad, err)
		}
	}
}

func TestLoadPlugins(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "plugins")
	cfg, err := Load(env(map[string]string{
		"REWIND_DATA_DIR": filepath.Join(base, "data"), "REWIND_PLUGIN_DIR": pluginDir, "REWIND_PLUGINS": " missing-tracks , other ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PluginDir != pluginDir || strings.Join(cfg.Plugins, ",") != "missing-tracks,other" {
		t.Errorf("cfg = %q %v", cfg.PluginDir, cfg.Plugins)
	}
	// Off by default.
	if cfg, err := Load(env(nil)); err != nil || cfg.PluginDir != "" || cfg.Plugins != nil {
		t.Errorf("defaults = %q %v %v", cfg.PluginDir, cfg.Plugins, err)
	}
}

func TestLoadRejectsBadPluginConfig(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	music := filepath.Join(base, "music")
	for name, vars := range map[string]map[string]string{
		"relative dir":       {"REWIND_PLUGIN_DIR": "plugins"},
		"ids without dir":    {"REWIND_PLUGINS": "missing-tracks"},
		"invalid id":         {"REWIND_PLUGIN_DIR": filepath.Join(base, "p"), "REWIND_PLUGINS": "../evil"},
		"shell in id":        {"REWIND_PLUGIN_DIR": filepath.Join(base, "p"), "REWIND_PLUGINS": "a;rm -rf /"},
		"duplicate id":       {"REWIND_PLUGIN_DIR": filepath.Join(base, "p"), "REWIND_PLUGINS": "a,a"},
		"dir is data dir":    {"REWIND_PLUGIN_DIR": data},
		"dir inside data":    {"REWIND_PLUGIN_DIR": filepath.Join(data, "plugins")},
		"dir contains data":  {"REWIND_PLUGIN_DIR": base},
		"dir inside music":   {"REWIND_PLUGIN_DIR": filepath.Join(music, "plugins"), "REWIND_LIBRARY_ROOTS": music},
		"dir contains music": {"REWIND_PLUGIN_DIR": filepath.Join(base, "x"), "REWIND_LIBRARY_ROOTS": filepath.Join(base, "x", "music")},
	} {
		vars["REWIND_DATA_DIR"] = data
		if _, err := Load(env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := make([]string, maxEnabledPlugins+1)
	for i := range many {
		many[i] = fmt.Sprintf("p%d", i)
	}
	if _, err := Load(env(map[string]string{"REWIND_PLUGIN_DIR": filepath.Join(base, "p"), "REWIND_PLUGINS": strings.Join(many, ",")})); err == nil {
		t.Error("too many plugins accepted")
	}
}

func TestLoadThemeDir(t *testing.T) {
	base := t.TempDir()
	themeDir := filepath.Join(base, "themes")
	cfg, err := Load(env(map[string]string{"REWIND_DATA_DIR": filepath.Join(base, "data"), "REWIND_THEME_DIR": themeDir + string(filepath.Separator)}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThemeDir != themeDir {
		t.Errorf("ThemeDir = %q", cfg.ThemeDir)
	}
	if cfg, err := Load(env(nil)); err != nil || cfg.ThemeDir != "" {
		t.Errorf("default ThemeDir = %q, %v", cfg.ThemeDir, err)
	}

	data := filepath.Join(base, "data")
	music := filepath.Join(base, "music")
	plugins := filepath.Join(base, "plugins")
	for name, vars := range map[string]map[string]string{
		"relative":          {"REWIND_THEME_DIR": "themes"},
		"is data dir":       {"REWIND_THEME_DIR": data},
		"inside data":       {"REWIND_THEME_DIR": filepath.Join(data, "themes")},
		"contains data":     {"REWIND_THEME_DIR": base},
		"inside music":      {"REWIND_THEME_DIR": filepath.Join(music, "themes"), "REWIND_LIBRARY_ROOTS": music},
		"is plugin dir":     {"REWIND_THEME_DIR": plugins, "REWIND_PLUGIN_DIR": plugins},
		"inside plugin dir": {"REWIND_THEME_DIR": filepath.Join(plugins, "themes"), "REWIND_PLUGIN_DIR": plugins},
	} {
		vars["REWIND_DATA_DIR"] = data
		if _, err := Load(env(vars)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
