#!/bin/sh
# telegraph-boot.sh [ENV_FILE...]
#
# Starts telegraph-supervise.sh for each node that is not already running,
# for hosts with no systemd, launchd or cron @reboot (containers, sandboxes).
# Safe to run often, e.g. from a shell startup file, which is the only
# start hook some sandboxes have: a node whose supervisor is running is left
# alone, a lock stops two shells starting the same node, and it prints
# nothing unless something was started (-q: nothing at all).
#
# With no arguments it boots every ~/.config/telegraph/*.env that has a
# token (TELEGRAPH_BOTS, _BOTS_FILE or _TOKEN_ENV) and no TELEGRAPH_BOOT=0;
# list env files to boot only those. A supervisor counts as running when ps
# shows "telegraph-supervise.sh ENV_FILE" with the same absolute path. Each
# one runs detached, logging to $TELEGRAPH_HOME/NODE.log (or
# ~/.local/state/telegraph/NODE.log).
# TELEGRAPH_SUPERVISE overrides the supervisor path (default: alongside).
#
# Shell hook (end of ~/.bashrc):
#   [ -r ~/telegraph/scripts/telegraph-boot.sh ] && sh ~/telegraph/scripts/telegraph-boot.sh -q
set -eu

quiet=
[ "${1:-}" = -q ] && { quiet=1; shift; }
say() { [ -n "$quiet" ] || echo "telegraph-boot: $*"; }

# Do not hand a parent shell's pipes to long-lived daemons.
if [ -d /proc/$$/fd ]; then
	for fd in "/proc/$$/fd"/*; do
		fd=${fd##*/}
		case $fd in
		[3-9]) eval "exec $fd>&-" 2>/dev/null || : ;;
		esac
	done
fi

here=$(cd "$(dirname "$0")" && pwd)
supervise=${TELEGRAPH_SUPERVISE:-$here/telegraph-supervise.sh}
cfg="${XDG_CONFIG_HOME:-$HOME/.config}/telegraph"
state="${XDG_STATE_HOME:-$HOME/.local/state}/telegraph"
[ -r "$supervise" ] || { echo "telegraph-boot: no supervisor at $supervise" >&2; exit 1; }

if [ $# -eq 0 ]; then
	set -- "$cfg"/*.env
	[ -e "$1" ] || exit 0
fi

umask 077
mkdir -p "$state"
lock="$state/boot.lock"
if ! mkdir "$lock" 2>/dev/null; then
	# Another run holds it; take it over only if that run is gone.
	holder=$(cat "$lock/pid" 2>/dev/null || true)
	if [ -n "$holder" ] && kill -0 "$holder" 2>/dev/null; then exit 0; fi
	rm -rf "$lock"
	mkdir "$lock" 2>/dev/null || exit 0
fi
echo $$ >"$lock/pid"
trap 'rm -rf "$lock"' EXIT INT TERM

# value KEY FILE: the last KEY=value in FILE, read without sourcing it.
value() { sed -n "s/^$1=//p" "$2" | tail -n 1 | sed -e 's/^["'\'']//' -e 's/["'\'']$//'; }

running() { # running ENV_FILE: is a supervisor for it alive?
	ps -eo args= 2>/dev/null | awk -v e="$1" '
		{ for (i = 1; i < NF; i++) if ($i ~ /telegraph-supervise\.sh$/ && $(i + 1) == e) f = 1 }
		END { exit !f }'
}

for envfile in "$@"; do
	case $envfile in /*) ;; *) envfile="$PWD/$envfile" ;; esac
	node=$(basename "$envfile" .env)
	[ -r "$envfile" ] || { echo "telegraph-boot: cannot read $envfile" >&2; continue; }
	[ "$(value TELEGRAPH_BOOT "$envfile")" = 0 ] && continue
	# Needs a token: never read into this script, only tested for.
	grep -Eq '^TELEGRAPH_(BOTS|BOTS_FILE|TOKEN_ENV)=.' "$envfile" || continue
	running "$envfile" && continue
	home=$(value TELEGRAPH_HOME "$envfile")
	# shellcheck disable=SC2088 # a literal ~/ in the env file
	case $home in '~/'*) home="$HOME/${home#??}" ;; esac
	if [ -n "$home" ] && [ -d "$home" ]; then log="$home/$node.log"; else log="$state/$node.log"; fi
	if command -v setsid >/dev/null 2>&1; then
		nohup setsid /bin/sh "$supervise" "$envfile" >>"$log" 2>&1 </dev/null &
	else
		nohup /bin/sh "$supervise" "$envfile" >>"$log" 2>&1 </dev/null &
	fi
	# Wait until ps shows it, so a run right after this one sees it too.
	n=0
	until running "$envfile" || [ "$n" -ge 5 ]; do sleep 1; n=$((n + 1)); done
	say "started $node (log $log)"
done
