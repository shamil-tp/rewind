// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	tsName = "rewind.tail1234.ts.net"
	lanIP  = "192.168.1.20"
)

// remoteVia makes a request look like it came from another device through
// host (the Host header and matching same-origin browser headers).
func remoteVia(peer, scheme, host string) reqOpt {
	return func(r *http.Request) {
		r.RemoteAddr = peer
		r.Host = host
		r.Header.Set("Origin", scheme+"://"+host)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
}

func newRemoteEnv(t *testing.T, secure bool) *testEnv {
	t.Helper()
	return newTestEnvWith(t, envConfig{secure: secure, trustedHosts: []string{tsName, lanIP}})
}

func TestTrustedHostAllowlist(t *testing.T) {
	e := newRemoteEnv(t, false)
	for host, wantOK := range map[string]bool{
		"localhost:8090":              true,
		"127.0.0.1:8090":              true,
		"[::1]:8090":                  true,
		tsName:                        true,
		"Rewind.Tail1234.TS.NET.":     true,
		tsName + ":443":               true,
		lanIP + ":8090":               true,
		"sub." + tsName:               false, // no implicit subdomains
		"tail1234.ts.net":             false,
		"192.168.1.21:8090":           false,
		"evil.example":                false,
		"rewind.tail1234.ts.net.evil": false,
		"":                            false,
	} {
		rec := e.send(t, http.MethodGet, "/api/health", nil, func(r *http.Request) { r.Host = host })
		if wantOK && rec.Code != http.StatusOK {
			t.Errorf("Host %q: status = %d, want 200", host, rec.Code)
		}
		if !wantOK {
			expectError(t, rec, http.StatusForbidden, "forbidden_host")
		}
	}
	if !strings.Contains(e.logs.String(), "rejected request for an untrusted host name") {
		t.Error("rejected host was not logged")
	}
}

func TestForwardingHeadersDoNotBypassHostCheck(t *testing.T) {
	e := newRemoteEnv(t, false)
	for _, h := range [][2]string{
		{"X-Forwarded-Host", "localhost"},
		{"X-Forwarded-Host", tsName},
		{"Forwarded", "for=127.0.0.1;host=localhost"},
		{"X-Real-IP", "127.0.0.1"},
	} {
		rec := e.send(t, http.MethodGet, "/api/health", nil, func(r *http.Request) { r.Host = "evil.example" }, withHeader(h[0], h[1]))
		expectError(t, rec, http.StatusForbidden, "forbidden_host")
	}
}

func TestRemoteClientsStillNeedToSignIn(t *testing.T) {
	e := newRemoteEnv(t, false)
	e.doSetup(t)
	via := remoteVia("100.101.102.103:51000", "http", lanIP+":8090")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/library"},
		{http.MethodGet, "/api/tracks"},
		{http.MethodGet, "/api/tracks/1"},
		{http.MethodGet, "/api/tracks/1/stream"},
		{http.MethodHead, "/api/tracks/1/stream"},
		{http.MethodPost, "/api/library/scan"},
	} {
		rec := e.send(t, c.method, c.path, nil, via)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s from a trusted remote host without a session = %d, want 401", c.method, c.path, rec.Code)
		}
	}
	// A spoofed loopback forwarding header changes nothing.
	rec := e.send(t, http.MethodGet, "/api/library", nil, via, withHeader("X-Forwarded-For", "127.0.0.1"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("with X-Forwarded-For: 127.0.0.1 = %d, want 401", rec.Code)
	}

	// Signing in from the remote device works and the session is then accepted.
	c := e.doLogin(t, via)
	rec = e.send(t, http.MethodGet, "/api/library", nil, via, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/library with a session = %d %s", rec.Code, rec.Body)
	}
}

func TestSetupNeverAvailableThroughATrustedHostName(t *testing.T) {
	e := newRemoteEnv(t, true)
	body := map[string]string{"setup_code": e.code, "username": ownerName, "password": ownerPass}
	// "tailscale serve" connects from loopback but forwards the tailnet Host.
	for _, host := range []string{tsName, lanIP + ":8090"} {
		rec := e.send(t, http.MethodPost, "/api/auth/setup", body, remoteVia("127.0.0.1:52000", "https", host))
		expectError(t, rec, http.StatusForbidden, "setup_not_local")
	}
	// A remote peer using a trusted host is refused too.
	rec := e.send(t, http.MethodPost, "/api/auth/setup", body, remoteVia("100.101.102.103:52000", "http", lanIP+":8090"))
	expectError(t, rec, http.StatusForbidden, "setup_not_local")
	// The setup code is still valid afterwards from the machine itself.
	if rec := e.send(t, http.MethodPost, "/api/auth/setup", body); rec.Code != http.StatusCreated {
		t.Errorf("local setup after refused attempts = %d %s", rec.Code, rec.Body)
	}
}

func TestTrustedHostsAreNotTrustedOrigins(t *testing.T) {
	e := newRemoteEnv(t, false)
	e.doSetup(t)
	login := map[string]string{"username": ownerName, "password": ownerPass}
	for _, c := range []struct {
		name, host, origin, site string
	}{
		{"cross-site page", lanIP + ":8090", "https://evil.example", "cross-site"},
		// Another trusted name is still a different origin.
		{"other trusted host", lanIP + ":8090", "https://" + tsName, ""},
		{"scheme-less mismatch", tsName, "http://" + lanIP + ":8090", ""},
	} {
		rec := e.send(t, http.MethodPost, "/api/auth/login", login, func(r *http.Request) {
			r.RemoteAddr = "100.101.102.103:53000"
			r.Host = c.host
			r.Header.Set("Origin", c.origin)
			r.Header.Del("Sec-Fetch-Site")
			if c.site != "" {
				r.Header.Set("Sec-Fetch-Site", c.site)
			}
		})
		expectError(t, rec, http.StatusForbidden, "cross_origin")
		if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want none", c.name, v)
		}
	}
	// No CORS headers are ever sent, even for a same-origin request.
	rec := e.send(t, http.MethodGet, "/api/health", nil, remoteVia("100.101.102.103:53000", "https", tsName),
		withHeader("Origin", "https://evil.example"))
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none", v)
	}
}

func TestSecureCookieFlagsForRemoteHTTPS(t *testing.T) {
	e := newRemoteEnv(t, true)
	e.doSetup(t)
	rec := e.send(t, http.MethodPost, "/api/auth/login", map[string]string{"username": ownerName, "password": ownerPass},
		remoteVia("127.0.0.1:54000", "https", tsName))
	if rec.Code != http.StatusOK {
		t.Fatalf("login via %s = %d %s", tsName, rec.Code, rec.Body)
	}
	raw := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-rewind_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q is missing %q", raw, want)
		}
	}
	if strings.Contains(strings.ToLower(raw), "domain=") {
		t.Errorf("Set-Cookie %q must not set a Domain", raw)
	}
}

func TestCheckBound(t *testing.T) {
	for _, c := range []struct {
		addr   net.Addr
		remote bool
		ok     bool
	}{
		{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8090}, false, true},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 8090}, false, true},
		{&net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 8090}, false, false},
		{&net.TCPAddr{IP: net.ParseIP("100.101.102.103"), Port: 8090}, false, false},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 8090}, false, false},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 8090}, false, false},
		{&net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 8090}, true, true},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 8090}, true, true},
		{&net.UnixAddr{Name: "/tmp/x", Net: "unix"}, true, false},
	} {
		err := checkBound(c.addr, c.remote)
		if (err == nil) != c.ok {
			t.Errorf("checkBound(%v, remote=%v) error = %v, want ok=%v", c.addr, c.remote, err, c.ok)
		}
	}
}

func TestListen(t *testing.T) {
	lns, err := Listen([]string{"127.0.0.1:0", "localhost:0"}, false)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	for _, ln := range lns {
		ln.Close()
	}
	if len(lns) != 2 {
		t.Errorf("got %d listeners, want 2", len(lns))
	}
	if ln6, err := Listen([]string{"[::1]:0"}, false); err == nil {
		ln6[0].Close()
	} else if probe, perr := net.Listen("tcp", "[::1]:0"); perr == nil {
		probe.Close()
		t.Errorf("Listen([::1]:0) error = %v, but IPv6 loopback is available", err)
	}
	if _, err := Listen(nil, true); err == nil {
		t.Error("Listen(nil) error = nil")
	}
}

func TestListenRejectsWildcardWithoutRemoteAccess(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		lns, err := Listen([]string{"127.0.0.1:0", addr}, false)
		if err == nil {
			for _, ln := range lns {
				ln.Close()
			}
			t.Errorf("Listen(%q) error = nil, want refusal", addr)
			continue
		}
		if !strings.Contains(err.Error(), "not a loopback address") {
			t.Errorf("Listen(%q) error = %v", addr, err)
		}
	}
}

func TestListenClosesEarlierListenersOnFailure(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	// The second bind to the same port fails; the first must then be closed.
	if lns, err := Listen([]string{addr, addr}, false); err == nil {
		for _, ln := range lns {
			ln.Close()
		}
		t.Fatal("Listen(same address twice) error = nil")
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		c.Close()
		t.Errorf("%s still accepts connections after Listen failed", addr)
	}
}

func TestServeMultipleListeners(t *testing.T) {
	lns, err := Listen([]string{"127.0.0.1:0", "127.0.0.1:0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, lns, newTestHandler(t), slog.New(slog.DiscardHandler)) }()
	for _, ln := range lns {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/health")
		if err != nil {
			t.Fatalf("GET via %s: %v", ln.Addr(), err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET via %s = %d", ln.Addr(), resp.StatusCode)
		}
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	for _, ln := range lns {
		if c, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
			c.Close()
			t.Errorf("%s still accepting after shutdown", ln.Addr())
		}
	}
}

func TestServeStopsWhenAListenerFails(t *testing.T) {
	lns, err := Listen([]string{"127.0.0.1:0", "127.0.0.1:0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- Serve(context.Background(), lns, newTestHandler(t), slog.New(slog.DiscardHandler)) }()
	time.Sleep(50 * time.Millisecond)
	lns[1].Close() // simulate an interface disappearing
	select {
	case err := <-served:
		if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
			t.Errorf("Serve() error = %v, want unexpected stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept running after a listener failed")
	}
	if c, err := net.DialTimeout("tcp", lns[0].Addr().String(), 200*time.Millisecond); err == nil {
		c.Close()
		t.Error("the other listener is still accepting after Serve returned")
	}
}
