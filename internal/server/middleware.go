// SPDX-License-Identifier: Apache-2.0

package server

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/shamil-tp/rewind/internal/config"
)

// maxRequestBodyBytes caps request bodies. No Phase 1 endpoint accepts a body;
// later endpoints that need more must opt in explicitly.
const maxRequestBodyBytes = 1 << 20

// contentSecurityPolicy allows only same-origin resources. The production
// frontend build uses no inline scripts or styles.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"media-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// requireTrustedHost rejects requests whose Host header is not a loopback
// name or address and not in allowed (config.Config.AllowedHosts: explicitly
// bound IP addresses and REWIND_TRUSTED_HOSTS). Any other Host value indicates
// DNS rebinding or a misrouted request. There are no wildcards.
func requireTrustedHost(allowed []string, logger *slog.Logger, next http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		set[normalizeHost(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := normalizeHost(hostname(r.Host))
		if !config.IsLoopbackHost(host) && !set[host] {
			if len(host) > 100 {
				host = host[:100]
			}
			logger.Warn("rejected request for an untrusted host name; add it to REWIND_TRUSTED_HOSTS if it is yours",
				"host", host, "remote", peerIP(r))
			writeError(w, http.StatusForbidden, "forbidden_host",
				"this host name is not trusted by the Rewind server; the administrator can add it to REWIND_TRUSTED_HOSTS")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// normalizeHost lower-cases a host name and drops a trailing dot, so
// "Rewind.example." and "rewind.example" compare equal.
func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// hostname strips an optional port and IPv6 brackets from a Host header value.
func hostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// crossOriginProtection rejects cross-origin browser requests that use
// unsafe methods (anything but GET, HEAD and OPTIONS) with a JSON 403. It uses
// the standard library's http.CrossOriginProtection, which checks
// Sec-Fetch-Site and falls back to comparing Origin with Host. Requests with
// neither header come from non-browser clients and are allowed; they cannot be
// CSRF because browsers always send at least one of them.
func crossOriginProtection(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin", "cross-origin request rejected")
	}))
	return cop.Handler(next)
}

// noStore prevents caching of responses that depend on the session.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
