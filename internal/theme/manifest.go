// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ManifestFile is the manifest's file name inside a theme's directory.
const ManifestFile = "theme.json"

// SchemaVersion is the only manifest format this version of Rewind reads.
const SchemaVersion = 1

// MaxManifestBytes bounds how much of a manifest file is read.
const MaxManifestBytes = 32 << 10

var (
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
)

const (
	maxIDLength         = 40
	maxNameRunes        = 64
	maxDescriptionRunes = 280
	maxAuthorRunes      = 100
	maxHomepageBytes    = 200
)

// Schemes a theme may declare. It tells the browser how to draw native
// controls and must match the theme's background.
const (
	SchemeDark  = "dark"
	SchemeLight = "light"
)

// Manifest is a theme package: metadata and design-token values. It is data
// only: no field is ever executed, used as a path, loaded as a URL or
// rendered as HTML or CSS text.
type Manifest struct {
	// Schema is the manifest format version; must be SchemaVersion.
	Schema int `json:"schema"`
	// ID is the stable identifier: lower-case letters, digits and single
	// hyphens, starting with a letter, at most 40 characters. For installed
	// themes it must equal the directory name.
	ID string `json:"id"`
	// Name is shown to people. Plain text.
	Name string `json:"name"`
	// Version is the theme's own version, MAJOR.MINOR.PATCH.
	Version string `json:"version"`
	// Description, Author and Homepage (https only) are optional.
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	// ThemeAPI is the token contract the theme was written for; it must
	// equal APIVersion.
	ThemeAPI int `json:"theme_api"`
	// Scheme is "dark" or "light" (the theme's category).
	Scheme string `json:"scheme"`
	// Tokens maps token names to values. Required tokens must be present;
	// optional ones default to Rewind Dark's (or are derived, for text on
	// colored fills).
	Tokens map[string]json.RawMessage `json:"tokens"`
	// Customizable lists the tokens users may change. Omitted means every
	// customizable token; [] means none.
	Customizable *[]string `json:"customizable,omitempty"`
}

// ManifestError explains why a manifest was rejected. The message is
// written by Rewind; the only manifest text it may contain is a token name,
// shortened and with control characters replaced, so it is safe to show as
// plain text.
type ManifestError struct {
	Msg string
	// Incompatible is set when the manifest is well-formed but written for
	// another theme API version.
	Incompatible bool
}

func (e *ManifestError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ManifestError{Msg: fmt.Sprintf(format, args...)}
}

// ValidID reports whether id has the theme ID format.
func ValidID(id string) bool {
	return len(id) <= maxIDLength && idPattern.MatchString(id)
}

// Theme is a validated theme, ready to use: complete, normalized token
// values and the tokens users may customize.
type Theme struct {
	ID          string
	Name        string
	Version     string
	Description string
	Author      string
	Homepage    string
	Scheme      string
	// Tokens holds a value for every token in the contract.
	Tokens map[string]any
	// Customizable lists the token names users may change, in contract
	// order.
	Customizable []string
}

// CanCustomize reports whether users may change the token.
func (t *Theme) CanCustomize(name string) bool {
	for _, n := range t.Customizable {
		if n == name {
			return true
		}
	}
	return false
}

// ParseManifest reads and validates one manifest and returns the theme it
// describes. It reads at most MaxManifestBytes, rejects unknown fields,
// duplicate keys and trailing data, and checks every field and token value.
func ParseManifest(r io.Reader) (*Theme, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return nil, invalid("manifest could not be read")
	}
	if len(data) > MaxManifestBytes {
		return nil, invalid("manifest is larger than %d bytes", MaxManifestBytes)
	}
	if !utf8.Valid(data) {
		return nil, invalid("manifest is not valid UTF-8")
	}
	if dupKey(data) {
		return nil, invalid("manifest has a key that appears twice in the same object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		var field *json.UnmarshalTypeError
		switch {
		case errors.As(err, &field):
			return nil, invalid("manifest field %q has the wrong type", field.Field)
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			return nil, invalid("manifest has a field this version of Rewind does not know")
		default:
			return nil, invalid("manifest is not a valid JSON object")
		}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return nil, invalid("manifest must contain a single JSON object")
	}
	return m.Validate()
}

// dupKey reports whether any JSON object in data repeats a key. Go's decoder
// silently keeps the last one, which could hide a value from a reviewer.
func dupKey(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		object bool
		keys   map[string]bool
		key    bool // the next string token in this object is a key
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err != nil {
			return false // syntax errors are reported by the real decode
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if top != nil && top.object {
					top.key = true
				}
				stack = append(stack, &frame{object: v == '{', keys: map[string]bool{}, key: v == '{'})
			default:
				stack = stack[:len(stack)-1]
			}
			continue
		case string:
			if top != nil && top.object && top.key {
				if top.keys[v] {
					return true
				}
				top.keys[v] = true
				top.key = false
				continue
			}
		}
		if top != nil && top.object {
			top.key = true
		}
	}
}

// Validate checks every field and token and returns the resulting theme.
// Text fields are untrusted: they are limited in length and may not contain
// control characters.
func (m Manifest) Validate() (*Theme, error) {
	if m.Schema != SchemaVersion {
		return nil, invalid("unsupported manifest schema %d; this version of Rewind reads schema %d", m.Schema, SchemaVersion)
	}
	if !ValidID(m.ID) {
		return nil, invalid("id must be lower-case letters, digits and single hyphens, start with a letter, and be at most %d characters", maxIDLength)
	}
	if err := text("name", m.Name, 1, maxNameRunes); err != nil {
		return nil, err
	}
	if !versionPattern.MatchString(m.Version) {
		return nil, invalid("version must look like 1.2.3")
	}
	if err := text("description", m.Description, 0, maxDescriptionRunes); err != nil {
		return nil, err
	}
	if err := text("author", m.Author, 0, maxAuthorRunes); err != nil {
		return nil, err
	}
	if m.Homepage != "" {
		u, err := url.Parse(m.Homepage)
		if err != nil || len(m.Homepage) > maxHomepageBytes || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, invalid("homepage must be an https:// URL of at most %d characters", maxHomepageBytes)
		}
	}
	if m.ThemeAPI < 1 {
		return nil, invalid("theme_api must be a positive theme API version")
	}
	if m.ThemeAPI != APIVersion {
		return nil, &ManifestError{Incompatible: true,
			Msg: fmt.Sprintf("written for theme API %d; this version of Rewind provides theme API %d", m.ThemeAPI, APIVersion)}
	}
	if m.Scheme != SchemeDark && m.Scheme != SchemeLight {
		return nil, invalid(`scheme must be "dark" or "light"`)
	}
	if m.Tokens == nil {
		return nil, invalid("tokens is required")
	}

	t := &Theme{
		ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description,
		Author: m.Author, Homepage: m.Homepage, Scheme: m.Scheme,
		Tokens: make(map[string]any, len(tokens)),
	}
	// Report unknown tokens in a stable order.
	names := make([]string, 0, len(m.Tokens))
	for name := range m.Tokens {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec, ok := Lookup(name)
		if !ok {
			return nil, invalid("tokens: %q is not a token of theme API %d", truncate(name, 40), APIVersion)
		}
		v, err := spec.Validate(m.Tokens[name])
		if err != nil {
			return nil, invalid("tokens: %v", err)
		}
		t.Tokens[name] = v
	}
	for _, spec := range tokens {
		if _, ok := t.Tokens[spec.Name]; ok {
			continue
		}
		if spec.Required {
			return nil, invalid("tokens: %s is required", spec.Name)
		}
	}
	deriveDefaults(t.Tokens)

	if want := schemeFor(t.Tokens["color.background"].(string)); m.Scheme != want {
		return nil, invalid("scheme must be %q for this background color", want)
	}
	if msg := contrastProblem(colorTokens(t.Tokens)); msg != "" {
		return nil, invalid("%s", msg)
	}

	if m.Customizable == nil {
		for _, spec := range tokens {
			if spec.Customizable {
				t.Customizable = append(t.Customizable, spec.Name)
			}
		}
	} else {
		allowed := map[string]bool{}
		for _, name := range *m.Customizable {
			spec, ok := Lookup(name)
			switch {
			case !ok:
				return nil, invalid("customizable: %q is not a token of theme API %d", truncate(name, 40), APIVersion)
			case !spec.Customizable:
				return nil, invalid("customizable: %s cannot be customized (it is derived or theme-only)", name)
			case allowed[name]:
				return nil, invalid("customizable: %s is listed twice", name)
			}
			allowed[name] = true
		}
		t.Customizable = []string{}
		for _, spec := range tokens {
			if allowed[spec.Name] {
				t.Customizable = append(t.Customizable, spec.Name)
			}
		}
	}
	return t, nil
}

// deriveDefaults fills tokens the manifest omitted: text on colored fills is
// derived from the fill (as the web UI does for custom colors), everything
// else takes Rewind Dark's value.
func deriveDefaults(v map[string]any) {
	set := func(name, value string) {
		if _, ok := v[name]; !ok {
			v[name] = value
		}
	}
	set("color.on-primary", TextOn(v["color.primary"].(string)))
	set("color.on-accent", TextOn(v["color.accent"].(string)))
	text, secondary := v["color.text"].(string), v["color.secondary"].(string)
	if Contrast(text, secondary) >= MinTextContrast {
		set("color.on-secondary", text)
	} else {
		set("color.on-secondary", TextOn(secondary))
	}
	for _, spec := range tokens {
		if _, ok := v[spec.Name]; !ok {
			v[spec.Name] = spec.Default
		}
	}
}

// schemeFor is the scheme matching a background: dark when white text is
// more readable on it.
func schemeFor(background string) string {
	if TextOn(background) == "#ffffff" {
		return SchemeDark
	}
	return SchemeLight
}

func colorTokens(v map[string]any) map[string]string {
	out := map[string]string{}
	for name, value := range v {
		if s, ok := value.(string); ok && strings.HasPrefix(name, "color.") {
			out[name] = s
		}
	}
	return out
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

// truncate bounds untrusted text for messages, replacing control
// characters.
func truncate(s string, n int) string {
	if !utf8.ValidString(s) {
		return "(unreadable)"
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
