#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# rewind-installer: installs, maintains and uninstalls Rewind. (Keep this
# line: rewind-ctl and uninstall.sh use it to recognize this script.)
#
# Installs Rewind on a Linux amd64 server with systemd (Ubuntu Server), from
# a release whose checksum is listed in the release's SHA256SUMS. Published
# next to each release as install.sh, and installed as
# /usr/local/sbin/rewind-ctl for later maintenance. See docs/install.md.
#
# Interactive (as root):
#   sudo bash install.sh                 wizard: installs Rewind, or offers to
#                                        inspect, repair, update, reconfigure
#                                        or uninstall an existing installation
#   sudo bash install.sh --check         read-only checks of this server
#   sudo bash install.sh --status        installation, service and health
#   sudo bash install.sh --update        update to another verified release
#   sudo bash install.sh --repair        restore missing parts, restart, check
#   sudo bash install.sh --reconfigure   change network, music and other settings
#   sudo bash install.sh --uninstall     uninstall wizard (same as uninstall.sh)
#   Release options for the wizard and --update:
#     --release vX.Y.Z   download this version instead of asking
#     --archive FILE     use rewind-<version>-linux-amd64.tar.gz from this server
#     --sums FILE        its SHA256SUMS (default: the file next to the archive)
#
# Scripted (as root; no questions, default settings):
#   sh install.sh install  ARCHIVE [SHA256SUMS]   first installation
#   sh install.sh upgrade  ARCHIVE [SHA256SUMS]   install a new version and switch to it
#   sh install.sh rollback RELEASE                switch back to releases/RELEASE
#   sh install.sh status                          show releases, service state and health
#
# Layout (fixed, matching docs/install.md and the deploy workflow):
#   /opt/rewind/releases/<version>/   one directory per installed version
#   /opt/rewind/current               symlink to the active release
#   /etc/rewind/rewind.env            configuration (environment variables)
#   /etc/rewind/install-manifest      what this installer created (for uninstalling)
#   /var/lib/rewind                   data directory: database, setup code
#   /var/backups/rewind               database and configuration copies
#   /etc/systemd/system/rewind.service   the unit; created once, never overwritten
#   /usr/local/sbin/rewind-ctl        a copy of this script
#
# Safety rules:
#   - nothing is changed before the preflight checks pass, and nothing is
#     extracted, installed or run before the archive's SHA-256 matches
#     SHA256SUMS;
#   - a new binary is test-started as the rewind user, on a spare loopback
#     port with an empty temporary data directory, before it is activated;
#   - the database, music and old releases are never deleted or overwritten
#     when installing, repairing, updating or reconfiguring; a database is
#     never restored automatically; the configuration is backed up before it
#     is changed;
#   - Rewind never runs as root; music folders are never modified, and their
#     permissions are never changed;
#   - no firewall, router, Tailscale or package-removal changes are made;
#   - uninstalling removes only the fixed paths above, only entries the
#     installer knows, never through symlinks, and never music.

# Started with sh (dash)? Run again with bash, which the rest requires.
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi

PREFIX=/opt/rewind
RELEASES=$PREFIX/releases
CURRENT=$PREFIX/current
CONFIG_DIR=/etc/rewind
CONFIG_FILE=$CONFIG_DIR/rewind.env
MANIFEST=$CONFIG_DIR/install-manifest
STATE_DIR=/var/lib/rewind
BACKUP_DIR=/var/backups/rewind
UNIT_NAME=rewind.service
UNIT_FILE=/etc/systemd/system/$UNIT_NAME
DROPIN_DIR=/etc/systemd/system/$UNIT_NAME.d
DROPIN_FILE=$DROPIN_DIR/50-installer.conf
CTL_PATH=/usr/local/sbin/rewind-ctl
SERVICE_USER=rewind
DEFAULT_PORT=8090
REPO_URL=https://github.com/shamil-tp/rewind
MARKER_RE='^# rewind-installer:'
# Files Rewind itself writes in its data directory.
DATA_FILES="rewind.db rewind.db-journal rewind.db-wal rewind.db-shm setup-code"

B="" D="" R="" G="" Y="" N=""
WIZARD=0
APPLYING=0
STEP="" STEP_N=0 STEP_T=0
SELF=""
work=""
incoming=""
TEMP_FILES=()
MANIFEST_PENDING=()
MANIFEST_READ=""
LEGACY=0
declare -A PRE=()
# Wizard answers (W_*), current settings (CUR_*) and release choice (REL_*).
W_NET_MODE="" W_PORT="" W_IP="" W_TS_NAME="" W_MOUNTS=no W_ADVANCED=no
W_LOG_LEVEL="" W_SCAN_ON_STARTUP="" W_SCAN_WORKERS="" W_LIFETIME="" W_IDLE=""
W_ROOTS=() W_GROUPS=() CUR_ROOTS=() UN_ROOTS=()
CFG_SET="" CONFIG_BACKUP="" DROPIN_BACKUP=none NEW_INSTALLER=""
OPT_RELEASE="" OPT_ARCHIVE="" OPT_SUMS=""
REL_MODE="" REL_VERSION="" REL_ARCHIVE="" REL_SUMS=""
version="" src="" mode=""

# --- output -------------------------------------------------------------------

setup_colors() {
	if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then
		B=$'\033[1m' D=$'\033[2m' R=$'\033[31m' G=$'\033[32m' Y=$'\033[33m' N=$'\033[0m'
	fi
}

say() {
	if [ "$WIZARD" = 1 ]; then printf '  %s\n' "$*"; else printf 'install: %s\n' "$*"; fi
}
warn() {
	if [ "$WIZARD" = 1 ]; then printf '  %sWarning:%s %s\n' "$Y" "$N" "$*"; else printf 'install: warning: %s\n' "$*"; fi
}
die() {
	printf 'install: %sERROR:%s %s\n' "$R" "$N" "$*" >&2
	exit 1
}
heading() { printf '\n%s%s%s\n' "$B" "$*" "$N"; }
step() {
	STEP_N=$((STEP_N + 1))
	STEP=$*
	printf '\n%s[%d/%d] %s%s\n' "$B" "$STEP_N" "$STEP_T" "$*" "$N"
}

usage() {
	cat <<'EOF'
Rewind installer for Linux amd64 servers with systemd (Ubuntu Server).

Interactive (as root):
  sudo bash install.sh                 wizard: install Rewind, or inspect, repair,
                                       update, reconfigure or uninstall it
  sudo bash install.sh --check         read-only checks: is this server ready?
  sudo bash install.sh --status        installation, service and health
  sudo bash install.sh --update        update to another verified release
  sudo bash install.sh --repair        restore missing parts, restart, check health
  sudo bash install.sh --reconfigure   change network, music and other settings
  sudo bash install.sh --uninstall     uninstall wizard (same as uninstall.sh)

  Release options for the wizard and --update:
    --release vX.Y.Z   download this version instead of asking
    --archive FILE     use rewind-<version>-linux-amd64.tar.gz from this server
    --sums FILE        its SHA256SUMS (default: the file next to the archive)

Scripted (as root; no questions, default settings):
  sh install.sh install  ARCHIVE [SHA256SUMS]   first installation
  sh install.sh upgrade  ARCHIVE [SHA256SUMS]   install a new version and switch to it
  sh install.sh rollback RELEASE                switch back to releases/RELEASE
  sh install.sh status                          show releases, service state and health

After installation, the same commands are available as: sudo rewind-ctl ...
Documentation: docs/install.md
EOF
}

# --- prompts (answers are read from standard input) -----------------------------

# read_answer VAR: one line from standard input, without surrounding spaces.
read_answer() {
	local __line
	if ! IFS= read -r __line; then
		echo
		die "no answer (end of input); run the installer in an interactive terminal"
	fi
	__line=${__line//$'\r'/}
	__line="${__line#"${__line%%[![:space:]]*}"}"
	__line="${__line%"${__line##*[![:space:]]}"}"
	printf -v "$1" '%s' "$__line"
}

# The helpers below return answers through a variable name. Each uses its
# own local names (__ask, __yn, __choice, __word), so that a helper calling
# another never writes to its own shadowed local instead of the caller's.

# ask VAR PROMPT [DEFAULT]: an answer, or DEFAULT for an empty one.
ask() {
	local __ask
	if [ -n "${3-}" ]; then
		printf '%s %s[%s]%s: ' "$2" "$D" "$3" "$N"
	else
		printf '%s: ' "$2"
	fi
	read_answer __ask
	[ -n "$__ask" ] || __ask=${3-}
	printf -v "$1" '%s' "$__ask"
}

# ask_yn PROMPT y|n: succeeds for yes.
ask_yn() {
	local __yn __hint=y/N
	[ "$2" = y ] && __hint=Y/n
	while :; do
		printf '%s [%s]: ' "$1" "$__hint"
		read_answer __yn
		case "${__yn:-$2}" in
		y | Y | yes | Yes | YES) return 0 ;;
		n | N | no | No | NO) return 1 ;;
		esac
		echo "  Please answer y or n."
	done
}

# ask_choice VAR PROMPT DEFAULT MAX: a number from 1 to MAX.
ask_choice() {
	local __choice
	while :; do
		ask __choice "$2" "$3"
		if [[ $__choice =~ ^[0-9]{1,2}$ ]] && [ "$((10#$__choice))" -ge 1 ] && [ "$((10#$__choice))" -le "$4" ]; then
			printf -v "$1" '%s' "$((10#$__choice))"
			return 0
		fi
		echo "  Enter a number from 1 to $4."
	done
}

# confirm_word WORD PROMPT: succeeds only if exactly WORD is typed.
confirm_word() {
	local __word
	printf '%s ' "$2"
	read_answer __word
	[ "$__word" = "$1" ]
}

# --- validation (no side effects; used by the wizard and the unit tests) --------

valid_port() { [[ $1 =~ ^[1-9][0-9]{0,4}$ ]] && [ "$1" -le 65535 ]; }

# valid_ipv4 ADDR: dotted quad without leading zeros (as Go's netip parses it).
valid_ipv4() {
	local o='(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])'
	[[ $1 =~ ^$o\.$o\.$o\.$o$ ]]
}

# ipv4_kind ADDR: loopback, unspecified, tailscale (100.64.0.0/10), private
# (RFC 1918), link-local, reserved or public.
ipv4_kind() {
	local a b c d
	IFS=. read -r a b c d <<<"$1"
	if [ "$a" -eq 127 ]; then
		echo loopback
	elif [ "$1" = 0.0.0.0 ]; then
		echo unspecified
	elif [ "$a" -eq 100 ] && [ "$b" -ge 64 ] && [ "$b" -le 127 ]; then
		echo tailscale
	elif [ "$a" -eq 10 ] || { [ "$a" -eq 172 ] && [ "$b" -ge 16 ] && [ "$b" -le 31 ]; } ||
		{ [ "$a" -eq 192 ] && [ "$b" -eq 168 ]; }; then
		echo private
	elif [ "$a" -eq 169 ] && [ "$b" -eq 254 ]; then
		echo link-local
	elif [ "$a" -eq 0 ] || [ "$a" -ge 224 ]; then
		echo reserved
	else
		echo public
	fi
}

# valid_hostname NAME: lower-case DNS name, as REWIND_TRUSTED_HOSTS accepts it.
valid_hostname() {
	local h=$1 label labels
	[ -n "$h" ] && [ "${#h}" -le 253 ] || return 1
	[[ $h =~ ^[a-z0-9.-]+$ ]] || return 1
	case "$h" in .* | *. | *..*) return 1 ;; esac
	IFS=. read -r -a labels <<<"$h"
	for label in "${labels[@]}"; do
		[[ $label =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || return 1
	done
}

# valid_ts_name NAME: a Tailscale MagicDNS name (*.ts.net).
valid_ts_name() { valid_hostname "$1" && [[ $1 == *?.ts.net ]]; }

# normalize_tag VERSION: prints vX.Y.Z[-pre] (adding the v), or fails. Same
# rule as the release workflow's tag check.
normalize_tag() {
	local v=$1
	case "$v" in v*) ;; *) v=v$v ;; esac
	[[ $v =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || return 1
	echo "$v"
}

# duration_minutes D: minutes in a duration written as <n>h or <n>m.
duration_minutes() {
	[[ $1 =~ ^([1-9][0-9]{0,5})([hm])$ ]] || return 1
	if [ "${BASH_REMATCH[2]}" = h ]; then
		echo $((BASH_REMATCH[1] * 60))
	else
		echo "${BASH_REMATCH[1]}"
	fi
}

# is_true VALUE: true the way Go's strconv.ParseBool reads it.
is_true() { case "$1" in 1 | t | T | true | TRUE | True) return 0 ;; esac; return 1; }

# normalize_path PATH: without trailing slashes.
normalize_path() {
	local p=$1
	while [ "${#p}" -gt 1 ] && [ "${p%/}" != "$p" ]; do p=${p%/}; done
	printf '%s' "$p"
}

# paths_overlap A B: A and B are equal or one contains the other.
paths_overlap() {
	case "$1/" in "$2/"*) return 0 ;; esac
	case "$2/" in "$1/"*) return 0 ;; esac
	return 1
}

# check_music_path PATH: whether PATH can be written to REWIND_LIBRARY_ROOTS
# (syntax only; existence and access are checked separately). Sets REASON.
check_music_path() {
	local p=$1 d
	REASON=""
	case "$p" in
	/*) ;;
	*)
		REASON="use an absolute path, starting with /"
		return 1
		;;
	esac
	if [[ $p == *[[:cntrl:]]* ]]; then
		REASON="the path contains control characters"
		return 1
	fi
	if [[ $p == *:* ]]; then
		REASON="the path must not contain ':' (it separates folders in REWIND_LIBRARY_ROOTS)"
		return 1
	fi
	if [[ $p == *[\"\'\\\$\`]* ]]; then
		REASON="the path must not contain quotes, backslashes, \$ or backticks"
		return 1
	fi
	if [[ $p =~ ^[[:space:]] || $p =~ [[:space:]]$ || $p == *//* || $p == */./* || $p == */../* || $p == */. || $p == */.. ]]; then
		REASON="write the path plainly: no surrounding spaces, '//', '.' or '..'"
		return 1
	fi
	case "$p" in
	/ | /bin | /boot | /dev | /etc | /lib | /lib32 | /lib64 | /proc | /run | /sbin | /sys | /usr | /var | /tmp | /dev/* | /proc/* | /sys/*)
		REASON="$p is a system directory, not a music folder"
		return 1
		;;
	esac
	for d in "$PREFIX" "$CONFIG_DIR" "$STATE_DIR" "$BACKUP_DIR"; do
		if paths_overlap "$p" "$d"; then
			REASON="the path overlaps Rewind's own directory $d"
			return 1
		fi
	done
}

# --- configuration file helpers -------------------------------------------------

# env_get FILE KEY: the last value set for KEY in FILE.
env_get() {
	[ -f "$1" ] || return 0
	sed -n "s/^[[:space:]]*$2=//p" "$1" | tail -n 1 | sed 's/^"\(.*\)"$/\1/; s/[[:space:]]*$//'
}

# config_value KEY: the last value set for KEY in the configuration file.
config_value() { env_get "$CONFIG_FILE" "$1"; }

# env_apply FILE SETS: prints FILE with SETS (KEY=VALUE lines) applied. The
# first line setting a key is replaced, later ones are dropped, and keys not
# present are appended. Comments and other settings are kept as they are.
# Values are passed through the environment, so awk never interprets them.
env_apply() {
	ENV_SETS=$2 awk '
	BEGIN {
		n = split(ENVIRON["ENV_SETS"], lines, "\n")
		for (i = 1; i <= n; i++) {
			if (lines[i] == "") continue
			p = index(lines[i], "=")
			k = substr(lines[i], 1, p - 1)
			if (!(k in val)) order[++m] = k
			val[k] = substr(lines[i], p + 1)
		}
	}
	{
		line = $0
		sub(/^[ \t]+/, "", line)
		p = index(line, "=")
		if (p > 1) {
			k = substr(line, 1, p - 1)
			if (k in val) {
				if (!(k in done)) { print k "=" val[k]; done[k] = 1 }
				next
			}
		}
		print
	}
	END { for (i = 1; i <= m; i++) if (!(order[i] in done)) print order[i] "=" val[order[i]] }
	' "$1"
}

# health_url: a URL on this machine for Rewind's health endpoint, derived
# from REWIND_ADDR (loopback entries preferred).
health_url() {
	local addrs pick="" a first
	addrs=$(config_value REWIND_ADDR)
	[ -n "$addrs" ] || addrs=127.0.0.1:$DEFAULT_PORT
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
# against the unit's WorkingDirectory, /var/lib/rewind). Without a
# configuration file, the data directory of the fixed layout.
data_dir() {
	local d
	if ! real_file "$CONFIG_FILE"; then
		echo "$STATE_DIR"
		return 0
	fi
	d=$(config_value REWIND_DATA_DIR)
	[ -n "$d" ] || d=data
	case "$d" in
	/*) normalize_path "$d" ;;
	*) echo "$STATE_DIR/$d" ;;
	esac
}

active_release() {
	if [ -L "$CURRENT" ]; then basename "$(readlink "$CURRENT")"; fi
}

list_releases() {
	if [ -d "$RELEASES" ]; then find "$RELEASES" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort; fi
}

real_dir() { [ -d "$1" ] && [ ! -L "$1" ]; }
real_file() { [ -f "$1" ] && [ ! -L "$1" ]; }
exists() { [ -e "$1" ] || [ -L "$1" ]; }
is_installer_script() { real_file "$1" && grep -q "$MARKER_RE" "$1"; }

# --- installation record (manifest) ---------------------------------------------
#
# /etc/rewind/install-manifest lists what the installer created ("created"),
# found from an installation by an older install.sh ("adopted"), or found
# already present and must never remove ("existing"):
#   dir /var/lib/rewind created
#   user rewind created
#   binary v1.2.0 <sha256 of the binary>
# It never adds paths to what the uninstaller may touch; that set is fixed.

# manifest_status KIND NAME: created, adopted, existing, or unrecorded.
manifest_status() {
	local f=${MANIFEST_READ:-$MANIFEST} s=""
	if real_file "$f"; then
		s=$(K="$1" P="$2" awk '$1 == ENVIRON["K"] && $2 == ENVIRON["P"] &&
			($3 == "created" || $3 == "adopted" || $3 == "existing") { s = $3 } END { print s }' "$f")
	fi
	echo "${s:-unrecorded}"
}

# is_managed KIND NAME: created or adopted by the installer. Unrecorded items
# (installed by an older install.sh, or after the record was removed) count as
# managed; the uninstaller then relies on its own checks of each path.
is_managed() {
	case "$(manifest_status "$1" "$2")" in created | adopted | unrecorded) return 0 ;; esac
	return 1
}

# manifest_add LINE...: appends lines, atomically. Lines are queued until
# /etc/rewind exists.
manifest_add() {
	local tmp
	if [ ! -d "$CONFIG_DIR" ]; then
		MANIFEST_PENDING+=("$@")
		return 0
	fi
	tmp=$(mktemp "$CONFIG_DIR/.install-manifest.XXXXXX")
	TEMP_FILES+=("$tmp")
	{
		if real_file "$MANIFEST"; then
			cat "$MANIFEST"
		else
			echo "# Rewind installation record, written by install.sh (rewind-ctl). It lists what"
			echo "# the installer created, so that uninstalling removes only that. Do not edit."
			echo "format 1"
		fi
		printf '%s\n' "$@"
	} >"$tmp"
	chmod 0644 "$tmp"
	mv -f "$tmp" "$MANIFEST"
}

manifest_flush() {
	if [ "${#MANIFEST_PENDING[@]}" -gt 0 ] && [ -d "$CONFIG_DIR" ]; then
		local pending=("${MANIFEST_PENDING[@]}")
		MANIFEST_PENDING=()
		manifest_add "${pending[@]}"
	fi
}

# manifest_drop KIND NAME: removes the record of an item.
manifest_drop() {
	local tmp
	real_file "$MANIFEST" || return 0
	tmp=$(mktemp "$CONFIG_DIR/.install-manifest.XXXXXX")
	TEMP_FILES+=("$tmp")
	K="$1" P="$2" awk '!($1 == ENVIRON["K"] && $2 == ENVIRON["P"])' "$MANIFEST" >"$tmp"
	chmod 0644 "$tmp"
	mv -f "$tmp" "$MANIFEST"
}

# snapshot_pre: records which managed items exist before a change, so the
# manifest can tell what the installer created.
snapshot_pre() {
	local p
	for p in "$PREFIX" "$CONFIG_DIR" "$CONFIG_FILE" "$STATE_DIR" "$BACKUP_DIR" "$UNIT_FILE" "$DROPIN_FILE" "$CTL_PATH"; do
		if exists "$p"; then PRE[$p]=1; else PRE[$p]=0; fi
	done
	PRE[user]=0
	PRE[group]=0
	if id -u "$SERVICE_USER" >/dev/null 2>&1; then PRE[user]=1; fi
	if getent group "$SERVICE_USER" >/dev/null 2>&1; then PRE[group]=1; fi
	LEGACY=0
	if ! real_file "$MANIFEST" && { [ -L "$CURRENT" ] || [ -f "$STATE_DIR/rewind.db" ] ||
		grep -qs "$CURRENT/rewind" "$UNIT_FILE"; }; then
		LEGACY=1
	fi
}

# manifest_note KIND NAME KEY: records NAME as created (absent before),
# adopted (part of an older installation) or existing. Never changes a record.
manifest_note() {
	local existed=${PRE[$3]:-1} status
	[ "$(manifest_status "$1" "$2")" = unrecorded ] || return 0
	if [ "$existed" = 0 ]; then
		status=created
	elif [ "$LEGACY" = 1 ]; then
		status=adopted
	else
		status=existing
	fi
	manifest_add "$1 $2 $status"
}

manifest_binary_get() {
	real_file "$MANIFEST" || return 0
	V="$1" awk '$1 == "binary" && $2 == ENVIRON["V"] { s = $3 } END { print s }' "$MANIFEST"
}

manifest_binary_set() { # VERSION FILE
	local sum
	sum=$(sha256sum "$2" | awk '{print $1}')
	[ "$(manifest_binary_get "$1")" = "$sum" ] && return 0
	manifest_drop binary "$1"
	manifest_add "binary $1 $sum"
}

# check_manifest_trust: the record must be root's and writable only by root.
check_manifest_trust() {
	exists "$MANIFEST" || return 0
	real_file "$MANIFEST" || die "$MANIFEST is not a regular file; refusing to use it"
	[ "$(stat -c %u "$MANIFEST")" = 0 ] || die "$MANIFEST is not owned by root; refusing to use it"
	case "$(stat -c %A "$MANIFEST")" in
	?????w???? | ????????w?) die "$MANIFEST is writable by other users; refusing to use it" ;;
	esac
}

# --- checks -------------------------------------------------------------------

require_root() {
	local cmd="bash $0"
	if [ "$(id -u)" -ne 0 ]; then
		[ "$SELF" != "$CTL_PATH" ] || cmd=rewind-ctl
		echo "Rewind's installer changes system files and must run as root. Run:" >&2
		echo "  sudo $cmd${1:+ $1}" >&2
		exit 1
	fi
}

# Legacy subcommands: the checks of the original install.sh.
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
	take_lock
}

take_lock() {
	mkdir -p /run/lock
	exec 9>/run/lock/rewind-install.lock
	flock -n 9 || die "another installer (install.sh or rewind-ctl) is running"
}

# Commands the installer uses, and the Ubuntu package providing each.
REQUIRED_CMDS="systemctl:systemd tar:tar gzip:gzip sha256sum:coreutils install:coreutils mktemp:coreutils
readlink:coreutils stat:coreutils od:coreutils df:coreutils curl:curl useradd:passwd groupadd:passwd
usermod:passwd userdel:passwd setpriv:util-linux flock:util-linux findmnt:util-linux cmp:diffutils
diff:diffutils awk:mawk sed:sed getent:libc-bin find:findutils pgrep:procps"

have_cmd() { command -v "$1" >/dev/null 2>&1; }

# missing_packages: Ubuntu packages to install for missing commands (and CA
# certificates for HTTPS downloads). systemd is never installed.
missing_packages() {
	local pair cmd pkg out=""
	for pair in $REQUIRED_CMDS; do
		cmd=${pair%%:*}
		pkg=${pair#*:}
		[ "$pkg" != systemd ] || continue
		if ! have_cmd "$cmd"; then
			case " $out " in *" $pkg "*) ;; *) out="$out $pkg" ;; esac
		fi
	done
	if [ ! -s /etc/ssl/certs/ca-certificates.crt ]; then out="$out ca-certificates"; fi
	echo "${out# }"
}

os_field() { sed -n "s/^$1=//p" /etc/os-release 2>/dev/null | head -n 1 | sed 's/^"\(.*\)"$/\1/'; }

# os_support ID VERSION_ID: supported (Ubuntu LTS releases Rewind targets),
# untested (expected to work), or unsupported.
os_support() {
	case "$1:$2" in
	ubuntu:24.04 | ubuntu:26.04) echo supported ;;
	ubuntu:2[4-9].* | debian:12 | debian:13) echo untested ;;
	*) echo unsupported ;;
	esac
}

systemd_version() { systemctl --version 2>/dev/null | awk 'NR == 1 {print $2 + 0}'; }

# free_mb PATH: free megabytes on the file system holding PATH (or its
# nearest existing parent).
free_mb() {
	local p=$1
	while [ ! -e "$p" ]; do p=$(dirname "$p"); done
	df -Pk "$p" | awk 'NR == 2 {print int($4 / 1024)}'
}

check_line() { # STATUS LABEL TEXT
	local tag
	case "$1" in
	ok) tag="${G}ok${N}  " ;;
	warn) tag="${Y}warn${N}" ;;
	fail) tag="${R}FAIL${N}" ;;
	*) tag="info" ;;
	esac
	printf '  [%s] %-20s %s\n' "$tag" "$2" "$3"
}

# preflight_report: prints every check; sets BLOCKERS, OS_STATUS and
# MISSING_PKGS. Changes nothing.
preflight_report() {
	local id ver pretty arch sd mb p
	BLOCKERS=0
	id=$(os_field ID)
	ver=$(os_field VERSION_ID)
	pretty=$(os_field PRETTY_NAME)
	OS_STATUS=$(os_support "$id" "$ver")
	case "$OS_STATUS" in
	supported) check_line ok "Operating system" "${pretty:-$id $ver}" ;;
	untested) check_line warn "Operating system" "${pretty:-$id $ver}: expected to work, but not tested" ;;
	*)
		check_line fail "Operating system" "${pretty:-unknown}: supported are Ubuntu 24.04 LTS and 26.04 LTS"
		BLOCKERS=$((BLOCKERS + 1))
		;;
	esac
	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) check_line ok "CPU architecture" "$arch (release builds: linux-amd64)" ;;
	aarch64 | arm64)
		check_line fail "CPU architecture" "$arch: no arm64 release is published yet"
		BLOCKERS=$((BLOCKERS + 1))
		;;
	*)
		check_line fail "CPU architecture" "$arch: releases exist for x86-64 (amd64) only"
		BLOCKERS=$((BLOCKERS + 1))
		;;
	esac
	if [ -d /run/systemd/system ] && have_cmd systemctl; then
		sd=$(systemd_version)
		check_line ok "systemd" "running (version ${sd:-unknown})"
	else
		check_line fail "systemd" "not running; Rewind is installed as a systemd service"
		BLOCKERS=$((BLOCKERS + 1))
	fi
	MISSING_PKGS=$(missing_packages)
	if [ -z "$MISSING_PKGS" ]; then
		check_line ok "Required tools" "all present"
	else
		check_line warn "Required tools" "missing packages: $MISSING_PKGS (the wizard offers to install them)"
	fi
	for p in "$PREFIX:100" "$STATE_DIR:50" "${TMPDIR:-/tmp}:50"; do
		mb=$(free_mb "${p%:*}")
		if [ "$mb" -ge "${p##*:}" ]; then
			check_line ok "Disk space" "${p%:*}: $mb MB free"
		else
			check_line fail "Disk space" "${p%:*}: $mb MB free, at least ${p##*:} MB needed"
			BLOCKERS=$((BLOCKERS + 1))
		fi
	done
	check_existing_paths
}

# check_existing_paths: problems with existing paths that would stop an
# installation half-way. Increments BLOCKERS.
check_existing_paths() {
	local owner
	if exists "$CURRENT" && [ ! -L "$CURRENT" ]; then
		check_line fail "Existing files" "$CURRENT exists but is not a symlink; check it before installing"
		BLOCKERS=$((BLOCKERS + 1))
	fi
	if exists "$STATE_DIR"; then
		if ! real_dir "$STATE_DIR"; then
			check_line fail "Data directory" "$STATE_DIR exists but is not a directory (or is a symlink)"
			BLOCKERS=$((BLOCKERS + 1))
		else
			owner=$(stat -c %U "$STATE_DIR")
			if [ "$owner" != "$SERVICE_USER" ]; then
				check_line fail "Data directory" "$STATE_DIR is owned by $owner, not $SERVICE_USER; check its contents, then: sudo chown $SERVICE_USER:$SERVICE_USER $STATE_DIR (not recursively)"
				BLOCKERS=$((BLOCKERS + 1))
			fi
		fi
	fi
	if exists "$CONFIG_DIR" && ! real_dir "$CONFIG_DIR"; then
		check_line fail "Configuration" "$CONFIG_DIR exists but is not a directory (or is a symlink)"
		BLOCKERS=$((BLOCKERS + 1))
	fi
	if exists "$CONFIG_FILE" && ! real_file "$CONFIG_FILE"; then
		check_line fail "Configuration" "$CONFIG_FILE is not a regular file (or is a symlink)"
		BLOCKERS=$((BLOCKERS + 1))
	fi
	if exists "$CTL_PATH" && ! is_installer_script "$CTL_PATH"; then
		check_line warn "Management command" "$CTL_PATH exists and is not Rewind's; it will not be replaced"
	fi
}

internet_ok() {
	curl -fsS -o /dev/null -I --proto '=https' --tlsv1.2 --connect-timeout 10 --max-time 20 "$REPO_URL" 2>/dev/null
}

# wizard_preflight: checks, then (after confirmation) installs missing
# packages and takes the installer lock.
wizard_preflight() {
	heading "Checking this server"
	preflight_report
	if [ "$BLOCKERS" -gt 0 ]; then
		die "this server does not meet the requirements above; nothing was changed"
	fi
	if [ "$OS_STATUS" = untested ]; then
		ask_yn "This system is expected to work but has not been tested. Continue?" n ||
			{ echo "  Nothing was changed."; exit 0; }
	fi
	take_lock
}

ensure_packages() {
	MISSING_PKGS=$(missing_packages)
	[ -n "$MISSING_PKGS" ] || return 0
	heading "System packages"
	echo "  These packages are needed and missing: $MISSING_PKGS"
	have_cmd apt-get || die "apt-get is not available; install these packages yourself, then run the installer again: $MISSING_PKGS"
	ask_yn "Install them now with apt-get?" y ||
		die "required packages are missing: $MISSING_PKGS (install them with: sudo apt-get install $MISSING_PKGS)"
	# shellcheck disable=SC2086 # a list of package names from the fixed table above
	if ! { DEBIAN_FRONTEND=noninteractive apt-get update &&
		DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $MISSING_PKGS; }; then
		die "installing packages failed; install them yourself, then run the installer again: sudo apt-get install $MISSING_PKGS"
	fi
	MISSING_PKGS=$(missing_packages)
	[ -z "$MISSING_PKGS" ] || die "still missing after apt-get: $MISSING_PKGS"
	say "packages installed"
}

# --- release archives -----------------------------------------------------------

# verify_archive ARCHIVE SUMS: checks the checksum, then extracts into $work.
# Sets $version and $src.
verify_archive() {
	local archive=$1 sums=$2 base want got top listing entry bad
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
	bad=$(tar -tvzf "$archive" | awk '{print substr($1, 1, 1)}' | grep -v '^[-d]$' || true)
	[ -z "$bad" ] || die "the archive contains links or special files; refusing to extract it"
	listing=$(tar -tzf "$archive") || die "cannot list the archive"
	while IFS= read -r entry; do
		case "$entry" in
		"$top/" | "$top/"*) ;;
		*) die "unexpected archive entry '$entry'" ;;
		esac
		case "/$entry/" in */../*) die "unexpected archive entry '$entry'" ;; esac
	done <<<"$listing"

	[ -n "$work" ] || work=$(mktemp -d)
	tar -xzf "$archive" -C "$work" --no-same-owner
	src="$work/$top"
	for f in rewind rewind.service rewind.env.example LICENSE THIRD_PARTY_NOTICES.md; do
		real_file "$src/$f" || die "the archive has no $f"
	done
	# ELF, 64-bit, little endian, machine x86-64 (0x3e).
	if [ "$(od -An -tx1 -N6 "$src/rewind" | tr -d ' \n')" != 7f454c460201 ] ||
		[ "$(od -An -tx1 -j18 -N2 "$src/rewind" | tr -d ' \n')" != 3e00 ]; then
		die "the archive's rewind file is not a Linux x86-64 executable"
	fi
}

# latest_release: the tag of the latest stable GitHub release.
latest_release() {
	local url v
	url=$(curl -fsSIL --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 60 \
		-o /dev/null -w '%{url_effective}' "$REPO_URL/releases/latest" 2>/dev/null) || return 1
	case "$url" in "$REPO_URL/releases/tag/"*) ;; *) return 1 ;; esac
	v=$(normalize_tag "${url##*/}") || return 1
	echo "$v"
}

# download_release VERSION DIR: downloads the Linux archive, SHA256SUMS and
# install.sh of a release over HTTPS. Nothing is verified here.
download_release() {
	local v=$1 dir=$2 f base
	base="$REPO_URL/releases/download/$v"
	for f in "rewind-$v-linux-amd64.tar.gz" SHA256SUMS install.sh; do
		say "downloading $base/$f"
		if ! curl -fsSL --proto '=https' --tlsv1.2 --retry 2 --connect-timeout 15 --max-time 900 \
			-o "$dir/$f" "$base/$f"; then
			[ "$f" != install.sh ] || {
				rm -f "$dir/$f"
				continue
			}
			die "could not download $f for release $v. Check the version at $REPO_URL/releases, or use a downloaded archive (--archive)"
		fi
	done
}

# choose_release: sets REL_MODE (download or local), REL_VERSION (a tag or
# "latest"), REL_ARCHIVE and REL_SUMS, from options or questions.
choose_release() {
	local c v a s
	if [ -n "$OPT_ARCHIVE" ]; then
		REL_MODE=local
		REL_ARCHIVE=$OPT_ARCHIVE
		REL_SUMS=${OPT_SUMS:-$(dirname "$OPT_ARCHIVE")/SHA256SUMS}
		return 0
	fi
	if [ -n "$OPT_RELEASE" ]; then
		REL_MODE=download
		REL_VERSION=$(normalize_tag "$OPT_RELEASE") || die "--release: '$OPT_RELEASE' is not a version like v1.2.0"
		return 0
	fi
	heading "Release"
	echo "  Releases come from $REPO_URL/releases."
	echo "  The archive is checked against the release's SHA256SUMS before anything is"
	echo "  extracted or installed."
	echo "   1) Latest stable release (download)"
	echo "   2) A specific version (download)"
	echo "   3) A release archive already on this server"
	ask_choice c "Choice" 1 3
	case "$c" in
	1)
		REL_MODE=download
		REL_VERSION=latest
		;;
	2)
		REL_MODE=download
		while :; do
			ask v "Version (for example v1.2.0)"
			if REL_VERSION=$(normalize_tag "$v"); then break; fi
			echo "  '$v' is not a version like v1.2.0."
		done
		;;
	3)
		REL_MODE=local
		while :; do
			ask a "Path of rewind-<version>-linux-amd64.tar.gz"
			case "$a" in
			/*) ;;
			*) a=$(pwd)/$a ;;
			esac
			if real_file "$a"; then break; fi
			echo "  Not found (or not a regular file): $a"
		done
		REL_ARCHIVE=$a
		while :; do
			ask s "Its SHA256SUMS file (from the same release page)" "$(dirname "$a")/SHA256SUMS"
			if real_file "$s"; then break; fi
			echo "  Not found: $s"
		done
		REL_SUMS=$s
		;;
	esac
}

# fetch_release: downloads (if needed) and verifies the chosen release. Sets
# $version and $src (verify_archive), and NEW_INSTALLER when the release's
# install.sh was downloaded and verified too.
fetch_release() {
	local dl want got
	NEW_INSTALLER=""
	[ -n "$work" ] || work=$(mktemp -d)
	heading "Downloading and verifying the release"
	if [ "$REL_MODE" = download ]; then
		internet_ok || die "cannot reach $REPO_URL over HTTPS. Check the network, or download the release elsewhere and use option 3 (--archive)"
		if [ "$REL_VERSION" = latest ]; then
			REL_VERSION=$(latest_release) || die "could not determine the latest release from $REPO_URL/releases"
			say "latest stable release: $REL_VERSION"
		fi
		dl="$work/download"
		mkdir -p "$dl"
		download_release "$REL_VERSION" "$dl"
		REL_ARCHIVE="$dl/rewind-$REL_VERSION-linux-amd64.tar.gz"
		REL_SUMS="$dl/SHA256SUMS"
		if [ -f "$dl/install.sh" ]; then
			want=$(awk '$2 == "install.sh" || $2 == "*install.sh" {print $1}' "$REL_SUMS")
			got=$(sha256sum "$dl/install.sh" | awk '{print $1}')
			if [ -n "$want" ] && [ "$want" = "$got" ] && is_installer_script "$dl/install.sh" && bash -n "$dl/install.sh" 2>/dev/null; then
				NEW_INSTALLER="$dl/install.sh"
			fi
		fi
	fi
	verify_archive "$REL_ARCHIVE" "$REL_SUMS"
	if [ "$REL_MODE" = download ] && [ "$version" != "$REL_VERSION" ]; then
		die "the downloaded archive is version $version, expected $REL_VERSION"
	fi
	say "release $version is ready to install"
}

# --- operations -------------------------------------------------------------

# service_health VERSION: waits up to 30 s for the running service to report
# status ok (and VERSION, unless empty).
service_health() {
	local want=$1 url i=0 body=""
	url=$(health_url)
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
	local bin=$1 want=$2 uid gid tdata log result=1 port=18090 pid up i ok status
	uid=$(id -u "$SERVICE_USER")
	gid=$(id -g "$SERVICE_USER")
	tdata=$(mktemp -d /tmp/rewind-install-check.XXXXXX)
	chown "$uid:$gid" "$tdata"
	chmod 0700 "$tdata"
	log="$work/test-start.log"
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
	elif getent group "$SERVICE_USER" >/dev/null 2>&1; then
		useradd --system --gid "$SERVICE_USER" --home-dir "$STATE_DIR" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
		say "created system user $SERVICE_USER (no login shell, no password)"
	else
		useradd --system --user-group --home-dir "$STATE_DIR" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
		say "created system user and group $SERVICE_USER (no login shell, no password)"
	fi
	manifest_note user "$SERVICE_USER" user
	manifest_note group "$SERVICE_USER" group
}

ensure_dirs() {
	local owner d
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
	manifest_flush
	manifest_note dir "$CONFIG_DIR" "$CONFIG_DIR"
	manifest_note dir "$PREFIX" "$PREFIX"
	manifest_note dir "$STATE_DIR" "$STATE_DIR"
	manifest_note dir "$BACKUP_DIR" "$BACKUP_DIR"
}

ensure_config() {
	if [ -e "$CONFIG_FILE" ]; then
		say "keeping existing configuration $CONFIG_FILE"
	else
		install -m 0640 -o root -g "$SERVICE_USER" "$src/rewind.env.example" "$CONFIG_FILE"
		say "created $CONFIG_FILE (root:$SERVICE_USER, mode 0640)"
	fi
	manifest_note file "$CONFIG_FILE" "$CONFIG_FILE"
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
	manifest_note file "$UNIT_FILE" "$UNIT_FILE"
	systemctl daemon-reload
}

# ensure_ctl [SCRIPT]: installs SCRIPT (default: this script) as rewind-ctl.
ensure_ctl() {
	local from=${1:-$SELF}
	if [ -z "$from" ] || ! is_installer_script "$from"; then
		warn "cannot install $CTL_PATH: this script is not a regular installer file (run it from a downloaded install.sh)"
		return 0
	fi
	if [ "$(readlink -f -- "$from")" = "$CTL_PATH" ]; then return 0; fi
	if exists "$CTL_PATH" && ! is_installer_script "$CTL_PATH"; then
		warn "$CTL_PATH exists and is not Rewind's installer; it was left unchanged"
		return 0
	fi
	if is_installer_script "$CTL_PATH" && cmp -s "$from" "$CTL_PATH"; then
		manifest_note file "$CTL_PATH" "$CTL_PATH"
		return 0
	fi
	bash -n "$from" || die "$from has a syntax error; not installing it as $CTL_PATH"
	install -d -m 0755 "$(dirname "$CTL_PATH")"
	install -m 0755 -o root -g root "$from" "$CTL_PATH.new"
	mv -f "$CTL_PATH.new" "$CTL_PATH"
	manifest_note file "$CTL_PATH" "$CTL_PATH"
	say "installed the management command $CTL_PATH"
}

# stage_release: copies the extracted archive to releases/.incoming-VERSION
# and test-starts it. Leaves $target set to the final release directory.
stage_release() {
	target="$RELEASES/$version"
	if [ -e "$target" ]; then
		if [ ! -d "$target" ] || ! cmp -s "$target/rewind" "$src/rewind"; then
			die "$target already exists with a different binary; refusing to replace it"
		fi
		say "release $version is already present in $target (identical binary)"
		test_start "$target/rewind" "$version" || die "release $version failed its test start"
	else
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
	fi
	manifest_binary_set "$version" "$target/rewind"
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
	local db
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
	local db
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
	local new=$1 expect=$2 label=$3 previous
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

# verify_unit: systemd-analyze verify on the unit and its drop-ins.
verify_unit() {
	local out
	if ! have_cmd systemd-analyze; then
		say "systemd-analyze is not installed; the unit was not verified"
		return 0
	fi
	if out=$(systemd-analyze verify "$UNIT_FILE" 2>&1); then
		say "systemd-analyze verify: no problems found"
		return 0
	fi
	if printf '%s\n' "$out" | grep -q -e "$UNIT_NAME" -e "$DROPIN_DIR"; then
		printf '%s\n' "$out" | sed 's/^/    /' >&2
		die "systemd-analyze verify found problems in $UNIT_NAME (see above)"
	fi
	warn "systemd-analyze verify reported problems in other units, not in Rewind's:"
	printf '%s\n' "$out" | sed 's/^/    /'
}

cmd_install() {
	[ ! -e "$CURRENT" ] || die "Rewind is already installed ($CURRENT exists); use: sh install.sh upgrade ARCHIVE"
	verify_archive "$1" "$2"
	snapshot_pre
	ensure_user
	ensure_dirs
	stage_release
	ensure_config
	ensure_unit
	ensure_ctl
	switch_to "$version"
	systemctl enable "$UNIT_NAME" >/dev/null 2>&1 || die "systemctl enable $UNIT_NAME failed"
	systemctl restart "$UNIT_NAME"
	service_health "$version" || die "Rewind did not become healthy; see: journalctl -u $UNIT_NAME -n 50"
	say "Rewind $version is installed, running and enabled at boot."
	cat <<EOF

Next steps (docs/install.md):
  1. Set your music folder(s) with: sudo rewind-ctl --reconfigure
     (or edit REWIND_LIBRARY_ROOTS in $CONFIG_FILE, then: sudo systemctl restart rewind)
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
	snapshot_pre
	stage_release
	ensure_unit
	switch_with_backup "$version" "$version" "before-$version"
	ensure_ctl
	say "Upgraded to $version. Previous releases are kept in $RELEASES."
}

cmd_rollback() {
	local rel=$1
	case "$rel" in "" | .* | */*) die "give a release directory name from $RELEASES" ;; esac
	[ -L "$CURRENT" ] || die "Rewind is not installed with this layout ($CURRENT missing)"
	[ -x "$RELEASES/$rel/rewind" ] || die "$RELEASES/$rel/rewind not found; installed releases: $(list_releases | tr '\n' ' ')"
	[ "$(active_release)" != "$rel" ] || die "release $rel is already active"
	say "rolling back from $(active_release) to $rel"
	say "note: if the newer release migrated the database, $rel refuses it and this rollback fails safely"
	switch_with_backup "$rel" "" "before-rollback-to-$rel"
	say "Rolled back to $rel."
}

# setup_state: owner account state, from the setup-code file Rewind writes
# at every start while no account exists.
setup_state() {
	local dd
	dd=$(data_dir)
	if [ -f "$dd/setup-code" ]; then
		echo "not created yet (setup code: sudo cat $dd/setup-code)"
	elif [ -f "$dd/rewind.db" ]; then
		echo "created"
	else
		echo "unknown (no database yet)"
	fi
}

cmd_status() {
	local url
	echo "active release: $(active_release)"
	echo "installed releases:"
	list_releases | sed 's/^/  /'
	echo "service: $(systemctl is-active "$UNIT_NAME" 2>/dev/null || true) ($(systemctl is-enabled "$UNIT_NAME" 2>/dev/null || true) at boot)"
	url=$(health_url)
	echo "health ($url): $(curl -fsS --max-time 2 "$url" 2>/dev/null || echo unreachable)"
	echo "configuration: $CONFIG_FILE"
	echo "data directory: $(data_dir)"
	echo "backups: $BACKUP_DIR"
	echo "listen addresses: $(config_value REWIND_ADDR)"
	echo "music folders: $(config_value REWIND_LIBRARY_ROOTS | sed 's/:/, /g')"
	echo "owner account: $(setup_state)"
	echo "service user: $(id -u "$SERVICE_USER" >/dev/null 2>&1 && echo "$SERVICE_USER" || echo "missing")"
	echo "management command: $(is_installer_script "$CTL_PATH" && echo "$CTL_PATH" || echo "not installed")"
	echo "installation record: $(real_file "$MANIFEST" && echo "$MANIFEST" || echo "none")"
}

# --- installation state ---------------------------------------------------------

# detect_state: sets S_KIND (fresh, leftovers or installed) and S_* details.
detect_state() {
	S_RELEASE=$(active_release)
	S_ACTIVE=$(systemctl is-active "$UNIT_NAME" 2>/dev/null || true)
	S_ENABLED=$(systemctl is-enabled "$UNIT_NAME" 2>/dev/null || true)
	S_USER=no
	if id -u "$SERVICE_USER" >/dev/null 2>&1; then S_USER=yes; fi
	S_DB=no
	if [ -f "$(data_dir)/rewind.db" ]; then S_DB=yes; fi
	if [ -n "$S_RELEASE" ]; then
		S_KIND=installed
	elif exists "$PREFIX" || exists "$CONFIG_FILE" || exists "$STATE_DIR" || exists "$UNIT_FILE" ||
		exists "$CTL_PATH" || [ "$S_USER" = yes ]; then
		S_KIND=leftovers
	else
		S_KIND=fresh
	fi
}

print_state() {
	local present=present missing="not present" service
	case "$S_ENABLED" in
	"" | not-found) service="$UNIT_NAME: not installed" ;;
	*) service="$UNIT_NAME: ${S_ACTIVE:-unknown}, $S_ENABLED at boot" ;;
	esac
	printf '  %-20s %s\n' "Active release" "${S_RELEASE:-none}"
	printf '  %-20s %s\n' "Service" "$service"
	printf '  %-20s %s\n' "Configuration" "$CONFIG_FILE ($(exists "$CONFIG_FILE" && echo $present || echo "$missing"))"
	printf '  %-20s %s\n' "Database" "$(data_dir)/rewind.db ($([ "$S_DB" = yes ] && echo $present || echo "$missing"))"
	printf '  %-20s %s\n' "Service user" "$SERVICE_USER ($([ "$S_USER" = yes ] && echo $present || echo "$missing"))"
	printf '  %-20s %s\n' "Management command" "$CTL_PATH ($(is_installer_script "$CTL_PATH" && echo $present || echo "$missing"))"
}

# --- wizard: settings ---------------------------------------------------------

# load_current: reads the current configuration (or the defaults) into CUR_*.
load_current() {
	local remote cookie trusted all a1 p1 ip p2
	CUR_ADDR=$(config_value REWIND_ADDR)
	[ -n "$CUR_ADDR" ] || CUR_ADDR=127.0.0.1:$DEFAULT_PORT
	remote=$(config_value REWIND_REMOTE_ACCESS)
	cookie=$(config_value REWIND_COOKIE_SECURE)
	trusted=$(config_value REWIND_TRUSTED_HOSTS)
	all=$(config_value REWIND_LISTEN_ALL_INTERFACES)
	CUR_PORT=$DEFAULT_PORT
	CUR_IP=""
	CUR_TS_NAME=""
	CUR_NET_MODE=custom
	if [[ $CUR_ADDR =~ ^127\.0\.0\.1:([0-9]+)$ ]]; then
		CUR_PORT=${BASH_REMATCH[1]}
		if [ -z "$trusted" ] && ! is_true "$cookie" && ! is_true "$all"; then
			CUR_NET_MODE=loopback
		elif valid_ts_name "$trusted" && is_true "$cookie" && ! is_true "$all"; then
			CUR_NET_MODE=ts-https
			CUR_TS_NAME=$trusted
		fi
	elif [[ $CUR_ADDR =~ ^127\.0\.0\.1:([0-9]+),([0-9.]+):([0-9]+)$ ]]; then
		p1=${BASH_REMATCH[1]}
		ip=${BASH_REMATCH[2]}
		p2=${BASH_REMATCH[3]}
		CUR_PORT=$p1
		if [ "$p1" = "$p2" ] && valid_ipv4 "$ip" && is_true "$remote" && ! is_true "$all" && ! is_true "$cookie"; then
			case "$(ipv4_kind "$ip")" in
			tailscale)
				if [ -z "$trusted" ] || valid_hostname "$trusted"; then
					CUR_NET_MODE=ts-ip CUR_IP=$ip CUR_TS_NAME=$trusted
				fi
				;;
			private)
				if [ -z "$trusted" ]; then CUR_NET_MODE=lan CUR_IP=$ip; fi
				;;
			esac
		fi
	else
		a1=${CUR_ADDR%%,*}
		if [[ $a1 =~ :([0-9]+)$ ]]; then CUR_PORT=${BASH_REMATCH[1]}; fi
	fi
	CUR_ROOTS=()
	local r
	IFS=: read -r -a CUR_ROOTS <<<"$(config_value REWIND_LIBRARY_ROOTS)"
	local cleaned=()
	for r in "${CUR_ROOTS[@]}"; do
		r=$(normalize_path "$(echo "$r" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')")
		[ -z "$r" ] || cleaned+=("$r")
	done
	CUR_ROOTS=("${cleaned[@]}")
	CUR_LOG_LEVEL=$(config_value REWIND_LOG_LEVEL)
	CUR_SCAN_ON_STARTUP=$(config_value REWIND_SCAN_ON_STARTUP)
	CUR_SCAN_WORKERS=$(config_value REWIND_SCAN_WORKERS)
	CUR_LIFETIME=$(config_value REWIND_SESSION_LIFETIME)
	CUR_IDLE=$(config_value REWIND_SESSION_IDLE_TIMEOUT)
	CUR_MOUNTS=no
	if grep -qs '^WantsMountsFor=' "$DROPIN_FILE"; then CUR_MOUNTS=yes; fi
}

port_busy() { # PORT: something other than Rewind listens on it
	local l
	have_cmd ss || return 1
	l=$(ss -Hltnp "sport = :$1" 2>/dev/null) || return 1
	[ -n "$l" ] || return 1
	case "$l" in *'"rewind"'*) return 1 ;; esac
	return 0
}

ip_assigned() { ip -4 -o addr show 2>/dev/null | awk '{split($4, a, "/"); print a[1]}' | grep -qx "$1"; }

local_ipv4s() { # KIND: global IPv4 addresses of this machine of that kind
	local a
	for a in $(ip -4 -o addr show scope global 2>/dev/null | awk '{split($4, x, "/"); print x[1]}'); do
		valid_ipv4 "$a" && [ "$(ipv4_kind "$a")" = "$1" ] && echo "$a"
	done
	return 0
}

collect_network() {
	local c def=1 max=4 port ip name kind
	heading "Network access"
	cat <<EOF
  Rewind serves its web interface on one TCP port. Every device must still sign in
  with your password, whichever option you choose. Who should be able to connect?
   1) Only this server (127.0.0.1). Other computers can still use an SSH tunnel.
   2) Devices in your Tailscale network over HTTPS, through "tailscale serve".
      Recommended for phones and laptops; Rewind itself stays on 127.0.0.1.
   3) Devices in your Tailscale network, on this server's Tailscale IP address
      (plain HTTP inside Tailscale's encrypted tunnel; no app installation).
   4) Devices on your home network (LAN IP address, plain HTTP: your password
      crosses the Wi-Fi unencrypted; no app installation).
EOF
	case "$CUR_NET_MODE" in
	loopback) def=1 ;;
	ts-https) def=2 ;;
	ts-ip) def=3 ;;
	lan) def=4 ;;
	custom)
		echo "   5) Keep the current custom settings (REWIND_ADDR=$CUR_ADDR)"
		def=5
		max=5
		;;
	esac
	echo "  Rewind is never exposed to the internet by this installer, and no firewall or"
	echo "  router settings are changed."
	ask_choice c "Choice" "$def" "$max"
	case "$c" in
	1) W_NET_MODE=loopback ;;
	2) W_NET_MODE=ts-https ;;
	3) W_NET_MODE=ts-ip ;;
	4) W_NET_MODE=lan ;;
	5)
		W_NET_MODE=keep
		W_PORT=$CUR_PORT
		return 0
		;;
	esac

	while :; do
		ask port "Port" "$CUR_PORT"
		if ! valid_port "$port"; then
			echo "  Enter a port number from 1 to 65535."
			continue
		fi
		if [ "$port" -lt 1024 ]; then
			echo "  Ports below 1024 need root privileges, and Rewind never runs as root. Choose 1024 or higher."
			continue
		fi
		if [ "$port" -ge 18090 ] && [ "$port" -le 18099 ]; then
			echo "  Ports 18090-18099 are used by the installer's test start. Choose another one."
			continue
		fi
		if port_busy "$port"; then
			echo "  Another program is listening on port $port. Choose another port."
			continue
		fi
		break
	done
	W_PORT=$port
	W_IP=""
	W_TS_NAME=""

	case "$W_NET_MODE" in
	ts-https)
		echo "  Enter this server's Tailscale name. It is shown by 'tailscale status' and in the"
		echo "  Tailscale admin console, for example myserver.tail1234.ts.net."
		while :; do
			ask name "Tailscale name" "$CUR_TS_NAME"
			name=${name,,}
			name=${name#https://}
			name=${name%/}
			name=${name%.}
			if valid_ts_name "$name"; then break; fi
			echo "  '$name' is not a Tailscale name ending in .ts.net."
		done
		W_TS_NAME=$name
		;;
	ts-ip | lan)
		if [ "$W_NET_MODE" = ts-ip ]; then
			kind=tailscale
			ip=$CUR_IP
			if have_cmd tailscale; then ip=$(tailscale ip -4 2>/dev/null | head -n 1 || true); fi
			[ -n "$ip" ] || ip=$(local_ipv4s tailscale | head -n 1)
			echo "  This server's Tailscale IPv4 address is shown by 'tailscale ip -4' (100.64.0.0/10)."
		else
			kind=private
			ip=$CUR_IP
			[ -n "$ip" ] || ip=$(local_ipv4s private | head -n 1)
			echo "  Private (LAN) addresses on this server: $(local_ipv4s private | tr '\n' ' ')"
			echo "  Give the server a fixed address (a DHCP reservation on your router), so it does not change."
		fi
		while :; do
			ask ip "IPv4 address to listen on (in addition to 127.0.0.1)" "$ip"
			if ! valid_ipv4 "$ip"; then
				echo "  '$ip' is not an IPv4 address."
				continue
			fi
			if [ "$(ipv4_kind "$ip")" != "$kind" ]; then
				if [ "$kind" = tailscale ]; then
					echo "  $ip is not a Tailscale address (100.64.0.0/10). For a LAN address, choose option 4."
				else
					echo "  $ip is not a private LAN address (10.x, 172.16-31.x or 192.168.x)."
					echo "  This installer never makes Rewind listen on public or Tailscale addresses here;"
					echo "  see \"Network access\" in docs/deployment.md."
				fi
				continue
			fi
			if ! ip_assigned "$ip"; then
				warn "$ip is not assigned to this server right now; Rewind fails to start until it is."
				ask_yn "Use it anyway?" n || continue
			fi
			break
		done
		W_IP=$ip
		if [ "$W_NET_MODE" = ts-ip ]; then
			echo "  Optionally, also accept this server's MagicDNS name in the browser (for example"
			echo "  myserver.tail1234.ts.net). Press Enter to skip, or type - to clear it."
			while :; do
				ask name "MagicDNS name (optional)" "$CUR_TS_NAME"
				name=${name,,}
				name=${name%.}
				[ "$name" != - ] || name=""
				if [ -z "$name" ] || valid_hostname "$name"; then break; fi
				echo "  '$name' is not a valid host name."
			done
			W_TS_NAME=$name
		fi
		echo
		warn "devices that can reach $W_IP:$W_PORT will see Rewind's sign-in page."
		if [ "$W_NET_MODE" = lan ]; then
			echo "  On a LAN, traffic is plain HTTP: anyone on your Wi-Fi could read your password."
			echo "  Tailscale with HTTPS (option 2) avoids this."
		fi
		ask_yn "Enable access on $W_IP:$W_PORT?" n || {
			echo "  Keeping Rewind on this server only (127.0.0.1)."
			W_NET_MODE=loopback
			W_IP=""
			W_TS_NAME=""
		}
		;;
	esac
}

# describe_access_problem PATH: why the service user cannot read PATH, and
# what to do. Sets FIX_GROUP to a group that would fix it, if a safe one exists.
describe_access_problem() {
	local p=$1 block fstype mode owner group parts i cur
	FIX_GROUP=""
	block=$p
	IFS=/ read -r -a parts <<<"${p#/}"
	cur=""
	for i in "${parts[@]}"; do
		cur="$cur/$i"
		if ! can_enter_dir "$cur"; then
			block=$cur
			break
		fi
	done
	read -r mode owner group <<<"$(stat -c '%A %U %G' "$block" 2>/dev/null || echo "? ? ?")"
	fstype=$(findmnt -n -o FSTYPE -T "$p" 2>/dev/null | head -n 1 || true)
	echo "  The '$SERVICE_USER' user cannot read this folder: $block is $mode, owner $owner, group $group."
	case "$fstype" in
	vfat | exfat | ntfs | ntfs3 | msdos | fuseblk)
		echo "  It is on a $fstype drive, which has no Unix permissions: the mount options decide. For"
		echo "  example, mount it read-only for Rewind with this /etc/fstab line (UUID from 'lsblk -f'):"
		echo "    UUID=XXXX-XXXX  /srv/usb-music  $fstype  ro,nofail,uid=$SERVICE_USER,gid=$SERVICE_USER,umask=0222  0  0"
		;;
	esac
	if [ "$block" != "$p" ]; then
		echo "  To let Rewind pass through $block without listing it, an administrator can run:"
		echo "    sudo setfacl -m u:$SERVICE_USER:--x $block      (package 'acl'; changes only that folder)"
	fi
	if [ "$group" != "?" ] && safe_group "$group" && can_read_dir_with "$p" "$(getent group "$group" | cut -d: -f3)"; then
		FIX_GROUP=$group
	fi
}

# safe_group NAME: an ordinary group the service user may join. System and
# administrative groups (sudo, disk, docker, ...) are never offered.
safe_group() {
	local g=$1 gid
	case "$g" in
	root | sudo | admin | wheel | adm | disk | shadow | docker | lxd | libvirt | kvm | systemd-journal | \
		staff | src | tty | kmem | lpadmin | sambashare | netdev | plugdev | users | nogroup | "$SERVICE_USER")
		return 1
		;;
	esac
	gid=$(getent group "$g" | cut -d: -f3) || return 1
	[ -n "$gid" ] && [ "$gid" -ge 1000 ] && [ "$gid" -lt 60000 ]
}

# as_service_user CMD...: runs CMD as the service user, including groups the
# wizard is about to add. Before that user exists, runs it as "nobody" without
# groups, which has the same access a new system user would have.
as_service_user() { as_service_user_extra "" "$@"; }

as_service_user_extra() {
	local extra=$1 gids g
	shift
	for g in "${W_GROUPS[@]}"; do
		extra="$extra${extra:+,}$(getent group "$g" | cut -d: -f3)"
	done
	if id -u "$SERVICE_USER" >/dev/null 2>&1; then
		gids=$(id -G "$SERVICE_USER" | tr ' ' ,)
		setpriv --reuid="$(id -u "$SERVICE_USER")" --regid="$(id -g "$SERVICE_USER")" \
			--groups="$gids${extra:+,$extra}" -- "$@"
	elif [ -n "$extra" ]; then
		setpriv --reuid=65534 --regid=65534 --groups="$extra" -- "$@"
	else
		setpriv --reuid=65534 --regid=65534 --clear-groups -- "$@"
	fi
}

# Access checks use bash's built-in test, which asks the kernel. The test
# program of rust-coreutils (Ubuntu 26.04) ignores supplementary groups.
# shellcheck disable=SC2016 # $1 is expanded by the inner shell
can_read_dir() { as_service_user bash -c 'test -d "$1" && test -r "$1" && test -x "$1"' _ "$1" 2>/dev/null; }
# shellcheck disable=SC2016
can_read_dir_with() { as_service_user_extra "$2" bash -c 'test -d "$1" && test -r "$1" && test -x "$1"' _ "$1" 2>/dev/null; }
# shellcheck disable=SC2016
can_enter_dir() { as_service_user bash -c 'test -x "$1"' _ "$1" 2>/dev/null; }

collect_music() {
	local p r c ok msrc mfs
	heading "Music folders"
	cat <<EOF
  Enter the folders that hold your music, one at a time, as absolute paths (for
  example the mount point of a USB drive, as shown by 'lsblk -o NAME,MOUNTPOINTS').
  Rewind only reads them: it never mounts, formats, writes, renames, deletes or
  changes the permissions of anything there.
  If a drive is unplugged later, Rewind keeps its tracks and playlists and shows them
  as unavailable; plug it in again at the same path and rescan.
EOF
	W_ROOTS=("${CUR_ROOTS[@]}")
	while :; do
		if [ "${#W_ROOTS[@]}" -gt 0 ]; then
			echo "  Music folders:"
			for c in "${!W_ROOTS[@]}"; do echo "    $((c + 1))) ${W_ROOTS[$c]}"; done
			echo "  Type a path to add a folder, 'remove N' to remove one (its tracks are kept"
			echo "  in the library, unused), or press Enter when done."
		else
			echo "  No music folders yet. Type a path, or press Enter to skip (add them later with"
			echo "  'sudo rewind-ctl --reconfigure')."
		fi
		ask p "Folder"
		[ -n "$p" ] || break
		if [[ $p =~ ^remove[[:space:]]+([0-9]+)$ ]]; then
			c=$((10#${BASH_REMATCH[1]} - 1))
			if [ "$c" -ge 0 ] && [ "$c" -lt "${#W_ROOTS[@]}" ]; then
				echo "  Removed ${W_ROOTS[$c]}"
				unset 'W_ROOTS[c]'
				W_ROOTS=("${W_ROOTS[@]}")
			else
				echo "  There is no folder number $((c + 1))."
			fi
			continue
		fi
		p=$(normalize_path "$p")
		if ! check_music_path "$p"; then
			echo "  Not added: $REASON."
			continue
		fi
		ok=1
		for r in "${W_ROOTS[@]}"; do
			if paths_overlap "$p" "$r"; then
				echo "  Not added: it overlaps $r, which is already in the list (a file may belong to one folder only)."
				ok=0
				break
			fi
		done
		[ "$ok" = 1 ] || continue
		if [ ! -e "$p" ]; then
			echo "  $p does not exist. Is the drive plugged in and mounted?"
			echo "  Rewind starts using a new folder only once it exists when Rewind starts."
			ask_yn "Add it anyway?" n || continue
		elif [ ! -d "$p" ]; then
			echo "  Not added: $p is not a folder."
			continue
		else
			read -r msrc mfs <<<"$(findmnt -n -o SOURCE,FSTYPE -T "$p" 2>/dev/null | head -n 1 || true)"
			[ -z "$msrc" ] || echo "  Found: a folder on $msrc ($mfs)."
			if ! can_read_dir "$p"; then
				describe_access_problem "$p"
				echo "   1) Keep it; I will fix the permissions myself (Rewind skips it until it is readable)"
				echo "   2) Do not add it"
				if [ -n "$FIX_GROUP" ]; then
					echo "   3) Add the $SERVICE_USER user to the group '$FIX_GROUP'. It can then read every file that"
					echo "      group may read, not only music. Nothing else changes."
					ask_choice c "Choice" 1 3
				else
					ask_choice c "Choice" 1 2
				fi
				case "$c" in
				2) continue ;;
				3) W_GROUPS+=("$FIX_GROUP") ;;
				esac
			fi
		fi
		W_ROOTS+=("$p")
		echo "  Added $p"
	done
}

collect_mounts() {
	local sd
	W_MOUNTS=no
	[ "${#W_ROOTS[@]}" -gt 0 ] || return 0
	sd=$(systemd_version)
	[ -n "$sd" ] && [ "$sd" -ge 256 ] || return 0
	heading "Drives at boot"
	echo "  Rewind can wait at boot until the drives of these folders are mounted (if they are"
	echo "  plugged in), so the startup scan finds them. It still starts without them."
	local def=y
	[ "$CUR_MOUNTS" = yes ] || [ "${#CUR_ROOTS[@]}" -eq 0 ] || def=n
	if ask_yn "Wait for the music drives at boot (systemd WantsMountsFor)?" "$def"; then W_MOUNTS=yes; fi
}

collect_advanced() {
	local v m
	W_ADVANCED=no
	heading "Other settings"
	echo "  Logging: ${CUR_LOG_LEVEL:-info}; scan at startup: ${CUR_SCAN_ON_STARTUP:-true};"
	echo "  scan workers: ${CUR_SCAN_WORKERS:-2}; session lifetime: ${CUR_LIFETIME:-720h}; idle timeout: ${CUR_IDLE:-168h}."
	ask_yn "Change these settings?" n || return 0
	W_ADVANCED=yes
	while :; do
		ask v "Log level (debug, info, warn, error)" "${CUR_LOG_LEVEL:-info}"
		case "${v,,}" in debug | info | warn | error) W_LOG_LEVEL=${v,,} && break ;; esac
		echo "  Choose debug, info, warn or error."
	done
	W_SCAN_ON_STARTUP=false
	if ask_yn "Scan the music folders every time Rewind starts?" "$(is_true "${CUR_SCAN_ON_STARTUP:-true}" && echo y || echo n)"; then
		W_SCAN_ON_STARTUP=true
	fi
	while :; do
		ask v "Files read in parallel while scanning (1-8; more rarely helps on USB drives)" "${CUR_SCAN_WORKERS:-2}"
		if [[ $v =~ ^[1-8]$ ]]; then
			W_SCAN_WORKERS=$v
			break
		fi
		echo "  Enter a number from 1 to 8."
	done
	while :; do
		ask v "Sign-in session lifetime (for example 720h = 30 days; 5m to 8760h)" "${CUR_LIFETIME:-720h}"
		if [ "$v" = "${CUR_LIFETIME:-}" ] && [ -n "$v" ]; then
			W_LIFETIME=$v
			break
		fi
		if m=$(duration_minutes "$v") && [ "$m" -ge 5 ] && [ "$m" -le $((8760 * 60)) ]; then
			W_LIFETIME=$v
			break
		fi
		echo "  Use hours or minutes, like 720h or 90m, from 5m to 8760h."
	done
	while :; do
		ask v "End sessions unused for (for example 168h = 7 days; at least 5m)" "${CUR_IDLE:-168h}"
		if [ "$v" = "${CUR_IDLE:-}" ] && [ -n "$v" ]; then
			W_IDLE=$v
			break
		fi
		if m=$(duration_minutes "$v") && [ "$m" -ge 5 ]; then
			W_IDLE=$v
			break
		fi
		echo "  Use hours or minutes, like 168h or 30m, of at least 5m."
	done
}

print_layout() {
	heading "Installation directories"
	echo "  These locations are fixed, so that updates, rollback, backups and the deploy"
	echo "  workflow always find them. Program, configuration and data are kept apart, so an"
	echo "  update never touches your data."
	printf '    %-36s %s\n' "$PREFIX/releases/<version>" "program files of each version" \
		"$CONFIG_FILE" "configuration" \
		"$STATE_DIR" "database: account, playlists, library" \
		"$BACKUP_DIR" "backups made before changes" \
		"$UNIT_FILE" "service, runs as user '$SERVICE_USER'" \
		"$CTL_PATH" "management command"
	echo "  Logs go to the systemd journal: journalctl -u rewind"
}

# collect_settings: asks every question; sets W_* and CFG_SET.
collect_settings() {
	W_GROUPS=()
	print_layout
	collect_network
	collect_music
	collect_mounts
	collect_advanced
	build_cfg_set
}

build_cfg_set() {
	local roots="" r
	CFG_SET=""
	case "$W_NET_MODE" in
	loopback) cfg_add REWIND_ADDR "127.0.0.1:$W_PORT" REWIND_REMOTE_ACCESS false REWIND_LISTEN_ALL_INTERFACES false \
		REWIND_TRUSTED_HOSTS "" REWIND_COOKIE_SECURE false ;;
	ts-https) cfg_add REWIND_ADDR "127.0.0.1:$W_PORT" REWIND_REMOTE_ACCESS false REWIND_LISTEN_ALL_INTERFACES false \
		REWIND_TRUSTED_HOSTS "$W_TS_NAME" REWIND_COOKIE_SECURE true ;;
	ts-ip | lan) cfg_add REWIND_ADDR "127.0.0.1:$W_PORT,$W_IP:$W_PORT" REWIND_REMOTE_ACCESS true \
		REWIND_LISTEN_ALL_INTERFACES false REWIND_TRUSTED_HOSTS "$W_TS_NAME" REWIND_COOKIE_SECURE false ;;
	esac
	for r in "${W_ROOTS[@]}"; do roots="$roots${roots:+:}$r"; done
	cfg_add REWIND_LIBRARY_ROOTS "$roots"
	if [ "$W_ADVANCED" = yes ]; then
		cfg_add REWIND_LOG_LEVEL "$W_LOG_LEVEL" REWIND_SCAN_ON_STARTUP "$W_SCAN_ON_STARTUP" \
			REWIND_SCAN_WORKERS "$W_SCAN_WORKERS" REWIND_SESSION_LIFETIME "$W_LIFETIME" \
			REWIND_SESSION_IDLE_TIMEOUT "$W_IDLE"
	fi
}

cfg_add() { # KEY VALUE [KEY VALUE ...]
	while [ "$#" -ge 2 ]; do
		case "$2" in *$'\n'*) die "internal error: a value for $1 contains a line break" ;; esac
		CFG_SET="$CFG_SET$1=$2"$'\n'
		shift 2
	done
}

final_addr() { # REWIND_ADDR after the change
	local v
	v=$(printf '%s' "$CFG_SET" | sed -n 's/^REWIND_ADDR=//p')
	[ -n "$v" ] || v=$CUR_ADDR
	echo "$v"
}

net_description() {
	case "$W_NET_MODE" in
	loopback) echo "this server only (127.0.0.1:$W_PORT)" ;;
	ts-https) echo "127.0.0.1:$W_PORT, for HTTPS through tailscale serve at https://$W_TS_NAME/" ;;
	ts-ip) echo "127.0.0.1:$W_PORT and Tailscale address $W_IP:$W_PORT${W_TS_NAME:+ (also as $W_TS_NAME)}" ;;
	lan) echo "127.0.0.1:$W_PORT and LAN address $W_IP:$W_PORT (plain HTTP)" ;;
	keep) echo "unchanged (REWIND_ADDR=$CUR_ADDR)" ;;
	esac
}

review_settings() {
	local r
	heading "Review"
	printf '  %-22s %s\n' "Network" "$(net_description)"
	if [ "${#W_ROOTS[@]}" -eq 0 ]; then
		printf '  %-22s %s\n' "Music folders" "none yet"
	else
		printf '  %-22s %s\n' "Music folders" "${W_ROOTS[0]}"
		for r in "${W_ROOTS[@]:1}"; do printf '  %-22s %s\n' "" "$r"; done
	fi
	if [ "${#W_GROUPS[@]}" -gt 0 ]; then
		printf '  %-22s %s\n' "Group membership" "add $SERVICE_USER to: ${W_GROUPS[*]}"
	fi
	[ "$W_MOUNTS" = no ] || printf '  %-22s %s\n' "Drives at boot" "wait for the music folders' mounts"
	if [ "$W_ADVANCED" = yes ]; then
		printf '  %-22s %s\n' "Logging" "$W_LOG_LEVEL"
		printf '  %-22s %s\n' "Scanning" "at startup: $W_SCAN_ON_STARTUP, workers: $W_SCAN_WORKERS"
		printf '  %-22s %s\n' "Sessions" "lifetime $W_LIFETIME, idle timeout $W_IDLE"
	fi
	echo "  Settings written to $CONFIG_FILE:"
	printf '%s' "$CFG_SET" | sed 's/^/    /'
}

# write_config BASE: writes the configuration from BASE with CFG_SET applied.
# An existing, different configuration is backed up first (CONFIG_BACKUP).
write_config() {
	local base=$1 tmp
	CONFIG_BACKUP=""
	tmp=$(mktemp "$CONFIG_DIR/.rewind.env.XXXXXX")
	TEMP_FILES+=("$tmp")
	env_apply "$base" "$CFG_SET" >"$tmp"
	chown root:"$SERVICE_USER" "$tmp"
	chmod 0640 "$tmp"
	if real_file "$CONFIG_FILE" && cmp -s "$tmp" "$CONFIG_FILE"; then
		rm -f "$tmp"
		say "configuration unchanged: $CONFIG_FILE"
	else
		if real_file "$CONFIG_FILE"; then
			CONFIG_BACKUP=$(backup_config before-change)
			say "previous configuration saved as $CONFIG_BACKUP"
			if have_cmd diff; then
				diff -u --label "before" --label "after" "$CONFIG_FILE" "$tmp" | sed -n '3,$p' | sed 's/^/    /' || true
			fi
		fi
		mv -f "$tmp" "$CONFIG_FILE"
		say "wrote $CONFIG_FILE (root:$SERVICE_USER, mode 0640)"
	fi
	manifest_note file "$CONFIG_FILE" "$CONFIG_FILE"
}

backup_config() { # LABEL: prints the backup path
	local dest
	[ -d "$BACKUP_DIR" ] || install -d -m 0700 "$BACKUP_DIR"
	dest="$BACKUP_DIR/rewind.env.$(date -u +%Y%m%dT%H%M%SZ)-$1"
	install -m 0600 -o root -g root "$CONFIG_FILE" "$dest"
	echo "$dest"
}

apply_groups() {
	local g
	for g in "${W_GROUPS[@]}"; do
		if id -nG "$SERVICE_USER" | tr ' ' '\n' | grep -qx "$g"; then continue; fi
		safe_group "$g" || die "refusing to add $SERVICE_USER to group $g"
		usermod -a -G "$g" "$SERVICE_USER"
		manifest_add "member $g created"
		say "added $SERVICE_USER to group $g"
	done
}

# dropin_content ADDR: the installer's systemd drop-in for these settings, or
# nothing. Paths with spaces are left out of WantsMountsFor (space-separated).
dropin_content() {
	local a lines=() after="" mounts="" r skipped="" addrs host
	IFS=, read -r -a addrs <<<"$1"
	for a in "${addrs[@]}"; do
		host=${a%:*}
		case "$a" in
		127.* | localhost:* | "[::1]":*) continue ;;
		esac
		[ -n "$after" ] || after="network-online.target"
		if valid_ipv4 "$host" && [ "$(ipv4_kind "$host")" = tailscale ]; then
			after="network-online.target tailscaled.service"
		fi
	done
	if [ -n "$after" ]; then
		lines+=("# Rewind listens on a LAN or Tailscale address: start once the network is up.")
		lines+=("Wants=network-online.target" "After=$after")
	fi
	if [ "$W_MOUNTS" = yes ]; then
		for r in "${W_ROOTS[@]}"; do
			if [[ $r == *[[:space:]]* ]]; then
				skipped="$skipped $r"
			else
				mounts="$mounts ${r//%/%%}"
			fi
		done
		[ -z "$mounts" ] || lines+=("# Wait for the music drives at boot, if they are plugged in." "WantsMountsFor=${mounts# }")
	fi
	[ "${#lines[@]}" -gt 0 ] || return 0
	echo "# rewind-installer: written by Rewind's installer (rewind-ctl --reconfigure), which"
	echo "# replaces it. Put your own changes in another drop-in: sudo systemctl edit rewind"
	echo "[Unit]"
	printf '%s\n' "${lines[@]}"
	[ -z "$skipped" ] || echo "# Not listed because their paths contain spaces:$skipped"
}

# write_dropin: installs, updates or removes the installer's drop-in. Saves the
# previous one in DROPIN_BACKUP ("none" when there was none).
write_dropin() {
	local content tmp
	content=$(dropin_content "$(final_addr)")
	DROPIN_BACKUP=none
	if real_file "$DROPIN_FILE"; then
		DROPIN_BACKUP="$work/dropin.previous"
		cp "$DROPIN_FILE" "$DROPIN_BACKUP"
	fi
	if exists "$DROPIN_FILE" && ! is_installer_script "$DROPIN_FILE"; then
		warn "$DROPIN_FILE was not written by the installer; it was left unchanged"
		return 0
	fi
	if [ -z "$content" ]; then
		if real_file "$DROPIN_FILE"; then
			rm -f "$DROPIN_FILE"
			rmdir "$DROPIN_DIR" 2>/dev/null || true
			say "removed $DROPIN_FILE (no longer needed)"
		fi
		return 0
	fi
	if real_file "$DROPIN_FILE" && [ "$(cat "$DROPIN_FILE")" = "$content" ]; then return 0; fi
	install -d -m 0755 "$DROPIN_DIR"
	tmp=$(mktemp "$DROPIN_DIR/.installer.XXXXXX")
	TEMP_FILES+=("$tmp")
	printf '%s\n' "$content" >"$tmp"
	chmod 0644 "$tmp"
	mv -f "$tmp" "$DROPIN_FILE"
	manifest_note file "$DROPIN_FILE" "$DROPIN_FILE"
	say "wrote $DROPIN_FILE"
}

# check_music_final: after installation, whether the service user can read
# each music folder. Warnings only.
check_music_final() {
	local r bad=0
	[ "${#W_ROOTS[@]}" -gt 0 ] || return 0
	W_GROUPS=()
	for r in "${W_ROOTS[@]}"; do
		if [ ! -e "$r" ]; then
			warn "$r does not exist yet: mount it, then run: sudo systemctl restart rewind"
			bad=1
		elif ! can_read_dir "$r"; then
			warn "the $SERVICE_USER user cannot read $r; fix the permissions (see docs/library.md), then rescan"
			bad=1
		else
			say "$SERVICE_USER can read $r"
		fi
	done
	return 0
}

# --- wizard: flows --------------------------------------------------------------

on_exit() {
	local status=$?
	cleanup
	if [ "$status" -ne 0 ] && [ "$APPLYING" = 1 ]; then
		{
			echo
			echo "${R}The operation did not complete.${N} It stopped during: ${STEP:-preparation}"
			echo "  Your music, database and backups were not deleted."
			echo "  Next steps:"
			echo "    sudo journalctl -u rewind -n 50 --no-pager     # Rewind's own log"
			echo "    sudo $(ctl_hint) --status                       # what is installed"
			echo "  Running the installer again offers to repair the installation."
		} >&2
	fi
	exit "$status"
}

ctl_hint() {
	if is_installer_script "$CTL_PATH"; then echo rewind-ctl; else echo "bash $(basename "${SELF:-install.sh}")"; fi
}

cleanup() {
	local f
	if [ -n "$incoming" ] && [ -d "$incoming" ]; then rm -rf "$incoming"; fi
	if [ -n "$work" ] && [ -d "$work" ]; then rm -rf "$work"; fi
	for f in "${TEMP_FILES[@]}"; do [ ! -f "$f" ] || rm -f "$f"; done
	if [ -f "$CTL_PATH.new" ] && [ "$(id -u)" -eq 0 ]; then rm -f "$CTL_PATH.new"; fi
}

banner() {
	printf '%sRewind installer%s\n' "$B" "$N"
	echo "Installs and maintains Rewind, a self-hosted music server, as a systemd service."
	echo "Nothing is changed until you confirm. Press Ctrl+C at any question to stop."
}

wizard_main() {
	local c
	banner
	wizard_preflight
	check_manifest_trust
	detect_state
	case "$S_KIND" in
	fresh)
		heading "Installation"
		echo "  No existing installation was found. The wizard asks a few questions, shows a"
		echo "  summary, and installs nothing before you confirm it."
		wizard_install
		;;
	leftovers)
		heading "Existing files"
		print_state
		echo "  Parts of an earlier installation were found. They are kept and reused: your"
		echo "  database (account, playlists) and configuration are not replaced."
		wizard_install
		;;
	installed)
		heading "Rewind is installed"
		print_state
		echo
		echo "  What would you like to do?"
		echo "   1) Show the full status"
		echo "   2) Repair the installation (restore missing parts, restart, check health)"
		echo "   3) Update to another verified release"
		echo "   4) Change settings (network, music folders, logging)"
		echo "   5) Uninstall"
		echo "   6) Quit"
		ask_choice c "Choice" 1 6
		case "$c" in
		1) cmd_status ;;
		2) cmd_repair ;;
		3) cmd_update ;;
		4) cmd_reconfigure ;;
		5) cmd_uninstall ;;
		6) echo "  Nothing was changed." ;;
		esac
		;;
	esac
}

wizard_install() {
	local base
	ask_yn "Continue?" y || {
		echo "  Nothing was changed."
		exit 0
	}
	ensure_packages
	choose_release
	fetch_release
	load_current
	collect_settings
	review_settings
	ask_yn "Install Rewind $version with these settings?" y || {
		echo "  Nothing was changed."
		exit 0
	}
	APPLYING=1
	STEP_N=0
	STEP_T=8
	snapshot_pre
	step "Creating the service user"
	ensure_user
	apply_groups
	step "Creating directories"
	ensure_dirs
	step "Installing release $version"
	stage_release
	step "Writing the configuration"
	base=$CONFIG_FILE
	real_file "$CONFIG_FILE" || base="$src/rewind.env.example"
	write_config "$base"
	step "Installing the systemd service"
	ensure_unit
	write_dropin
	systemctl daemon-reload
	step "Installing the management command"
	ensure_ctl
	step "Starting Rewind"
	switch_to "$version"
	verify_unit
	systemctl enable "$UNIT_NAME" >/dev/null 2>&1 || die "systemctl enable $UNIT_NAME failed"
	systemctl restart "$UNIT_NAME" || true
	step "Checking health"
	service_health "$version" || die "Rewind did not become healthy; see: journalctl -u $UNIT_NAME -n 50"
	say "healthy: Rewind $version is running"
	APPLYING=0
	check_music_final
	final_summary "Rewind $version is installed and running."
}

cmd_reconfigure() {
	[ -n "$(active_release)" ] || die "Rewind is not installed; run: sudo bash install.sh"
	real_file "$CONFIG_FILE" || die "$CONFIG_FILE is missing; run --repair first"
	[ -n "$work" ] || work=$(mktemp -d)
	load_current
	collect_settings
	review_settings
	ask_yn "Apply these settings and restart Rewind?" y || {
		echo "  Nothing was changed."
		exit 0
	}
	APPLYING=1
	STEP_N=0
	STEP_T=3
	snapshot_pre
	step "Writing the configuration"
	apply_groups
	write_config "$CONFIG_FILE"
	write_dropin
	systemctl daemon-reload
	verify_unit
	step "Restarting Rewind"
	systemctl restart "$UNIT_NAME" || true
	step "Checking health"
	if ! service_health ""; then
		restore_settings
		die "Rewind did not become healthy with the new settings; the previous settings were restored. See: journalctl -u $UNIT_NAME -n 50"
	fi
	say "healthy"
	APPLYING=0
	check_music_final
	final_summary "The new settings are active."
}

# restore_settings: puts back the configuration and drop-in from before a
# failed reconfiguration, and restarts.
restore_settings() {
	warn "restoring the previous settings"
	if [ -n "$CONFIG_BACKUP" ] && real_file "$CONFIG_BACKUP"; then
		install -m 0640 -o root -g "$SERVICE_USER" "$CONFIG_BACKUP" "$CONFIG_FILE"
	fi
	if [ "$DROPIN_BACKUP" = none ]; then
		if is_installer_script "$DROPIN_FILE"; then rm -f "$DROPIN_FILE"; fi
	elif [ -f "$DROPIN_BACKUP" ]; then
		install -m 0644 "$DROPIN_BACKUP" "$DROPIN_FILE"
	fi
	systemctl daemon-reload || true
	systemctl reset-failed "$UNIT_NAME" 2>/dev/null || true
	systemctl restart "$UNIT_NAME" || true
	if service_health ""; then say "Rewind is running again with the previous settings"; fi
}

cmd_update() {
	local cur
	detect_state
	[ "$S_KIND" = installed ] || die "Rewind is not installed; run: sudo bash install.sh"
	ensure_packages
	choose_release
	fetch_release
	cur=$(active_release)
	if [ "$version" = "$cur" ]; then
		say "Rewind $cur is already the active release; nothing to do."
		return 0
	fi
	if [ "$(printf '%s\n%s\n' "$cur" "$version" | sort -V | head -n 1)" = "$version" ]; then
		warn "$version is older than the active release $cur."
		echo "  If $cur changed the database format, $version refuses it, and the installer"
		echo "  switches back to $cur. To go back to an installed release, use rollback instead."
		ask_yn "Switch to the older release $version anyway?" n || {
			echo "  Nothing was changed."
			exit 0
		}
	fi
	ask_yn "Update Rewind from $cur to $version? The database is backed up first." y || {
		echo "  Nothing was changed."
		exit 0
	}
	APPLYING=1
	STEP_N=0
	STEP_T=3
	snapshot_pre
	step "Installing release $version"
	stage_release
	ensure_unit
	step "Switching to $version (database backup, restart, health check)"
	switch_with_backup "$version" "$version" "before-$version"
	step "Updating the management command"
	ensure_ctl "${NEW_INSTALLER:-$SELF}"
	APPLYING=0
	load_current
	W_NET_MODE=$CUR_NET_MODE W_PORT=$CUR_PORT W_IP=$CUR_IP W_TS_NAME=$CUR_TS_NAME
	final_summary "Rewind is updated to $version. Previous releases are kept in $RELEASES."
}

cmd_repair() {
	local rel fixes=() want got db dd
	detect_state
	if [ "$S_KIND" != installed ]; then
		die "there is no active release to repair; run the installer without options to install Rewind"
	fi
	rel=$(active_release)
	if ! real_file "$RELEASES/$rel/rewind" || [ ! -x "$RELEASES/$rel/rewind" ]; then
		die "the active release $rel has no executable; install a verified release with --update"
	fi
	want=$(manifest_binary_get "$rel")
	if [ -n "$want" ]; then
		got=$(sha256sum "$RELEASES/$rel/rewind" | awk '{print $1}')
		[ "$got" = "$want" ] || die "the binary of release $rel was changed since it was installed (checksum mismatch); reinstall a verified release with --update"
	fi
	heading "Repair"
	[ "$S_USER" = yes ] || fixes+=("create the service user $SERVICE_USER")
	for d in "$STATE_DIR" "$CONFIG_DIR" "$BACKUP_DIR" "$RELEASES"; do
		[ -d "$d" ] || fixes+=("create $d")
	done
	if [ -d "$STATE_DIR" ] && [ "$(stat -c %a "$STATE_DIR")" != 700 ]; then fixes+=("set $STATE_DIR to mode 0700"); fi
	if ! real_file "$CONFIG_FILE"; then
		fixes+=("create $CONFIG_FILE from release $rel's template (default settings)")
	elif [ "$(stat -c '%U:%G %a' "$CONFIG_FILE")" != "root:$SERVICE_USER 640" ]; then
		fixes+=("set $CONFIG_FILE to root:$SERVICE_USER, mode 0640")
	fi
	dd=$(data_dir)
	db="$dd/rewind.db"
	if real_file "$db" && [ "$(stat -c '%U %a' "$db")" != "$SERVICE_USER 600" ]; then
		fixes+=("set $db to owner $SERVICE_USER, mode 0600")
	fi
	exists "$UNIT_FILE" || fixes+=("install $UNIT_FILE from release $rel")
	if ! is_installer_script "$CTL_PATH" || ! cmp -s "${SELF:-/dev/null}" "$CTL_PATH"; then
		[ "$(readlink -f -- "${SELF:-/}")" = "$CTL_PATH" ] || fixes+=("install or update $CTL_PATH")
	fi
	[ "$S_ENABLED" = enabled ] || fixes+=("enable $UNIT_NAME at boot")
	fixes+=("restart $UNIT_NAME and check its health")
	echo "  Planned:"
	printf '    - %s\n' "${fixes[@]}"
	echo "  Your database, music, configuration values and backups are kept."
	ask_yn "Repair now?" y || {
		echo "  Nothing was changed."
		exit 0
	}
	APPLYING=1
	STEP_N=0
	STEP_T=3
	snapshot_pre
	src="$RELEASES/$rel"
	version=$rel
	step "Restoring missing parts"
	ensure_user
	ensure_dirs
	if [ "$(stat -c %a "$STATE_DIR")" != 700 ]; then chmod 0700 "$STATE_DIR"; fi
	ensure_config
	if [ "$(stat -c '%U:%G %a' "$CONFIG_FILE")" != "root:$SERVICE_USER 640" ]; then
		chown "root:$SERVICE_USER" "$CONFIG_FILE"
		chmod 0640 "$CONFIG_FILE"
		say "set $CONFIG_FILE to root:$SERVICE_USER, mode 0640"
	fi
	if real_file "$db" && [ "$(stat -c '%U %a' "$db")" != "$SERVICE_USER 600" ]; then
		chown "$SERVICE_USER:$SERVICE_USER" "$db"
		chmod 0600 "$db"
		say "set $db to owner $SERVICE_USER, mode 0600"
	fi
	ensure_unit
	ensure_ctl
	real_file "$RELEASES/$rel/rewind" && [ -z "$want" ] && manifest_binary_set "$rel" "$RELEASES/$rel/rewind"
	verify_unit
	step "Enabling and restarting Rewind"
	systemctl reset-failed "$UNIT_NAME" 2>/dev/null || true
	systemctl enable "$UNIT_NAME" >/dev/null 2>&1 || die "systemctl enable $UNIT_NAME failed"
	systemctl restart "$UNIT_NAME" || true
	step "Checking health"
	service_health "" || die "Rewind did not become healthy; see: journalctl -u $UNIT_NAME -n 50"
	APPLYING=0
	load_current
	W_NET_MODE=$CUR_NET_MODE W_PORT=$CUR_PORT W_IP=$CUR_IP W_TS_NAME=$CUR_TS_NAME
	final_summary "The installation is repaired and Rewind is running."
}

cmd_check() {
	heading "Rewind preflight check (read-only: nothing is changed)"
	preflight_report
	if internet_ok; then
		check_line ok "Release downloads" "$REPO_URL is reachable"
	else
		check_line warn "Release downloads" "$REPO_URL is not reachable; install from a downloaded archive (--archive)"
	fi
	if port_busy "$DEFAULT_PORT"; then
		check_line warn "Port $DEFAULT_PORT" "in use by another program; choose another port in the wizard"
	else
		check_line ok "Port $DEFAULT_PORT" "available (or used by Rewind)"
	fi
	detect_state
	case "$S_KIND" in
	fresh) check_line info "Installation" "none found: the wizard installs Rewind" ;;
	leftovers) check_line info "Installation" "parts of an earlier one found: the wizard reuses them and keeps the data" ;;
	installed) check_line info "Installation" "release $S_RELEASE (service ${S_ACTIVE:-unknown}): the wizard offers status, repair, update, settings and uninstall" ;;
	esac
	echo
	if [ "$BLOCKERS" -gt 0 ]; then
		echo "  ${R}Not ready:${N} $BLOCKERS problem(s) above must be fixed first."
		return 1
	fi
	echo "  ${G}Ready.${N} Start the wizard with: sudo bash install.sh"
}

final_summary() {
	local port mode rel dd host r
	rel=$(active_release)
	dd=$(data_dir)
	port=$W_PORT
	mode=$W_NET_MODE
	if [ "$mode" = keep ] || [ "$mode" = custom ]; then mode=custom; fi
	host=$(hostname 2>/dev/null || echo this-server)
	heading "$1"
	printf '  %-20s %s\n' "Version" "$rel ($CURRENT)"
	printf '  %-20s %s\n' "Service" "$UNIT_NAME: $(systemctl is-active "$UNIT_NAME" 2>/dev/null || true), $(systemctl is-enabled "$UNIT_NAME" 2>/dev/null || true) at boot, runs as '$SERVICE_USER'"
	printf '  %-20s %s\n' "Configuration" "$CONFIG_FILE"
	printf '  %-20s %s\n' "Data" "$dd"
	printf '  %-20s %s\n' "Backups" "$BACKUP_DIR"
	printf '  %-20s %s\n' "Listening on" "$(config_value REWIND_ADDR)"
	load_current
	if [ "${#CUR_ROOTS[@]}" -eq 0 ]; then
		printf '  %-20s %s\n' "Music folders" "none (add them with: sudo rewind-ctl --reconfigure)"
	else
		printf '  %-20s %s\n' "Music folders" "${CUR_ROOTS[0]}"
		for r in "${CUR_ROOTS[@]:1}"; do printf '  %-20s %s\n' "" "$r"; done
	fi
	printf '  %-20s %s\n' "Owner account" "$(setup_state)"

	heading "Opening Rewind"
	case "$mode" in
	loopback)
		echo "  Rewind is reachable from this server only: http://localhost:$port"
		echo "  From another computer, open an SSH tunnel and browse to http://localhost:$port :"
		echo "    ssh -L $port:127.0.0.1:$port ${SUDO_USER:-you}@$host"
		;;
	ts-https)
		echo "  1. Once, on this server, publish Rewind inside your tailnet (Tailscale's HTTPS"
		echo "     certificates must be enabled in its admin console):"
		echo "       sudo tailscale serve --bg http://127.0.0.1:$port"
		echo "     Never use 'tailscale funnel': it publishes Rewind on the internet."
		echo "  2. On a device in your tailnet, open https://$W_TS_NAME/"
		;;
	ts-ip)
		echo "  On devices in your tailnet, open http://$W_IP:$port${W_TS_NAME:+ or http://$W_TS_NAME:$port}"
		;;
	lan)
		echo "  On devices on your home network, open http://$W_IP:$port"
		if have_cmd ufw && ufw status 2>/dev/null | grep -q '^Status: active'; then
			echo "  The ufw firewall is active and was not changed. To allow your LAN, for example:"
			echo "    sudo ufw allow from ${W_IP%.*}.0/24 to any port $port proto tcp"
		fi
		;;
	*)
		echo "  Custom network settings are in use (REWIND_ADDR=$(config_value REWIND_ADDR))."
		echo "  See \"Network access\" in docs/deployment.md."
		;;
	esac
	if [ -f "$dd/setup-code" ]; then
		heading "Create the owner account (first run)"
		echo "  For security this works only from the server itself (a loopback address):"
		echo "  1. Open http://localhost:$port on this server, or through the SSH tunnel:"
		echo "       ssh -L $port:127.0.0.1:$port ${SUDO_USER:-you}@$host"
		echo "  2. Enter the one-time setup code. Show it with:"
		echo "       sudo cat $dd/setup-code"
		echo "     (Rewind writes a new code at every start until the account exists.)"
		echo "  3. Choose a username and a password of at least 12 characters, then sign in."
	fi
	heading "Managing Rewind"
	local ctl
	ctl=$(ctl_hint)
	printf '  %-36s %s\n' "sudo systemctl status rewind" "service state" \
		"sudo journalctl -u rewind -f" "follow the log" \
		"sudo systemctl restart rewind" "restart (after editing $CONFIG_FILE)" \
		"sudo systemctl stop rewind" "stop" \
		"sudo $ctl" "status, repair, update, settings, uninstall" \
		"sudo $ctl --update" "update to a verified release" \
		"sudo $ctl --uninstall" "uninstall (keeps your data unless you purge)"
}

# --- uninstall ------------------------------------------------------------------

remove_item() { # MODE DESCRIPTION COMMAND...: plans or performs one removal
	local mode=$1 desc=$2
	shift 2
	if [ "$mode" = plan ]; then
		PLAN_REMOVE+=("$desc")
		return 0
	fi
	if "$@"; then
		REMOVED+=("$desc")
	else
		KEPT+=("$desc (removal FAILED)")
		FAILURES=$((FAILURES + 1))
	fi
}

keep_item() { # MODE DESCRIPTION
	if [ "$1" = plan ]; then PLAN_KEEP+=("$2"); else KEPT+=("$2"); fi
}

dir_entries() { # DIR: sets ENTRIES to the names in DIR
	ENTRIES=()
	mapfile -d '' -t ENTRIES < <(find "$1" -mindepth 1 -maxdepth 1 -printf '%f\0' 2>/dev/null)
}

owner_is() { [ "$(stat -c %U "$1" 2>/dev/null)" = "$2" ]; }

release_dir_ok() { # NAME: a release directory the installer may remove
	local n=$1 d="$RELEASES/$1"
	[[ $n =~ ^(\.incoming-)?[A-Za-z0-9][A-Za-z0-9._+-]*$ ]] || return 1
	real_dir "$d" && [ "$(stat -c %u "$d")" = 0 ] || return 1
	[[ $n == .incoming-* ]] || real_file "$d/rewind"
}

stop_service() {
	systemctl disable --now "$UNIT_NAME" >/dev/null 2>&1 || true
	if systemctl is-active -q "$UNIT_NAME" 2>/dev/null; then
		echo "install: ERROR: $UNIT_NAME is still running after 'systemctl disable --now'" >&2
		return 1
	fi
}

un_service() {
	local mode=$1 frag
	have_cmd systemctl || return 0
	frag=$(systemctl show -p FragmentPath --value "$UNIT_NAME" 2>/dev/null || true)
	if [ -n "$frag" ] && [ "$frag" != "$UNIT_FILE" ]; then
		keep_item "$mode" "systemd unit $frag (not installed by Rewind's installer; check it yourself)"
		return 0
	fi
	if exists "$UNIT_FILE"; then
		if real_file "$UNIT_FILE" && is_managed file "$UNIT_FILE" && grep -q "$CURRENT/rewind" "$UNIT_FILE"; then
			if [ "$mode" = plan ]; then
				PLAN_REMOVE+=("$UNIT_NAME: stop, disable, and remove $UNIT_FILE")
			else
				stop_service || die "could not stop $UNIT_NAME; nothing was removed"
				remove_item apply "$UNIT_FILE (service stopped and disabled)" rm -f -- "$UNIT_FILE"
			fi
		else
			keep_item "$mode" "$UNIT_FILE (not Rewind's installed unit)"
		fi
	fi
	if exists "$DROPIN_FILE"; then
		if is_installer_script "$DROPIN_FILE"; then
			remove_item "$mode" "$DROPIN_FILE" rm -f -- "$DROPIN_FILE"
		else
			keep_item "$mode" "$DROPIN_FILE (not written by the installer)"
		fi
	fi
	if real_dir "$DROPIN_DIR"; then
		dir_entries "$DROPIN_DIR"
		local e others=()
		for e in "${ENTRIES[@]}"; do [ "$DROPIN_DIR/$e" = "$DROPIN_FILE" ] || others+=("$e"); done
		if [ "${#others[@]}" -eq 0 ]; then
			remove_item "$mode" "$DROPIN_DIR (empty)" rmdir -- "$DROPIN_DIR"
		else
			keep_item "$mode" "your own drop-ins in $DROPIN_DIR: ${others[*]}"
		fi
	fi
	if [ "$mode" = apply ]; then
		systemctl daemon-reload || true
		systemctl reset-failed "$UNIT_NAME" >/dev/null 2>&1 || true
	fi
}

un_program() {
	local mode=$1 e r kept=0 rk top
	exists "$PREFIX" || return 0
	if ! real_dir "$PREFIX" || [ "$(stat -c %u "$PREFIX")" != 0 ]; then
		keep_item "$mode" "$PREFIX (not a root-owned directory; check it yourself)"
		return 0
	fi
	dir_entries "$PREFIX"
	top=("${ENTRIES[@]}")
	for e in "${top[@]}"; do
		case "$e" in
		current | .current-new)
			if [ -L "$PREFIX/$e" ]; then
				remove_item "$mode" "$PREFIX/$e (symlink to $(readlink "$PREFIX/$e"))" rm -f -- "$PREFIX/$e"
			else
				keep_item "$mode" "$PREFIX/$e (not a symlink)"
				kept=$((kept + 1))
			fi
			;;
		releases)
			if real_dir "$RELEASES"; then
				rk=0
				dir_entries "$RELEASES"
				for r in "${ENTRIES[@]}"; do
					if release_dir_ok "$r"; then
						remove_item "$mode" "$RELEASES/$r" rm -rf --one-file-system -- "$RELEASES/$r"
					else
						keep_item "$mode" "$RELEASES/$r (not a release directory)"
						rk=$((rk + 1))
					fi
				done
				if [ "$rk" -eq 0 ]; then
					remove_item "$mode" "$RELEASES" rmdir -- "$RELEASES"
				else
					kept=$((kept + 1))
				fi
			else
				keep_item "$mode" "$RELEASES (not a directory)"
				kept=$((kept + 1))
			fi
			;;
		*)
			keep_item "$mode" "$PREFIX/$e (not created by the installer)"
			kept=$((kept + 1))
			;;
		esac
	done
	if [ "$kept" -eq 0 ] && is_managed dir "$PREFIX"; then
		remove_item "$mode" "$PREFIX" rmdir -- "$PREFIX"
	elif [ "$kept" -eq 0 ]; then
		keep_item "$mode" "$PREFIX (existed before the installation; now empty)"
	fi
}

un_ctl() {
	local mode=$1
	exists "$CTL_PATH" || return 0
	if is_installer_script "$CTL_PATH"; then
		remove_item "$mode" "$CTL_PATH" rm -f -- "$CTL_PATH"
	else
		keep_item "$mode" "$CTL_PATH (not Rewind's installer)"
	fi
}

remove_config_with_backup() {
	local b
	b=$(backup_config before-uninstall) || return 1
	rm -f -- "$CONFIG_FILE"
}

un_config() { # MODE BACKUP(0|1)
	local mode=$1 backup=$2 e kept=0
	exists "$CONFIG_DIR" || return 0
	if ! real_dir "$CONFIG_DIR" || ! owner_is "$CONFIG_DIR" root; then
		keep_item "$mode" "$CONFIG_DIR (not a root-owned directory; check it yourself)"
		return 0
	fi
	dir_entries "$CONFIG_DIR"
	for e in "${ENTRIES[@]}"; do
		case "$e" in
		rewind.env)
			if real_file "$CONFIG_FILE" && is_managed file "$CONFIG_FILE"; then
				if [ "$backup" = 1 ]; then
					remove_item "$mode" "$CONFIG_FILE (a copy is saved in $BACKUP_DIR)" remove_config_with_backup
				else
					remove_item "$mode" "$CONFIG_FILE" rm -f -- "$CONFIG_FILE"
				fi
			else
				keep_item "$mode" "$CONFIG_FILE (existed before the installation)"
				kept=$((kept + 1))
			fi
			;;
		install-manifest | .rewind.env.* | .install-manifest.*)
			if real_file "$CONFIG_DIR/$e"; then
				remove_item "$mode" "$CONFIG_DIR/$e" rm -f -- "$CONFIG_DIR/$e"
			else
				keep_item "$mode" "$CONFIG_DIR/$e (not a regular file)"
				kept=$((kept + 1))
			fi
			;;
		*)
			keep_item "$mode" "$CONFIG_DIR/$e (not created by the installer)"
			kept=$((kept + 1))
			;;
		esac
	done
	if [ "$kept" -eq 0 ] && is_managed dir "$CONFIG_DIR"; then
		remove_item "$mode" "$CONFIG_DIR" rmdir -- "$CONFIG_DIR"
	elif [ "$kept" -eq 0 ]; then
		keep_item "$mode" "$CONFIG_DIR (existed before the installation)"
	fi
}

un_data() {
	local mode=$1 e kept=0 r
	DATA_REMAINS=0
	if [ "$CONF_DATA_DIR" != "$STATE_DIR" ]; then
		keep_item "$mode" "configured data directory $CONF_DATA_DIR (outside $STATE_DIR; delete it yourself if you want)"
	fi
	exists "$STATE_DIR" || return 0
	DATA_REMAINS=1
	if ! real_dir "$STATE_DIR" || ! owner_is "$STATE_DIR" "$SERVICE_USER"; then
		keep_item "$mode" "$STATE_DIR (not a directory owned by $SERVICE_USER; check it yourself)"
		return 0
	fi
	for r in "${UN_ROOTS[@]}"; do
		if paths_overlap "$STATE_DIR" "$r"; then
			keep_item "$mode" "$STATE_DIR (overlaps the music folder $r)"
			return 0
		fi
	done
	dir_entries "$STATE_DIR"
	for e in "${ENTRIES[@]}"; do
		if [[ " $DATA_FILES " == *" $e "* ]] && real_file "$STATE_DIR/$e"; then
			remove_item "$mode" "$STATE_DIR/$e" rm -f -- "$STATE_DIR/$e"
		else
			keep_item "$mode" "$STATE_DIR/$e (not a file Rewind creates)"
			kept=$((kept + 1))
		fi
	done
	if [ "$kept" -eq 0 ] && is_managed dir "$STATE_DIR"; then
		remove_item "$mode" "$STATE_DIR" rmdir -- "$STATE_DIR"
		DATA_REMAINS=0
	elif [ "$kept" -eq 0 ]; then
		keep_item "$mode" "$STATE_DIR (existed before the installation)"
	fi
	if [ "$mode" = apply ]; then
		if exists "$STATE_DIR"; then DATA_REMAINS=1; else DATA_REMAINS=0; fi
	fi
}

backup_name_ok() { [[ $1 =~ ^rewind-[A-Za-z0-9._+-]+\.db$ || $1 =~ ^rewind\.env\.[A-Za-z0-9._+-]+$ ]]; }

un_backups() {
	local mode=$1 e kept=0
	exists "$BACKUP_DIR" || return 0
	if ! real_dir "$BACKUP_DIR" || ! owner_is "$BACKUP_DIR" root; then
		keep_item "$mode" "$BACKUP_DIR (not a root-owned directory; check it yourself)"
		return 0
	fi
	dir_entries "$BACKUP_DIR"
	for e in "${ENTRIES[@]}"; do
		if backup_name_ok "$e" && real_file "$BACKUP_DIR/$e"; then
			remove_item "$mode" "$BACKUP_DIR/$e" rm -f -- "$BACKUP_DIR/$e"
		else
			keep_item "$mode" "$BACKUP_DIR/$e (not a backup made by the installer)"
			kept=$((kept + 1))
		fi
	done
	if [ "$kept" -eq 0 ] && is_managed dir "$BACKUP_DIR"; then
		remove_item "$mode" "$BACKUP_DIR" rmdir -- "$BACKUP_DIR"
	elif [ "$kept" -eq 0 ]; then
		keep_item "$mode" "$BACKUP_DIR (existed before the installation)"
	fi
}

count_backups() {
	local e n=0
	if real_dir "$BACKUP_DIR"; then
		dir_entries "$BACKUP_DIR"
		for e in "${ENTRIES[@]}"; do if backup_name_ok "$e" && real_file "$BACKUP_DIR/$e"; then n=$((n + 1)); fi; done
	fi
	echo "$n"
}

# user_signature: the account looks like the one useradd created above.
user_signature() {
	local ent uid shell home
	ent=$(getent passwd "$SERVICE_USER") || return 1
	IFS=: read -r _ _ uid _ _ home shell <<<"$ent"
	[ "$uid" -lt 1000 ] && [ "$home" = "$STATE_DIR" ] &&
		case "$shell" in /usr/sbin/nologin | /sbin/nologin | /bin/false | /usr/bin/false) true ;; *) false ;; esac
}

remove_user() {
	local members
	if pgrep -u "$SERVICE_USER" >/dev/null 2>&1; then
		echo "install: processes are still running as $SERVICE_USER" >&2
		return 1
	fi
	userdel "$SERVICE_USER" || return 1
	if getent group "$SERVICE_USER" >/dev/null 2>&1 && is_managed group "$SERVICE_USER"; then
		members=$(getent group "$SERVICE_USER" | cut -d: -f4)
		[ -n "$members" ] || groupdel "$SERVICE_USER" || return 1
	fi
}

un_user() {
	local mode=$1 st
	id -u "$SERVICE_USER" >/dev/null 2>&1 || return 0
	st=$(manifest_status user "$SERVICE_USER")
	if [ "$st" = existing ]; then
		keep_item "$mode" "user $SERVICE_USER (existed before the installation)"
		return 0
	fi
	if [ "$st" = unrecorded ] && ! user_signature; then
		keep_item "$mode" "user $SERVICE_USER (does not look like the installer's system user)"
		return 0
	fi
	if [ "$DATA_REMAINS" = 1 ]; then
		keep_item "$mode" "user $SERVICE_USER (still owns $STATE_DIR)"
		return 0
	fi
	remove_item "$mode" "system user and group $SERVICE_USER" remove_user
}

# uninstall_items MODE OPTION: every step, in order.
uninstall_items() {
	local mode=$1 option=$2
	un_service "$mode"
	un_program "$mode"
	un_ctl "$mode"
	if [ "$option" -ge 2 ]; then
		if [ "$option" = 2 ]; then un_config "$mode" 1; else un_config "$mode" 0; fi
	elif exists "$CONFIG_DIR"; then
		keep_item "$mode" "configuration $CONFIG_FILE"
	fi
	if [ "$option" = 3 ]; then
		un_data "$mode"
		if [ "$PURGE_BACKUPS" = 1 ]; then
			un_backups "$mode"
		elif exists "$BACKUP_DIR"; then
			keep_item "$mode" "backups in $BACKUP_DIR"
		fi
		un_user "$mode"
	else
		DATA_REMAINS=1
		exists "$STATE_DIR" && keep_item "$mode" "database and application data in $STATE_DIR"
		exists "$BACKUP_DIR" && keep_item "$mode" "backups in $BACKUP_DIR"
		id -u "$SERVICE_USER" >/dev/null 2>&1 && keep_item "$mode" "user $SERVICE_USER (owns the data)"
	fi
	return 0
}

cmd_uninstall() {
	local c r found=0
	check_manifest_trust
	[ -n "$work" ] || work=$(mktemp -d)
	MANIFEST_READ="$work/manifest"
	if real_file "$MANIFEST"; then cp "$MANIFEST" "$MANIFEST_READ"; else : >"$MANIFEST_READ"; fi
	CONF_DATA_DIR=$(data_dir)
	UN_ROOTS=()
	IFS=: read -r -a UN_ROOTS <<<"$(config_value REWIND_LIBRARY_ROOTS)"
	PURGE_BACKUPS=0

	heading "Uninstall Rewind"
	for p in "$UNIT_FILE" "$PREFIX" "$CTL_PATH" "$CONFIG_DIR" "$STATE_DIR" "$BACKUP_DIR" "$DROPIN_FILE"; do
		if exists "$p"; then found=1; fi
	done
	if id -u "$SERVICE_USER" >/dev/null 2>&1; then found=1; fi
	if [ "$found" = 0 ]; then
		echo "  Rewind is not installed here; there is nothing to remove."
		return 0
	fi
	detect_state
	print_state
	printf '  %-20s %s\n' "Backups" "$(count_backups) file(s) in $BACKUP_DIR"
	if [ "${#UN_ROOTS[@]}" -gt 0 ]; then
		local joined
		joined=$(IFS=,; echo "${UN_ROOTS[*]}")
		printf '  %-20s %s\n' "Music folders" "${joined//,/, } (never touched)"
	fi
	[ -s "$MANIFEST_READ" ] || echo "  No installation record was found (an installation by an older install.sh, or the
  configuration was removed). Only paths of Rewind's fixed layout that pass checks are removed."
	echo
	echo "  What should be removed? Your music is never touched."
	echo "   1) The application only. Keeps configuration, database, playlists and backups."
	echo "   2) The application and its configuration. Keeps database, playlists and backups."
	echo "   3) ${R}Complete purge (destructive):${N} also deletes the database: your account,"
	echo "      playlists, preferences and library index."
	echo "   4) Cancel"
	ask_choice c "Choice" 1 4
	if [ "$c" = 4 ]; then
		echo "  Nothing was removed."
		return 0
	fi
	if [ "$c" = 3 ] && [ "$(count_backups)" -gt 0 ]; then
		echo "  $BACKUP_DIR holds $(count_backups) backup file(s): your only copies of earlier databases."
		if ask_yn "Delete these backups too?" n; then PURGE_BACKUPS=1; fi
	fi
	PLAN_REMOVE=()
	PLAN_KEEP=()
	uninstall_items plan "$c"
	heading "Plan"
	if [ "${#PLAN_REMOVE[@]}" -eq 0 ]; then
		echo "  Nothing to remove with this option."
	else
		echo "  Will be removed:"
		printf '    - %s\n' "${PLAN_REMOVE[@]}"
	fi
	if [ "${#PLAN_KEEP[@]}" -gt 0 ]; then
		echo "  Will be kept:"
		printf '    - %s\n' "${PLAN_KEEP[@]}"
	fi
	for r in "${UN_ROOTS[@]}"; do echo "    - music folder $r (never touched)"; done
	[ "${#PLAN_REMOVE[@]}" -gt 0 ] || return 0
	if [ "$c" = 3 ]; then
		echo
		echo "  ${R}This permanently deletes the paths listed above.${N} It cannot be undone."
		if ! confirm_word PURGE "  Type PURGE (in capitals) to delete them, or anything else to cancel:"; then
			echo "  Cancelled. Nothing was removed."
			return 0
		fi
	else
		ask_yn "Remove the items listed above?" n || {
			echo "  Nothing was removed."
			return 0
		}
	fi
	REMOVED=()
	KEPT=()
	FAILURES=0
	uninstall_items apply "$c"
	heading "Done"
	if [ "${#REMOVED[@]}" -gt 0 ]; then
		echo "  Removed:"
		printf '    - %s\n' "${REMOVED[@]}"
	fi
	if [ "${#KEPT[@]}" -gt 0 ]; then
		echo "  Kept:"
		printf '    - %s\n' "${KEPT[@]}"
	fi
	for r in "${UN_ROOTS[@]}"; do echo "    - music folder $r (never touched)"; done
	echo "  System packages were not removed."
	if [ "$c" != 3 ] && exists "$STATE_DIR"; then
		echo "  To install Rewind again with this data: sudo bash install.sh"
	fi
	[ "$FAILURES" -eq 0 ] || die "$FAILURES item(s) could not be removed (see above)"
}

# --- main -----------------------------------------------------------------------

main() {
	local action="" a
	set -eu
	setup_colors
	SELF=$(readlink -f -- "$0" 2>/dev/null || true)
	trap on_exit EXIT
	trap 'exit 130' INT TERM

	mode=${1:-}
	case "$mode" in
	install | upgrade)
		if [ $# -lt 2 ] || [ $# -gt 3 ]; then
			usage >&2
			exit 2
		fi
		preflight
		check_manifest_trust
		archive=$2
		sums=${3:-$(dirname "$archive")/SHA256SUMS}
		if [ "$mode" = install ]; then cmd_install "$archive" "$sums"; else cmd_upgrade "$archive" "$sums"; fi
		exit 0
		;;
	rollback)
		[ $# -eq 2 ] || {
			usage >&2
			exit 2
		}
		preflight
		cmd_rollback "$2"
		exit 0
		;;
	status)
		[ $# -eq 1 ] || {
			usage >&2
			exit 2
		}
		require_root status
		cmd_status
		exit 0
		;;
	esac

	OPT_RELEASE="" OPT_ARCHIVE="" OPT_SUMS=""
	while [ "$#" -gt 0 ]; do
		a=$1
		shift
		case "$a" in
		-h | --help | help)
			usage
			exit 0
			;;
		--check | --status | --repair | --update | --reconfigure | --uninstall)
			[ -z "$action" ] || die "choose one of --check, --status, --repair, --update, --reconfigure, --uninstall"
			action=${a#--}
			;;
		--release | --archive | --sums)
			[ "$#" -gt 0 ] || die "$a needs a value"
			case "$a" in
			--release) OPT_RELEASE=$1 ;;
			--archive) OPT_ARCHIVE=$(readlink -f -- "$1") || die "--archive: $1 not found" ;;
			--sums) OPT_SUMS=$(readlink -f -- "$1") || die "--sums: $1 not found" ;;
			esac
			shift
			;;
		*)
			echo "install: unknown option '$a'" >&2
			usage >&2
			exit 2
			;;
		esac
	done
	[ -z "$OPT_RELEASE" ] || [ -z "$OPT_ARCHIVE" ] || die "use either --release or --archive"
	case "$action" in "" | update) ;; *) [ -z "$OPT_RELEASE$OPT_ARCHIVE$OPT_SUMS" ] || die "--release, --archive and --sums apply to the wizard and --update only" ;; esac

	WIZARD=1
	require_root "${action:+--$action}"
	case "$action" in
	check) cmd_check ;;
	status) cmd_status ;;
	uninstall)
		[ -d /run/systemd/system ] || warn "systemd is not running; the service cannot be stopped"
		take_lock
		cmd_uninstall
		;;
	repair | update | reconfigure)
		wizard_preflight
		check_manifest_trust
		W_GROUPS=()
		W_MOUNTS=no
		case "$action" in
		repair) cmd_repair ;;
		update) cmd_update ;;
		reconfigure) cmd_reconfigure ;;
		esac
		;;
	"") wizard_main ;;
	esac
}

# Sourcing the script (for the unit tests) only defines the functions.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	main "$@"
	exit $?
fi
