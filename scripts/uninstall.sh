#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
#
# Uninstalls Rewind: runs the uninstall wizard of Rewind's installer, which
# shows what it found and offers to remove
#   1) the application only (keeps configuration, data and backups),
#   2) the application and its configuration (keeps data and backups), or
#   3) everything it created, after you type PURGE (backups only if you
#      say so separately).
# Music is never touched. See docs/install.md, "Uninstalling".
#
# Usage (as root):  sudo bash uninstall.sh
#
# The wizard lives in install.sh. This script runs the installed copy,
# /usr/local/sbin/rewind-ctl, or else the install.sh next to this file.
set -eu

ctl=/usr/local/sbin/rewind-ctl
here=$(cd "$(dirname "$0")" && pwd)

is_installer() { [ -f "$1" ] && [ ! -L "$1" ] && grep -q '^# rewind-installer:' "$1"; }

if [ "$(id -u)" -ne 0 ]; then
	echo "Uninstalling changes system files and must run as root. Run:" >&2
	echo "  sudo bash $0" >&2
	exit 1
fi
if is_installer "$ctl" && [ "$(stat -c %u "$ctl")" = 0 ]; then
	exec bash "$ctl" --uninstall "$@"
fi
if is_installer "$here/install.sh"; then
	exec bash "$here/install.sh" --uninstall "$@"
fi
echo "uninstall: ERROR: Rewind's installer was not found: neither $ctl nor $here/install.sh." >&2
echo "  Download install.sh from the same release page into $here and run this again," >&2
echo "  or follow the manual steps in docs/install.md, \"Uninstalling\"." >&2
exit 1
