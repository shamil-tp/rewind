// SPDX-License-Identifier: Apache-2.0

//go:build rewind_embed

package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// Compilation fails here if web/dist does not exist, so a production binary
// cannot be built without the frontend. "all:" keeps files whose names start
// with "_" or ".", which Vite may emit.
//
//go:embed all:dist
var dist embed.FS

// Embedded reports whether the compiled frontend (web/dist) is embedded.
const Embedded = true

// Frontend returns the files to serve at "/".
func Frontend() (fs.FS, error) {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		return nil, fmt.Errorf("embedded frontend has no index.html; rebuild with scripts/build.sh: %w", err)
	}
	return files, nil
}
