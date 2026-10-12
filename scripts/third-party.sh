#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Collects third-party license texts and notices for a Rewind binary
# distribution and checks them.
#
# It fails (exit 1) if:
#   - the binary links a Go module that is not listed in
#     release/third-party-modules.txt (an unreviewed dependency), or
#   - a linked module ships no license file, or
#   - the license files of the embedded npm runtime code are missing
#     (run `npm ci` in web/ first).
#
# Output (in OUTDIR):
#   LICENSES/<module path>/...      license, notice and patent files
#   LICENSES/go/                    Go standard library and runtime
#   LICENSES/npm/<package>/         Svelte runtime and Vite preload helper
#                                   compiled into the embedded frontend
#   LICENSES/go.senan.xyz/taglib/wasm/  components inside taglib.wasm
#   THIRD_PARTY_NOTICES.md          summary
#
# Usage: scripts/third-party.sh OUTDIR [BINARY]
#   BINARY  a built rewind binary; its embedded build information is the
#           authoritative list of linked modules. Without it, the list is
#           computed with `go list` for GOOS/GOARCH (default linux/amd64).
# Requires: go, with modules downloaded (`go mod download`).
set -eu

out=${1:?usage: third-party.sh OUTDIR [BINARY]}
binary=${2:-}
if [ -n "$binary" ]; then
	binary=$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")
fi
cd "$(dirname "$0")/.."
approved=release/third-party-modules.txt
mkdir -p "$out/LICENSES"
out=$(cd "$out" && pwd)

errors=0
err() {
	echo "third-party: ERROR: $*" >&2
	errors=$((errors + 1))
}

# Modules linked into the binary. Dependencies differ per platform (for
# example, modernc.org/libc uses github.com/google/uuid only on Linux), so the
# binary's own build information is preferred.
if [ -n "$binary" ]; then
	mods=$(go version -m "$binary" | awk '$1 == "dep" {print $2, $3}' | sort -u)
	[ -n "$mods" ] || { echo "third-party: no module information in $binary" >&2; exit 1; }
else
	mods=$(GOOS=${GOOS:-linux} GOARCH=${GOARCH:-amd64} go list -deps 		-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./cmd/rewind | sort -u)
fi

table=""
for path in $(echo "$mods" | awk '{print $1}'); do
	version=$(echo "$mods" | awk -v p="$path" '$1 == p {print $2}')
	license=$(awk -v p="$path" '$1 == p {print $2}' "$approved")
	if [ -z "$license" ]; then
		err "$path $version is linked into the binary but not listed in $approved; review its license and add it"
		continue
	fi
	dir=$(go list -m -f '{{.Dir}}' "$path")
	if [ -z "$dir" ] || [ ! -d "$dir" ]; then
		err "$path: module source not found; run 'go mod download'"
		continue
	fi
	dest="$out/LICENSES/$path"
	mkdir -p "$dest"
	found=0
	for f in "$dir"/*; do
		[ -f "$f" ] || continue
		name=$(basename "$f")
		case $(echo "$name" | tr '[:lower:]' '[:upper:]') in
		LICENSE* | LICENCE* | COPYING* | NOTICE* | PATENTS* | COPYRIGHT*)
			cp "$f" "$dest/$name"
			found=$((found + 1))
			;;
		esac
	done
	if [ "$found" -eq 0 ]; then
		err "$path $version ships no license file"
	fi
	table="$table| \`$path\` | $version | $license |
"
done

# Approved entries that are no longer linked are only reported.
grep -v '^#' "$approved" | awk 'NF {print $1}' | while read -r path; do
	echo "$mods" | awk '{print $1}' | grep -qx "$path" ||
		echo "third-party: note: $path is approved but no longer linked; consider removing it from $approved" >&2
done

# Go standard library and runtime, compiled into every Go binary.
goroot=$(go env GOROOT)
goversion=$(go env GOVERSION)
mkdir -p "$out/LICENSES/go"
cp "$goroot/LICENSE" "$out/LICENSES/go/LICENSE"
[ -f "$goroot/PATENTS" ] && cp "$goroot/PATENTS" "$out/LICENSES/go/PATENTS"

# npm packages whose code is compiled into the embedded frontend bundle.
npm_table=""
for pkg in svelte vite; do
	pdir="web/node_modules/$pkg"
	lic=""
	for f in "$pdir"/[Ll][Ii][Cc][Ee][Nn][CcSs][Ee]*; do
		if [ -f "$f" ]; then
			lic=$(basename "$f")
			break
		fi
	done
	if [ -z "$lic" ]; then
		err "license file for npm package '$pkg' not found; run 'npm ci' in web/"
		continue
	fi
	mkdir -p "$out/LICENSES/npm/$pkg"
	cp "$pdir/$lic" "$out/LICENSES/npm/$pkg/$lic"
	pver=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$pdir/package.json" | head -n 1)
	npm_table="$npm_table| \`$pkg\` | $pver | MIT |
"
done

# Components statically linked inside taglib.wasm.
mkdir -p "$out/LICENSES/go.senan.xyz/taglib/wasm"
for f in release/notices/*; do
	cp "$f" "$out/LICENSES/go.senan.xyz/taglib/wasm/"
done

if [ "$errors" -gt 0 ]; then
	echo "third-party: $errors problem(s); refusing to continue" >&2
	exit 1
fi

cat >"$out/THIRD_PARTY_NOTICES.md" <<EOF
# Third-party notices

Rewind is licensed under the Apache License 2.0 (see \`LICENSE\`). This
binary distribution also contains the third-party software listed below.
Full license and notice texts are in the \`LICENSES/\` directory.

## Go modules compiled into the binary

| Module | Version | License |
| --- | --- | --- |
${table}
The Go standard library and runtime ($goversion) are also compiled in:
\`LICENSES/go/\` (BSD-3-Clause, with the Go patent grant).

## Code compiled into the embedded web interface

| npm package | Version | License |
| --- | --- | --- |
${npm_table}
## Components inside go-taglib's WebAssembly module

\`go.senan.xyz/taglib\` embeds \`taglib.wasm\`, which contains TagLib 2.1.1
(LGPL-2.1, also available under MPL-1.1), utfcpp (BSL-1.0), wasi-libc
(Apache-2.0 WITH LLVM-exception OR Apache-2.0 OR MIT, including musl code
under MIT) and LLVM libc++ (Apache-2.0 WITH LLVM-exception). See
\`LICENSES/go.senan.xyz/taglib/wasm/README.md\`.

## Apache-2.0 NOTICE files

\`github.com/tetratelabs/wazero\` provides a NOTICE file, reproduced in
\`LICENSES/github.com/tetratelabs/wazero/NOTICE\`:

$(sed 's/^/    /' "$out/LICENSES/github.com/tetratelabs/wazero/NOTICE")

## GNU LGPL-2.1 components

This binary contains \`go.senan.xyz/taglib\` and TagLib, which are licensed
under the GNU Lesser General Public License version 2.1
(\`LICENSES/go.senan.xyz/taglib/LICENSE\`). Their complete corresponding
source code is published next to this binary on the same release page, in
\`rewind-<version>-lgpl-sources.tar.gz\`, together with instructions for
rebuilding Rewind with a modified version of those libraries. Rewind's own
source is published there too (\`rewind-<version>-source.tar.gz\`). You may
modify Rewind and these libraries for your own use and reverse-engineer
them for debugging such modifications.
EOF

echo "third-party: collected licenses for $(echo "$mods" | wc -l | tr -d ' ') Go modules, Go $goversion, 2 npm packages and taglib.wasm components into $out"
