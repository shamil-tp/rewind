#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Production build: installs locked frontend dependencies, builds the frontend,
# verifies the output, and compiles a pure-Go binary with the frontend
# embedded (build tag rewind_embed).
#
# Usage:  scripts/build.sh
#   GOOS / GOARCH   cross-compile target (default: this machine)
#   VERSION         version string (default: git describe, or "dev")
#
# Output: bin/rewind (bin/rewind.exe when GOOS=windows)
set -eu

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

echo "==> Building frontend"
(cd web && npm ci && npm run build)

if [ ! -f web/dist/index.html ]; then
	echo "error: web/dist/index.html was not produced; the frontend build failed" >&2
	exit 1
fi

out="bin/rewind$(go env GOEXE)"
echo "==> Building $out (version $VERSION, $(go env GOOS)/$(go env GOARCH))"
CGO_ENABLED=0 go build -tags rewind_embed -trimpath \
	-ldflags "-s -w -X main.version=$VERSION" \
	-o "$out" ./cmd/rewind

echo "==> Done: $out"
