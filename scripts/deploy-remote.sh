#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Server-side half of .github/workflows/deploy.yml. The workflow streams this
# script over SSH (`ssh host sh -s -- ARGS < scripts/deploy-remote.sh`); it is
# never installed on the server. See docs/deployment.md for the required
# server layout. It never touches Rewind's data directory, database (except
# for an optional read-only online backup), configuration, or music.
#
# Usage:
#   deploy-remote.sh preflight DIR SERVICE HEALTH_URL [DB_PATH BACKUP_DIR]
#   deploy-remote.sh activate  DIR SERVICE HEALTH_URL RELEASE_ID SHA256 VERSION [DB_PATH BACKUP_DIR]
#
#   DIR         deployment directory; contains releases/ and the current symlink
#   SERVICE     systemd unit whose ExecStart runs DIR/current/rewind
#   HEALTH_URL  loopback health URL, e.g. http://127.0.0.1:8090/api/health
#   RELEASE_ID  name for the new release directory under DIR/releases
#   SHA256      expected checksum of the uploaded binary
#   VERSION     version the new binary must report on /api/health
#   DB_PATH, BACKUP_DIR  optional: back up the database with sqlite3 first
#
# The uploaded binary must already be at DIR/releases/.incoming-RELEASE_ID.
set -eu

die() {
	echo "deploy: ERROR: $*" >&2
	exit 1
}
say() { echo "deploy: $*"; }

mode=${1:-}
dir=${2:-}
service=${3:-}
health=${4:-}
[ -n "$mode" ] && [ -n "$dir" ] && [ -n "$service" ] && [ -n "$health" ] || die "missing arguments"
case "$dir" in /*) ;; *) die "DIR must be absolute" ;; esac
[ "$dir" != "/" ] || die "DIR must not be /"

releases="$dir/releases"
current="$dir/current"

health_check() { # expected-version (may be empty) -> 0 when healthy
	want=$1
	i=0
	while [ "$i" -lt 30 ]; do
		body=$(curl -fsS --max-time 2 "$health" 2>/dev/null || true)
		case "$body" in
		*'"status":"ok"'*)
			if [ -z "$want" ]; then return 0; fi
			case "$body" in *"\"version\":\"$want\""*) return 0 ;; esac
			;;
		esac
		i=$((i + 1))
		sleep 1
	done
	say "last health response: ${body:-<none>}"
	return 1
}

preflight() {
	db=${1:-}
	backup=${2:-}
	for cmd in curl sha256sum readlink mv ln systemctl sudo; do
		command -v "$cmd" >/dev/null 2>&1 || die "required command '$cmd' not found on the server"
	done
	[ -d "$dir" ] || die "deployment directory $dir does not exist (prepare the server first)"
	[ -d "$releases" ] || die "$releases does not exist (prepare the server first)"
	[ -w "$releases" ] || die "$releases is not writable by $(id -un)"
	if [ -e "$current" ] && [ ! -L "$current" ]; then
		die "$current exists but is not a symlink; refusing to replace it"
	fi
	unit=$(systemctl cat "$service" 2>/dev/null) || die "systemd unit '$service' not found"
	echo "$unit" | grep -E '^[[:space:]]*ExecStart=' | grep -qF "$current/rewind" ||
		die "unit '$service' does not run $current/rewind in ExecStart"
	sudo -n -l systemctl restart "$service" >/dev/null 2>&1 ||
		die "$(id -un) may not run 'sudo systemctl restart $service' without a password"
	if [ -n "$db" ]; then
		command -v sqlite3 >/dev/null 2>&1 || die "DEPLOY_DB_PATH is set but sqlite3 is not installed"
		[ -r "$db" ] || die "database $db is not readable by $(id -un)"
		[ -d "$backup" ] && [ -w "$backup" ] || die "backup directory $backup does not exist or is not writable"
	fi
	if [ -L "$current" ]; then
		say "currently deployed: $(readlink "$current")"
	else
		say "no release deployed yet (first deployment)"
	fi
	say "preflight passed"
}

activate() {
	id=${1:-}
	sha=${2:-}
	version=${3:-}
	db=${4:-}
	backup=${5:-}
	[ -n "$id" ] && [ -n "$sha" ] && [ -n "$version" ] || die "missing arguments for activate"
	case "$id" in *[!A-Za-z0-9._-]* | .*) die "invalid release id" ;; esac

	preflight "$db" "$backup"

	incoming="$releases/.incoming-$id"
	target="$releases/$id"
	[ -f "$incoming" ] || die "uploaded binary $incoming not found"
	[ ! -e "$target" ] || die "release directory $target already exists"
	got=$(sha256sum "$incoming" | awk '{print $1}')
	[ "$got" = "$sha" ] || die "checksum mismatch for the uploaded binary (want $sha, got $got)"
	chmod 0755 "$incoming"
	mkdir "$target"
	mv "$incoming" "$target/rewind"
	say "installed $target/rewind"

	previous=""
	if [ -L "$current" ]; then previous=$(readlink "$current"); fi

	if [ -n "$db" ]; then
		dest="$backup/rewind-before-$id.db"
		# The backup holds the password hash and session hashes: mode 0600,
		# whatever the deploy user's umask is.
		(umask 077 && sqlite3 "$db" ".backup '$dest'") || die "database backup failed; nothing was switched"
		say "database backed up to $dest"
	fi

	ln -sfn "releases/$id" "$dir/.current-new"
	mv -T "$dir/.current-new" "$current"
	say "switched $current -> releases/$id; restarting $service"
	sudo -n systemctl restart "$service" || say "systemctl restart reported an error"

	if health_check "$version"; then
		say "healthy: $service is running version $version"
		return 0
	fi

	echo "deploy: ERROR: new release releases/$id did not become healthy" >&2
	if [ -z "$previous" ]; then
		die "this was the first deployment, so there is nothing to roll back to; inspect 'journalctl -u $service' on the server"
	fi
	ln -sfn "$previous" "$dir/.current-new"
	mv -T "$dir/.current-new" "$current"
	sudo -n systemctl restart "$service" || true
	if health_check ""; then
		die "rolled back to $previous, which is healthy again; inspect 'journalctl -u $service' for the failed release"
	fi
	die "rolled back to $previous, but it is NOT healthy either; if the failed release applied a database migration, restore the backup (see docs/deployment.md)"
}

case "$mode" in
preflight) preflight "${5:-}" "${6:-}" ;;
activate) activate "${5:-}" "${6:-}" "${7:-}" "${8:-}" "${9:-}" ;;
*) die "unknown mode '$mode'" ;;
esac
