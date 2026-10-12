// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/shamil-tp/rewind/internal/auth"
	"github.com/shamil-tp/rewind/internal/config"
)

// maxAuthBodyBytes caps the JSON bodies of authentication requests.
const maxAuthBodyBytes = 16 << 10

// authAPI holds the authentication handlers and middleware.
type authAPI struct {
	svc       *auth.Service
	setupCode *auth.SetupCode // nil when setup was already complete at startup
	limiter   *auth.Limiter
	secure    bool
	logger    *slog.Logger
}

// cookieName returns the session cookie name. Over HTTPS the __Host- prefix
// makes browsers enforce Secure, Path=/ and no Domain attribute.
func (a *authAPI) cookieName() string {
	if a.secure {
		return "__Host-rewind_session"
	}
	return "rewind_session"
}

func (a *authAPI) sessionToken(r *http.Request) string {
	c, err := r.Cookie(a.cookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

func (a *authAPI) setSessionCookie(w http.ResponseWriter, s auth.Session) {
	maxAge := int(math.Ceil(time.Until(s.ExpiresAt).Seconds()))
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName(),
		Value:    s.Token,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  s.ExpiresAt,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *authAPI) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// requireAuth rejects requests without a valid session with a JSON 401 and
// otherwise stores the user in the request context (see auth.UserFrom).
func (a *authAPI) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := a.sessionToken(r)
		user, err := a.svc.Authenticate(r.Context(), token)
		if errors.Is(err, auth.ErrNoSession) {
			if token != "" {
				a.clearSessionCookie(w)
			}
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if err != nil {
			a.internalError(w, "authenticating request", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
	})
}

type statusResponse struct {
	SetupRequired bool `json:"setup_required"`
	Authenticated bool `json:"authenticated"`
}

func (a *authAPI) status(w http.ResponseWriter, r *http.Request) {
	required, err := a.svc.SetupRequired(r.Context())
	if err != nil {
		a.internalError(w, "reading setup state", err)
		return
	}
	authenticated := false
	if token := a.sessionToken(r); token != "" {
		if _, err := a.svc.Authenticate(r.Context(), token); err == nil {
			authenticated = true
		} else if !errors.Is(err, auth.ErrNoSession) {
			a.internalError(w, "authenticating request", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, statusResponse{SetupRequired: required, Authenticated: authenticated})
}

type userResponse struct {
	User auth.User `json:"user"`
}

type setupRequest struct {
	SetupCode string `json:"setup_code"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// setup creates the owner account. It is only available while no account
// exists, only to clients connecting from a loopback address with a loopback
// Host header, and only with
// the one-time setup code from the data directory. On success the new owner
// is signed in.
func (a *authAPI) setup(w http.ResponseWriter, r *http.Request) {
	// Both the TCP peer and the Host header must be loopback. A local reverse
	// proxy such as "tailscale serve" connects from loopback but forwards a
	// non-loopback Host, so setup stays unavailable through it.
	if !isLoopbackPeer(r) || !config.IsLoopbackHost(hostname(r.Host)) {
		writeError(w, http.StatusForbidden, "setup_not_local", "setup is only available from the server itself")
		return
	}
	if !a.allow(w, r) {
		return
	}
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	required, err := a.svc.SetupRequired(r.Context())
	if err != nil {
		a.internalError(w, "reading setup state", err)
		return
	}
	if !required {
		writeError(w, http.StatusConflict, "setup_complete", auth.ErrSetupComplete.Error())
		return
	}
	if a.setupCode == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable", "restart Rewind to generate a new setup code")
		return
	}
	if !a.setupCode.Matches(req.SetupCode) {
		a.logger.Warn("setup attempt with an invalid setup code", "remote", peerIP(r))
		writeError(w, http.StatusForbidden, "invalid_setup_code", "the setup code is not valid")
		return
	}

	user, err := a.svc.Setup(r.Context(), req.Username, req.Password)
	var ve *auth.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", ve.Msg)
		return
	case errors.Is(err, auth.ErrSetupComplete):
		writeError(w, http.StatusConflict, "setup_complete", err.Error())
		return
	case err != nil:
		a.internalError(w, "creating owner account", err)
		return
	}
	if err := a.setupCode.Consume(); err != nil {
		a.logger.Warn("could not delete the setup code file; delete it manually", "err", err)
	}

	_, sess, err := a.svc.Login(r.Context(), req.Username, req.Password, a.sessionToken(r))
	if err != nil {
		a.internalError(w, "signing in after setup", err)
		return
	}
	a.setSessionCookie(w, sess)
	writeJSON(w, http.StatusCreated, userResponse{User: user})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r) {
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, sess, err := a.svc.Login(r.Context(), req.Username, req.Password, a.sessionToken(r))
	if errors.Is(err, auth.ErrInvalidCredentials) {
		a.logger.Info("login failed", "remote", peerIP(r))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	}
	if err != nil {
		a.internalError(w, "logging in", err)
		return
	}
	a.logger.Info("login succeeded", "user_id", user.ID)
	a.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, userResponse{User: user})
}

// logout revokes the current session (if any) and clears the cookie. It is
// idempotent and succeeds without a session.
func (a *authAPI) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Logout(r.Context(), a.sessionToken(r)); err != nil {
		a.internalError(w, "logging out", err)
		return
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authAPI) me(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, userResponse{User: user})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword requires the current password. On success every session of
// the account is revoked and the caller receives a new one.
func (a *authAPI) changePassword(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r) {
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, _ := auth.UserFrom(r.Context())
	sess, err := a.svc.ChangePassword(r.Context(), user.ID, req.CurrentPassword, req.NewPassword)
	var ve *auth.ValidationError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "invalid_current_password", "the current password is not correct")
		return
	case errors.As(err, &ve):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", ve.Msg)
		return
	case errors.Is(err, auth.ErrNoSession):
		a.clearSessionCookie(w)
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	case err != nil:
		a.internalError(w, "changing password", err)
		return
	}
	a.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, userResponse{User: user})
}

// allow applies the authentication rate limit for the client's address.
func (a *authAPI) allow(w http.ResponseWriter, r *http.Request) bool {
	ok, wait := a.limiter.Allow(peerIP(r))
	if ok {
		return true
	}
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again in "+strconv.Itoa(secs)+" seconds")
	return false
}

// internalError logs err and returns a generic 500 without internal details.
func (a *authAPI) internalError(w http.ResponseWriter, action string, err error) {
	a.logger.Error("request failed", "action", action, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// decodeJSON decodes a single JSON object into dst, requiring an
// application/json content type, rejecting unknown fields, trailing data, and
// bodies over maxAuthBodyBytes. It writes the error response itself.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONLimit(w, r, dst, maxAuthBodyBytes)
}

// decodeJSONLimit is decodeJSON with a different body size limit.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "request body must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body is not valid JSON for this endpoint")
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain a single JSON object")
		return false
	}
	return true
}

// peerIP is the IP address of the TCP peer. Forwarding headers such as
// X-Forwarded-For are deliberately ignored.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isLoopbackPeer reports whether the TCP peer is a loopback IP address.
func isLoopbackPeer(r *http.Request) bool {
	ip, err := netip.ParseAddr(peerIP(r))
	return err == nil && ip.IsLoopback()
}
