#!/bin/sh
# node-join.sh NODE PIN HANDLE [DID]
#
# Onboards a new committee node onto an existing Telegraph network:
#   1. creates ~/.config/telegraph/NODE.env (mode 0600) if missing and stops
#      so you can paste the node's own bot token into it;
#   2. initialises the node's home and key;
#   3. bootstraps the log from peers, trusting only history that leads to PIN;
#   4. registers HANDLE with the node's key, which makes the node an issuer
#      and so eligible for the checkpoint committee;
#   5. publishes it and prints the public key to add to every peer's
#      TELEGRAPH_PEERS;
#   6. installs the restart-on-failure service (scripts/install-service.sh).
#
# Example: sh scripts/node-join.sh node2 c700daaa…0080 node2 did:web:example.com
# Safe to re-run: finished steps are skipped. Docs: docs/NODE-OPERATIONS.md
set -eu

node=${1:?usage: node-join.sh NODE PIN HANDLE [DID]}
pin=${2:?usage: node-join.sh NODE PIN HANDLE [DID]}
name=${3:?usage: node-join.sh NODE PIN HANDLE [DID]}
case $name in *@*) handle=$name ;; *) handle="$name@nym" ;; esac
did=${4:-did:web:$name.invalid}
here=$(cd "$(dirname "$0")" && pwd)
envfile="${XDG_CONFIG_HOME:-$HOME/.config}/telegraph/$node.env"
bin=$(command -v telegraph || echo "$HOME/go/bin/telegraph")
[ -x "$bin" ] || { echo "telegraph not found: go install github.com/ao3575911/telegraph/cmd/telegraph@latest" >&2; exit 1; }
case $pin in *[!0-9a-f]*) echo "PIN must be the 64-hex checkpoint hash" >&2; exit 2 ;; esac
[ ${#pin} -eq 64 ] || { echo "PIN must be the 64-hex checkpoint hash" >&2; exit 2; }

if [ ! -f "$envfile" ]; then
	sh "$here/install-service.sh" "$node" # writes the template, then exits
	exit 0
fi
set -a
# shellcheck disable=SC1090
. "$envfile"
set +a
export GODEBUG="${GODEBUG:-http2client=0}"
[ -n "${TELEGRAPH_BOTS:-}${TELEGRAPH_BOTS_FILE:-}" ] || { echo "add the bot token to $envfile first" >&2; exit 1; }

"$bin" init >/dev/null
if ! grep -q '"entries":\[{' "${TELEGRAPH_HOME:?set TELEGRAPH_HOME in $envfile}/data.json"; then
	echo "== bootstrapping from peers (pin ${pin%"${pin#????????}"}…)"
	"$bin" sync --pin "$pin" --secs 60
fi
# resolve exits non-zero for an unregistered handle (after 0.3.0); the grep also
# covers older binaries, which printed "unregistered" and exited 0.
if ! "$bin" resolve "$handle" 2>/dev/null | grep -q '→'; then
	echo "== registering $handle with this node's key"
	"$bin" register --handle "$handle" --did "$did"
fi
echo "== publishing"
"$bin" sync --secs 10
"$bin" committee || true
echo
echo "Node key (add to TELEGRAPH_PEERS on every other node, comma-separated):"
"$bin" config key
echo
sh "$here/install-service.sh" "$node"
