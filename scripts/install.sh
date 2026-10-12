#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Installs, upgrades or rolls back Rewind on a Linux amd64 server with
# systemd, from a release archive whose checksum is listed in SHA256SUMS.
# Published next to each release as install.sh. See docs/install.md.
#
# Usage (as root):
#   sh install.sh install  ARCHIVE [SHA256SUMS]   first installation
#   sh install.sh upgrade  ARCHIVE [SHA256SUMS]   install a new version and switch to it
#   sh install.sh rollback RELEASE                switch back to releases/RELEASE
#   sh install.sh status                          show releases, service state and health
#
#   ARCHIVE     rewind-<version>-linux-amd64.tar.gz, downloaded from the release page
#   SHA256SUMS  defaults to the SHA256SUMS file next to ARCHIVE
#
# Layout (fixed, matching docs/install.md and the deploy workflow):
#   /opt/rewind/releases/<version>/   one directory per installed version
#   /opt/rewind/current               symlink to the active release
#   /etc/rewind/rewind.env            configuration; created once, never overwritten
#   /var/lib/rewind                   data directory; never modified by this script
#   /var/backups/rewind               database copies made before upgrades and rollbacks
#   /etc/systemd/system/rewind.service   created once, never overwritten
#
# Safety rules:
#   - nothing is extracted, installed or run before the archive's SHA-256
#     matches SHA256SUMS;
#   - a new binary is test-started as the rewind user, on a spare loopback
#     port with an empty temporary data directory, before it is activated;
#   - the database, configuration, music and old releases are never deleted
#     or overwritten; a database is never restored automatically;
#   - no firewall, network, Tailscale or listen-address settings are changed.
set -eu

PREFIX=/opt/rewind
RELEASES=$PREFIX/releases
CURRENT=$PREFIX/current
CONFIG_DIR=/etc/rewind
CONFIG_FILE=$CONFIG_DIR/rewind.env
STATE_DIR=/var/lib/rewind
BACKUP_DIR=/var/backups/rewind
UNIT_NAME=rewind.service
UNIT_FILE=/etc/systemd/system/$UNIT_NAME
SERVICE_USER=rewind

work=""
incoming=""
cleanup() {
	if [ -n "$incoming" ] && [ -d "$incoming" ]; then rm -rf "$incoming"; fi
	if [ -n "$work" ] && [ -d "$work" ]; then rm -rf "$work"; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

die() {
	echo "install: ERROR: $*" >&2
	exit 1
}
say() { echo "install: $*"; }

usage() {
	sed -n '8,15p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

# --- checks ------------------------------------------------------------------

preflight() {
	[ "$(id -u)" -eq 0 ] || die "run this script as root (for example: sudo sh install.sh $mode ...)"
	[ "$(uname -s)" = Linux ] || die "Rewind releases support Linux only"
	case "$(uname -m)" in
	x86_64 | amd64) ;;
	*) die "this release is built for x86-64 (amd64); this machine is $(uname -m)" ;;
	esac
	[ -d /run/systemd/system ] || die "systemd is not running; install manually as described in docs/install.md"
	for cmd in systemctl tar gzip sha256sum curl useradd groupadd setpriv flock install cmp od mktemp readlink stat; do
		command -v "$cmd" >/dev/null 2>&1 || die "required command '$cmd' not found (Debian/Ubuntu: coreutils, util-linux, passwd, curl, tar, gzip)"
	done
	mkdir -p /run/lock
	exec 9>/run/lock/rewind-install.lock
	flock -n 9 || die "another install.sh is running"
}

# verify_archive ARCHIVE SUMS: checks the checksum, then extracts into $work.
# Sets $version and $src.
verify_archive() {
	archive=$1
	sums=$2
	[ -f "$archive" ] || die "archive '$archive' not found"
	[ -f "$sums" ] || die "checksum file '$sums' not found; download SHA256SUMS from the same release page"
	base=$(basename "$archive")
	case "$base" in
	rewind-*-linux-amd64.tar.gz) ;;
	*) die "'$base' is not a Linux amd64 release archive (rewind-<version>-linux-amd64.tar.gz)" ;;
	esac
	version=${base#rewind-}
	version=${version%-linux-amd64.tar.gz}
	case "$version" in
	"" | .* | *[!A-Za-z0-9._+-]*) die "unexpected version '$version' in the archive name" ;;
	esac

	want=$(awk -v f="$base" '$2 == f || $2 == "*" f {print $1}' "$sums")
	[ -n "$want" ] || die "$base is not listed in $sums"
	[ "$(echo "$want" | wc -l)" -eq 1 ] || die "$base is listed more than once in $sums"
	got=$(sha256sum "$archive" | awk '{print $1}')
	[ "$got" = "$want" ] || die "checksum mismatch for $base (expected $want, got $got); download it again and do not install it"
	say "checksum verified: $base"

	top="rewind-$version-linux-amd64"
	# Only regular files and directories under the expected top directory.
	tar -tvzf "$archive" | awk '{print substr($1, 1, 1)}' | grep -qv '^[-d]$' &&
		die "the archive contains links or special files; refusing to extract it"
	tar -tzf "$archive" | while IFS= read -r entry; do
		case "$entry" in
		"$top/" | "$top/"*) ;;
		*) die "unexpected archive entry '$entry'" ;;
		esac
		case "/$entry/" in */../*) die "unexpected archive entry '$entry'" ;; esac
	done

	work=$(mktemp -d)
	tar -xzf "$archive" -C "$work" --no-same-owner
	src="$work/$top"
	for f in rewind rewind.service rewind.env.example LICENSE THIRD_PARTY_NOTICES.md; do
		[ -f "$src/$f" ] && [ ! -L "$src/$f" ] || die "the archive has no $f"
	done
	# ELF, 64-bit, little endian, machine x86-64 (0x3e).
	[ "$(od -An -tx1 -N6 "$src/rewind" | tr -d ' \n')" = 7f454c460201 ] &&
		[ "$(od -An -tx1 -j18 -N2 "$src/rewind" | tr -d ' \n')" = 3e00 ] ||
		die "the archive's rewind file is not a Linux x86-64 executable"
}

# --- configuration helpers --------------------------------------------------

# config_value KEY: the last value set for KEY in the configuration file.
config_value() {
	[ -f "$CONFIG_FILE" ] || return 0
	sed -n "s/^[[:space:]]*$1=//p" "$CONFIG_FILE" | tail -n 1 | sed 's/^"\(.*\)"$/\1/; s/[[:space:]]*$//'
}

# health_url: a URL on this machine for Rewind's health endpoint, derived
# from REWIND_ADDR (loopback entries preferred).
health_url() {
	addrs=$(config_value REWIND_ADDR)
	[ -n "$addrs" ] || addrs=127.0.0.1:8090
	pick=""
	for a in $(echo "$addrs" | tr ',' ' '); do
		case "$a" in
		127.*:* | localhost:* | "[::1]":*)
			pick=$a
			break
			;;
		esac
	done
	if [ -z "$pick" ]; then
		first=$(echo "$addrs" | tr ',' ' ' | awk '{print $1}')
		case "$first" in
		0.0.0.0:* | :*) pick=127.0.0.1:${first##*:} ;;
		"[::]":*) pick="[::1]:${first##*:}" ;;
		*) pick=$first ;;
		esac
	fi
	echo "http://$pick/api/health"
}

# data_dir: the configured data directory (relative paths are resolved
# against the unit's WorkingDirectory, /var/lib/rewind).
data_dir() {
	d=$(config_value REWIND_DATA_DIR)
	[ -n "$d" ] || d=data
	case "$d" in
	/*) echo "$d" ;;
	*) echo "$STATE_DIR/$d" ;;
	esac
}

active_release() {
	if [ -L "$CURRENT" ]; then basename "$(readlink "$CURRENT")"; fi
}

# --- operations -------------------------------------------------------------

# service_health VERSION: waits up to 30 s for the running service to report
# status ok (and VERSION, unless empty).
service_health() {
	want=$1
	url=$(health_url)
	i=0
	body=""
	while [ "$i" -lt 30 ]; do
		body=$(curl -fsS --max-time 2 "$url" 2>/dev/null || true)
		case "$body" in
		*'"status":"ok"'*)
			if [ -z "$want" ]; then return 0; fi
			case "$body" in *"\"version\":\"$want\""*) return 0 ;; esac
			;;
		esac
		i=$((i + 1))
		sleep 1
	done
	say "health check $url failed; last response: ${body:-<none>}"
	return 1
}

# test_start BINARY VERSION: starts BINARY as the service user on a spare
# loopback port with an empty temporary data directory and no music, and
# checks health, version, the embedded web interface and a clean shutdown.
# The real configuration and data are not read.
test_start() {
	bin=$1
	want=$2
	uid=$(id -u "$SERVICE_USER")
	gid=$(id -g "$SERVICE_USER")
	tdata=$(mktemp -d /tmp/rewind-install-check.XXXXXX)
	chown "$uid:$gid" "$tdata"
	chmod 0700 "$tdata"
	log="$work/test-start.log"
	result=1
	port=18090
	while [ "$port" -lt 18100 ]; do
		setpriv --reuid="$uid" --regid="$gid" --clear-groups -- \
			env -i PATH=/usr/bin:/bin REWIND_ADDR="127.0.0.1:$port" REWIND_DATA_DIR="$tdata/data" \
			REWIND_SCAN_ON_STARTUP=false REWIND_LOG_LEVEL=info "$bin" >"$log" 2>&1 &
		pid=$!
		up=0
		i=0
		while [ "$i" -lt 100 ]; do
			if curl -fsS --max-time 2 "http://127.0.0.1:$port/api/health" -o "$work/health.json" 2>/dev/null; then
				up=1
				break
			fi
			kill -0 "$pid" 2>/dev/null || break
			i=$((i + 1))
			sleep 0.2
		done
		if [ "$up" -eq 0 ]; then
			if kill -0 "$pid" 2>/dev/null; then kill -KILL "$pid" 2>/dev/null || true; fi
			wait "$pid" 2>/dev/null || true
			if grep -q 'cannot listen on' "$log"; then
				port=$((port + 1))
				continue
			fi
			say "the new binary did not start; its output was:"
			sed 's/^/    /' "$log" >&2
			break
		fi
		ok=1
		grep -q '"status":"ok"' "$work/health.json" || ok=0
		grep -q "\"version\":\"$want\"" "$work/health.json" || {
			say "the new binary reports $(cat "$work/health.json"), expected version $want"
			ok=0
		}
		if curl -fsS --max-time 2 "http://127.0.0.1:$port/" -o "$work/index.html" 2>/dev/null &&
			grep -q '<title>Rewind</title>' "$work/index.html" &&
			! grep -q 'frontend is not included in this build' "$work/index.html"; then
			:
		else
			say "the new binary does not serve the web interface"
			ok=0
		fi
		kill -TERM "$pid"
		status=0
		wait "$pid" || status=$?
		[ "$status" -eq 0 ] || {
			say "the new binary exited with status $status after SIGTERM"
			ok=0
		}
		[ "$ok" -eq 0 ] || result=0
		break
	done
	[ "$port" -lt 18100 ] || say "no free loopback port between 18090 and 18099 for the test start"
	case "$tdata" in /tmp/rewind-install-check.*) rm -rf "$tdata" ;; esac
	return "$result"
}

ensure_user() {
	if id -u "$SERVICE_USER" >/dev/null 2>&1; then
		say "using existing user $SERVICE_USER"
		return
	fi
	if getent group "$SERVICE_USER" >/dev/null 2>&1; then
		useradd --system --gid "$SERVICE_USER" --home-dir "$STATE_DIR" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
	else
		useradd --system --user-group --home-dir "$STATE_DIR" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
	fi
	say "created system user $SERVICE_USER (no login shell, no password)"
}

ensure_dirs() {
	if [ -e "$STATE_DIR" ]; then
		[ -d "$STATE_DIR" ] || die "$STATE_DIR exists but is not a directory"
		owner=$(stat -c %U "$STATE_DIR")
		[ "$owner" = "$SERVICE_USER" ] ||
			die "$STATE_DIR is owned by $owner, not $SERVICE_USER; fix it with 'chown $SERVICE_USER:$SERVICE_USER $STATE_DIR' (not recursively unless you know its contents) and run again"
		say "keeping existing data directory $STATE_DIR"
	else
		install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$STATE_DIR"
		say "created data directory $STATE_DIR (mode 0700)"
	fi
	# Existing directories are left exactly as they are.
	for d in "$PREFIX" "$RELEASES" "$CONFIG_DIR"; do
		[ -d "$d" ] || install -d -m 0755 "$d"
	done
	if [ ! -e "$BACKUP_DIR" ]; then install -d -m 0700 "$BACKUP_DIR"; fi
}

ensure_config() {
	if [ -e "$CONFIG_FILE" ]; then
		say "keeping existing configuration $CONFIG_FILE"
		return
	fi
	install -m 0640 -o root -g "$SERVICE_USER" "$src/rewind.env.example" "$CONFIG_FILE"
	say "created $CONFIG_FILE (root:$SERVICE_USER, mode 0640)"
}

ensure_unit() {
	if [ -e "$UNIT_FILE" ]; then
		if cmp -s "$UNIT_FILE" "$src/rewind.service"; then
			say "keeping existing $UNIT_FILE (same as this release's)"
		else
			say "keeping your existing $UNIT_FILE; it differs from this release's unit."
			say "  compare: diff -u $UNIT_FILE $RELEASES/$version/rewind.service"
		fi
	else
		install -m 0644 "$src/rewind.service" "$UNIT_FILE"
		say "created $UNIT_FILE"
	fi
	systemctl daemon-reload
}

# stage_release: copies the extracted archive to releases/.incoming-VERSION
# and test-starts it. Leaves $target set to the final release directory.
stage_release() {
	target="$RELEASES/$version"
	if [ -e "$target" ]; then
		[ -d "$target" ] && cmp -s "$target/rewind" "$src/rewind" ||
			die "$target already exists with a different binary; refusing to replace it"
		say "release $version is already present in $target (identical binary)"
		test_start "$target/rewind" "$version" || die "release $version failed its test start"
		return
	fi
	incoming="$RELEASES/.incoming-$version"
	rm -rf "$incoming"
	cp -R "$src" "$incoming"
	chown -R root:root "$incoming"
	chmod -R go-w "$incoming"
	chmod 0755 "$incoming/rewind"
	say "test-starting $version on a spare loopback port (temporary data, no music)"
	test_start "$incoming/rewind" "$version" || die "release $version failed its test start; nothing was changed"
	mv "$incoming" "$target"
	incoming=""
	say "installed $target"
}

switch_to() { # release directory name
	ln -sfn "releases/$1" "$PREFIX/.current-new"
	mv -T "$PREFIX/.current-new" "$CURRENT"
	say "switched $CURRENT -> releases/$1"
}

# backup_db LABEL: with the service stopped, copies the database to
# BACKUP_DIR (mode 0600). Fails if a rollback journal shows an unfinished
# write. Prints the backup path in $backup.
backup_db() {
	backup=""
	db="$(data_dir)/rewind.db"
	if [ ! -f "$db" ]; then
		say "no database at $db yet; nothing to back up"
		return 0
	fi
	if [ -e "$db-journal" ]; then
		return 1
	fi
	backup="$BACKUP_DIR/rewind-$(date -u +%Y%m%dT%H%M%SZ)-$1.db"
	(umask 077 && cp "$db" "$backup")
	cmp -s "$db" "$backup" || die "database backup to $backup could not be verified"
	say "database backed up to $backup"
}

restore_hint() {
	db="$(data_dir)/rewind.db"
	echo "  To return to the database as it was before this change, restore the backup:" >&2
	echo "    sudo systemctl stop $UNIT_NAME" >&2
	echo "    sudo install -m 0600 -o $SERVICE_USER -g $SERVICE_USER ${backup:-$BACKUP_DIR/<backup>.db} $db" >&2
	echo "    sudo rm -f $db-journal" >&2
	echo "    sudo systemctl start $UNIT_NAME" >&2
	echo "  The script never does this for you. See docs/operations.md, \"Restore\"." >&2
}

# switch_with_backup NEW_RELEASE EXPECTED_VERSION LABEL: stops the service,
# backs up the database, switches, starts and checks health; switches back
# if the new release is unhealthy.
switch_with_backup() {
	new=$1
	expect=$2
	label=$3
	previous=$(active_release)
	systemctl stop "$UNIT_NAME"
	if ! backup_db "$label"; then
		systemctl start "$UNIT_NAME" || true
		die "$(data_dir)/rewind.db-journal exists, so a write was not finished; Rewind was started again to recover it. Run this command again."
	fi
	switch_to "$new"
	systemctl start "$UNIT_NAME" || say "systemctl start reported an error"
	if service_health "$expect"; then
		say "healthy: $UNIT_NAME is running release $new"
		return 0
	fi
	echo "install: ERROR: release $new did not become healthy; see: journalctl -u $UNIT_NAME -n 50" >&2
	if [ -z "$previous" ]; then
		die "there is no previous release to switch back to"
	fi
	switch_to "$previous"
	systemctl restart "$UNIT_NAME" || true
	if service_health ""; then
		echo "install: switched back to release $previous, which is healthy again." >&2
		[ -z "$backup" ] || echo "  The database backup is $backup." >&2
		exit 1
	fi
	echo "install: ERROR: switched back to release $previous, but it is NOT healthy either." >&2
	echo "  If release $new applied a database migration, the older release refuses that database." >&2
	restore_hint
	exit 1
}

cmd_install() {
	[ ! -e "$CURRENT" ] || die "Rewind is already installed ($CURRENT exists); use: sh install.sh upgrade ARCHIVE"
	verify_archive "$1" "$2"
	ensure_user
	ensure_dirs
	stage_release
	ensure_config
	ensure_unit
	switch_to "$version"
	systemctl enable "$UNIT_NAME" >/dev/null 2>&1 || die "systemctl enable $UNIT_NAME failed"
	systemctl restart "$UNIT_NAME"
	service_health "$version" || die "Rewind did not become healthy; see: journalctl -u $UNIT_NAME -n 50"
	say "Rewind $version is installed, running and enabled at boot."
	cat <<EOF

Next steps (docs/install.md):
  1. Set your music folder(s) in $CONFIG_FILE (REWIND_LIBRARY_ROOTS),
     then: sudo systemctl restart rewind
  2. Create the owner account from this server, or through an SSH tunnel:
       ssh -L 8090:127.0.0.1:8090 you@this-server
     open http://localhost:8090 and enter the code shown by:
       sudo cat $(data_dir)/setup-code
  3. Rewind listens on this machine only. To use it from other devices, see
     "Network access" in docs/deployment.md.
EOF
}

cmd_upgrade() {
	[ -L "$CURRENT" ] || die "Rewind is not installed with this layout ($CURRENT missing); use: sh install.sh install ARCHIVE"
	[ -e "$UNIT_FILE" ] || systemctl cat "$UNIT_NAME" >/dev/null 2>&1 || die "systemd unit $UNIT_NAME not found"
	id -u "$SERVICE_USER" >/dev/null 2>&1 || die "user $SERVICE_USER does not exist"
	verify_archive "$1" "$2"
	[ "$(active_release)" != "$version" ] || die "release $version is already active"
	stage_release
	ensure_unit
	switch_with_backup "$version" "$version" "before-$version"
	say "Upgraded to $version. Previous releases are kept in $RELEASES."
}

cmd_rollback() {
	rel=$1
	case "$rel" in "" | .* | */*) die "give a release directory name from $RELEASES" ;; esac
	[ -L "$CURRENT" ] || die "Rewind is not installed with this layout ($CURRENT missing)"
	[ -x "$RELEASES/$rel/rewind" ] || die "$RELEASES/$rel/rewind not found; installed releases: $(ls "$RELEASES" | tr '\n' ' ')"
	[ "$(active_release)" != "$rel" ] || die "release $rel is already active"
	say "rolling back from $(active_release) to $rel"
	say "note: if the newer release migrated the database, $rel refuses it and this rollback fails safely"
	switch_with_backup "$rel" "" "before-rollback-to-$rel"
	say "Rolled back to $rel."
}

cmd_status() {
	echo "active release: $(active_release)"
	echo "installed releases:"
	if [ -d "$RELEASES" ]; then ls -1 "$RELEASES" | sed 's/^/  /'; fi
	echo "service: $(systemctl is-active "$UNIT_NAME" 2>/dev/null || true) ($(systemctl is-enabled "$UNIT_NAME" 2>/dev/null || true) at boot)"
	url=$(health_url)
	echo "health ($url): $(curl -fsS --max-time 2 "$url" 2>/dev/null || echo unreachable)"
	echo "configuration: $CONFIG_FILE"
	echo "data directory: $(data_dir)"
	echo "backups: $BACKUP_DIR"
}

mode=${1:-}
case "$mode" in
install | upgrade)
	[ $# -ge 2 ] && [ $# -le 3 ] || usage
	preflight
	archive=$2
	sums=${3:-$(dirname "$archive")/SHA256SUMS}
	if [ "$mode" = install ]; then cmd_install "$archive" "$sums"; else cmd_upgrade "$archive" "$sums"; fi
	;;
rollback)
	[ $# -eq 2 ] || usage
	preflight
	cmd_rollback "$2"
	;;
status)
	[ $# -eq 1 ] || usage
	cmd_status
	;;
*) usage ;;
esac
