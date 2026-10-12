#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Pre-release validation on Linux: the checks of ci.yml and release.yml in
# one run, without publishing anything. It runs Go and frontend checks,
# builds the release binary with the embedded frontend, smoke-tests it,
# packages the release (license notices, LGPL sources, SHA256SUMS), verifies
# the checksums and the archive contents, and syntax-checks the scripts.
#
# Usage: scripts/release-check.sh VERSION [OUTDIR]
#   VERSION  the version to build, for example v0.1.0-rc.1 (nothing is tagged)
#   OUTDIR   where the release files are written (default: dist)
#   RELEASE_CHECK_BINARY_ONLY=1  package only the binary archive and
#            install.sh, which also works with uncommitted changes
#   RELEASE_CHECK_INSTALL=1      also run scripts/install-test.sh through
#            sudo: installs, upgrades and rolls back a real systemd service.
#            Only on a disposable machine.
#
# Requires: Linux amd64, Go (go.mod's toolchain), Node.js 24 with npm, git,
# GNU tar, curl, sha256sum. The race detector needs a C compiler; without
# one, the tests run without -race and the summary says so.
set -eu

version=${1:?usage: release-check.sh VERSION [OUTDIR]}
outdir=${2:-dist}
cd "$(dirname "$0")/.."
[ "$(uname -s)" = Linux ] || { echo "release-check: run on Linux" >&2; exit 1; }

notes=""
note() { notes="$notes
  - $*"; }
step() { echo "==> $*"; }

step "Go formatting, modules, vet"
unformatted=$(gofmt -l .)
[ -z "$unformatted" ] || { echo "gofmt needed:" >&2; echo "$unformatted" >&2; exit 1; }
go mod tidy -diff
go mod verify
go vet ./...

step "Go tests"
if CGO_ENABLED=1 go test -race -count=1 ./internal/config/ >/dev/null 2>&1; then
	go test -race -count=1 ./...
else
	note "go test ran WITHOUT the race detector (no C compiler); CI runs it with -race"
	go test -count=1 ./...
fi

step "Frontend checks"
(cd web && npm ci && npm run check && npm test)

step "Release build ($version, linux/amd64)"
GOOS=linux GOARCH=amd64 VERSION=$version sh scripts/build.sh
go vet -tags rewind_embed ./...
go test -count=1 -tags rewind_embed ./web/ ./internal/server/

step "Smoke test"
sh scripts/smoke-test.sh bin/rewind "$version"

step "Package"
go mod download
rm -rf "$outdir"
if [ "${RELEASE_CHECK_BINARY_ONLY:-}" = 1 ]; then
	note "binary-only packaging: the source and LGPL archives were NOT built"
	PACKAGE_BINARY_ONLY=1 sh scripts/package-release.sh "$version" "$outdir"
else
	sh scripts/package-release.sh "$version" "$outdir"
fi

step "Verify checksums and archive contents"
(cd "$outdir" && sha256sum -c SHA256SUMS)
name="rewind-$version-linux-amd64"
listing=$(tar -tzf "$outdir/$name.tar.gz")
echo "$listing" | grep -v '^[^/]*/LICENSES/' | sed 's/^/    /'
echo "    ... and $(echo "$listing" | grep -c '/LICENSES/.') files under LICENSES/"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$outdir/$name.tar.gz" -C "$tmp"
cmp -s "$tmp/$name/rewind" bin/rewind || { echo "the archived binary differs from bin/rewind" >&2; exit 1; }
SMOKE_PORT=18732 sh scripts/smoke-test.sh "$tmp/$name/rewind" "$version"
if [ -f "$outdir/rewind-$version-lgpl-sources.tar.gz" ]; then
	tar -tzf "$outdir/rewind-$version-lgpl-sources.tar.gz" | sed 's/^/    /'
fi

step "Script syntax"
for s in scripts/*.sh; do sh -n "$s"; done
if command -v shellcheck >/dev/null 2>&1; then
	shellcheck scripts/*.sh
else
	note "shellcheck is not installed; scripts were only syntax-checked (sh -n)"
fi

if [ "${RELEASE_CHECK_INSTALL:-}" = 1 ]; then
	step "Installation test (sudo)"
	old="$tmp/install-old"
	mkdir -p "$old"
	GOOS=linux GOARCH=amd64 VERSION="$version-previous" sh scripts/build.sh
	PACKAGE_BINARY_ONLY=1 sh scripts/package-release.sh "$version-previous" "$old"
	# Restore bin/rewind for the version under test.
	GOOS=linux GOARCH=amd64 VERSION=$version sh scripts/build.sh
	sudo sh scripts/install-test.sh "$old" "$outdir"
else
	note "the installation test did not run (set RELEASE_CHECK_INSTALL=1 on a disposable machine)"
fi

echo
echo "release-check: all checks passed for $version. Files in $outdir:"
cat "$outdir/SHA256SUMS"
if [ -n "$notes" ]; then
	echo "Not covered by this run:$notes"
fi
