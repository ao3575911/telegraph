# Running Telegraph nodes

How to keep a serving node up, run several nodes, and add a node to the
checkpoint committee. Everything here runs as your user: no sudo.

## Settings

A node reads its settings from the environment. Under a service they come
from one file per node, `~/.config/telegraph/<node>.env`, mode 0600:

```sh
TELEGRAPH_HOME=/home/you/.telegraph-node1   # identity.key + data.json (the node key)
TELEGRAPH_NODE_NAME=node1                   # added to every log record
TELEGRAPH_BOTS=<token from @BotFather>      # this node's own bot; never share it
TELEGRAPH_CHANNELS=-1004305320319           # gossip channel (@tgraph_net)
TELEGRAPH_CHANNEL_URL=https://t.me/tgraph_net
TELEGRAPH_PEERS=<hex key of node2>,<hex key of node3>
TELEGRAPH_BOT_REGISTER=1                    # 1 on the node that registers for Telegram users
# TELEGRAPH_REGISTER_BOT=@dsmdioefncoedcbot # on other nodes: where /register sends users
TELEGRAPH_LOG_FORMAT=text                   # or json
```

| Variable | Purpose |
|---|---|
| `TELEGRAPH_HOME` | Node directory. Its `identity.key` is the node key; keep it backed up and private. |
| `TELEGRAPH_BOTS` | Bot token(s), comma-separated. Each node uses its own bot. |
| `TELEGRAPH_BOTS_FILE` | Read the token(s) from this file instead (must be mode 0600). |
| `TELEGRAPH_TOKEN_ENV` | Name of the variable that holds the token, for nodes sharing one environment, e.g. `TELEGRAPH_TOKEN_ENV=NODE2_BOT_TOKEN`. |
| `TELEGRAPH_CHANNELS` | Channel id(s) or `@username`(s) to gossip on. All nodes of a network share them. |
| `TELEGRAPH_CHANNEL_URL` | Public link on the bot's Channel button and description. Invite links (`t.me/+…`) are refused. |
| `TELEGRAPH_PEERS` | The other committee nodes' public keys (`telegraph config key` on each). Used by `telegraph committee` and startup logs; membership itself is decided by the protocol (below). |
| `TELEGRAPH_BOT_REGISTER` | `1` lets Telegram users `/register` through this node (custodial). |
| `TELEGRAPH_REGISTER_BOT` | On a node with registration off: the bot (`@username`) that `/register`, `/card`, the command menu and the bot description point users to, e.g. `@dsmdioefncoedcbot` (node 1). Ignored when `TELEGRAPH_BOT_REGISTER=1`. Same as `--register-bot`. |
| `TELEGRAPH_LOG_FORMAT`, `TELEGRAPH_LOG_LEVEL` | `text`/`json`; `debug`/`info`/`warn`/`error`. Logs go to stderr (the journal under systemd). |

`telegraph config` prints the effective settings without the token.
Never pass the token as `--bots`: flags show up in the process list.

## Keep a node running

### Linux (systemd user service)

```sh
go install github.com/ao3575911/telegraph/cmd/telegraph@latest   # or the release script
sh scripts/install-service.sh node1     # first run writes ~/.config/telegraph/node1.env
$EDITOR ~/.config/telegraph/node1.env   # paste the token, check the other values
sh scripts/install-service.sh node1     # installs telegraph@.service, enables and starts it
```

| Task | Command |
|---|---|
| Status | `systemctl --user status telegraph@node1` |
| Logs | `journalctl --user -u telegraph@node1 -f` |
| Restart after an upgrade | `systemctl --user restart telegraph@node1` |
| Stop | `systemctl --user stop telegraph@node1` |

The unit (`scripts/service/telegraph@.service`) restarts the node whenever
it exits, backing off from 5 s to 5 min on repeated failures (systemd 254+),
and never gives up. To start at boot without anyone logged in, the script
runs `loginctl enable-linger $USER`; if your system needs an admin for
that, ask once.

### macOS (launchd)

The same `sh scripts/install-service.sh node1` writes
`~/Library/LaunchAgents/com.ao3575911.telegraph.node1.plist`, which runs
the supervisor below at login and keeps it alive. Logs:
`~/Library/Logs/telegraph-node1.log`.

### Anything else (containers, no systemd)

`scripts/telegraph-supervise.sh ~/.config/telegraph/node1.env` sources the
env file (refusing one that others can read), runs the node and restarts
it with backoff (5 s doubling to 5 min, reset after an hour up). Start it
at boot with whatever the host offers, for example cron:

```cron
@reboot /bin/sh /path/to/telegraph/scripts/telegraph-supervise.sh /home/you/.config/telegraph/node1.env >>/home/you/telegraph-node1.log 2>&1
```

### What the node does on failure

- Telegram timeouts, proxy hangs, 5xx and 429s: the poll backs off
  exponentially (1 s doubling to 30 s with jitter, or Telegram's
  `retry_after`), drops idle connections and carries on. Unsent entries stay
  pending and are retried.
- SIGTERM/SIGINT: the long poll is aborted, the store is saved, exit 0.
- A panic in a bot command is caught, logged and answered with a generic
  error; the node keeps serving.
- `data.json` is written atomically (temp file, fsync, rename).

Back up `TELEGRAPH_HOME` (`identity.key` and `data.json`) before upgrades.

## The checkpoint committee

Checkpoints need signatures from a committee: the three **issuers** (keys
that have signed a register or other non-heartbeat entry) closest to a
target derived from the log. With two issuers the committee has two members
and both must sign; from three on, any two of the three. Membership cannot
be configured, only earned: a node becomes an issuer by registering a
handle with its own key. A committee member co-signs pending checkpoints
automatically while serving.

`telegraph committee` shows the current committee, which member is this
node or a configured peer, and warns when this node or a peer is not an
issuer yet. Issuers that never serve (for example a laptop that only
registered a handle) can be picked and then never sign; the fallback
members issue after 1 h and 2 h, and two online members are enough.

## Runbook: add a second (or third) committee node

You need: a new bot from @BotFather owned by the new node's operator, the
network pin (`/pin` in the bot, or `telegraph pin` on node 1), and a name
for the node's own handle.

1. **Bot.** Create the bot, add it to the gossip channel (@tgraph_net) as
   an administrator allowed to post. Keep the token private.
2. **Install** telegraph on the new machine (same version as node 1).
3. **Env file.** `sh scripts/node-join.sh node2 <pin> node2` writes
   `~/.config/telegraph/node2.env` and stops. Edit it: set `TELEGRAPH_BOTS`
   to the new token, `TELEGRAPH_PEERS` to node 1's key (`telegraph config key`
   on node 1), keep `TELEGRAPH_BOT_REGISTER=0` unless this node should also
   register for Telegram users, and set `TELEGRAPH_REGISTER_BOT` to node 1's
   bot so `/register` on this bot points users there.
4. **Join.** Run `sh scripts/node-join.sh node2 <pin> node2 [did]` again. It
   initialises the key, bootstraps the log from node 1 (accepting only
   history that leads to the pin), registers `node2@nym` with the node key
   (making it an issuer), publishes, shows `telegraph committee`, prints the
   node key, and installs the service.
5. **Peers.** Add the printed key to `TELEGRAPH_PEERS` on node 1 (and every
   other node), then restart them (`systemctl --user restart telegraph@node1`,
   or restart the supervisor).
6. **Check.** After the next checkpoint (leader due 6 h after the oldest
   unchecked entry), `/status` shows `signatures 2 of 2` (or `2 of 3`), and
   `telegraph committee` on either node lists both.

Two nodes can run on one machine: give each its own env file, home and
token, and `install-service.sh node1` / `install-service.sh node2`.

## Upgrading

0.3.0 changes how handles resolve (first-come ownership, #15). Upgrade
every node: a 0.2.x node still resolves last-writer-wins and can disagree
with 0.3.0 nodes about a handle that two keys registered.

```sh
cp -a "$TELEGRAPH_HOME" "$TELEGRAPH_HOME.bak-$(date +%Y%m%d-%H%M)"
go install github.com/ao3575911/telegraph/cmd/telegraph@v0.3.0
systemctl --user restart telegraph@node1
```
