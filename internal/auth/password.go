// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's recommended minimum configuration
// (19 MiB memory, 2 iterations, 1 lane). They are stored in each hash, so
// they can be raised later; hashes using older parameters are upgraded on the
// next successful login.
const (
	argonMemoryKiB  = 19 * 1024
	argonIterations = 2
	argonThreads    = 1
	argonSaltLen    = 16
	argonKeyLen     = 32

	// Upper bounds accepted when parsing a stored hash, so a tampered database
	// cannot make verification consume unbounded memory or CPU.
	maxArgonMemoryKiB  = 1024 * 1024
	maxArgonIterations = 16
	maxArgonThreads    = 16
)

// maxConcurrentHashes caps simultaneous Argon2id computations so concurrent
// login attempts cannot exhaust memory on modest hardware.
const maxConcurrentHashes = 2

var hashSlots = make(chan struct{}, maxConcurrentHashes)

// Password and username rules.
const (
	MinPasswordLength = 12  // characters
	MaxPasswordLength = 256 // characters
	maxPasswordBytes  = 1024
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidationError describes input that does not meet Rewind's rules. Its
// message is safe to show to the user.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// ValidateUsername checks the username rules: 1–64 characters from
// A–Z, a–z, 0–9, '.', '_' and '-'.
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return &ValidationError{"username must be 1–64 characters using letters, digits, '.', '_' or '-'"}
	}
	return nil
}

// ValidatePassword checks the password rules: 12–256 characters, valid UTF-8,
// and not the same as the username. Passphrases are encouraged; there are no
// character-class requirements.
func ValidatePassword(password, username string) error {
	if !utf8.ValidString(password) || len(password) > maxPasswordBytes {
		return &ValidationError{"password contains invalid characters or is too long"}
	}
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength {
		return &ValidationError{fmt.Sprintf("password must be at least %d characters", MinPasswordLength)}
	}
	if n > MaxPasswordLength {
		return &ValidationError{fmt.Sprintf("password must be at most %d characters", MaxPasswordLength)}
	}
	if username != "" && strings.EqualFold(password, username) {
		return &ValidationError{"password must not be the same as the username"}
	}
	return nil
}

// hashPassword returns an Argon2id hash in PHC string format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
func hashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquireHashSlot(ctx); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonThreads, argonKeyLen)
	releaseHashSlot()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemoryKiB, argonIterations, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword reports whether password matches encoded, using a
// constant-time comparison. needsRehash is true when the hash uses parameters
// other than the current ones.
func verifyPassword(ctx context.Context, password, encoded string) (ok, needsRehash bool, err error) {
	p, salt, want, err := parseHash(encoded)
	if err != nil {
		return false, false, err
	}
	if err := acquireHashSlot(ctx); err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.threads, uint32(len(want)))
	releaseHashSlot()
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = p.memory != argonMemoryKiB || p.iterations != argonIterations ||
		p.threads != argonThreads || len(want) != argonKeyLen || len(salt) != argonSaltLen
	return ok, needsRehash, nil
}

type argonParams struct {
	memory     uint32
	iterations uint32
	threads    uint8
}

var errMalformedHash = errors.New("malformed password hash")

func parseHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, errMalformedHash
	}
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &threads); err != nil {
		return p, nil, nil, errMalformedHash
	}
	if p.memory < 8 || p.memory > maxArgonMemoryKiB || p.iterations < 1 || p.iterations > maxArgonIterations ||
		threads < 1 || threads > maxArgonThreads {
		return p, nil, nil, errMalformedHash
	}
	p.threads = uint8(threads)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, errMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, errMalformedHash
	}
	return p, salt, key, nil
}

func acquireHashSlot(ctx context.Context) error {
	select {
	case hashSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseHashSlot() { <-hashSlots }
