// SPDX-License-Identifier: Apache-2.0

//go:build !rewind_embed

// Package web provides the frontend files served by the Go server.
//
// By default the binary embeds web/placeholder, a static page explaining that
// the real frontend was not built in. Production builds use the rewind_embed
// build tag (see scripts/build.sh), which embeds the Vite output in web/dist
// instead. This keeps `go build`, `go test` and `go vet` working on a fresh
// checkout without Node.js.
package web

import (
	"embed"
	"io/fs"
)

//go:embed placeholder
var placeholder embed.FS

// Embedded reports whether the compiled frontend (web/dist) is embedded.
const Embedded = false

// Frontend returns the files to serve at "/".
func Frontend() (fs.FS, error) {
	return fs.Sub(placeholder, "placeholder")
}
