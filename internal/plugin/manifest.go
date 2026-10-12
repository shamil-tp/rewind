// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ManifestFile is the manifest's file name inside a plugin's directory.
const ManifestFile = "plugin.json"

// SchemaVersion is the only manifest format this version of Rewind reads.
const SchemaVersion = 1

// MaxManifestBytes bounds how much of a manifest file is read.
const MaxManifestBytes = 16 << 10

var (
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
)

const (
	maxIDLength          = 40
	maxNameRunes         = 64
	maxDescriptionRunes  = 280
	maxAuthorRunes       = 100
	maxHomepageBytes     = 200
	maxCapabilitiesCount = 8
)

// Manifest describes an installed plugin. It is data only: no field is ever
// executed, used as a path or rendered as HTML.
type Manifest struct {
	// Schema is the manifest format version; must be SchemaVersion.
	Schema int `json:"schema"`
	// ID is the stable identifier: lower-case letters, digits and single
	// hyphens, starting with a letter, at most 40 characters. It must equal
	// the name of the plugin's directory and the compiled-in plugin's ID.
	ID string `json:"id"`
	// Name is shown to people. Plain text.
	Name string `json:"name"`
	// Version is MAJOR.MINOR.PATCH and must equal the compiled-in version.
	Version string `json:"version"`
	// Description is optional plain text.
	Description string `json:"description,omitempty"`
	// RewindAPI is the plugin API version the plugin was written for; it must
	// equal APIVersion.
	RewindAPI int `json:"rewind_api"`
	// Capabilities lists the extension points the plugin may use. The plugin
	// cannot register any capability not listed here.
	Capabilities []string `json:"capabilities"`
	// Author and Homepage (https only) are optional.
	Author   string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
}

// ManifestError explains why a manifest was rejected. The message is written
// by Rewind (it never echoes manifest text), so it is safe to show.
type ManifestError struct{ Msg string }

func (e *ManifestError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ManifestError{Msg: fmt.Sprintf(format, args...)}
}

// ValidID reports whether id has the plugin ID format.
func ValidID(id string) bool {
	return len(id) <= maxIDLength && idPattern.MatchString(id)
}

// ParseManifest reads and validates one manifest. It reads at most
// MaxManifestBytes, rejects unknown fields and trailing data, and checks
// every field.
func ParseManifest(r io.Reader) (Manifest, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, invalid("manifest could not be read")
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, invalid("manifest is larger than %d bytes", MaxManifestBytes)
	}
	if !utf8.Valid(data) {
		return Manifest{}, invalid("manifest is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		var field *json.UnmarshalTypeError
		switch {
		case errors.As(err, &field):
			return Manifest{}, invalid("manifest field %q has the wrong type", field.Field)
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			return Manifest{}, invalid("manifest has a field this version of Rewind does not know")
		default:
			return Manifest{}, invalid("manifest is not a valid JSON object")
		}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Manifest{}, invalid("manifest must contain a single JSON object")
	}
	return m, m.Validate()
}

// Validate checks every field. Text fields are untrusted: they are limited
// in length and may not contain control characters.
func (m Manifest) Validate() error {
	if m.Schema != SchemaVersion {
		return invalid("unsupported manifest schema %d; this version of Rewind reads schema %d", m.Schema, SchemaVersion)
	}
	if !ValidID(m.ID) {
		return invalid("id must be lower-case letters, digits and single hyphens, start with a letter, and be at most %d characters", maxIDLength)
	}
	if err := text("name", m.Name, 1, maxNameRunes); err != nil {
		return err
	}
	if !versionPattern.MatchString(m.Version) {
		return invalid("version must look like 1.2.3")
	}
	if err := text("description", m.Description, 0, maxDescriptionRunes); err != nil {
		return err
	}
	if m.RewindAPI < 1 {
		return invalid("rewind_api must be a positive plugin API version")
	}
	if m.Capabilities == nil {
		return invalid("capabilities is required (use [] for none)")
	}
	if len(m.Capabilities) > maxCapabilitiesCount {
		return invalid("at most %d capabilities may be listed", maxCapabilitiesCount)
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !knownCapabilities[c] {
			return invalid("unknown capability; known capabilities: %s", strings.Join(Capabilities(), ", "))
		}
		if seen[c] {
			return invalid("a capability is listed twice")
		}
		seen[c] = true
	}
	if err := text("author", m.Author, 0, maxAuthorRunes); err != nil {
		return err
	}
	if m.Homepage != "" {
		u, err := url.Parse(m.Homepage)
		if err != nil || len(m.Homepage) > maxHomepageBytes || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return invalid("homepage must be an https:// URL of at most %d characters", maxHomepageBytes)
		}
	}
	return nil
}

func text(field, v string, minRunes, maxRunes int) error {
	n := utf8.RuneCountInString(v)
	if n < minRunes || n > maxRunes {
		if minRunes > 0 {
			return invalid("%s must be %d to %d characters", field, minRunes, maxRunes)
		}
		return invalid("%s must be at most %d characters", field, maxRunes)
	}
	if strings.TrimSpace(v) != v {
		return invalid("%s must not start or end with spaces", field)
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return invalid("%s must not contain control characters", field)
	}
	return nil
}
