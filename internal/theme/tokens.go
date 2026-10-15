// SPDX-License-Identifier: Apache-2.0

// Package theme is Rewind's theme system: declarative, data-only visual
// themes. A theme is a validated set of design-token values (colors, type,
// shape, surfaces) plus metadata. It contains no code, styles, markup or
// files: the web UI turns token values into CSS custom properties itself, so
// a theme can never inject CSS, HTML or scripts. See docs/themes.md.
//
// Themes are separate from plugins (internal/plugin): installing or enabling
// a theme never runs code and grants no capability.
package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// APIVersion is the token contract this version of Rewind provides. A
// manifest's theme_api must equal it. It changes only when tokens are
// removed or their meaning or range changes incompatibly; adding an optional
// token does not change it.
const APIVersion = 1

// Kind is how a token's value is written and checked.
type Kind string

const (
	// KindColor is a lower-case "#rrggbb" string.
	KindColor Kind = "color"
	// KindLength is a whole number of CSS pixels between Min and Max.
	KindLength Kind = "length"
	// KindNumber is a number between Min and Max, a multiple of Step.
	KindNumber Kind = "number"
	// KindChoice is one of Choices' values.
	KindChoice Kind = "choice"
)

// Choice is one allowed value of a KindChoice token.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Token describes one design token: its type, limits and default. The web
// UI builds the editor and validates edits from this table (mirrored in
// web/src/services/theme-tokens.json, checked by TestTokensMatchFrontend).
type Token struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Kind  Kind   `json:"kind"`
	// Min, Max and Step bound KindLength and KindNumber values.
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
	Step float64 `json:"step,omitempty"`
	// Choices lists the values of a KindChoice token.
	Choices []Choice `json:"choices,omitempty"`
	// Default is the Rewind Dark value, used when a theme omits an optional
	// token. It equals the default in web/src/app.css.
	Default any `json:"default"`
	// Required tokens must be in every manifest.
	Required bool `json:"required"`
	// Customizable tokens may be changed by users (if the theme allows it).
	// The others are derived or theme-only, to keep text readable.
	Customizable bool `json:"customizable"`
}

// Token groups, in display order.
const (
	GroupColors     = "colors"
	GroupTypography = "typography"
	GroupShape      = "shape"
	GroupSurface    = "surface"
)

func color(name, label, help, def string, required, customizable bool) Token {
	return Token{Name: name, Group: GroupColors, Label: label, Help: help, Kind: KindColor, Default: def, Required: required, Customizable: customizable}
}

func length(group, name, label, help string, min, max, def float64) Token {
	return Token{Name: name, Group: group, Label: label, Help: help, Kind: KindLength, Min: min, Max: max, Step: 1, Default: def, Customizable: true}
}

// tokens is the token contract (theme API 1). Order is display order.
var tokens = []Token{
	color("color.background", "Background", "Page and input backgrounds, the Now Playing screen.", "#0b0b0b", true, true),
	color("color.surface", "Surface", "Panels, the mini player, hovered and selected rows.", "#151515", true, true),
	color("color.text", "Text", "Primary text.", "#f2f2f2", true, true),
	color("color.text-muted", "Secondary text", "Artists, counts, hints and positions.", "#a3a3a3", true, true),
	color("color.border", "Border", "Structural borders and outlines.", "#f2f2f2", true, true),
	color("color.primary", "Primary", "Main buttons, links and the current page in the menu.", "#c6ff00", true, true),
	color("color.secondary", "Secondary", "Secondary buttons and supporting surfaces, such as the artwork placeholder.", "#262626", true, true),
	color("color.accent", "Accent", "Highlights: playback progress, the playing track, focus outlines and shadows.", "#c6ff00", true, true),
	color("color.on-primary", "Text on primary", "Text on primary buttons. Chosen automatically when omitted.", "#0b0b0b", false, false),
	color("color.on-secondary", "Text on secondary", "Text on secondary buttons. Chosen automatically when omitted.", "#f2f2f2", false, false),
	color("color.on-accent", "Text on accent", "Text on accent markers. Chosen automatically when omitted.", "#0b0b0b", false, false),
	color("color.danger", "Danger", "Errors and unavailable badges; also used as text.", "#ff5c5c", true, false),

	{Name: "font.family", Group: GroupTypography, Label: "Font", Help: "Typeface for text. Only fonts installed on the device are used; nothing is downloaded.",
		Kind: KindChoice, Choices: []Choice{
			{"system", "System"}, {"humanist", "Humanist"}, {"geometric", "Geometric"}, {"serif", "Serif"}, {"mono", "Monospace"},
		}, Default: "system", Customizable: true},
	length(GroupTypography, "font.size", "Base font size", "Body text size in pixels at the browser's default size; it follows the browser's own text size setting.", 14, 20, 16),
	{Name: "font.heading-scale", Group: GroupTypography, Label: "Heading scale", Help: "Size of headings and titles relative to the default.",
		Kind: KindNumber, Min: 0.8, Max: 1.3, Step: 0.05, Default: 1.0, Customizable: true},
	{Name: "font.weight", Group: GroupTypography, Label: "Font weight", Help: "Weight of headings, titles and labels.",
		Kind: KindChoice, Choices: []Choice{{"regular", "Regular"}, {"strong", "Strong"}, {"heavy", "Heavy"}}, Default: "heavy", Customizable: true},
	{Name: "font.line-height", Group: GroupTypography, Label: "Line height", Help: "Spacing between lines of body text.",
		Kind: KindNumber, Min: 1.3, Max: 1.8, Step: 0.05, Default: 1.5, Customizable: true},

	length(GroupShape, "border.width", "Border width", "Width of structural borders, in pixels.", 1, 4, 2),
	{Name: "border.style", Group: GroupShape, Label: "Border style", Help: "Line style of structural borders.",
		Kind: KindChoice, Choices: []Choice{{"solid", "Solid"}, {"dashed", "Dashed"}}, Default: "solid", Customizable: true},
	length(GroupShape, "radius.small", "Small radius", "List rows and small controls.", 0, 12, 8),
	length(GroupShape, "radius.medium", "Medium radius", "Notices, menu items and compact components.", 0, 16, 10),
	length(GroupShape, "radius.large", "Large radius (cards)", "Cards and panels.", 0, 24, 14),
	length(GroupShape, "radius.xlarge", "Extra-large radius", "The mini player.", 0, 32, 20),
	length(GroupShape, "radius.button", "Button radius", "Buttons. Limited so buttons never become pills.", 0, 16, 10),
	length(GroupShape, "radius.input", "Input radius", "Text fields and menus.", 0, 14, 8),
	length(GroupShape, "radius.artwork", "Artwork radius", "Album artwork on the Now Playing screen.", 0, 32, 18),

	{Name: "shadow.size", Group: GroupSurface, Label: "Shadow", Help: "Size of the hard offset shadow under panels and the player.",
		Kind: KindChoice, Choices: []Choice{{"none", "None"}, {"small", "Small"}, {"medium", "Medium"}, {"large", "Large"}}, Default: "medium", Customizable: true},
	{Name: "spacing.density", Group: GroupSurface, Label: "Density", Help: "Spacing between and inside components. Touch targets keep their size.",
		Kind: KindChoice, Choices: []Choice{{"compact", "Compact"}, {"comfortable", "Comfortable"}, {"spacious", "Spacious"}}, Default: "comfortable", Customizable: true},
	{Name: "spacing.panel", Group: GroupSurface, Label: "Panel padding", Help: "Space inside panels, in pixels.",
		Kind: KindLength, Min: 12, Max: 40, Step: 4, Default: 24.0, Customizable: true},
}

var tokenIndex = func() map[string]*Token {
	m := make(map[string]*Token, len(tokens))
	for i := range tokens {
		m[tokens[i].Name] = &tokens[i]
	}
	return m
}()

// Tokens returns the token contract in display order. The result must not be
// modified.
func Tokens() []Token { return tokens }

// Lookup returns the token named name.
func Lookup(name string) (*Token, bool) {
	t, ok := tokenIndex[name]
	return t, ok
}

var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// ValueError explains why a token value was rejected. The message is
// written by Rewind and never echoes the value, so it is safe to show.
type ValueError struct{ Msg string }

func (e *ValueError) Error() string { return e.Msg }

// Validate checks a JSON value against the token's kind and limits and
// returns the normalized value: a string for colors and choices, a float64
// for lengths and numbers.
func (t *Token) Validate(raw json.RawMessage) (any, error) {
	switch t.Kind {
	case KindColor:
		var s string
		if json.Unmarshal(raw, &s) != nil || !colorPattern.MatchString(s) {
			return nil, &ValueError{fmt.Sprintf("%s must be a color like \"#c6ff00\" (# and six lower-case hex digits)", t.Name)}
		}
		return s, nil
	case KindChoice:
		var s string
		if json.Unmarshal(raw, &s) == nil {
			for _, c := range t.Choices {
				if c.Value == s {
					return s, nil
				}
			}
		}
		return nil, &ValueError{fmt.Sprintf("%s must be one of: %s", t.Name, strings.Join(t.choiceValues(), ", "))}
	default:
		var v float64
		// json.Unmarshal into float64 rejects strings, booleans and null.
		if json.Unmarshal(raw, &v) != nil || !t.inRange(v) {
			return nil, &ValueError{t.rangeMessage()}
		}
		return t.round(v), nil
	}
}

func (t *Token) choiceValues() []string {
	out := make([]string, len(t.Choices))
	for i, c := range t.Choices {
		out[i] = c.Value
	}
	return out
}

func (t *Token) inRange(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < t.Min-1e-9 || v > t.Max+1e-9 {
		return false
	}
	steps := (v - t.Min) / t.Step
	return math.Abs(steps-math.Round(steps)) < 1e-6
}

// round removes floating-point noise, so 1.1500000001 is stored as 1.15.
func (t *Token) round(v float64) float64 {
	snapped := t.Min + math.Round((v-t.Min)/t.Step)*t.Step
	return math.Round(snapped*1e6) / 1e6
}

func (t *Token) rangeMessage() string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	unit := ""
	if t.Kind == KindLength {
		unit = " pixels"
	}
	msg := fmt.Sprintf("%s must be a number from %s to %s%s", t.Name, f(t.Min), f(t.Max), unit)
	if t.Step != 1 {
		msg += fmt.Sprintf(" in steps of %s", f(t.Step))
	}
	return msg
}

// --- Contrast (WCAG 2.x), as in web/src/services/themes.ts ---

// Minimum contrast ratios.
const (
	MinTextContrast = 4.5
	MinUIContrast   = 3.0
)

func luminance(hex string) float64 {
	channel := func(i int) float64 {
		n, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(n) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(1) + 0.7152*channel(3) + 0.0722*channel(5)
}

// Contrast is the WCAG contrast ratio of two "#rrggbb" colors, 1 to 21.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TextOn returns black or white, whichever is more readable on fill.
func TextOn(fill string) string {
	if Contrast(fill, "#000000") >= Contrast(fill, "#ffffff") {
		return "#000000"
	}
	return "#ffffff"
}

// contrastPair is a foreground/background pair the UI draws, with the
// minimum contrast it needs.
type contrastPair struct {
	fg, bg string
	min    float64
	what   string
}

// contrastPairs are checked for every theme. Text needs 4.5:1 (WCAG AA),
// borders and other indicators 3:1.
var contrastPairs = []contrastPair{
	{"color.text", "color.background", MinTextContrast, "text on the background"},
	{"color.text", "color.surface", MinTextContrast, "text on surfaces"},
	{"color.text-muted", "color.background", MinTextContrast, "secondary text on the background"},
	{"color.text-muted", "color.surface", MinTextContrast, "secondary text on surfaces"},
	{"color.primary", "color.background", MinTextContrast, "primary (links) on the background"},
	{"color.primary", "color.surface", MinTextContrast, "primary (links) on surfaces"},
	{"color.on-primary", "color.primary", MinTextContrast, "text on primary buttons"},
	{"color.on-secondary", "color.secondary", MinTextContrast, "text on secondary buttons"},
	{"color.accent", "color.background", MinTextContrast, "accent (focus ring) on the background"},
	{"color.accent", "color.surface", MinTextContrast, "accent (focus ring) on surfaces"},
	{"color.on-accent", "color.accent", MinTextContrast, "text on accent markers"},
	{"color.danger", "color.background", MinTextContrast, "error text on the background"},
	{"color.danger", "color.surface", MinUIContrast, "error badges on surfaces"},
	{"color.border", "color.background", MinUIContrast, "borders on the background"},
	{"color.border", "color.secondary", MinUIContrast, "outlines of secondary buttons"},
}

// contrastProblem returns a message for the first pair of colors (a map of
// complete, valid color tokens) that is not readable, or "".
func contrastProblem(colors map[string]string) string {
	for _, p := range contrastPairs {
		if r := Contrast(colors[p.fg], colors[p.bg]); r < p.min-1e-9 {
			return fmt.Sprintf("too little contrast for %s (%s and %s: %.2f:1, at least %.1f:1 is needed)", p.what, p.fg, p.bg, r, p.min)
		}
	}
	return ""
}
