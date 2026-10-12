#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end test of install.sh on a disposable Linux machine with systemd
# (a CI runner, a VM or a throwaway WSL distribution). It installs a real
# release archive as a systemd service, completes first-run setup over
# loopback, indexes and streams synthetic music (generated silent WAV files),
# mounts a second music folder after Rewind started, upgrades, checks the
# database backup, rejects tampered and mismatched archives, rolls back, and
# finally removes everything it created.
#
# Usage (as root): scripts/install-test.sh OLD_DIST NEW_DIST
#   OLD_DIST, NEW_DIST  directories with install.sh, SHA256SUMS and one
#                       rewind-<version>-linux-amd64.tar.gz each, for two
#                       different versions (see PACKAGE_BINARY_ONLY in
#                       scripts/package-release.sh)
#   KEEP=1              leave the installation in place afterwards
#
# It refuses to run if Rewind's paths, user or service already exist, so it
# never touches a real installation. It uses port 8090 on loopback.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "install-test: run as root" >&2; exit 1; }
old_dist=$(cd "${1:?usage: install-test.sh OLD_DIST NEW_DIST}" && pwd)
new_dist=$(cd "${2:?usage: install-test.sh OLD_DIST NEW_DIST}" && pwd)

for p in /opt/rewind /etc/rewind /var/lib/rewind /var/backups/rewind /etc/systemd/system/rewind.service; do
	[ ! -e "$p" ] || { echo "install-test: $p exists; refusing to run on a machine with Rewind installed" >&2; exit 1; }
done
! id -u rewind >/dev/null 2>&1 || { echo "install-test: user rewind exists; refusing to run" >&2; exit 1; }

archive_of() { ls "$1"/rewind-*-linux-amd64.tar.gz; }
version_of() {
	v=$(basename "$1")
	v=${v#rewind-}
	echo "${v%-linux-amd64.tar.gz}"
}
old_archive=$(archive_of "$old_dist")
new_archive=$(archive_of "$new_dist")
old_version=$(version_of "$old_archive")
new_version=$(version_of "$new_archive")
[ "$old_version" != "$new_version" ] || { echo "install-test: both archives are version $old_version" >&2; exit 1; }

work=$(mktemp -d)
music=/srv/rewind-install-test-music
# Mounts made after a service started reach its private mount namespace only
# through shared mount propagation. systemd makes / shared at boot; some
# environments (WSL) leave / private but /run shared. Use a folder under a
# shared mount, as a real drive mount point would be.
if awk '$5 == "/" {print $7}' /proc/1/mountinfo | grep -q '^shared:'; then
	late=/srv/rewind-install-test-late
else
	late=/run/rewind-install-test-late
	echo "install-test: note: / is not a shared mount here (as in WSL); testing the late mount under /run"
fi
B=http://127.0.0.1:8090
jar="$work/cookies"
passed=0

cleanup() {
	status=$?
	if [ "${KEEP:-}" = 1 ]; then
		echo "install-test: KEEP=1, leaving the installation in place"
	else
		systemctl disable --now rewind.service >/dev/null 2>&1 || true
		rm -f /etc/systemd/system/rewind.service
		rm -rf /etc/systemd/system/rewind.service.d
		systemctl daemon-reload || true
		mountpoint -q "$late" && umount "$late"
		rm -rf /opt/rewind /etc/rewind /var/lib/rewind /var/backups/rewind "$music" "$late"
		if id -u rewind >/dev/null 2>&1; then userdel rewind || true; fi
		if getent group rewind >/dev/null 2>&1; then groupdel rewind || true; fi
	fi
	rm -rf "$work"
	if [ "$status" -eq 0 ]; then
		echo "install-test: PASSED ($passed checks)"
	else
		echo "install-test: FAILED" >&2
		journalctl -u rewind.service -n 40 --no-pager >&2 || true
	fi
}
trap cleanup EXIT

fail() {
	echo "install-test: FAIL: $*" >&2
	exit 1
}
ok() {
	passed=$((passed + 1))
	echo "install-test: ok - $*"
}

# A 0.5 s silent 8 kHz mono 8-bit WAV file without tags.
wav() {
	{
		printf 'RIFF\304\017\000\000WAVEfmt \020\000\000\000\001\000\001\000'
		printf '\100\037\000\000\100\037\000\000\001\000\010\000data\240\017\000\000'
		head -c 4000 /dev/zero | tr '\000' '\200'
	} >"$1"
}

json_field() { # NAME FILE -> first numeric value of "NAME":
	sed -n "s/.*\"$1\":\([0-9][0-9]*\).*/\1/p" "$2" | head -n 1
}

wait_scans() { # waits until no scan is running or queued
	i=0
	while [ "$i" -lt 60 ]; do
		curl -fsS -b "$jar" "$B/api/library" -o "$work/library.json"
		grep -q '"running":false' "$work/library.json" && return 0
		i=$((i + 1))
		sleep 0.5
	done
	fail "scans did not finish: $(cat "$work/library.json")"
}

expect_fail() { # description command...: the command must fail
	d=$1
	shift
	if "$@" >"$work/out.log" 2>&1; then
		cat "$work/out.log" >&2
		fail "$d: expected a failure"
	fi
	ok "$d ($(grep -m1 'ERROR' "$work/out.log" | sed 's/^install: //'))"
}

active() { basename "$(readlink /opt/rewind/current)"; }

# --- music folders ------------------------------------------------------------
mkdir -p "$music/Artist/Album" "$late"
for n in 1 2 3; do wav "$music/Artist/Album/0$n - Song $n.wav"; done
chmod -R a+rX "$music"

# --- install -------------------------------------------------------------------
sh "$old_dist/install.sh" install "$old_archive"
ok "install $old_version"
[ "$(active)" = "$old_version" ] || fail "current is $(active)"
systemctl is-active -q rewind.service || fail "service not active"
systemctl is-enabled -q rewind.service || fail "service not enabled"
ok "service active and enabled"

[ "$(stat -c '%U %a' /var/lib/rewind)" = "rewind 700" ] || fail "data dir: $(stat -c '%U %a' /var/lib/rewind)"
[ "$(stat -c '%U:%G %a' /etc/rewind/rewind.env)" = "root:rewind 640" ] || fail "config: $(stat -c '%U:%G %a' /etc/rewind/rewind.env)"
[ "$(stat -c '%U %a' /var/lib/rewind/rewind.db)" = "rewind 600" ] || fail "database: $(stat -c '%U %a' /var/lib/rewind/rewind.db)"
[ "$(stat -c '%U %a' /var/lib/rewind/setup-code)" = "rewind 600" ] || fail "setup code: $(stat -c '%U %a' /var/lib/rewind/setup-code)"
[ "$(stat -c '%U' /opt/rewind/releases/"$old_version"/rewind)" = root ] || fail "binary not owned by root"
[ "$(ps -o user= -p "$(systemctl show -p MainPID --value rewind.service)")" = rewind ] || fail "service does not run as rewind"
ok "ownership and permissions (data 0700, config 0640, database and setup code 0600, process user rewind)"

listeners=$(ss -Hltn | awk '{print $4}' | grep ':8090$' || true)
[ "$listeners" = "127.0.0.1:8090" ] || fail "listening on: $listeners"
ok "listens on 127.0.0.1:8090 only"

if command -v systemd-analyze >/dev/null 2>&1; then
	systemd-analyze verify /etc/systemd/system/rewind.service >"$work/verify.log" 2>&1 ||
		{ cat "$work/verify.log" >&2; fail "systemd-analyze verify"; }
	ok "systemd-analyze verify"
fi

# --- music, setup, scan, stream --------------------------------------------------
sed -i "s|^REWIND_LIBRARY_ROOTS=.*|REWIND_LIBRARY_ROOTS=$music:$late|" /etc/rewind/rewind.env
systemctl restart rewind.service
i=0
until curl -fsS "$B/api/health" >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 50 ] || fail "not healthy after restart"
	sleep 0.2
done

code=$(cat /var/lib/rewind/setup-code)
umask 077
printf '{"setup_code":"%s","username":"owner","password":"install-test-password"}' "$code" >"$work/setup.json"
printf '{"username":"owner","password":"install-test-password"}' >"$work/login.json"
umask 022
status=$(curl -sS -o "$work/setup-out.json" -w '%{http_code}' -c "$jar" \
	-H 'Content-Type: application/json' -d @"$work/setup.json" "$B/api/auth/setup")
[ "$status" = 201 ] || fail "setup returned $status: $(cat "$work/setup-out.json")"
[ ! -e /var/lib/rewind/setup-code ] || fail "setup code file still exists after setup"
ok "first-run setup over loopback; setup code consumed"

wait_scans
[ "$(json_field tracks "$work/library.json")" = 3 ] || fail "expected 3 tracks: $(cat "$work/library.json")"
[ "$(json_field total_duration_ms "$work/library.json")" -gt 0 ] || fail "durations not read (metadata reader failed?)"
curl -fsS -b "$jar" "$B/api/library/roots" -o "$work/roots.json"
grep -q '"files_failed":[1-9]' "$work/roots.json" && fail "files failed to read: $(cat "$work/roots.json")"
ok "scanned 3 synthetic tracks under the hardened unit, durations read"

id=$(curl -fsS -b "$jar" "$B/api/tracks?limit=1" | grep -o '"id":[0-9]*' | head -n 1 | cut -d: -f2)
status=$(curl -sS -b "$jar" -H 'Range: bytes=0-99' -o "$work/part" -w '%{http_code}' "$B/api/tracks/$id/stream")
[ "$status" = 206 ] && [ "$(wc -c <"$work/part")" -eq 100 ] || fail "range stream returned $status"
status=$(curl -sS -o /dev/null -w '%{http_code}' "$B/api/tracks/$id/stream")
[ "$status" = 401 ] || fail "stream without a session returned $status"
ok "streaming with Range (206) and without a session (401)"

# A drive mounted after Rewind started must become visible to the service.
mount -t tmpfs -o size=1m,mode=0755 rewind-install-test "$late"
mkdir -p "$late/Other"
wav "$late/Other/Late.wav"
chmod -R a+rX "$late"
curl -fsS -b "$jar" -X POST "$B/api/library/scan" >/dev/null
i=0
until [ "$(curl -fsS -b "$jar" "$B/api/library" -o "$work/library.json" && json_field tracks "$work/library.json")" = 4 ]; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || fail "late mount not visible: $(cat "$work/library.json")"
	sleep 0.5
done
wait_scans
ok "a folder mounted after start is visible to the service (ProtectSystem=strict)"

setpriv --reuid=rewind --regid=rewind --clear-groups sh -c "echo x > '$music/probe'" 2>/dev/null &&
	fail "the rewind user can write to the music folder in this test setup"
ok "music folder is not writable by the rewind user"

# --- rejected archives, nothing changes --------------------------------------------
expect_fail "install on an existing installation is refused" sh "$new_dist/install.sh" install "$new_archive"

mkdir "$work/tampered"
cp "$new_archive" "$new_dist/SHA256SUMS" "$work/tampered/"
printf 'x' >>"$work/tampered/$(basename "$new_archive")"
expect_fail "tampered archive is refused" sh "$new_dist/install.sh" upgrade "$work/tampered/$(basename "$new_archive")"
[ "$(active)" = "$old_version" ] || fail "current changed after a refused upgrade"

mkdir "$work/mismatch"
cp "$new_archive" "$work/mismatch/rewind-$new_version-wrong-linux-amd64.tar.gz"
(cd "$work/mismatch" && tar -xzf "rewind-$new_version-wrong-linux-amd64.tar.gz" &&
	mv "rewind-$new_version-linux-amd64" "rewind-$new_version-wrong-linux-amd64" &&
	tar -czf "rewind-$new_version-wrong-linux-amd64.tar.gz" "rewind-$new_version-wrong-linux-amd64" &&
	sha256sum "rewind-$new_version-wrong-linux-amd64.tar.gz" >SHA256SUMS)
expect_fail "archive whose binary reports another version is refused before activation" \
	sh "$new_dist/install.sh" upgrade "$work/mismatch/rewind-$new_version-wrong-linux-amd64.tar.gz"
[ "$(active)" = "$old_version" ] || fail "current changed after a refused upgrade"
[ ! -e "/opt/rewind/releases/$new_version-wrong" ] || fail "refused release was left installed"
systemctl is-active -q rewind.service || fail "service stopped by a refused upgrade"
ok "refused upgrades left the active release and service untouched"

# --- upgrade ---------------------------------------------------------------------
echo "# local edit" >>/etc/rewind/rewind.env
cp /etc/rewind/rewind.env "$work/env-before"
sh "$new_dist/install.sh" upgrade "$new_archive"
[ "$(active)" = "$new_version" ] || fail "current is $(active) after upgrade"
curl -fsS "$B/api/health" | grep -q "\"version\":\"$new_version\"" || fail "health does not report $new_version"
cmp -s /etc/rewind/rewind.env "$work/env-before" || fail "configuration changed by the upgrade"
[ -x "/opt/rewind/releases/$old_version/rewind" ] || fail "old release removed"
backup=$(ls /var/backups/rewind/*-before-"$new_version".db)
[ "$(stat -c '%U %a' "$backup")" = "root 600" ] || fail "backup: $(stat -c '%U %a' "$backup")"
ok "upgrade to $new_version: config kept, old release kept, database backup $(basename "$backup") (root, 0600)"

curl -fsS -c "$jar" -H 'Content-Type: application/json' -d @"$work/login.json" "$B/api/auth/login" >/dev/null ||
	fail "sign-in after upgrade failed"
curl -fsS -b "$jar" "$B/api/library" -o "$work/library.json"
[ "$(json_field tracks "$work/library.json")" = 4 ] || fail "library changed by the upgrade: $(cat "$work/library.json")"
ok "account and library survived the upgrade"

expect_fail "upgrade to the active version is refused" sh "$new_dist/install.sh" upgrade "$new_archive"

# The metadata library's compiled-code cache must be in Rewind's own private
# directory, not directly in /tmp (security-audit.md, F8). A new file makes
# the scanner run the metadata library in this process.
wav "$music/Artist/Album/04 - Song 4.wav"
chmod a+r "$music/Artist/Album/04 - Song 4.wav"
curl -fsS -b "$jar" -X POST "$B/api/library/scan" >/dev/null
i=0
until [ "$(curl -fsS -b "$jar" "$B/api/library" -o "$work/library.json" && json_field tracks "$work/library.json")" = 5 ]; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || fail "new file not indexed: $(cat "$work/library.json")"
	sleep 0.5
done
pid=$(systemctl show -p MainPID --value rewind.service)
nsenter -t "$pid" -m sh -c 'ls -d /tmp/rewind-*/go-taglib-wasm' >/dev/null 2>&1 ||
	fail "metadata cache not found in a private rewind-* directory: $(nsenter -t "$pid" -m ls -la /tmp)"
! nsenter -t "$pid" -m test -e /tmp/go-taglib-wasm || fail "metadata cache created directly in /tmp"
[ "$(nsenter -t "$pid" -m sh -c 'stat -c %a /tmp/rewind-*' | head -n 1)" = 700 ] || fail "private temporary directory is not 0700"
ok "metadata cache is in a private 0700 directory, not directly in /tmp"

# --- rollback ----------------------------------------------------------------------
sh "$new_dist/install.sh" rollback "$old_version"
[ "$(active)" = "$old_version" ] || fail "current is $(active) after rollback"
curl -fsS "$B/api/health" | grep -q "\"version\":\"$old_version\"" || fail "health does not report $old_version"
ls /var/backups/rewind/*-before-rollback-to-"$old_version".db >/dev/null || fail "no backup before rollback"
curl -fsS -c "$jar" -H 'Content-Type: application/json' -d @"$work/login.json" "$B/api/auth/login" >/dev/null ||
	fail "sign-in after rollback failed"
ok "rollback to $old_version with a database backup first"

sh "$new_dist/install.sh" status >"$work/status.txt"
grep -q "active release: $old_version" "$work/status.txt" || { cat "$work/status.txt"; fail "status output"; }
ok "status"

# A restart after a stop works and keeps data (graceful shutdown on SIGTERM).
systemctl stop rewind.service
[ ! -e /var/lib/rewind/rewind.db-journal ] || fail "journal left after a clean stop"
journalctl -u rewind.service -n 20 --no-pager | grep -q 'server stopped' || fail "no clean shutdown logged"
systemctl start rewind.service
ok "clean stop (no journal left, 'server stopped' logged) and start"
