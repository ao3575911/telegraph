#!/bin/sh
# telegraph-supervise.sh ENV_FILE
#
# Runs `telegraph sync --serve` with the settings in ENV_FILE and restarts
# it with exponential backoff (5 s doubling to 5 min, reset after an hour
# of healthy running). For hosts without systemd (containers, launchd);
# start it at boot with launchd, or cron:
#   @reboot /bin/sh /path/to/telegraph-supervise.sh ~/.config/telegraph/node1.env
# SIGTERM/SIGINT stop the node cleanly (it saves its store) and exit.
set -eu

envfile=${1:?usage: telegraph-supervise.sh ENV_FILE}
[ -r "$envfile" ] || { echo "telegraph-supervise: cannot read $envfile" >&2; exit 2; }
case $(ls -l "$envfile") in
-???------*) ;;
*) echo "telegraph-supervise: $envfile must not be group/world readable (chmod 600)" >&2; exit 2 ;;
esac

set -a
# shellcheck disable=SC1090
. "$envfile"
set +a
export GODEBUG="${GODEBUG:-http2client=0}"
bin=${TELEGRAPH_BIN:-$(command -v telegraph || echo "$HOME/go/bin/telegraph")}

child=
stop() {
	[ -n "$child" ] && kill -TERM "$child" 2>/dev/null && wait "$child" 2>/dev/null
	exit 0
}
trap stop TERM INT

delay=5
while :; do
	started=$(date +%s)
	"$bin" sync --serve &
	child=$!
	set +e
	wait "$child"
	code=$?
	set -e
	child=
	if [ $(( $(date +%s) - started )) -ge 3600 ]; then delay=5; fi
	echo "telegraph-supervise: node exited ($code), restarting in ${delay}s" >&2
	sleep "$delay"
	delay=$((delay * 2))
	[ "$delay" -gt 300 ] && delay=300
done
