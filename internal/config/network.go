// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Network access is opt-in and fails closed:
//
//   - By default Rewind listens on loopback only.
//   - Non-loopback addresses (LAN or Tailscale IPs) need
//     REWIND_REMOTE_ACCESS=true, and must be IP literals: host names other than
//     localhost could resolve to unexpected interfaces.
//   - Listening on every interface (0.0.0.0, [::] or an empty host)
//     additionally needs REWIND_LISTEN_ALL_INTERFACES=true.
//   - The Host header of every request must be a loopback name, an IP address
//     Rewind explicitly listens on, or a name in REWIND_TRUSTED_HOSTS. There
//     are no wildcards.

const (
	maxListenAddrs  = 8
	maxTrustedHosts = 32
)

// docsHint points users at the documentation for remote access.
const docsHint = `see "Network access" in docs/deployment.md`

// parseAddrs validates the comma-separated REWIND_ADDR value and returns the
// normalized host:port addresses.
func parseAddrs(value string, remote, allInterfaces bool) ([]string, []error) {
	var (
		addrs []string
		errs  []error
		seen  = map[string]bool{}
	)
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		addr, err := parseAddr(raw, remote, allInterfaces)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[addr] {
			errs = append(errs, fmt.Errorf("REWIND_ADDR: %q is listed more than once", raw))
			continue
		}
		seen[addr] = true
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("REWIND_ADDR=%q contains no address (example: %s)", value, DefaultAddr))
	}
	if len(addrs) > maxListenAddrs {
		errs = append(errs, fmt.Errorf("REWIND_ADDR: at most %d addresses are allowed", maxListenAddrs))
	}
	return addrs, errs
}

func parseAddr(addr string, remote, allInterfaces bool) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("REWIND_ADDR: %q is not a valid host:port address (example: %s)", addr, DefaultAddr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("REWIND_ADDR: %q: port must be a number from 1 to 65535", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return net.JoinHostPort("localhost", port), nil
	}

	var ip netip.Addr
	if host != "" {
		ip, err = netip.ParseAddr(host)
		if err != nil {
			return "", fmt.Errorf("REWIND_ADDR: %q: use an IP address; host names other than localhost are not accepted "+
				"because they may resolve to unexpected interfaces", addr)
		}
		if ip.Zone() != "" {
			return "", fmt.Errorf("REWIND_ADDR: %q: addresses with an interface zone are not supported", addr)
		}
		if ip.Is4In6() {
			ip = ip.Unmap()
		}
	}

	switch {
	case ip.IsValid() && ip.IsLoopback():
		return net.JoinHostPort(ip.String(), port), nil
	case !ip.IsValid() || ip.IsUnspecified():
		if !remote || !allInterfaces {
			return "", fmt.Errorf("REWIND_ADDR: %q listens on all network interfaces, which requires both "+
				"REWIND_REMOTE_ACCESS=true and REWIND_LISTEN_ALL_INTERFACES=true; a specific LAN or Tailscale IP address "+
				"is safer (%s)", addr, docsHint)
		}
		if !ip.IsValid() {
			return net.JoinHostPort("", port), nil
		}
		return net.JoinHostPort(ip.String(), port), nil
	case ip.IsMulticast():
		return "", fmt.Errorf("REWIND_ADDR: %q is a multicast address", addr)
	default:
		if !remote {
			return "", fmt.Errorf("REWIND_ADDR: %q is not a loopback address; listening on a LAN or Tailscale address "+
				"requires REWIND_REMOTE_ACCESS=true (%s). Without it, use 127.0.0.1:%s, [::1]:%s or localhost:%s",
				addr, docsHint, port, port, port)
		}
		return net.JoinHostPort(ip.String(), port), nil
	}
}

// parseTrustedHosts validates the comma-separated REWIND_TRUSTED_HOSTS value
// and returns lower-case host names and IP addresses without ports.
func parseTrustedHosts(value string) ([]string, []error) {
	var (
		hosts []string
		errs  []error
		seen  = map[string]bool{}
	)
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host, err := normalizeTrustedHost(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	if len(hosts) > maxTrustedHosts {
		errs = append(errs, fmt.Errorf("REWIND_TRUSTED_HOSTS: at most %d host names are allowed", maxTrustedHosts))
	}
	return hosts, errs
}

func normalizeTrustedHost(raw string) (string, error) {
	h := strings.ToLower(raw)
	if strings.Contains(h, "*") {
		return "", fmt.Errorf("REWIND_TRUSTED_HOSTS: %q: wildcards are not allowed; list each host name", raw)
	}
	if strings.Contains(h, "/") {
		return "", fmt.Errorf("REWIND_TRUSTED_HOSTS: %q: give a host name only, without scheme or path (for example rewind.example.ts.net)", raw)
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return "", fmt.Errorf("REWIND_TRUSTED_HOSTS: %q is not a usable host address", raw)
		}
		return ip.Unmap().String(), nil
	}
	if strings.Contains(h, ":") {
		return "", fmt.Errorf("REWIND_TRUSTED_HOSTS: %q: give a host name without a port", raw)
	}
	h = strings.TrimSuffix(h, ".")
	if !validHostname(h) {
		return "", fmt.Errorf("REWIND_TRUSTED_HOSTS: %q is not a valid host name", raw)
	}
	return h, nil
}

// validHostname reports whether h is a lower-case DNS name made of letters,
// digits and hyphens (RFC 1123 labels).
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// IsLoopbackHost reports whether host is "localhost" or a loopback IP literal
// such as 127.0.0.1 or ::1. An empty host (all interfaces) is not loopback.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// isWildcardHost reports whether host means "all interfaces".
func isWildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsUnspecified()
}

// ExposedAddrs returns the listen addresses that are not loopback, that is
// the addresses reachable from other devices.
func (c Config) ExposedAddrs() []string {
	var out []string
	for _, a := range c.Addrs {
		if host, _, err := net.SplitHostPort(a); err == nil && !IsLoopbackHost(host) {
			out = append(out, a)
		}
	}
	return out
}

// ListensOnAllInterfaces reports whether any listen address is a wildcard.
func (c Config) ListensOnAllInterfaces() bool {
	for _, a := range c.Addrs {
		if host, _, err := net.SplitHostPort(a); err == nil && isWildcardHost(host) {
			return true
		}
	}
	return false
}

// AllowedHosts returns the Host header values accepted in addition to
// loopback names: REWIND_TRUSTED_HOSTS plus every specific non-loopback IP
// address Rewind listens on. Wildcard listen addresses add nothing.
func (c Config) AllowedHosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, a := range c.ExposedAddrs() {
		if host, _, err := net.SplitHostPort(a); err == nil && !isWildcardHost(host) {
			add(host)
		}
	}
	for _, h := range c.TrustedHosts {
		add(h)
	}
	return out
}
