#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Packages a release from an already built bin/rewind (scripts/build.sh with
# GOOS=linux GOARCH=amd64). Produces in OUTDIR:
#
#   rewind-<version>-linux-amd64.tar.gz   binary, LICENSE, notices, docs,
#                                         configuration template, systemd unit
#   rewind-<version>-source.tar.gz        Rewind source at the release commit
#   rewind-<version>-lgpl-sources.tar.gz  LGPL corresponding source (go-taglib,
#                                         TagLib, utfcpp) and relink notes
#   install.sh                            installer (scripts/install.sh)
#   SHA256SUMS                            checksums of the files above
#
# Files are copied from an explicit allowlist; the archives are then checked
# for anything that must never be published (.env files, databases, keys,
# audio files) and for the files an installation needs.
#
# Usage: scripts/package-release.sh VERSION [OUTDIR]
#   PACKAGE_BINARY_ONLY=1  test mode for scripts/install-test.sh: builds only
#                          the binary archive and install.sh (SHA256SUMS lists
#                          just those), and allows uncommitted changes. Never
#                          use it for a release.
# Requires: Linux (as in the release workflow; file modes must be real), go,
# git, GNU tar, gzip, sha256sum, curl, and web/node_modules (npm ci).
set -eu

version=${1:?usage: package-release.sh VERSION [OUTDIR]}
outdir=${2:-dist}
cd "$(dirname "$0")/.."

case "$version" in
*[!A-Za-z0-9._+-]*) echo "package-release: invalid version '$version'" >&2; exit 1 ;;
esac
binary_only=${PACKAGE_BINARY_ONLY:-}
if [ -z "$binary_only" ] && [ -n "$(git status --porcelain --untracked-files=no)" ]; then
	echo "package-release: the working tree has uncommitted changes; release from a clean checkout" >&2
	exit 1
fi

bin=bin/rewind
[ -f "$bin" ] || { echo "package-release: $bin not found; run scripts/build.sh first" >&2; exit 1; }
info=$(go version -m "$bin")
# The version itself is checked by scripts/smoke-test.sh through /api/health
# (-trimpath omits -ldflags from the build information).
for want in "build	GOOS=linux" "build	GOARCH=amd64" "build	CGO_ENABLED=0" "build	-tags=rewind_embed"; do
	echo "$info" | grep -q "$want" || {
		echo "package-release: $bin was not built as expected (missing '$want'):" >&2
		echo "$info" >&2
		exit 1
	}
done

mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
name="rewind-$version-linux-amd64"
stage="$work/$name"
mkdir -p "$stage/docs"

cp "$bin" "$stage/rewind"
chmod 0755 "$stage/rewind"
cp LICENSE README.md CHANGELOG.md SECURITY.md "$stage/"
cp release/rewind.env "$stage/rewind.env.example"
cp release/rewind.service "$stage/rewind.service"
cp docs/install.md docs/configuration.md docs/operations.md docs/library.md \
	docs/deployment.md docs/api.md docs/themes.md docs/plugins.md "$stage/docs/"
sh scripts/third-party.sh "$stage" "$bin"

epoch=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$epoch" \
	-C "$work" -cf - "$name" | gzip -n -9 >"$outdir/$name.tar.gz"

mode=$(tar -tvzf "$outdir/$name.tar.gz" "$name/rewind" | awk '{print $1}')
case "$mode" in
-rwx*) ;;
*)
	echo "package-release: the binary is not executable in the archive (mode $mode)" >&2
	exit 1
	;;
esac

# Everything install.sh and a manual installation need.
for f in rewind rewind.service rewind.env.example LICENSE THIRD_PARTY_NOTICES.md \
	LICENSES/go.senan.xyz/taglib/LICENSE LICENSES/github.com/tetratelabs/wazero/NOTICE docs/install.md; do
	tar -tzf "$outdir/$name.tar.gz" "$name/$f" >/dev/null 2>&1 || {
		echo "package-release: $name.tar.gz is missing $f" >&2
		exit 1
	}
done

cp scripts/install.sh "$outdir/install.sh"
archives="$outdir/$name.tar.gz"
sums_files="$name.tar.gz install.sh"
if [ -z "$binary_only" ]; then
	git archive --format=tar --prefix="rewind-$version/" HEAD | gzip -n -9 >"$outdir/rewind-$version-source.tar.gz"
	sh scripts/lgpl-sources.sh "$outdir/rewind-$version-lgpl-sources.tar.gz"
	archives="$archives $outdir/rewind-$version-source.tar.gz"
	sums_files="$name.tar.gz rewind-$version-source.tar.gz rewind-$version-lgpl-sources.tar.gz install.sh"
fi

# Nothing private may be published.
for archive in $archives; do
	bad=$(tar -tzf "$archive" | grep -E -i '(^|/)\.env($|\.)|\.db($|-)|\.sqlite3?$|setup-code$|id_(rsa|ed25519|ecdsa)|\.pem$|\.key$|\.(mp3|flac|m4a|ogg|oga|wav|opus)$' | grep -v -E '(^|/)\.env\.example$' || true)
	if [ -n "$bad" ]; then
		echo "package-release: refusing to publish $archive; it contains:" >&2
		echo "$bad" >&2
		exit 1
	fi
done

# shellcheck disable=SC2086 # sums_files is a list of plain file names
(cd "$outdir" && sha256sum $sums_files >SHA256SUMS && sha256sum -c --quiet SHA256SUMS)
echo "package-release: wrote $outdir:"
cat "$outdir/SHA256SUMS"
