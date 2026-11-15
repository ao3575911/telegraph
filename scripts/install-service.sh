#!/bin/sh
# install-service.sh [NODE]   (default NODE=node1)
#
# Installs a Telegraph node as a user service that restarts on failure and
# comes back after a reboot. No sudo. Uses systemd --user on Linux,
# launchd on macOS. Settings come from ~/.config/telegraph/NODE.env,
# created from a template (mode 0600) if missing: fill in the token there.
# The binary is TELEGRAPH_BIN from NODE.env if set, else telegraph on PATH,
# else ~/go/bin/telegraph; the systemd unit reads TELEGRAPH_BIN per node.
set -eu

node=${1:-node1}
case $node in *[!a-zA-Z0-9_-]*|'') echo "node name: letters, digits, _ and - only" >&2; exit 2 ;; esac
here=$(cd "$(dirname "$0")" && pwd)
cfg="${XDG_CONFIG_HOME:-$HOME/.config}/telegraph"
envfile="$cfg/$node.env"

umask 077
mkdir -p "$cfg"
if [ ! -f "$envfile" ]; then
	cat > "$envfile" <<ENV
# Telegraph node "$node". Keep this file private (chmod 600).
TELEGRAPH_HOME=$HOME/.telegraph-$node
TELEGRAPH_NODE_NAME=$node
# Bot token from @BotFather (never commit or share it)
TELEGRAPH_BOTS=
TELEGRAPH_CHANNELS=-1004305320319
TELEGRAPH_CHANNEL_URL=https://t.me/tgraph_net
# Other committee nodes' public keys (telegraph config shows yours), comma-separated
TELEGRAPH_PEERS=
# Required: the network's committee keys, the same list on every node
TELEGRAPH_COMMITTEE=
TELEGRAPH_BOT_REGISTER=0
# With registration off: the bot users are sent to for /register and /card
TELEGRAPH_REGISTER_BOT=@dsmdioefncoedcbot
TELEGRAPH_LOG_FORMAT=text
ENV
	echo "wrote $envfile: add the bot token, then run this script again"
	exit 0
fi
chmod 600 "$envfile"
grep -q '^TELEGRAPH_BOTS=.\+' "$envfile" || grep -q '^TELEGRAPH_BOTS_FILE=.\+' "$envfile" ||
	{ echo "$envfile has no TELEGRAPH_BOTS token yet" >&2; exit 1; }
# TELEGRAPH_BIN only; the subshell keeps the token out of this script.
envbin=$(
	set -a
	# shellcheck disable=SC1090
	. "$envfile" >/dev/null 2>&1
	printf %s "${TELEGRAPH_BIN:-}"
)
defbin=$(command -v telegraph || echo "$HOME/go/bin/telegraph")
bin=${envbin:-$defbin}
[ -x "$bin" ] || { echo "telegraph not found at $bin: set TELEGRAPH_BIN in $envfile or go install github.com/ao3575911/telegraph/cmd/telegraph@latest" >&2; exit 1; }

case $(uname -s) in
Darwin)
	label="com.ao3575911.telegraph.$node"
	plist="$HOME/Library/LaunchAgents/$label.plist"
	mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
	sed -e "s|@NODE@|$node|g" -e "s|@SUPERVISE@|$here/telegraph-supervise.sh|g" \
		-e "s|@ENVFILE@|$envfile|g" -e "s|@LOG@|$HOME/Library/Logs/telegraph-$node.log|g" \
		"$here/service/com.ao3575911.telegraph.plist" > "$plist"
	launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
	launchctl bootstrap "gui/$(id -u)" "$plist"
	echo "installed $label; logs: tail -f ~/Library/Logs/telegraph-$node.log"
	;;
*)
	if ! systemctl --user show-environment >/dev/null 2>&1; then
		echo "no systemd user session here. Run the supervisor instead, e.g. from cron:" >&2
		echo "  @reboot /bin/sh $here/telegraph-supervise.sh $envfile >>$HOME/telegraph-$node.log 2>&1" >&2
		exit 1
	fi
	unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
	mkdir -p "$unit"
	sed "s|%h/go/bin/telegraph|$defbin|" "$here/service/telegraph@.service" > "$unit/telegraph@.service"
	systemctl --user daemon-reload
	systemctl --user enable --now "telegraph@$node"
	user=${USER:-$(id -un)}
	if [ "$(loginctl show-user "$user" -p Linger --value 2>/dev/null)" != yes ]; then
		loginctl enable-linger "$user" 2>/dev/null ||
			echo "note: run 'loginctl enable-linger $user' (may need an admin) so the node starts at boot without a login"
	fi
	echo "installed telegraph@$node"
	echo "  status: systemctl --user status telegraph@$node"
	echo "  logs:   journalctl --user -u telegraph@$node -f"
	;;
esac
