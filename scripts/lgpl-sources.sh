#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Builds the LGPL-2.1 corresponding-source bundle that must be published next
# to every Rewind binary (see docs/deployment.md, "Licensing of release
# binaries"). The bundle contains:
#   - the exact go.senan.xyz/taglib module that is linked into the binary
#     (wrapper, build scripts, and the prebuilt taglib.wasm), as verified by
#     the Go checksum database,
#   - the TagLib 2.1.1 and utfcpp sources at the commits go-taglib pins as git
#     submodules (they are not part of the Go module download),
#   - instructions for rebuilding taglib.wasm and relinking Rewind.
#
# It refuses to run if go.mod requires a go-taglib version other than the
# reviewed one, or if a downloaded archive does not match its pinned SHA-256.
#
# Usage: scripts/lgpl-sources.sh OUTPUT.tar.gz
# Requires: go, curl, tar, gzip, sha256sum.
set -eu

out=${1:?usage: lgpl-sources.sh OUTPUT.tar.gz}
cd "$(dirname "$0")/.."

# Reviewed versions. Changing go-taglib requires re-checking its submodule
# commits, its wasm toolchain, and release/notices/, then updating these pins.
TAGLIB_MODULE=go.senan.xyz/taglib
TAGLIB_MODULE_VERSION=v0.14.0
TAGLIB_COMMIT=7d86716194777e0294453bfdc9dd170bd033e1f4 # TagLib v2.1.1
TAGLIB_SHA256=4e8ecdd894854441465784869d4d0092f32c98455c51e9b21102fdda2dbe5081
UTFCPP_COMMIT=df857efc5bbc2aa84012d865f7d7e9cccdc08562
UTFCPP_SHA256=911ff4f13cc7bfece2b5f65e7468b962db19c9727f89d560b2617360af08f538

linked=$(go list -m -f '{{.Version}}' "$TAGLIB_MODULE")
if [ "$linked" != "$TAGLIB_MODULE_VERSION" ]; then
	echo "lgpl-sources: go.mod requires $TAGLIB_MODULE $linked, but $TAGLIB_MODULE_VERSION is the reviewed version." >&2
	echo "lgpl-sources: re-review the LGPL source obligations and update the pins in $0" >&2
	exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bundle="$work/lgpl-sources"
mkdir -p "$bundle"

# The module zip from the Go module cache; `go mod download` verifies it
# against go.sum and the checksum database.
zip=$(go mod download -json "$TAGLIB_MODULE@$TAGLIB_MODULE_VERSION" | sed -n 's/.*"Zip": "\(.*\)".*/\1/p' | sed 's/\\\\/\//g')
[ -f "$zip" ] || {
	echo "lgpl-sources: module zip not found" >&2
	exit 1
}
cp "$zip" "$bundle/go-taglib-$TAGLIB_MODULE_VERSION.zip"

fetch() { # url sha256 dest
	curl -fsSL --retry 3 -o "$3" "$1"
	got=$(sha256sum "$3" | awk '{print $1}')
	if [ "$got" != "$2" ]; then
		echo "lgpl-sources: checksum mismatch for $1" >&2
		echo "  want $2" >&2
		echo "  got  $got" >&2
		exit 1
	fi
}
fetch "https://codeload.github.com/taglib/taglib/tar.gz/$TAGLIB_COMMIT" "$TAGLIB_SHA256" \
	"$bundle/taglib-2.1.1-$TAGLIB_COMMIT.tar.gz"
fetch "https://codeload.github.com/nemtrif/utfcpp/tar.gz/$UTFCPP_COMMIT" "$UTFCPP_SHA256" \
	"$bundle/utfcpp-$UTFCPP_COMMIT.tar.gz"

cat >"$bundle/README.md" <<EOF
# LGPL-2.1 corresponding source for Rewind

Rewind binaries contain \`$TAGLIB_MODULE\` $TAGLIB_MODULE_VERSION and TagLib 2.1.1,
which are licensed under the GNU Lesser General Public License version 2.1.
This archive contains their complete corresponding source:

| File | Contents |
| --- | --- |
| \`go-taglib-$TAGLIB_MODULE_VERSION.zip\` | The Go module exactly as linked into Rewind (wrapper \`taglib.go\`, C++ glue \`taglib.cpp\`, \`CMakeLists.txt\`, \`build/\` scripts, and the prebuilt \`taglib.wasm\`). |
| \`taglib-2.1.1-$TAGLIB_COMMIT.tar.gz\` | TagLib at commit $TAGLIB_COMMIT (tag v2.1.1), the \`taglib\` submodule of go-taglib $TAGLIB_MODULE_VERSION. |
| \`utfcpp-$UTFCPP_COMMIT.tar.gz\` | utfcpp at commit $UTFCPP_COMMIT, TagLib's \`3rdparty/utfcpp\` submodule. |

Rewind's own source code (Apache-2.0) is published on the same release page
as \`rewind-<version>-source.tar.gz\`.

## Rebuilding taglib.wasm

1. Unzip \`go-taglib-$TAGLIB_MODULE_VERSION.zip\`. The module is in
   \`$TAGLIB_MODULE@$TAGLIB_MODULE_VERSION/\`.
2. Extract the TagLib archive into that module's \`taglib/\` directory and the
   utfcpp archive into \`taglib/3rdparty/utfcpp/\`. Apply your modifications.
3. Build with wasi-sdk 27 and binaryen, as go-taglib's \`build/Dockerfile\` and
   \`build/build.sh\` describe:

       sh build/build.sh /path/to/wasi-sdk-27

   This produces a new \`taglib.wasm\`.

## Relinking Rewind with a modified library

From Rewind's source tree, either:

- point the build at your modified go-taglib module:

      go mod edit -replace $TAGLIB_MODULE=/path/to/modified/go-taglib
      scripts/build.sh

- or keep go-taglib and make Rewind load your \`taglib.wasm\` from a file at
  run time instead of the embedded one (go-taglib reads the path, set at link
  time, when it initializes):

      go build -tags rewind_embed \\
        -ldflags "-X '$TAGLIB_MODULE.binaryPath=/path/to/taglib.wasm'" \\
        -o rewind ./cmd/rewind

  Build the frontend first (\`cd web && npm ci && npm run build\`) so the
  \`rewind_embed\` build has its assets.
EOF

mkdir -p "$(dirname "$out")"
# Reproducible archive: fixed order, owners and timestamps.
epoch=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$epoch" \
	-C "$work" -cf - lgpl-sources | gzip -n -9 >"$out"
echo "lgpl-sources: wrote $out"
