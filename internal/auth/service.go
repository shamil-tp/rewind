// SPDX-License-Identifier: Apache-2.0

// Package auth implements Rewind's owner account, password hashing, and
// persistent database-backed sessions.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shamil-tp/rewind/internal/db"
)

// Errors returned by Service. Their messages are safe to show to clients.
var (
	// ErrSetupComplete means the owner account already exists.
	ErrSetupComplete = errors.New("setup has already been completed")
	// ErrInvalidCredentials is returned for an unknown username or a wrong
	// password alike, so callers cannot tell which one failed.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrNoSession means the session token is missing, unknown, expired, or
	// revoked.
	ErrNoSession = errors.New("not signed in")
)

// lastSeenGranularity limits how often a session's last_seen_at is written,
// so ordinary requests do not each cause a database write.
const lastSeenGranularity = time.Minute

// User is the safe, public view of an account.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Session is an issued login session. Token goes only into the cookie.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// Options configures a Service.
type Options struct {
	DB          *sql.DB
	Lifetime    time.Duration
	IdleTimeout time.Duration
	Logger      *slog.Logger
	// Now returns the current time; defaults to time.Now. Tests override it.
	Now func() time.Time
}

// Service provides account setup, login, and session validation.
type Service struct {
	db       *sql.DB
	lifetime time.Duration
	idle     time.Duration
	logger   *slog.Logger
	now      func() time.Time
	// dummyHash is verified against when a login names an unknown user, so
	// that response timing does not reveal whether the username exists.
	dummyHash string
}

// NewService returns a Service. It computes one password hash at startup for
// timing equalization.
func NewService(ctx context.Context, opts Options) (*Service, error) {
	if opts.DB == nil || opts.Lifetime <= 0 || opts.IdleTimeout <= 0 || opts.Logger == nil {
		return nil, errors.New("auth: invalid options")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	dummy, err := hashPassword(ctx, "rewind-timing-equalization-placeholder")
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{
		db: opts.DB, lifetime: opts.Lifetime, idle: opts.IdleTimeout,
		logger: opts.Logger, now: opts.Now, dummyHash: dummy,
	}, nil
}

// SetupRequired reports whether no account exists yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// Setup creates the owner account if, and only if, no account exists. The
// check and insert run in one immediate (write-locked) transaction, so
// concurrent calls create at most one account; the others get
// ErrSetupComplete.
func (s *Service) Setup(ctx context.Context, username, password string) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password, username); err != nil {
		return User{}, err
	}
	// Hash before opening the transaction so the write lock is held briefly.
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return User{}, err
	}

	var user User
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupComplete
		}
		now := s.now().Unix()
		res, err := tx.ExecContext(ctx,
			"INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES (?, ?, ?, ?, ?)",
			username, hash, now, now, now)
		if err != nil {
			return err
		}
		user.ID, err = res.LastInsertId()
		user.Username = username
		return err
	})
	if err != nil {
		return User{}, err
	}
	s.logger.Info("owner account created", "user_id", user.ID)
	return user, nil
}

// Login verifies credentials and issues a new session. If previousToken
// identifies an existing session, that session is revoked, so a login always
// rotates to a fresh token.
func (s *Service) Login(ctx context.Context, username, password, previousToken string) (User, Session, error) {
	var user User
	var hash string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, password_hash FROM users WHERE username = ?", username).
		Scan(&user.ID, &user.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend the same work as a real verification.
		if _, _, err := verifyPassword(ctx, password, s.dummyHash); err != nil {
			return User{}, Session{}, err
		}
		return User{}, Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, Session{}, err
	}

	ok, needsRehash, err := verifyPassword(ctx, password, hash)
	if err != nil {
		return User{}, Session{}, fmt.Errorf("verifying password for user %d: %w", user.ID, err)
	}
	if !ok {
		return User{}, Session{}, ErrInvalidCredentials
	}

	var newHash string
	if needsRehash {
		if newHash, err = hashPassword(ctx, password); err != nil {
			return User{}, Session{}, err
		}
	}

	var sess Session
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.now()
		if err := s.deleteExpired(ctx, tx, now); err != nil {
			return err
		}
		if wellFormedToken(previousToken) {
			if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(previousToken)); err != nil {
				return err
			}
		}
		if newHash != "" {
			if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
				newHash, now.Unix(), user.ID); err != nil {
				return err
			}
		}
		var err error
		sess, err = s.insertSession(ctx, tx, user.ID, now)
		return err
	})
	if err != nil {
		return User{}, Session{}, err
	}
	return user, sess, nil
}

// Authenticate returns the user for a valid session token. Expired or idle
// sessions are deleted and reported as ErrNoSession.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if !wellFormedToken(token) {
		return User{}, ErrNoSession
	}
	th := hashToken(token)
	var (
		user                       User
		sessionID                  int64
		lastSeen, expires, created int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.created_at, s.last_seen_at, s.expires_at, u.id, u.username
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`, th).
		Scan(&sessionID, &created, &lastSeen, &expires, &user.ID, &user.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, err
	}

	now := s.now()
	if !now.Before(time.Unix(expires, 0)) || !now.Before(time.Unix(lastSeen, 0).Add(s.idle)) {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", sessionID); err != nil {
			return User{}, err
		}
		return User{}, ErrNoSession
	}
	if now.Sub(time.Unix(lastSeen, 0)) >= lastSeenGranularity {
		if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", now.Unix(), sessionID); err != nil {
			return User{}, err
		}
	}
	return user, nil
}

// Logout revokes the session identified by token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, token string) error {
	if !wellFormedToken(token) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
	return err
}

// ChangePassword replaces the user's password after checking the current
// one. All of the user's sessions are revoked, including the caller's, and a
// new session is returned for the caller, all in one transaction.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next string) (Session, error) {
	var username, hash string
	err := s.db.QueryRowContext(ctx, "SELECT username, password_hash FROM users WHERE id = ?", userID).Scan(&username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	ok, _, err := verifyPassword(ctx, current, hash)
	if err != nil {
		return Session{}, fmt.Errorf("verifying password for user %d: %w", userID, err)
	}
	if !ok {
		return Session{}, ErrInvalidCredentials
	}
	if err := ValidatePassword(next, username); err != nil {
		return Session{}, err
	}
	if next == current {
		return Session{}, &ValidationError{"new password must be different from the current password"}
	}
	newHash, err := hashPassword(ctx, next)
	if err != nil {
		return Session{}, err
	}

	var sess Session
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		now := s.now()
		if _, err := tx.ExecContext(ctx,
			"UPDATE users SET password_hash = ?, updated_at = ?, password_changed_at = ? WHERE id = ?",
			newHash, now.Unix(), now.Unix(), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
			return err
		}
		var err error
		sess, err = s.insertSession(ctx, tx, userID, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	s.logger.Info("password changed; all sessions revoked", "user_id", userID)
	return sess, nil
}

func (s *Service) insertSession(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) (Session, error) {
	token, th, err := newToken()
	if err != nil {
		return Session{}, err
	}
	expires := now.Add(s.lifetime)
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		th, userID, now.Unix(), now.Unix(), expires.Unix()); err != nil {
		return Session{}, err
	}
	return Session{Token: token, ExpiresAt: expires}, nil
}

// deleteExpired removes sessions past their absolute or idle expiry. It runs
// during login rather than in a background worker.
func (s *Service) deleteExpired(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?",
		now.Unix(), now.Add(-s.idle).Unix())
	return err
}
