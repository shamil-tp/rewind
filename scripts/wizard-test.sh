#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end test of the interactive installer (install.sh without a
# subcommand, rewind-ctl and uninstall.sh) on a disposable Linux machine with
# systemd: a CI runner, a VM or a throwaway WSL distribution. Answers are fed
# to the wizard on standard input. It:
#
#   - installs from a local release archive, with a music folder whose path
#     contains spaces, refuses an unreadable folder, and grants access to
#     another through a dedicated group;
#   - completes first-run setup, scans and signs in;
#   - reconfigures (LAN address if the machine has a private IPv4, new port,
#     another folder, scan workers), and checks the configuration backup;
#   - makes a reconfiguration fail on purpose and checks that the previous
#     settings come back;
#   - updates to a second release (database backup, config unchanged);
#   - breaks the installation and repairs it;
#   - uninstalls the application only, reinstalls over the kept data (the
#     account still works), removes application and configuration, refuses a
#     mistyped purge, purges around an unknown file, then purges completely;
#   - checks that the music folders never changed;
#   - with WIZARD_TEST_DOWNLOAD=vX.Y.Z, also installs that published release
#     from GitHub and updates to the latest one (needs internet access).
#
# Usage (as root): bash scripts/wizard-test.sh OLD_DIST NEW_DIST
#   OLD_DIST, NEW_DIST  directories with SHA256SUMS and one
#                       rewind-<version>-linux-amd64.tar.gz each, for two
#                       different versions (release downloads, or
#                       PACKAGE_BINARY_ONLY packages)
#   KEEP=1              leave the last installation in place (no cleanup)
#   WIZARD_TEST_LOGS=DIR  copy the output of every wizard run into DIR
#
# It refuses to run if Rewind's paths, user or service already exist, so it
# never touches a real installation. It uses ports 8090 and 8091.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "wizard-test: run as root" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
installer="$here/install.sh"
uninstaller="$here/uninstall.sh"
old_dist=$(cd "${1:?usage: wizard-test.sh OLD_DIST NEW_DIST}" && pwd)
new_dist=$(cd "${2:?usage: wizard-test.sh OLD_DIST NEW_DIST}" && pwd)

for p in /opt/rewind /etc/rewind /var/lib/rewind /var/backups/rewind /etc/systemd/system/rewind.service \
	/etc/systemd/system/rewind.service.d /usr/local/sbin/rewind-ctl; do
	[ ! -e "$p" ] || { echo "wizard-test: $p exists; refusing to run on a machine with Rewind installed" >&2; exit 1; }
done
! id -u rewind >/dev/null 2>&1 || { echo "wizard-test: user rewind exists; refusing to run" >&2; exit 1; }
! getent group rwtestgrp >/dev/null 2>&1 || { echo "wizard-test: group rwtestgrp exists; refusing to run" >&2; exit 1; }

archive_of() { ls "$1"/rewind-*-linux-amd64.tar.gz; }
version_of() {
	local v
	v=$(basename "$1")
	v=${v#rewind-}
	echo "${v%-linux-amd64.tar.gz}"
}
old_archive=$(archive_of "$old_dist")
new_archive=$(archive_of "$new_dist")
old_version=$(version_of "$old_archive")
new_version=$(version_of "$new_archive")
[ "$old_version" != "$new_version" ] || { echo "wizard-test: both archives are version $old_version" >&2; exit 1; }

work=$(mktemp -d)
base=/srv/rewind-wizard-test
music1="$base/My Music"   # a path with a space
music2="$base/group-music" # readable through a group only
music3="$base/more"
private="$base/private"    # unreadable for rewind
B=http://127.0.0.1:8090
jar="$work/cookies"
passed=0
n=0
systemd_ver=$(systemctl --version | awk 'NR == 1 {print $2 + 0}')

cleanup() {
	local status=$?
	if [ "${KEEP:-}" = 1 ]; then
		echo "wizard-test: KEEP=1, leaving the installation in place"
	else
		systemctl disable --now rewind.service >/dev/null 2>&1 || true
		rm -f /etc/systemd/system/rewind.service /usr/local/sbin/rewind-ctl /usr/local/sbin/rewind-ctl.new
		rm -rf /etc/systemd/system/rewind.service.d
		systemctl daemon-reload || true
		rm -rf /opt/rewind /etc/rewind /var/lib/rewind /var/backups/rewind "$base"
		if id -u rewind >/dev/null 2>&1; then userdel rewind || true; fi
		if getent group rewind >/dev/null 2>&1; then groupdel rewind || true; fi
		if getent group rwtestgrp >/dev/null 2>&1; then groupdel rwtestgrp || true; fi
	fi
	if [ -n "${WIZARD_TEST_LOGS:-}" ]; then
		mkdir -p "$WIZARD_TEST_LOGS"
		cp "$work"/wizard-*.log "$WIZARD_TEST_LOGS"/ 2>/dev/null || true
	fi
	if [ "$status" -eq 0 ]; then
		echo "wizard-test: PASSED ($passed checks)"
	else
		echo "wizard-test: FAILED; output of the last wizard run ($work/wizard-$n.log):" >&2
		[ ! -f "$work/wizard-$n.log" ] || tail -n 60 "$work/wizard-$n.log" >&2
		journalctl -u rewind.service -n 30 --no-pager >&2 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "wizard-test: FAIL: $*" >&2
	exit 1
}
ok() {
	passed=$((passed + 1))
	echo "wizard-test: ok - $*"
}

# wizard EXPECTED-STATUS COMMAND... < answers: runs the installer with the
# answers on standard input and checks its exit status.
wizard() {
	local want=$1 got=0
	shift
	n=$((n + 1))
	"$@" >"$work/wizard-$n.log" 2>&1 || got=$?
	[ "$got" = "$want" ] || fail "'$*' exited with $got, expected $want (see the log below)"
}
log() { cat "$work/wizard-$n.log"; }
answers() { printf '%s\n' "$@"; }

wav() { # a 0.5 s silent 8 kHz mono 8-bit WAV file without tags
	{
		printf 'RIFF\304\017\000\000WAVEfmt \020\000\000\000\001\000\001\000'
		printf '\100\037\000\000\100\037\000\000\001\000\010\000data\240\017\000\000'
		head -c 4000 /dev/zero | tr '\000' '\200'
	} >"$1"
}
json_field() { sed -n "s/.*\"$1\":\([0-9][0-9]*\).*/\1/p" "$2" | head -n 1; }
health() { curl -fsS --max-time 3 "$B/api/health"; }
tracks() { # waits until no scan is running; prints the track count
	local i=0
	while [ "$i" -lt 60 ]; do
		curl -fsS -b "$jar" "$B/api/library" -o "$work/library.json"
		if grep -q '"running":false' "$work/library.json"; then
			json_field tracks "$work/library.json"
			return 0
		fi
		i=$((i + 1))
		sleep 0.5
	done
	fail "scans did not finish: $(cat "$work/library.json")"
}
rescan_until() { # COUNT
	local i=0
	curl -fsS -b "$jar" -X POST "$B/api/library/scan" >/dev/null
	while [ "$(tracks)" != "$1" ]; do
		i=$((i + 1))
		[ "$i" -lt 20 ] || fail "expected $1 tracks: $(cat "$work/library.json")"
		curl -fsS -b "$jar" -X POST "$B/api/library/scan" >/dev/null 2>&1 || true
		sleep 1
	done
}
login() {
	curl -fsS -c "$jar" -H 'Content-Type: application/json' \
		-d '{"username":"owner","password":"wizard-test-password"}' "$B/api/auth/login" >/dev/null
}
music_state() { # a fingerprint of the music folders: names, modes, owners, sizes, times, contents
	(cd "$base" && find . -path ./private -prune -o -printf '%p %m %u:%g %s %T@\n' | sort
		find . -type f -exec sha256sum {} + | sort)
}
older_first() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$1" ]; }
mounts_answer() { # the answer to "wait for drives" only where the question is asked (systemd >= 256)
	if [ "$systemd_ver" -ge 256 ]; then echo "$1"; fi
}

# --- music folders ------------------------------------------------------------------
mkdir -p "$music1/Artist/Album" "$music2" "$music3" "$private"
for i in 1 2 3; do wav "$music1/Artist/Album/0$i - Song $i.wav"; done
wav "$music2/Group Song.wav"
wav "$music3/More.wav"
wav "$private/Secret.wav"
chmod -R a+rX "$music1" "$music3"
chmod 0755 "$base"
chmod 0700 "$private"
groupadd rwtestgrp
chgrp -R rwtestgrp "$music2"
chmod 0750 "$music2"
chmod 0640 "$music2/Group Song.wav"
music_before=$(music_state)

# --- preflight ------------------------------------------------------------------------
wizard 0 bash "$installer" --check
log | grep -q 'Ready.' || fail "--check did not report ready"
ok "--check: ready, read-only"

# --- 1. fresh installation from a local archive ------------------------------------
lan_ip=$(ip -4 -o addr show scope global | awk '{split($4, a, "/"); print a[1]}' |
	grep -E '^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)' | head -n 1 || true)
{
	answers y 3 "$old_archive" "" # continue; local archive; path; default SHA256SUMS
	answers 1 ""                  # this server only; port 8090
	answers "$music1"             # readable folder with a space
	answers relative/path         # refused: not absolute
	answers "$private" 2          # unreadable: do not add
	answers "$music2" 3           # readable through a group: add rewind to it
	answers "$music1/Artist"      # refused: overlaps
	answers ""                    # done
	mounts_answer y
	answers n y # other settings: no; install: yes
} >"$work/a1"
wizard 0 bash "$installer" <"$work/a1"
log | grep -q "Not added: use an absolute path" || fail "relative path not refused"
log | grep -q "cannot read this folder" || fail "unreadable folder not detected"
log | grep -q "Not added: it overlaps" || fail "overlapping folder not refused"
log | grep -q "installed and running" || fail "no success summary"
health | grep -q "\"version\":\"$old_version\"" || fail "health does not report $old_version"
ok "wizard installs $old_version from a local archive"

[ "$(stat -c '%U:%G %a' /etc/rewind/rewind.env)" = "root:rewind 640" ] || fail "config: $(stat -c '%U:%G %a' /etc/rewind/rewind.env)"
[ "$(stat -c '%U %a' /var/lib/rewind)" = "rewind 700" ] || fail "data dir"
[ "$(stat -c '%U %a' /var/lib/rewind/setup-code)" = "rewind 600" ] || fail "setup code"
[ "$(stat -c '%U:%G %a' /etc/rewind/install-manifest)" = "root:root 644" ] || fail "manifest"
[ "$(stat -c '%U:%G %a' /usr/local/sbin/rewind-ctl)" = "root:root 755" ] || fail "rewind-ctl"
cmp -s /usr/local/sbin/rewind-ctl "$installer" || fail "rewind-ctl is not this installer"
[ "$(ps -o user= -p "$(systemctl show -p MainPID --value rewind.service)")" = rewind ] || fail "not running as rewind"
systemctl is-enabled -q rewind.service || fail "not enabled"
id -nG rewind | tr ' ' '\n' | grep -qx rwtestgrp || fail "rewind not added to rwtestgrp"
for g in sudo adm disk docker lxd; do ! id -nG rewind | tr ' ' '\n' | grep -qx "$g" || fail "rewind is in $g"; done
[ "$(getent passwd rewind | cut -d: -f7)" = /usr/sbin/nologin ] || fail "rewind has a login shell"
ok "permissions, service user (no shell, groups: rewind rwtestgrp), manifest, rewind-ctl"

grep -qx "REWIND_LIBRARY_ROOTS=$music1:$music2" /etc/rewind/rewind.env || fail "roots: $(grep ROOTS /etc/rewind/rewind.env)"
grep -qx "REWIND_ADDR=127.0.0.1:8090" /etc/rewind/rewind.env || fail "addr"
grep -qx "REWIND_REMOTE_ACCESS=false" /etc/rewind/rewind.env || fail "remote access"
grep -qx "REWIND_DATA_DIR=/var/lib/rewind" /etc/rewind/rewind.env || fail "data dir setting"
! grep -q "$private" /etc/rewind/rewind.env || fail "unreadable folder was added"
for item in "dir /opt/rewind created" "dir /var/lib/rewind created" "user rewind created" "group rewind created" \
	"file /etc/rewind/rewind.env created" "file /etc/systemd/system/rewind.service created" \
	"file /usr/local/sbin/rewind-ctl created" "member rwtestgrp created"; do
	grep -qx "$item" /etc/rewind/install-manifest || fail "manifest lacks '$item'"
done
grep -q "^binary $old_version [0-9a-f]\{64\}$" /etc/rewind/install-manifest || fail "manifest lacks the binary checksum"
ok "configuration and installation record"

if [ "$systemd_ver" -ge 256 ]; then
	grep -qx "WantsMountsFor=$music2" /etc/systemd/system/rewind.service.d/50-installer.conf ||
		fail "drop-in: $(cat /etc/systemd/system/rewind.service.d/50-installer.conf)"
	ok "drop-in waits for the music mounts (the folder with a space is left out)"
fi
systemd-analyze verify /etc/systemd/system/rewind.service >"$work/verify.log" 2>&1 || { cat "$work/verify.log" >&2; fail "systemd-analyze verify"; }
ss -Hltn | awk '{print $4}' | grep -q '^127.0.0.1:8090$' || fail "not listening on 127.0.0.1:8090"
[ -z "$(ss -Hltn | awk '{print $4}' | grep ':8090$' | grep -v '^127.0.0.1:8090$' || true)" ] || fail "listening beyond loopback"
ok "unit verifies; listens on 127.0.0.1:8090 only"

code=$(cat /var/lib/rewind/setup-code)
log | grep -q "$code" && fail "the wizard printed the setup code"
umask 077
printf '{"setup_code":"%s","username":"owner","password":"wizard-test-password"}' "$code" >"$work/setup.json"
umask 022
status=$(curl -sS -o "$work/setup-out.json" -w '%{http_code}' -c "$jar" -H 'Content-Type: application/json' \
	-d @"$work/setup.json" "$B/api/auth/setup")
[ "$status" = 201 ] || fail "setup returned $status: $(cat "$work/setup-out.json")"
[ "$(tracks)" = 4 ] || fail "expected 4 tracks (3 + group folder): $(cat "$work/library.json")"
ok "setup over loopback; 4 tracks scanned, including the path with a space and the group-readable folder"

# --- 2. reconfigure ------------------------------------------------------------------
cp /etc/rewind/rewind.env "$work/env-1"
if [ -n "$lan_ip" ]; then
	net=(4 8091 "" y) # LAN; port; default address; confirm
else
	net=(1 8091)
fi
{
	answers "${net[@]}"
	answers "$music3" "" # add a folder; done
	mounts_answer ""
	answers y "" "" 3 "" "" # other settings: log level, scan at startup, workers 3, lifetime, idle
	answers y               # apply
} >"$work/a2"
wizard 0 /usr/local/sbin/rewind-ctl --reconfigure <"$work/a2"
B=http://127.0.0.1:8091
health >/dev/null || fail "not healthy on port 8091"
envbak=$(find /var/backups/rewind -name 'rewind.env.*-before-change' | head -n 1)
[ -n "$envbak" ] || fail "no configuration backup"
cmp -s "$envbak" "$work/env-1" || fail "backup differs from the old configuration"
[ "$(stat -c '%U %a' "$envbak")" = "root 600" ] || fail "backup mode"
grep -qx "REWIND_SCAN_WORKERS=3" /etc/rewind/rewind.env || fail "scan workers"
grep -qx "REWIND_LIBRARY_ROOTS=$music1:$music2:$music3" /etc/rewind/rewind.env || fail "roots after reconfigure"
if [ -n "$lan_ip" ]; then
	grep -qx "REWIND_ADDR=127.0.0.1:8091,$lan_ip:8091" /etc/rewind/rewind.env || fail "LAN addr"
	grep -qx "REWIND_REMOTE_ACCESS=true" /etc/rewind/rewind.env || fail "LAN remote access"
	grep -qx "REWIND_LISTEN_ALL_INTERFACES=false" /etc/rewind/rewind.env || fail "LAN all interfaces"
	grep -qx "Wants=network-online.target" /etc/systemd/system/rewind.service.d/50-installer.conf || fail "drop-in network"
	ss -Hltn | awk '{print $4}' | grep -q "^$lan_ip:8091$" || fail "not listening on $lan_ip:8091"
	ok "reconfigured: LAN address $lan_ip:8091 (+ loopback), new folder, workers; configuration backed up"
else
	ok "reconfigured: port 8091, new folder, workers; configuration backed up (no private IPv4 here for the LAN check)"
fi
login || fail "sign-in after reconfigure"
rescan_until 5
ok "the added folder is scanned (5 tracks)"

# --- 3. a failing reconfiguration is rolled back ----------------------------------------
cp /etc/rewind/rewind.env "$work/env-2"
bogus=192.168.254.253
! ip -4 -o addr show | grep -q " $bogus/" || fail "$bogus is assigned here; cannot test the rollback"
{
	answers 4 "" "$bogus" y y # LAN; same port; an unassigned address; use anyway; enable
	answers ""                # music unchanged
	mounts_answer ""
	answers n y # other settings: no; apply
} >"$work/a3"
wizard 1 /usr/local/sbin/rewind-ctl --reconfigure <"$work/a3"
log | grep -q "previous settings were restored" || fail "no restore message"
cmp -s /etc/rewind/rewind.env "$work/env-2" || fail "configuration not restored"
health >/dev/null || fail "not healthy after the restore"
ok "a reconfiguration that fails its health check restores the previous settings"

# --- 4. update ------------------------------------------------------------------------
cp /etc/rewind/rewind.env "$work/env-3"
{
	if older_first "$new_version" "$old_version"; then answers y; fi # "older release" warning (CI names)
	answers y
} >"$work/a4"
wizard 0 /usr/local/sbin/rewind-ctl --update --archive "$new_archive" <"$work/a4"
health | grep -q "\"version\":\"$new_version\"" || fail "health does not report $new_version"
cmp -s /etc/rewind/rewind.env "$work/env-3" || fail "configuration changed by the update"
backup=$(ls /var/backups/rewind/rewind-*-before-"$new_version".db)
[ "$(stat -c '%U %a' "$backup")" = "root 600" ] || fail "database backup mode"
[ -x "/opt/rewind/releases/$old_version/rewind" ] || fail "old release removed"
grep -q "^binary $new_version " /etc/rewind/install-manifest || fail "new binary not recorded"
login || fail "sign-in after update"
[ "$(tracks)" = 5 ] || fail "library changed by the update"
ok "update to $new_version: database backup, configuration kept, account and library kept"

# --- 5. status and the menu ----------------------------------------------------------
wizard 0 /usr/local/sbin/rewind-ctl --status
log | grep -q "active release: $new_version" || fail "status: release"
log | grep -q "owner account: created" || fail "status: account"
answers 6 >"$work/a5"
wizard 0 /usr/local/sbin/rewind-ctl <"$work/a5"
log | grep -q "Rewind is installed" || fail "menu not shown"
ok "--status and the maintenance menu"

# --- 6. repair --------------------------------------------------------------------------
systemctl disable --now rewind.service >/dev/null 2>&1
chmod 0644 /etc/rewind/rewind.env
rm /etc/systemd/system/rewind.service /usr/local/sbin/rewind-ctl
systemctl daemon-reload
answers y >"$work/a6"
wizard 0 bash "$installer" --repair <"$work/a6"
[ -f /etc/systemd/system/rewind.service ] || fail "unit not restored"
[ "$(stat -c '%U:%G %a' /etc/rewind/rewind.env)" = "root:rewind 640" ] || fail "config mode not restored"
[ -x /usr/local/sbin/rewind-ctl ] || fail "rewind-ctl not restored"
systemctl is-enabled -q rewind.service || fail "service not enabled"
systemctl is-active -q rewind.service || fail "service not running"
health >/dev/null || fail "not healthy after repair"
cmp -s /etc/rewind/rewind.env "$work/env-3" || fail "repair changed configuration values"
ok "repair restores the unit, rewind-ctl, permissions, and restarts"

systemctl stop rewind.service
printf 'x' >>"/opt/rewind/releases/$new_version/rewind"
answers y >"$work/a6b"
wizard 1 /usr/local/sbin/rewind-ctl --repair <"$work/a6b"
log | grep -q "checksum mismatch" || fail "a modified binary was not detected"
truncate -s -1 "/opt/rewind/releases/$new_version/rewind"
systemctl start rewind.service
i=0
until health >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 50 ] || fail "not healthy after restoring the binary"
	sleep 0.2
done
ok "repair refuses a modified binary"

# --- 7. uninstall: application only, then reinstall over the kept data --------------
answers 1 y >"$work/a7"
wizard 0 bash "$uninstaller" <"$work/a7"
for p in /etc/systemd/system/rewind.service /opt/rewind /usr/local/sbin/rewind-ctl /etc/systemd/system/rewind.service.d; do
	[ ! -e "$p" ] || fail "$p still exists after uninstall option 1"
done
[ -f /etc/rewind/rewind.env ] || fail "option 1 removed the configuration"
[ -f /var/lib/rewind/rewind.db ] || fail "option 1 removed the database"
id -u rewind >/dev/null || fail "option 1 removed the user"
ls /var/backups/rewind/*.db >/dev/null || fail "option 1 removed backups"
! systemctl is-active -q rewind.service || fail "service still running"
[ "$(music_state)" = "$music_before" ] || fail "music changed"
ok "uninstall option 1: application removed; configuration, database, backups, user and music kept"

{
	answers y           # continue (leftovers found)
	if [ -n "$lan_ip" ]; then answers "" "" "" y; else answers "" ""; fi # keep the current network settings
	answers ""          # music unchanged
	mounts_answer ""
	answers n y
} >"$work/a8"
wizard 0 bash "$installer" --archive "$new_archive" <"$work/a8"
log | grep -q "Parts of an earlier installation were found" || fail "leftovers not detected"
log | grep -q "configuration unchanged" || fail "configuration was rewritten"
login || fail "the old account does not work after reinstalling"
[ "$(tracks)" = 5 ] || fail "library not kept"
[ ! -e /var/lib/rewind/setup-code ] || fail "setup code reappeared"
ok "reinstall over kept data: same account, library and configuration"

# --- 8. uninstall: application and configuration -------------------------------------
answers 2 y >"$work/a9"
wizard 0 bash "$uninstaller" <"$work/a9"
[ ! -e /etc/rewind ] || fail "/etc/rewind still exists"
ls /var/backups/rewind/rewind.env.*-before-uninstall >/dev/null || fail "no configuration copy"
[ -f /var/lib/rewind/rewind.db ] || fail "database removed by option 2"
id -u rewind >/dev/null || fail "user removed by option 2"
ok "uninstall option 2: configuration removed (copy in backups); database, backups and user kept"

# --- 9. purge -------------------------------------------------------------------------
answers 3 n purge >"$work/a10"
wizard 0 bash "$uninstaller" <"$work/a10"
log | grep -q "Cancelled. Nothing was removed." || fail "mistyped confirmation not refused"
[ -f /var/lib/rewind/rewind.db ] || fail "database removed despite the mistyped confirmation"
ok "purge needs exactly PURGE"

echo "keep me" >/var/lib/rewind/notes.txt
answers 3 n PURGE >"$work/a11"
wizard 0 bash "$uninstaller" <"$work/a11"
[ ! -e /var/lib/rewind/rewind.db ] || fail "database not purged"
[ -f /var/lib/rewind/notes.txt ] || fail "unknown file deleted"
id -u rewind >/dev/null || fail "user removed although it still owns files"
ls /var/backups/rewind/*.db >/dev/null || fail "backups removed although declined"
ok "purge removes Rewind's files only, keeps an unknown file, the user owning it, and declined backups"

rm /var/lib/rewind/notes.txt
answers 3 y PURGE >"$work/a12"
wizard 0 bash "$uninstaller" <"$work/a12"
for p in /var/lib/rewind /var/backups/rewind /etc/rewind /opt/rewind; do [ ! -e "$p" ] || fail "$p still exists"; done
! id -u rewind >/dev/null 2>&1 || fail "user rewind still exists"
! getent group rewind >/dev/null 2>&1 || fail "group rewind still exists"
getent group rwtestgrp >/dev/null || fail "an unrelated group was removed"
[ "$(music_state)" = "$music_before" ] || fail "music changed"
ok "complete purge: nothing of Rewind left; music and unrelated groups untouched"

wizard 0 bash "$uninstaller" </dev/null
log | grep -q "nothing to remove" || fail "uninstall on a clean machine"
ok "uninstall on a clean machine changes nothing"

# --- 10. downloads from GitHub (optional) ------------------------------------------------
if [ -n "${WIZARD_TEST_DOWNLOAD:-}" ]; then
	B=http://127.0.0.1:8090
	answers y 2 "$WIZARD_TEST_DOWNLOAD" 1 "" "" n y >"$work/a13"
	wizard 0 bash "$installer" <"$work/a13"
	log | grep -q "checksum verified" || fail "download not verified"
	health | grep -q "\"version\":\"$WIZARD_TEST_DOWNLOAD\"" || fail "downloaded version not running"
	ok "downloads and installs $WIZARD_TEST_DOWNLOAD from GitHub"
	latest=$(curl -fsSIL -o /dev/null -w '%{url_effective}' https://github.com/shamil-tp/rewind/releases/latest)
	latest=${latest##*/}
	if [ "$latest" != "$WIZARD_TEST_DOWNLOAD" ]; then
		answers 1 y >"$work/a14"
		wizard 0 /usr/local/sbin/rewind-ctl --update <"$work/a14"
		health | grep -q "\"version\":\"$latest\"" || fail "latest release $latest not running"
		ok "updates to the latest release $latest"
	fi
	answers 3 y PURGE >"$work/a15"
	wizard 0 /usr/local/sbin/rewind-ctl --uninstall <"$work/a15"
	[ ! -e /opt/rewind ] || fail "/opt/rewind left after the download test"
	! id -u rewind >/dev/null 2>&1 || fail "user rewind left after the download test"
	ok "purge after the download test"
fi
