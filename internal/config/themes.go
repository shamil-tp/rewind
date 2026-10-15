// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// parseThemes reads REWIND_THEME_DIR (see docs/themes.md). Without it, only
// the bundled themes are available. The directory holds theme manifests
// only, so it is kept apart from user data, music and plugins: no theme
// setting can point at the database, music files or plugin manifests.
func parseThemes(getenv func(string) string, cfg *Config) []error {
	v := strings.TrimSpace(getenv("REWIND_THEME_DIR"))
	if v == "" {
		return nil
	}
	if !filepath.IsAbs(v) {
		return []error{fmt.Errorf("REWIND_THEME_DIR=%q must be an absolute path", v)}
	}
	cfg.ThemeDir = filepath.Clean(v)

	var errs []error
	others := append([]string{cfg.DataDir, cfg.PluginDir}, cfg.LibraryRoots...)
	for _, other := range others {
		if other != "" && (within(cfg.ThemeDir, other) || within(other, cfg.ThemeDir)) {
			errs = append(errs, fmt.Errorf("REWIND_THEME_DIR=%q must not be, contain or be inside the data directory, the plugin directory or a library root (%q)",
				cfg.ThemeDir, other))
		}
	}
	return errs
}
