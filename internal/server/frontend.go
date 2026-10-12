// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// staticTypes pins content types for frontend files so they do not depend on
// the host's MIME database (the Windows registry can map .js to text/plain).
var staticTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

// newFrontendHandler serves the compiled frontend. Existing files are served
// by http.FileServerFS; extension-less paths that match no file are treated as
// client-side routes and receive index.html; missing assets return 404.
func newFrontendHandler(files fs.FS) (http.Handler, error) {
	if files == nil {
		return nil, fmt.Errorf("frontend: no files provided")
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, fmt.Errorf("frontend: index.html is missing: %w", err)
	}
	fileServer := http.FileServerFS(files)

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", staticTypes[".html"])
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r)
			return
		}
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			if ct, ok := staticTypes[path.Ext(name)]; ok {
				w.Header().Set("Content-Type", ct)
			}
			if strings.HasPrefix(name, "assets/") {
				// Vite puts content-hashed files under assets/.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) == "" {
			serveIndex(w, r)
			return
		}
		http.NotFound(w, r)
	}), nil
}
