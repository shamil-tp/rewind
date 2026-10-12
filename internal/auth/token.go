// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// tokenBytes is the amount of randomness in a session token (256 bits).
const tokenBytes = 32

// tokenLen is the length of an encoded token (unpadded base64url).
var tokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

// newToken returns a random session token for the cookie and the SHA-256
// hash that is stored in the database.
func newToken() (token string, hash []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// wellFormedToken rejects values that cannot be Rewind tokens before any
// database lookup.
func wellFormedToken(token string) bool {
	if len(token) != tokenLen {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil
}
