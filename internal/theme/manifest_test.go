// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/prefs"
)

// manifest returns a valid manifest as a map, so tests can break one field.
func manifest(id string) map[string]any {
	return map[string]any{
		"schema": 1, "id": id, "name": "Test " + id, "version": "1.0.0", "theme_api": 1, "scheme": "dark",
		"tokens": map[string]any{
			"color.background": "#101010", "color.surface": "#1a1a1a", "color.text": "#f0f0f0",
			"color.text-muted": "#b0b0b0", "color.border": "#f0f0f0", "color.primary": "#ffcc00",
			"color.secondary": "#333333", "color.accent": "#66ddff", "color.danger": "#ff6666",
		},
	}
}

func encode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, m map[string]any) (*Theme, error) {
	t.Helper()
	return ParseManifest(bytes.NewReader(encode(t, m)))
}

func tokensOf(m map[string]any) map[string]any { return m["tokens"].(map[string]any) }

func TestParseValidManifestFillsDefaults(t *testing.T) {
	th, err := parse(t, manifest("sunset"))
	if err != nil {
		t.Fatal(err)
	}
	if th.ID != "sunset" || th.Scheme != SchemeDark {
		t.Fatalf("theme = %+v", th)
	}
	// Every token has a value: omitted ones are derived or defaulted.
	for _, spec := range Tokens() {
		if _, ok := th.Tokens[spec.Name]; !ok {
			t.Errorf("%s missing", spec.Name)
		}
	}
	if th.Tokens["color.on-primary"] != "#000000" || th.Tokens["color.on-accent"] != "#000000" {
		t.Errorf("derived text colors = %v, %v", th.Tokens["color.on-primary"], th.Tokens["color.on-accent"])
	}
	if th.Tokens["color.on-secondary"] != "#f0f0f0" { // the theme's text is readable on its secondary
		t.Errorf("on-secondary = %v", th.Tokens["color.on-secondary"])
	}
	if th.Tokens["radius.button"] != 10.0 || th.Tokens["font.family"] != "system" {
		t.Errorf("defaults not applied: %v %v", th.Tokens["radius.button"], th.Tokens["font.family"])
	}
	// Without a customizable list, every customizable token may be changed.
	var want []string
	for _, spec := range Tokens() {
		if spec.Customizable {
			want = append(want, spec.Name)
		}
	}
	if strings.Join(th.Customizable, ",") != strings.Join(want, ",") {
		t.Errorf("customizable = %v", th.Customizable)
	}
}

func TestParseRejectsInvalidManifests(t *testing.T) {
	cases := map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"schema":            {func(m map[string]any) { m["schema"] = 2 }, "unsupported manifest schema 2"},
		"id":                {func(m map[string]any) { m["id"] = "../evil" }, "id must be"},
		"id upper":          {func(m map[string]any) { m["id"] = "Sunset" }, "id must be"},
		"id long":           {func(m map[string]any) { m["id"] = "a" + strings.Repeat("b", 40) }, "id must be"},
		"name empty":        {func(m map[string]any) { m["name"] = "" }, "name must be 1 to 64"},
		"name control":      {func(m map[string]any) { m["name"] = "a\u0007b" }, "control characters"},
		"name spaces":       {func(m map[string]any) { m["name"] = " x" }, "must not start or end"},
		"version":           {func(m map[string]any) { m["version"] = "1.0" }, "version must look like"},
		"version leading 0": {func(m map[string]any) { m["version"] = "01.0.0" }, "version must look like"},
		"homepage http":     {func(m map[string]any) { m["homepage"] = "http://example.org" }, "https://"},
		"homepage js":       {func(m map[string]any) { m["homepage"] = "javascript:alert(1)" }, "https://"},
		"theme_api zero":    {func(m map[string]any) { m["theme_api"] = 0 }, "positive theme API"},
		"scheme":            {func(m map[string]any) { m["scheme"] = "sepia" }, `scheme must be "dark" or "light"`},
		"scheme mismatch":   {func(m map[string]any) { m["scheme"] = "light" }, `scheme must be "dark" for this background`},
		"no tokens":         {func(m map[string]any) { delete(m, "tokens") }, "tokens is required"},
		"required token":    {func(m map[string]any) { delete(tokensOf(m), "color.danger") }, "color.danger is required"},
		"unknown token":     {func(m map[string]any) { tokensOf(m)["color.glow"] = "#ffffff" }, `"color.glow" is not a token`},
		"css in color":      {func(m map[string]any) { tokensOf(m)["color.accent"] = "red;}body{display:none" }, "color.accent must be a color"},
		"url in color":      {func(m map[string]any) { tokensOf(m)["color.accent"] = "url(https://evil.example/x)" }, "must be a color"},
		"upper hex":         {func(m map[string]any) { tokensOf(m)["color.accent"] = "#66DDFF" }, "must be a color"},
		"short hex":         {func(m map[string]any) { tokensOf(m)["color.accent"] = "#6df" }, "must be a color"},
		"var()":             {func(m map[string]any) { tokensOf(m)["color.accent"] = "var(--rw-color-bg)" }, "must be a color"},
		"length range":      {func(m map[string]any) { tokensOf(m)["radius.button"] = 40 }, "radius.button must be a number from 0 to 16 pixels"},
		"length negative":   {func(m map[string]any) { tokensOf(m)["border.width"] = -1 }, "border.width must be a number from 1 to 4"},
		"length fraction":   {func(m map[string]any) { tokensOf(m)["border.width"] = 2.5 }, "border.width must be"},
		"length string":     {func(m map[string]any) { tokensOf(m)["border.width"] = "2px" }, "border.width must be"},
		"number step":       {func(m map[string]any) { tokensOf(m)["font.line-height"] = 1.52 }, "in steps of 0.05"},
		"choice":            {func(m map[string]any) { tokensOf(m)["font.family"] = "Comic Sans" }, "font.family must be one of: system,"},
		"choice css":        {func(m map[string]any) { tokensOf(m)["border.style"] = "solid; color: red" }, "border.style must be one of"},
		"null token":        {func(m map[string]any) { tokensOf(m)["border.width"] = nil }, "border.width must be"},
		"text contrast": {func(m map[string]any) { tokensOf(m)["color.text-muted"] = "#333333" },
			"too little contrast for secondary text on the background"},
		"accent contrast": {func(m map[string]any) { tokensOf(m)["color.accent"] = "#202040" }, "too little contrast for accent"},
		"on-primary contrast": {func(m map[string]any) { tokensOf(m)["color.on-primary"] = "#ffffff" },
			"too little contrast for text on primary buttons"},
		"customizable unknown":    {func(m map[string]any) { m["customizable"] = []string{"color.glow"} }, `"color.glow" is not a token`},
		"customizable derived":    {func(m map[string]any) { m["customizable"] = []string{"color.on-primary"} }, "cannot be customized"},
		"customizable theme-only": {func(m map[string]any) { m["customizable"] = []string{"color.danger"} }, "cannot be customized"},
		"customizable twice":      {func(m map[string]any) { m["customizable"] = []string{"font.size", "font.size"} }, "listed twice"},
		"unknown field":           {func(m map[string]any) { m["script"] = "rm -rf /" }, "field this version of Rewind does not know"},
		"unknown field css":       {func(m map[string]any) { m["css"] = "body{}" }, "does not know"},
		"wrong type":              {func(m map[string]any) { m["schema"] = "1" }, `"schema" has the wrong type`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := manifest("sunset")
			c.edit(m)
			_, err := parse(t, m)
			var me *ManifestError
			if !errors.As(err, &me) || me.Incompatible {
				t.Fatalf("error = %v, want a ManifestError", err)
			}
			if !strings.Contains(me.Msg, c.want) {
				t.Errorf("error = %q, want it to contain %q", me.Msg, c.want)
			}
		})
	}
}

func TestParseRejectsMalformedFiles(t *testing.T) {
	valid := string(encode(t, manifest("sunset")))
	for name, data := range map[string]string{
		"trailing data":   valid + `{}`,
		"not an object":   `[1,2]`,
		"empty":           ``,
		"invalid utf8":    "{\"name\": \"\xff\"}",
		"duplicate key":   strings.Replace(valid, `"id":"sunset"`, `"id":"sunset","id":"other"`, 1),
		"duplicate token": strings.Replace(valid, `"color.accent":"#66ddff"`, `"color.accent":"#66ddff","color.accent":"#202040"`, 1),
		"too large":       valid[:len(valid)-1] + `,"description":"` + strings.Repeat("x", MaxManifestBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if name == "duplicate key" || name == "duplicate token" {
				if !strings.Contains(data, `,"`) || data == valid {
					t.Fatal("test setup: the duplicate was not inserted")
				}
			}
			var me *ManifestError
			if _, err := ParseManifest(strings.NewReader(data)); !errors.As(err, &me) {
				t.Errorf("error = %v, want a ManifestError", err)
			}
		})
	}
}

func TestParseReportsIncompatibleAPI(t *testing.T) {
	m := manifest("future")
	m["theme_api"] = 2
	// A newer theme may use tokens this version does not know; it is
	// reported as incompatible, not invalid.
	tokensOf(m)["color.glow"] = "#ffffff"
	_, err := parse(t, m)
	var me *ManifestError
	if !errors.As(err, &me) || !me.Incompatible || !strings.Contains(me.Msg, "theme API 2") {
		t.Fatalf("error = %v, want incompatible", err)
	}
}

func TestCustomizableList(t *testing.T) {
	m := manifest("locked")
	m["customizable"] = []string{"font.size", "color.accent"}
	th, err := parse(t, m)
	if err != nil {
		t.Fatal(err)
	}
	// Contract order, not manifest order.
	if strings.Join(th.Customizable, ",") != "color.accent,font.size" || th.CanCustomize("color.background") {
		t.Errorf("customizable = %v", th.Customizable)
	}
	m["customizable"] = []string{}
	if th, err = parse(t, m); err != nil || len(th.Customizable) != 0 {
		t.Errorf("empty list: %v, %v", th, err)
	}
}

func TestTokenValidationNormalizes(t *testing.T) {
	lh, _ := Lookup("font.line-height")
	// 1.3 + 7 steps of 0.05 is 1.6500000000000001 in floating point; stored as 1.65.
	v, err := lh.Validate(json.RawMessage(`1.65`))
	if err != nil || v != 1.65 {
		t.Errorf("1.65 = %v, %v", v, err)
	}
	for _, bad := range []string{`1.29`, `1.85`, `"1.5"`, `true`, `null`, `[]`, `1e400`} {
		if _, err := lh.Validate(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	panel, _ := Lookup("spacing.panel")
	if _, err := panel.Validate(json.RawMessage(`26`)); err == nil {
		t.Error("26 accepted for a step-4 token")
	}
	if v, err := panel.Validate(json.RawMessage(`28`)); err != nil || v != 28.0 {
		t.Errorf("28 = %v, %v", v, err)
	}
}

func TestTokenDefaultsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range Tokens() {
		if seen[spec.Name] {
			t.Errorf("%s defined twice", spec.Name)
		}
		seen[spec.Name] = true
		raw, _ := json.Marshal(spec.Default)
		if _, err := spec.Validate(raw); err != nil {
			t.Errorf("%s default %v: %v", spec.Name, spec.Default, err)
		}
		if spec.Kind == KindColor && spec.Name != "color.danger" && spec.Customizable != spec.Required {
			t.Errorf("%s: customizable colors are exactly the required ones except danger", spec.Name)
		}
	}
}

func TestBundledThemes(t *testing.T) {
	themes, err := loadBundled()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, th := range themes {
		names = append(names, th.ID+"="+th.Name)
	}
	if got := strings.Join(names, ","); got != "rewind-dark=Rewind Dark,light=Rewind Light,oled-black=Rewind OLED" {
		t.Errorf("bundled = %s", got)
	}
	// Rewind Dark is exactly the contract's defaults, which equal app.css.
	for _, spec := range Tokens() {
		if themes[0].Tokens[spec.Name] != spec.Default {
			t.Errorf("rewind-dark %s = %v, default %v", spec.Name, themes[0].Tokens[spec.Name], spec.Default)
		}
	}
	// OLED keeps its pure black background.
	for _, th := range themes {
		if th.ID == "oled-black" && (th.CanCustomize("color.background") || th.Tokens["color.background"] != "#000000") {
			t.Error("OLED background must be black and fixed")
		}
	}
}

// TestPrefsDefaultThemes keeps the preference store's built-in list (used
// when it is not given the registry) equal to the bundled themes.
func TestPrefsDefaultThemes(t *testing.T) {
	themes, err := loadBundled()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, th := range themes {
		ids = append(ids, th.ID)
	}
	if strings.Join(ids, ",") != strings.Join(prefs.Themes, ",") || prefs.Defaults().Theme != DefaultID {
		t.Errorf("bundled %v, prefs.Themes %v, default %q", ids, prefs.Themes, prefs.Defaults().Theme)
	}
}

var update = flag.Bool("update", false, "rewrite web/src/services/theme-tokens.json from the token contract")

// TestTokensMatchFrontend keeps the web UI's copy of the token contract in
// step with this package. Run with -update after changing tokens.go.
func TestTokensMatchFrontend(t *testing.T) {
	want, err := json.MarshalIndent(struct {
		ThemeAPI int     `json:"theme_api"`
		Tokens   []Token `json:"tokens"`
	}{APIVersion, Tokens()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("..", "..", "web", "src", "services", "theme-tokens.json")
	if *update {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Errorf("%s is out of date; run: go test ./internal/theme -run TestTokensMatchFrontend -update", path)
	}
}
