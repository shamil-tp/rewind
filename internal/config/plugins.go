// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shamil-tp/rewind/internal/plugin"
)

// maxEnabledPlugins bounds REWIND_PLUGINS.
const maxEnabledPlugins = 32

// parsePlugins reads REWIND_PLUGIN_DIR and REWIND_PLUGINS (see
// docs/plugins.md). Plugins are off unless a directory is set, and only the
// listed IDs are ever started.
func parsePlugins(getenv func(string) string, cfg *Config) []error {
	var errs []error
	if v := strings.TrimSpace(getenv("REWIND_PLUGIN_DIR")); v != "" {
		if !filepath.IsAbs(v) {
			errs = append(errs, fmt.Errorf("REWIND_PLUGIN_DIR=%q must be an absolute path", v))
		} else {
			cfg.PluginDir = filepath.Clean(v)
		}
	}

	seen := map[string]bool{}
	for _, raw := range strings.Split(getenv("REWIND_PLUGINS"), ",") {
		id := strings.TrimSpace(raw)
		switch {
		case id == "":
		case !plugin.ValidID(id):
			errs = append(errs, fmt.Errorf("REWIND_PLUGINS: %q is not a plugin ID (lower-case letters, digits and hyphens)", id))
		case seen[id]:
			errs = append(errs, fmt.Errorf("REWIND_PLUGINS: %q is listed more than once", id))
		default:
			seen[id] = true
			cfg.Plugins = append(cfg.Plugins, id)
		}
	}
	if len(cfg.Plugins) > maxEnabledPlugins {
		errs = append(errs, fmt.Errorf("REWIND_PLUGINS: at most %d plugins can be enabled", maxEnabledPlugins))
	}
	if len(cfg.Plugins) > 0 && cfg.PluginDir == "" && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("REWIND_PLUGINS is set but REWIND_PLUGIN_DIR is not; set the directory that holds the plugin manifests"))
	}

	// The plugin directory holds only manifests. Keeping it apart from user
	// data means no plugin setting can point at the database or music files.
	if cfg.PluginDir != "" {
		others := append([]string{cfg.DataDir}, cfg.LibraryRoots...)
		for _, other := range others {
			if other != "" && (within(cfg.PluginDir, other) || within(other, cfg.PluginDir)) {
				errs = append(errs, fmt.Errorf("REWIND_PLUGIN_DIR=%q must not be, contain or be inside the data directory or a library root (%q)",
					cfg.PluginDir, other))
			}
		}
	}
	return errs
}

// within reports whether path is dir or inside it. On Windows the
// comparison ignores case, like the file system.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
