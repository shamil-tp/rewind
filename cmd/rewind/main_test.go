// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/config"
	"github.com/shamil-tp/rewind/internal/testaudio"
)

// syncBuffer is a bytes.Buffer safe for the concurrent writes slog makes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func envFor(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	return addr
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8090", "[::]:8090", "192.168.1.20:8090", "nonsense"} {
		dir := t.TempDir()
		var logs syncBuffer
		err := run(context.Background(), envFor(map[string]string{"REWIND_ADDR": addr, "REWIND_DATA_DIR": dir}), &logs)
		if err == nil || !strings.Contains(err.Error(), "invalid configuration") {
			t.Errorf("run(REWIND_ADDR=%q) error = %v, want invalid configuration", addr, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("run(REWIND_ADDR=%q) touched the data directory before validating config", addr)
		}
	}
}

// TestRunLifecycle starts the full application on a free loopback port with a
// fresh data directory, checks health and first-run state, then cancels the
// context (as a signal would) and expects a clean exit.
func TestRunLifecycle(t *testing.T) {
	addr := freeLoopbackAddr(t)
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, envFor(map[string]string{"REWIND_ADDR": addr, "REWIND_DATA_DIR": dataDir}), &logs)
	}()

	var resp *http.Response
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err = http.Get("http://" + addr + "/api/health")
		if err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("run exited early: %v\nlogs:\n%s", err, logs.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "rewind.db")); err != nil {
		t.Errorf("database not created in data dir: %v", err)
	}
	code, err := os.ReadFile(filepath.Join(dataDir, "setup-code"))
	if err != nil {
		t.Fatalf("setup code file not created: %v", err)
	}
	if strings.Contains(logs.String(), strings.TrimSpace(string(code))) {
		t.Error("setup code was written to the log")
	}

	resp, err = http.Get("http://" + addr + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auth status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	for _, want := range []string{"starting rewind", "created data directory", "database migrations applied",
		"initial setup required", "server listening", "server stopped", "database closed"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q:\n%s", want, logs.String())
		}
	}
}

func TestRunFailsClearlyOnBadDataDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(file, []byte("x"), 0o600)
	var logs syncBuffer
	err := run(context.Background(), envFor(map[string]string{"REWIND_ADDR": freeLoopbackAddr(t), "REWIND_DATA_DIR": file}), &logs)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("run() error = %v, want not-a-directory error", err)
	}
}

func TestRunFailsClearlyOnCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rewind.db")
	garbage := []byte(strings.Repeat("not a database ", 500))
	os.WriteFile(dbPath, garbage, 0o600)
	var logs syncBuffer
	err := run(context.Background(), envFor(map[string]string{"REWIND_ADDR": freeLoopbackAddr(t), "REWIND_DATA_DIR": dir}), &logs)
	if err == nil || !strings.Contains(err.Error(), "rewind.db") {
		t.Fatalf("run() error = %v, want database error naming the file", err)
	}
	if after, _ := os.ReadFile(dbPath); !bytes.Equal(after, garbage) {
		t.Error("corrupt database was modified")
	}
}

// TestRunIndexesConfiguredLibraryOnStartup runs the whole application with a
// library root and checks that the background startup scan indexed it and
// finished before the database was closed.
func TestRunIndexesConfiguredLibraryOnStartup(t *testing.T) {
	music := t.TempDir()
	testaudio.Write(t, music, "Artist/Album/01 Song.flac", 2, map[string][]string{"TITLE": {"Song"}, "ARTIST": {"Artist"}})
	testaudio.Write(t, music, "loose.mp3", 1, nil)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, envFor(map[string]string{
			"REWIND_ADDR": addr, "REWIND_DATA_DIR": dataDir, "REWIND_LIBRARY_ROOTS": music,
		}), &logs)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), "library scan finished") {
		select {
		case err := <-done:
			t.Fatalf("run exited early: %v\n%s", err, logs.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup scan did not finish:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	l := logs.String()
	if strings.Index(l, "library scan finished") > strings.Index(l, "database closed") {
		t.Error("database closed before the scan finished")
	}

	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dataDir, "rewind.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var tracks int
	var status string
	conn.QueryRow("SELECT count(*) FROM tracks WHERE status = 'present'").Scan(&tracks)
	conn.QueryRow("SELECT status FROM scans ORDER BY id DESC LIMIT 1").Scan(&status)
	if tracks != 2 || status != "completed" {
		t.Errorf("tracks = %d, last scan = %q; want 2 and completed\n%s", tracks, status, l)
	}
}

func TestRunWithoutRootsDoesNotScan(t *testing.T) {
	addr := freeLoopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, envFor(map[string]string{"REWIND_ADDR": addr, "REWIND_DATA_DIR": t.TempDir()}), &logs)
	}()
	for i := 0; i < 500 && !strings.Contains(logs.String(), "server listening"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "no library roots configured") || strings.Contains(logs.String(), "library scan started") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

// TestRunServesTrustedHostOnLoopback covers the "tailscale serve" layout:
// several loopback listeners plus a trusted tailnet name, nothing exposed.
func TestRunServesTrustedHostOnLoopback(t *testing.T) {
	a1, a2 := freeLoopbackAddr(t), freeLoopbackAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, envFor(map[string]string{
			"REWIND_ADDR": a1 + "," + a2, "REWIND_DATA_DIR": t.TempDir(),
			"REWIND_TRUSTED_HOSTS": "rewind.tail1234.ts.net", "REWIND_SCAN_ON_STARTUP": "false",
		}), &logs)
	}()
	get := func(addr, host string) int {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/health", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return -1
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	deadline := time.Now().Add(10 * time.Second)
	for get(a1, "localhost") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready\nlogs:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, c := range []struct {
		addr, host string
		want       int
	}{
		{a2, "localhost", http.StatusOK},
		{a1, "rewind.tail1234.ts.net", http.StatusOK},
		{a2, "evil.example", http.StatusForbidden},
	} {
		if got := get(c.addr, c.host); got != c.want {
			t.Errorf("GET via %s with Host %q = %d, want %d", c.addr, c.host, got, c.want)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	for _, want := range []string{"listening on loopback only", "rewind.tail1234.ts.net", "rejected request for an untrusted host name"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q:\n%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "remote access is enabled") {
		t.Error("loopback-only run logged remote access as enabled")
	}
}

func TestLogNetworkExposureWarnings(t *testing.T) {
	for _, c := range []struct {
		env        map[string]string
		want, deny []string
	}{
		{
			env:  map[string]string{"REWIND_ADDR": "127.0.0.1:8090,192.168.1.20:8090", "REWIND_REMOTE_ACCESS": "true"},
			want: []string{"remote access is enabled", "192.168.1.20:8090", "plain HTTP"},
			deny: []string{"ALL network interfaces"},
		},
		{
			env:  map[string]string{"REWIND_ADDR": "0.0.0.0:8090", "REWIND_REMOTE_ACCESS": "true", "REWIND_LISTEN_ALL_INTERFACES": "true", "REWIND_COOKIE_SECURE": "true"},
			want: []string{"remote access is enabled", "ALL network interfaces"},
			deny: []string{"plain HTTP"},
		},
	} {
		cfg, err := config.Load(envFor(c.env))
		if err != nil {
			t.Fatal(err)
		}
		var logs syncBuffer
		logNetworkExposure(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
		for _, w := range c.want {
			if !strings.Contains(logs.String(), w) {
				t.Errorf("%v: logs missing %q:\n%s", c.env, w, logs.String())
			}
		}
		for _, d := range c.deny {
			if strings.Contains(logs.String(), d) {
				t.Errorf("%v: logs unexpectedly contain %q", c.env, d)
			}
		}
	}
}

// runUntil starts run with vars and stops it once the log contains want.
func runUntil(t *testing.T, vars map[string]string, want string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, envFor(vars), &logs) }()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), want) {
		select {
		case err := <-done:
			t.Fatalf("run exited early: %v\n%s", err, logs.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q not logged:\n%s", want, logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v\n%s", err, logs.String())
	}
	return logs.String()
}

// The example plugin, compiled in, installed and enabled, receives the
// startup scan's event and warns about the missing track.
func TestRunWithExamplePlugin(t *testing.T) {
	music := t.TempDir()
	testaudio.Write(t, music, "keep.flac", 1, nil)
	gone := testaudio.Write(t, music, "gone.flac", 1, nil)
	dataDir := t.TempDir()
	vars := map[string]string{"REWIND_ADDR": freeLoopbackAddr(t), "REWIND_DATA_DIR": dataDir, "REWIND_LIBRARY_ROOTS": music}
	runUntil(t, vars, "library scan finished")

	os.Remove(gone)
	pluginDir := t.TempDir()
	os.MkdirAll(filepath.Join(pluginDir, "missing-tracks"), 0o755)
	manifest, err := os.ReadFile(filepath.Join("..", "..", "plugins", "missingtracks", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pluginDir, "missing-tracks", "plugin.json"), manifest, 0o644)
	vars["REWIND_PLUGIN_DIR"] = pluginDir
	vars["REWIND_PLUGINS"] = "missing-tracks"
	l := runUntil(t, vars, "library scan marked tracks as missing")

	if !strings.Contains(l, `msg="plugin started" plugin=missing-tracks version=1.0.0`) || !strings.Contains(l, "missing=1") {
		t.Errorf("plugin log:\n%s", l)
	}
	order := []string{"plugins ready", "library scan finished", "plugins stopped", "database closed"}
	for i := 1; i < len(order); i++ {
		if strings.Index(l, order[i-1]) > strings.Index(l, order[i]) {
			t.Errorf("%q logged after %q:\n%s", order[i-1], order[i], l)
		}
	}
}

// Rewind starts normally without plugins, and with an installed but not
// enabled plugin, which is never initialized.
func TestRunWithoutEnabledPlugins(t *testing.T) {
	pluginDir := t.TempDir()
	os.MkdirAll(filepath.Join(pluginDir, "missing-tracks"), 0o755)
	manifest, _ := os.ReadFile(filepath.Join("..", "..", "plugins", "missingtracks", "plugin.json"))
	os.WriteFile(filepath.Join(pluginDir, "missing-tracks", "plugin.json"), manifest, 0o644)
	l := runUntil(t, map[string]string{
		"REWIND_ADDR": freeLoopbackAddr(t), "REWIND_DATA_DIR": t.TempDir(), "REWIND_PLUGIN_DIR": pluginDir,
	}, "server listening")
	if strings.Contains(l, "plugin started") || !strings.Contains(l, `msg="plugins ready" running=0 installed=1`) {
		t.Errorf("log:\n%s", l)
	}
}
