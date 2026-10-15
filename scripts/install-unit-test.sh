#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
#
# Unit tests for install.sh and uninstall.sh that need neither root nor
# systemd and change nothing outside a temporary directory: input
# validation, configuration-file editing, network-mode detection, the
# systemd drop-in, the installation record, uninstall planning on a scratch
# directory tree, the prompts, and the command line as a non-root user.
#
# Usage: bash scripts/install-unit-test.sh     (run as a normal user)
#
# The privileged paths (users, systemd, real installs and uninstalls) are
# covered by scripts/wizard-test.sh and scripts/install-test.sh, which need a
# disposable machine.
# The tests set variables that the sourced installer functions read, and
# define stand-ins for some of them:
# shellcheck disable=SC1091,SC2034,SC2153,SC2154,SC2317
set -u

here=$(cd "$(dirname "$0")" && pwd)
installer="$here/install.sh"
[ "$(uname -s)" = Linux ] || { echo "install-unit-test: run on Linux (the installer's target)" >&2; exit 1; }
[ "$(id -u)" -ne 0 ] || { echo "install-unit-test: run as a normal user, not root" >&2; exit 1; }

# shellcheck source=install.sh
. "$installer"
set +e

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
passed=0
failed=0

ok() { passed=$((passed + 1)); }
bad() {
	failed=$((failed + 1))
	echo "FAIL: $*" >&2
}
yes_() { if "$@"; then ok; else bad "expected success: $*"; fi; }
no_() { if "$@"; then bad "expected failure: $*"; else ok; fi; }
eq() { # GOT WANT DESCRIPTION
	if [ "$1" = "$2" ]; then ok; else bad "$3: got [$1], want [$2]"; fi
}
has() { # HAYSTACK NEEDLE DESCRIPTION
	case "$1" in *"$2"*) ok ;; *) bad "$3: [$2] not in [$1]" ;; esac
}
hasnt() {
	case "$1" in *"$2"*) bad "$3: [$2] unexpectedly in [$1]" ;; *) ok ;; esac
}

# --- validation ---------------------------------------------------------------

for p in 1 1024 8090 65535; do yes_ valid_port "$p"; done
for p in 0 65536 080 abc "" "80 " -1 99999; do no_ valid_port "$p"; done

for a in 0.0.0.0 127.0.0.1 192.168.1.20 255.255.255.255 100.64.0.1; do yes_ valid_ipv4 "$a"; done
for a in 256.1.1.1 1.2.3 01.2.3.4 "1.2.3.4 " 1.2.3.4.5 a.b.c.d "" ::1; do no_ valid_ipv4 "$a"; done

eq "$(ipv4_kind 127.0.0.1)" loopback "ipv4_kind loopback"
eq "$(ipv4_kind 0.0.0.0)" unspecified "ipv4_kind unspecified"
eq "$(ipv4_kind 100.64.0.1)" tailscale "ipv4_kind 100.64"
eq "$(ipv4_kind 100.127.255.254)" tailscale "ipv4_kind 100.127"
eq "$(ipv4_kind 100.128.0.1)" public "ipv4_kind 100.128"
eq "$(ipv4_kind 10.0.0.5)" private "ipv4_kind 10/8"
eq "$(ipv4_kind 172.16.0.1)" private "ipv4_kind 172.16"
eq "$(ipv4_kind 172.31.255.1)" private "ipv4_kind 172.31"
eq "$(ipv4_kind 172.32.0.1)" public "ipv4_kind 172.32"
eq "$(ipv4_kind 192.168.1.20)" private "ipv4_kind 192.168"
eq "$(ipv4_kind 169.254.1.1)" link-local "ipv4_kind link-local"
eq "$(ipv4_kind 224.0.0.1)" reserved "ipv4_kind multicast"
eq "$(ipv4_kind 8.8.8.8)" public "ipv4_kind public"

for h in rewind.tail1234.ts.net localhost a b-c.d 0a.example; do yes_ valid_hostname "$h"; done
long=$(printf 'a%.0s' $(seq 1 64))
for h in Rewind.ts.net -a.b a-.b a..b .a a. "*.ts.net" "a b" "a_b" "http://a" "a:80" "$long.com" ""; do
	no_ valid_hostname "$h"
done
yes_ valid_ts_name rewind.tail1234.ts.net
for h in ts.net .ts.net example.com rewind.ts.net.evil.com; do no_ valid_ts_name "$h"; done

eq "$(normalize_tag 1.2.0)" v1.2.0 "normalize_tag adds v"
eq "$(normalize_tag v1.2.0-rc.1)" v1.2.0-rc.1 "normalize_tag pre-release"
for v in v1.2 "v1.2.0;id" ../v1 "v1.2.0 " v1.2.0/x latest ""; do no_ normalize_tag "$v"; done

eq "$(duration_minutes 720h)" 43200 "duration 720h"
eq "$(duration_minutes 90m)" 90 "duration 90m"
for d in 0h 1.5h 10s 5 h 720h0m "" 1d; do no_ duration_minutes "$d"; done

for b in 1 t T true TRUE True; do yes_ is_true "$b"; done
for b in 0 f false yes on ""; do no_ is_true "$b"; done

eq "$(normalize_path /a/b/)" /a/b "normalize_path trailing slash"
eq "$(normalize_path /a/b///)" /a/b "normalize_path several slashes"
eq "$(normalize_path /)" / "normalize_path root"

yes_ paths_overlap /a /a
yes_ paths_overlap /a /a/b
yes_ paths_overlap /a/b /a
no_ paths_overlap /a /ab
no_ paths_overlap /srv/music /srv/music2

for p in /srv/music "/media/nihal/MY MUSIC" /run/media/u/STICK "/mnt/a&b (1)" /media "/srv/#music"; do
	yes_ check_music_path "$p"
done
# Literal ~ and $ are intended: they must be refused.
# shellcheck disable=SC2088,SC2016
for p in music ./music "~/Music" /a:b '/a"b' "/a'b" '/a\b' '/a$b' '/a`b' "/a//b" "/a/../b" "/a/./b" \
	"/a/." "/a/.." / /etc /usr /var /tmp /proc/1 /sys/x /var/lib/rewind /var/lib/rewind/music /var/lib \
	/etc/rewind /opt /opt/rewind/x " /srv/m" "/srv/m " "/srv/a"$'\n'"b" "/srv/a"$'\t'"b"; do
	no_ check_music_path "$p"
done
check_music_path /var/lib
has "$REASON" "/var/lib/rewind" "check_music_path names the overlapping directory"

eq "$(os_support ubuntu 24.04)" supported "os 24.04"
eq "$(os_support ubuntu 26.04)" supported "os 26.04"
eq "$(os_support ubuntu 25.10)" untested "os 25.10"
eq "$(os_support debian 12)" untested "os debian 12"
eq "$(os_support ubuntu 22.04)" unsupported "os 22.04"
eq "$(os_support fedora 40)" unsupported "os fedora"

# --- missing packages ---------------------------------------------------------

have_cmd() { case "$1" in curl | setpriv | flock | diff) return 1 ;; esac; return 0; }
pk=$(missing_packages)
has " $pk " " curl " "missing curl"
has " $pk " " util-linux " "missing setpriv/flock"
has " $pk " " diffutils " "missing diff"
eq "$(echo "$pk" | tr ' ' '\n' | grep -c '^util-linux$')" 1 "util-linux listed once"
have_cmd() { case "$1" in systemctl) return 1 ;; esac; return 0; }
hasnt "$(missing_packages)" systemd "systemd is never installed"
unset -f have_cmd
have_cmd() { command -v "$1" >/dev/null 2>&1; }

# --- configuration files ------------------------------------------------------

cat >"$tmp/base.env" <<'EOF'
# comment REWIND_ADDR=ignored
REWIND_DATA_DIR=/var/lib/rewind
REWIND_ADDR=127.0.0.1:8090
  REWIND_LIBRARY_ROOTS=/old
#REWIND_TRUSTED_HOSTS=commented
REWIND_LIBRARY_ROOTS=/older/duplicate
CUSTOM_LINE_KEPT=yes
EOF
sets=$'REWIND_ADDR=127.0.0.1:8091,192.168.1.20:8091\nREWIND_LIBRARY_ROOTS=/media/u/MY MUSIC:/srv/a&b\nREWIND_TRUSTED_HOSTS=\nREWIND_REMOTE_ACCESS=true\n'
env_apply "$tmp/base.env" "$sets" >"$tmp/out.env"
cat >"$tmp/want.env" <<'EOF'
# comment REWIND_ADDR=ignored
REWIND_DATA_DIR=/var/lib/rewind
REWIND_ADDR=127.0.0.1:8091,192.168.1.20:8091
REWIND_LIBRARY_ROOTS=/media/u/MY MUSIC:/srv/a&b
#REWIND_TRUSTED_HOSTS=commented
CUSTOM_LINE_KEPT=yes
REWIND_TRUSTED_HOSTS=
REWIND_REMOTE_ACCESS=true
EOF
if cmp -s "$tmp/out.env" "$tmp/want.env"; then ok; else
	bad "env_apply output differs:"
	diff "$tmp/want.env" "$tmp/out.env" >&2
fi
eq "$(env_apply "$tmp/want.env" "$sets" | cmp -s - "$tmp/want.env" && echo same)" same "env_apply is idempotent"

printf 'K=one\nK="two words"\n' >"$tmp/q.env"
eq "$(env_get "$tmp/q.env" K)" "two words" "env_get last value, quotes removed"
eq "$(env_get "$tmp/missing.env" K)" "" "env_get missing file"

CONFIG_FILE="$tmp/rewind.env"
printf 'REWIND_ADDR=127.0.0.1:8091,192.168.1.2:8091\n' >"$CONFIG_FILE"
eq "$(health_url)" http://127.0.0.1:8091/api/health "health_url prefers loopback"
printf 'REWIND_ADDR=192.168.1.2:9000\n' >"$CONFIG_FILE"
eq "$(health_url)" http://192.168.1.2:9000/api/health "health_url without loopback"
printf 'REWIND_ADDR=0.0.0.0:8090\n' >"$CONFIG_FILE"
eq "$(health_url)" http://127.0.0.1:8090/api/health "health_url wildcard"
: >"$CONFIG_FILE"
eq "$(health_url)" http://127.0.0.1:8090/api/health "health_url default"
eq "$(data_dir)" /var/lib/rewind/data "data_dir default is relative to the working directory"
printf 'REWIND_DATA_DIR=/srv/data/\n' >"$CONFIG_FILE"
eq "$(data_dir)" /srv/data "data_dir absolute"
eq "$(CONFIG_FILE="$tmp/none.env" data_dir)" /var/lib/rewind "data_dir without a configuration file"

mode_of() { # CONFIG-LINES -> CUR_NET_MODE/CUR_PORT/CUR_IP/CUR_TS_NAME
	printf '%s\n' "$@" >"$CONFIG_FILE"
	load_current
	echo "$CUR_NET_MODE/$CUR_PORT/$CUR_IP/$CUR_TS_NAME"
}
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:8090')" "loopback/8090//" "mode loopback"
eq "$(mode_of '')" "loopback/8090//" "mode default"
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:9000' 'REWIND_TRUSTED_HOSTS=box.tail1.ts.net' 'REWIND_COOKIE_SECURE=true')" \
	"ts-https/9000//box.tail1.ts.net" "mode tailscale https"
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:8090,100.101.102.103:8090' 'REWIND_REMOTE_ACCESS=true')" \
	"ts-ip/8090/100.101.102.103/" "mode tailscale ip"
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:8090,192.168.1.20:8090' 'REWIND_REMOTE_ACCESS=true')" \
	"lan/8090/192.168.1.20/" "mode lan"
eq "$(mode_of 'REWIND_ADDR=0.0.0.0:8090' 'REWIND_REMOTE_ACCESS=true' 'REWIND_LISTEN_ALL_INTERFACES=true')" \
	"custom/8090//" "mode all interfaces is custom"
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:8090,192.168.1.20:8091' 'REWIND_REMOTE_ACCESS=true')" \
	"custom/8090//" "mode with different ports is custom"
eq "$(mode_of 'REWIND_ADDR=127.0.0.1:8090,8.8.8.8:8090' 'REWIND_REMOTE_ACCESS=true')" \
	"custom/8090//" "mode with a public address is custom"
printf 'REWIND_LIBRARY_ROOTS=/a/b/: /c d :\n' >"$CONFIG_FILE"
load_current
eq "${#CUR_ROOTS[@]}" 2 "two library roots"
eq "${CUR_ROOTS[0]}|${CUR_ROOTS[1]}" "/a/b|/c d" "roots trimmed and normalized"

# Settings written for each network mode.
W_ROOTS=("/srv/music" "/media/u/MY MUSIC" "/srv/100%")
W_ADVANCED=no
W_MOUNTS=yes
W_NET_MODE=lan W_PORT=8091 W_IP=192.168.1.20 W_TS_NAME=""
build_cfg_set
has "$CFG_SET" $'REWIND_ADDR=127.0.0.1:8091,192.168.1.20:8091\n' "lan address"
has "$CFG_SET" $'REWIND_REMOTE_ACCESS=true\n' "lan remote access"
has "$CFG_SET" $'REWIND_LISTEN_ALL_INTERFACES=false\n' "lan never all interfaces"
has "$CFG_SET" $'REWIND_COOKIE_SECURE=false\n' "lan cookie"
has "$CFG_SET" $'REWIND_LIBRARY_ROOTS=/srv/music:/media/u/MY MUSIC:/srv/100%\n' "roots joined with :"
hasnt "$CFG_SET" REWIND_LOG_LEVEL "advanced settings untouched unless chosen"
d=$(dropin_content "127.0.0.1:8091,192.168.1.20:8091")
has "$d" "# rewind-installer:" "drop-in marker"
has "$d" "Wants=network-online.target" "drop-in waits for the network"
hasnt "$d" tailscaled "no tailscaled ordering for a LAN address"
has "$d" "WantsMountsFor=/srv/music /srv/100%%" "drop-in mounts, % escaped"
has "$d" "contain spaces: /media/u/MY MUSIC" "path with spaces left out"
W_NET_MODE=ts-ip W_IP=100.101.102.103 W_TS_NAME=box.tail1.ts.net
build_cfg_set
has "$CFG_SET" $'REWIND_TRUSTED_HOSTS=box.tail1.ts.net\n' "ts-ip trusted host"
has "$(dropin_content "127.0.0.1:8091,100.101.102.103:8091")" "After=network-online.target tailscaled.service" "ts-ip ordering"
W_NET_MODE=ts-https
build_cfg_set
has "$CFG_SET" $'REWIND_ADDR=127.0.0.1:8091\n' "ts-https stays on loopback"
has "$CFG_SET" $'REWIND_COOKIE_SECURE=true\n' "ts-https secure cookie"
has "$CFG_SET" $'REWIND_REMOTE_ACCESS=false\n' "ts-https no remote access"
W_MOUNTS=no
eq "$(dropin_content "127.0.0.1:8091")" "" "no drop-in for loopback without mounts"
W_NET_MODE=keep
build_cfg_set
hasnt "$CFG_SET" REWIND_ADDR "keep leaves network settings alone"
W_ADVANCED=yes W_LOG_LEVEL=warn W_SCAN_ON_STARTUP=false W_SCAN_WORKERS=3 W_LIFETIME=24h W_IDLE=2h
build_cfg_set
has "$CFG_SET" $'REWIND_SCAN_WORKERS=3\n' "advanced settings written when chosen"

# --- installation record --------------------------------------------------------

CONFIG_DIR="$tmp/etc"
MANIFEST="$CONFIG_DIR/install-manifest"
MANIFEST_READ=""
TEMP_FILES=()
MANIFEST_PENDING=()
manifest_add "dir /queued created"
eq "${#MANIFEST_PENDING[@]}" 1 "manifest lines queued while the directory is missing"
mkdir "$CONFIG_DIR"
manifest_flush
eq "$(manifest_status dir /queued)" created "queued line written"
PRE=([/new]=0 [/old]=1 [/legacy]=1)
LEGACY=0
manifest_note dir /new /new
manifest_note dir /old /old
LEGACY=1
manifest_note dir /legacy /legacy
manifest_note dir /new /old
eq "$(manifest_status dir /new)" created "absent before: created"
eq "$(manifest_status dir /old)" existing "present before: existing"
eq "$(manifest_status dir /legacy)" adopted "older installation: adopted"
eq "$(manifest_status dir /other)" unrecorded "unknown item"
eq "$(grep -c ' /new ' "$MANIFEST")" 1 "a record is never rewritten"
yes_ is_managed dir /new
yes_ is_managed dir /legacy
yes_ is_managed dir /other
no_ is_managed dir /old
printf 'dir /evil created; rm -rf /\nfile /x created extra\n' >>"$MANIFEST"
eq "$(manifest_status dir /evil)" unrecorded "malformed lines are ignored"
manifest_drop dir /new
eq "$(manifest_status dir /new)" unrecorded "manifest_drop"
printf 'binary-content' >"$tmp/bin"
manifest_binary_set v1.0.0 "$tmp/bin"
manifest_binary_set v1.0.0 "$tmp/bin"
eq "$(manifest_binary_get v1.0.0)" "$(sha256sum "$tmp/bin" | awk '{print $1}')" "binary checksum recorded"
eq "$(grep -c '^binary v1.0.0 ' "$MANIFEST")" 1 "binary recorded once"
eq "$(find "$CONFIG_DIR" -name '.install-manifest.*' | wc -l)" 0 "no temporary files left"
(check_manifest_trust) 2>"$tmp/trust.err"
eq $? 1 "a manifest not owned by root is refused"
has "$(cat "$tmp/trust.err")" "not owned by root" "trust error message"

# --- uninstall planning on a scratch tree ----------------------------------------

SERVICE_USER=$(id -un)
STATE_DIR="$tmp/state"
CONF_DATA_DIR=$STATE_DIR
MANIFEST_READ="$tmp/empty-manifest"
: >"$MANIFEST_READ"
UN_ROOTS=("$tmp/music")
mkdir -p "$STATE_DIR" "$tmp/music" "$tmp/outside"
printf 'db' >"$STATE_DIR/rewind.db"
printf 'code' >"$STATE_DIR/setup-code"
printf 'mine' >"$STATE_DIR/notes.txt"
printf 'precious' >"$tmp/outside/rewind.db-journal"
ln -s "$tmp/outside/rewind.db-journal" "$STATE_DIR/rewind.db-journal"
printf 'song' >"$tmp/music/song.mp3"
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
plan=$(printf '%s\n' "${PLAN_REMOVE[@]}")
keep=$(printf '%s\n' "${PLAN_KEEP[@]}")
has "$plan" "$STATE_DIR/rewind.db" "plan removes the database"
has "$plan" "$STATE_DIR/setup-code" "plan removes the setup code"
hasnt "$plan" notes.txt "plan keeps unknown files"
hasnt "$plan" "rewind.db-journal" "plan keeps a symlink named like a data file"
has "$keep" notes.txt "unknown file listed as kept"
eq "$DATA_REMAINS" 1 "data directory remains"
REMOVED=() KEPT=() FAILURES=0
un_data apply
no_ test -e "$STATE_DIR/rewind.db"
no_ test -e "$STATE_DIR/setup-code"
yes_ test -f "$STATE_DIR/notes.txt"
yes_ test -f "$tmp/outside/rewind.db-journal"
yes_ test -L "$STATE_DIR/rewind.db-journal"
yes_ test -f "$tmp/music/song.mp3"
eq "$DATA_REMAINS" 1 "data directory still there"
rm "$STATE_DIR/notes.txt" "$STATE_DIR/rewind.db-journal"
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
has "$(printf '%s\n' "${PLAN_REMOVE[@]}")" "$STATE_DIR" "empty data directory removed"
eq "$DATA_REMAINS" 0 "nothing remains"
UN_ROOTS=("$tmp")
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
eq "${#PLAN_REMOVE[@]}" 0 "nothing removed when a music folder contains the data directory"
CONF_DATA_DIR=/srv/elsewhere
UN_ROOTS=()
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
has "$(printf '%s\n' "${PLAN_KEEP[@]}")" "/srv/elsewhere" "a data directory elsewhere is never removed"
mv "$STATE_DIR" "$tmp/state-real"
ln -s "$tmp/state-real" "$STATE_DIR"
CONF_DATA_DIR=$STATE_DIR
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
eq "${#PLAN_REMOVE[@]}" 0 "a symlinked data directory is not followed"

# The artwork cache: only its renditions and temporary files are removed.
STATE_DIR="$tmp/state-art"
CONF_DATA_DIR=$STATE_DIR
art="$STATE_DIR/artwork"
mkdir -p "$art"
printf 'db' >"$STATE_DIR/rewind.db"
hash=0123456789abcdef0123456789abcdef
printf 'jpg' >"$art/$hash-small.jpg"
printf 'jpg' >"$art/$hash-large.jpg"
printf 'tmp' >"$art/.tmp-12345"
printf 'mine' >"$art/my-cover.jpg"
PLAN_REMOVE=() PLAN_KEEP=()
un_data plan
plan=$(printf '%s\n' "${PLAN_REMOVE[@]}")
keep=$(printf '%s\n' "${PLAN_KEEP[@]}")
has "$plan" "$art/$hash-small.jpg" "plan removes artwork renditions"
has "$plan" "$art/.tmp-12345" "plan removes artwork temporary files"
has "$keep" "$art/my-cover.jpg" "an unknown file in the artwork cache is kept"
REMOVED=() KEPT=() FAILURES=0
un_data apply
no_ test -e "$art/$hash-small.jpg"
yes_ test -f "$art/my-cover.jpg"
yes_ test -d "$art"
eq "$DATA_REMAINS" 1 "data directory kept around the unknown file"
rm "$art/my-cover.jpg"
printf 'jpg' >"$art/$hash-large.jpg"
REMOVED=() KEPT=() FAILURES=0
un_data apply
no_ test -e "$art"
no_ test -e "$STATE_DIR"
eq "$DATA_REMAINS" 0 "a data directory holding only the artwork cache is removed completely"

for n in rewind-20261014T000000Z-before-v1.2.0.db rewind.env.20261014T000000Z-before-change; do yes_ backup_name_ok "$n"; done
for n in notes.txt rewind.db "rewind-../x.db" rewind-x.db.bak rewind.env "rewind.env.a b"; do no_ backup_name_ok "$n"; done

mkdir -p "$tmp/names"
touch "$tmp/names/a b" "$tmp/names/.hidden" "$tmp/names/x"$'\n'"y"
dir_entries "$tmp/names"
eq "${#ENTRIES[@]}" 3 "dir_entries handles spaces, dot files and newlines"

# --- prompts --------------------------------------------------------------------

WIZARD=1
r=$(printf '  answer  \n' | { ask v "Q" def; echo "[$v]"; })
has "$r" "[answer]" "ask trims"
r=$(printf '\n' | { ask v "Q" def; echo "[$v]"; })
has "$r" "[def]" "ask default"
r=$(printf 'maybe\nY\n' | { ask_yn "Q" n && echo YES; })
has "$r" "Please answer y or n" "ask_yn retries"
has "$r" YES "ask_yn yes"
r=$(printf '\n' | { ask_yn "Q" n || echo NO; })
has "$r" NO "ask_yn default no"
r=$(printf '9\n0\nx\n2\n' | { ask_choice c "Q" 1 3; echo "[$c]"; })
has "$r" "[2]" "ask_choice rejects out of range"
r=$(printf '\n' | { ask_choice c "Q" 3 3; echo "[$c]"; })
has "$r" "[3]" "ask_choice default"
r=$(printf 'purge\n' | { confirm_word PURGE "Type PURGE" && echo CONFIRMED; })
hasnt "$r" CONFIRMED "confirm_word is case-sensitive"
r=$(printf 'PURGE\n' | { confirm_word PURGE "Type PURGE" && echo CONFIRMED; })
has "$r" CONFIRMED "confirm_word"
(ask v "Q" </dev/null) >/dev/null 2>"$tmp/eof.err"
eq $? 1 "end of input stops the wizard"
has "$(cat "$tmp/eof.err")" "end of input" "end of input message"

# --- command line as a normal user --------------------------------------------------

run() { # EXPECTED-STATUS DESCRIPTION COMMAND...
	local want=$1 desc=$2 got
	shift 2
	"$@" >"$tmp/cli.out" 2>&1 </dev/null
	got=$?
	eq "$got" "$want" "$desc (exit status)"
}
run 0 "--help" bash "$installer" --help
has "$(cat "$tmp/cli.out")" "--uninstall" "--help lists --uninstall"
run 0 "--help through sh" sh "$installer" --help
has "$(cat "$tmp/cli.out")" "Rewind installer" "sh re-runs the script with bash"
run 2 "unknown option" bash "$installer" --frobnicate
run 2 "legacy install without an archive" bash "$installer" install
run 1 "wizard as a normal user" bash "$installer"
has "$(cat "$tmp/cli.out")" "sudo bash" "non-root hint"
run 1 "--check as a normal user" bash "$installer" --check
run 1 "--uninstall as a normal user" bash "$installer" --uninstall
run 1 "--release without a value" bash "$installer" --release
run 1 "--release with --archive" bash "$installer" --release v1.0.0 --archive /x.tar.gz
run 1 "--archive with --status" bash "$installer" --status --archive /x.tar.gz
run 1 "two actions" bash "$installer" --check --status
run 1 "uninstall.sh as a normal user" bash "$here/uninstall.sh"
has "$(cat "$tmp/cli.out")" "sudo bash" "uninstall.sh non-root hint"

echo "install-unit-test: $passed passed, $failed failed"
[ "$failed" -eq 0 ]
