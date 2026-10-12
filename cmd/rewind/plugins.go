// SPDX-License-Identifier: Apache-2.0

package main

// Plugins compiled into this binary. Each blank import registers a plugin
// (see internal/plugin.Register); a compiled-in plugin still does nothing
// until its manifest is installed and its ID is listed in REWIND_PLUGINS.
// To add a plugin, add its package here and rebuild (docs/plugins.md).
import (
	_ "github.com/shamil-tp/rewind/plugins/missingtracks"
)
