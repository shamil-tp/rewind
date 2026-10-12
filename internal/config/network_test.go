// SPDX-License-Identifier: Apache-2.0

package config

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestRemoteAddrsRequireOptIn(t *testing.T) {
	for _, addr := range []string{"192.168.1.20:8090", "10.0.0.5:8090", "100.64.0.1:8090", "[fd7a:115c:a1e0::1]:8090"} {
		for _, remote := range []string{"", "false", "0"} {
			_, err := Load(env(map[string]string{"REWIND_ADDR": addr, "REWIND_REMOTE_ACCESS": remote}))
			if err == nil || !strings.Contains(err.Error(), "requires REWIND_REMOTE_ACCESS=true") {
				t.Errorf("REWIND_ADDR=%q REWIND_REMOTE_ACCESS=%q: error = %v, want opt-in error", addr, remote, err)
			}
		}
	}
}

func TestRemoteAddrsWithOptIn(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"REWIND_ADDR":          "127.0.0.1:8090, 192.168.1.20:8090,[fd7a:115c:a1e0::1]:8090",
		"REWIND_REMOTE_ACCESS": "true",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"127.0.0.1:8090", "192.168.1.20:8090", "[fd7a:115c:a1e0::1]:8090"}
	if !slices.Equal(cfg.Addrs, want) {
		t.Errorf("Addrs = %q, want %q", cfg.Addrs, want)
	}
	if got := cfg.ExposedAddrs(); !slices.Equal(got, want[1:]) {
		t.Errorf("ExposedAddrs() = %q", got)
	}
	if cfg.ListensOnAllInterfaces() {
		t.Error("ListensOnAllInterfaces() = true")
	}
	// Explicitly bound IPs are trusted Host values; loopback is handled separately.
	if got := cfg.AllowedHosts(); !slices.Equal(got, []string{"192.168.1.20", "fd7a:115c:a1e0::1"}) {
		t.Errorf("AllowedHosts() = %q", got)
	}
}

func TestWildcardNeedsBothFlags(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8090", "[::]:8090", ":8090", "[::ffff:0.0.0.0]:8090"} {
		for _, flags := range []map[string]string{
			{},
			{"REWIND_REMOTE_ACCESS": "true"},
			{"REWIND_LISTEN_ALL_INTERFACES": "true"},
		} {
			vars := map[string]string{"REWIND_ADDR": addr}
			for k, v := range flags {
				vars[k] = v
			}
			_, err := Load(env(vars))
			if err == nil || !strings.Contains(err.Error(), "all network interfaces") {
				t.Errorf("Load(%v) error = %v, want all-interfaces error", vars, err)
			}
		}
		cfg, err := Load(env(map[string]string{"REWIND_ADDR": addr, "REWIND_REMOTE_ACCESS": "true", "REWIND_LISTEN_ALL_INTERFACES": "true"}))
		if err != nil {
			t.Errorf("Load(%q with both flags) error = %v", addr, err)
			continue
		}
		if !cfg.ListensOnAllInterfaces() {
			t.Errorf("%q: ListensOnAllInterfaces() = false", addr)
		}
		// A wildcard never adds itself to the trusted hosts.
		if got := cfg.AllowedHosts(); len(got) != 0 {
			t.Errorf("%q: AllowedHosts() = %q, want none", addr, got)
		}
	}
}

func TestRejectsUnsafeListenAddrs(t *testing.T) {
	on := map[string]string{"REWIND_REMOTE_ACCESS": "true", "REWIND_LISTEN_ALL_INTERFACES": "true"}
	for addr, want := range map[string]string{
		"example.com:8090":              "use an IP address",
		"rewind.local:8090":             "use an IP address",
		"[fe80::1%eth0]:8090":           "interface zone",
		"224.0.0.1:8090":                "multicast",
		"127.0.0.1:8090,,":              "",
		"127.0.0.1:8090,127.0.0.1:8090": "more than once",
		" , ":                           "contains no address",
	} {
		vars := map[string]string{"REWIND_ADDR": addr}
		for k, v := range on {
			vars[k] = v
		}
		_, err := Load(env(vars))
		if want == "" {
			if err != nil {
				t.Errorf("Load(REWIND_ADDR=%q) error = %v, want nil", addr, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Load(REWIND_ADDR=%q) error = %v, want %q", addr, err, want)
		}
	}
	var many []string
	for port := 1; port <= maxListenAddrs+1; port++ {
		many = append(many, "127.0.0.1:"+strconv.Itoa(port))
	}
	if _, err := Load(env(map[string]string{"REWIND_ADDR": strings.Join(many, ",")})); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("Load(%d addresses) error = %v", maxListenAddrs+1, err)
	}
}

func TestBadBooleanFlags(t *testing.T) {
	for _, name := range []string{"REWIND_REMOTE_ACCESS", "REWIND_LISTEN_ALL_INTERFACES"} {
		_, err := Load(env(map[string]string{name: "yes please"}))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=invalid: error = %v", name, err)
		}
	}
}

func TestTrustedHosts(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"REWIND_TRUSTED_HOSTS": " Rewind.Tail1234.TS.net. ,music-box, [FD7A:115C:A1E0::5],100.101.102.103,music-box",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"rewind.tail1234.ts.net", "music-box", "fd7a:115c:a1e0::5", "100.101.102.103"}
	if !slices.Equal(cfg.TrustedHosts, want) {
		t.Errorf("TrustedHosts = %q, want %q", cfg.TrustedHosts, want)
	}
	// Trusted hosts do not open any listener: the default is still loopback only.
	if len(cfg.ExposedAddrs()) != 0 || !slices.Equal(cfg.AllowedHosts(), want) {
		t.Errorf("ExposedAddrs() = %q, AllowedHosts() = %q", cfg.ExposedAddrs(), cfg.AllowedHosts())
	}
}

func TestTrustedHostsRejectsWildcardsAndJunk(t *testing.T) {
	for value, want := range map[string]string{
		"*":                     "wildcards are not allowed",
		"*.ts.net":              "wildcards are not allowed",
		"rewind.*":              "wildcards are not allowed",
		".ts.net":               "not a valid host name",
		"https://rewind.ts.net": "without scheme or path",
		"rewind.ts.net/app":     "without scheme or path",
		"rewind.ts.net:443":     "without a port",
		"0.0.0.0":               "not a usable host address",
		"::":                    "not a usable host address",
		"bad_name.example":      "not a valid host name",
		"-lead.example":         "not a valid host name",
		"two..dots":             "not a valid host name",
		strings.Repeat("a", 64): "not a valid host name",
	} {
		_, err := Load(env(map[string]string{"REWIND_TRUSTED_HOSTS": value}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("REWIND_TRUSTED_HOSTS=%q error = %v, want %q", value, err, want)
		}
	}
}
