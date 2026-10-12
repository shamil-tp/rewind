// SPDX-License-Identifier: Apache-2.0

// Package missingtracks is Rewind's example plugin. After each library scan
// it logs a warning if the scan marked tracks as missing, which usually means
// files were deleted or moved outside Rewind. It shows the whole plugin
// contract: registration, a manifest, a declared capability, a handler and
// Close. Install its manifest (plugin.json in this directory) and enable it
// with REWIND_PLUGINS=missing-tracks; see docs/plugins.md.
package missingtracks

import (
	"context"
	"log/slog"

	"github.com/shamil-tp/rewind/internal/plugin"
)

// ID and Version must match plugin.json.
const (
	ID      = "missing-tracks"
	Version = "1.0.0"
)

func init() { plugin.Register(&Plugin{}) }

// Plugin implements plugin.Plugin.
type Plugin struct {
	log *slog.Logger
}

func (*Plugin) ID() string      { return ID }
func (*Plugin) Version() string { return Version }

// Init registers the scan handler. It keeps no goroutines or files open.
func (p *Plugin) Init(_ context.Context, host *plugin.Host) error {
	p.log = host.Logger()
	return host.OnLibraryScan(p.scanned)
}

func (p *Plugin) scanned(_ context.Context, e plugin.ScanEvent) error {
	if e.TracksMissing > 0 {
		p.log.Warn("library scan marked tracks as missing; were files deleted or moved outside Rewind?",
			"root_id", e.RootID, "scan_id", e.ScanID, "missing", e.TracksMissing)
	}
	return nil
}

// Close has nothing to release.
func (*Plugin) Close(context.Context) error { return nil }
