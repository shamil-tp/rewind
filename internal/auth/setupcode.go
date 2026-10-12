// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SetupCodeFile is the file in the data directory that holds the one-time
// setup code while no account exists.
const SetupCodeFile = "setup-code"

// SetupCode is the one-time code required to create the owner account. It is
// written to a 0600 file in the data directory, so only someone with access
// to the server's files can complete setup; the code itself is never logged.
// A new code is generated at every start while setup is incomplete.
type SetupCode struct {
	mu   sync.Mutex
	code string // normalized; empty once consumed
	path string
}

// NewSetupCode generates a code and writes it to dataDir/setup-code,
// replacing any stale file.
func NewSetupCode(dataDir string) (*SetupCode, error) {
	raw := make([]byte, 10) // 80 bits
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw) // 16 chars
	path := filepath.Join(dataDir, SetupCodeFile)
	display := strings.Join([]string{code[0:4], code[4:8], code[8:12], code[12:16]}, "-")

	os.Remove(path) // a stale file may have looser permissions; recreate it
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot write setup code file %s: %w", path, err)
	}
	if _, err := f.WriteString(display + "\n"); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot write setup code file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &SetupCode{code: code, path: path}, nil
}

// Path is the location of the setup code file.
func (c *SetupCode) Path() string { return c.path }

// Matches reports, in constant time, whether input equals the code. Case,
// spaces and dashes are ignored.
func (c *SetupCode) Matches(input string) bool {
	norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(input))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.code == "" || len(norm) != len(c.code) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(norm), []byte(c.code)) == 1
}

// Consume invalidates the code and deletes its file. Call it once setup has
// succeeded.
func (c *SetupCode) Consume() error {
	c.mu.Lock()
	c.code = ""
	c.mu.Unlock()
	return RemoveSetupCodeFile(filepath.Dir(c.path))
}

// RemoveSetupCodeFile deletes a leftover setup code file, if any.
func RemoveSetupCodeFile(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, SetupCodeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
