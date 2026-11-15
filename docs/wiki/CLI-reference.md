# CLI reference

```
telegraph <command> [flags]
```

| Command | Flags | Does |
|---|---|---|
| `init` | | Create the identity key and local state in `TELEGRAPH_HOME` |
| `register` | `--handle H --did D` | Claim a free handle for a DID, or change the DID of one you own (refused when another key owns it) |
| `revoke` | `--handle H` | Revoke a handle you own (it becomes free) |
| `resolve H` | | Handle → DID, owner and state |
| `verify H` | | `resolve` plus checkpoint attestation |
| `audit` | | Check signatures, chains and the latest checkpoint |
| `sync` | `--secs N` (5), `--serve`, `--pin P`, `--genesis`, `--bots`, `--channels`, `--bus`, `--checkpoint-only`, `--bot-register` | Publish pending entries and merge gossip; `--genesis` starts or joins a network with no checkpoint yet; `--bot-register` lets Telegram users `/register` handles signed by this node |
| `checkpoint` | | Issue a checkpoint if this node leads the committee, or as fallback #1/#2 when the leader is overdue |
| `pin` | `--rotate` | Show the trust root, or advance it |
| `peers` | | Known peers and liveness |
| `net` | | Network health |
| `card` | `--handle H` | Print the bio card line |
| `attest` | `--line L` | Sign a card line |
| `scan L` | | Verify a card line |
| `swap offer` | `--gives H --wants H [--hours 24]` | Offer a swap and lock your handle |
| `swap accept` | `--sid ID` | Lock your handle to the initiator |
| `swap claim` | `--sid ID` | Take the handle locked to you |
| `swap reclaim` | `--sid ID` | Recover your handle after expiry |
| `swap status` | `--sid ID` | Show locks, claims and reclaims |
| `committee` | | The next checkpoint committee (leader, fallbacks), which are this node or a configured peer, and their liveness |
| `config` | `[key]` | Effective node settings without secrets; `config key` prints this node's public key |
| `version` | | Print the version |

## Environment

| Variable | |
|---|---|
| `TELEGRAPH_HOME` | Node state directory (default `~/.telegraph`) |
| `TELEGRAPH_BOTS` | Comma-separated Telegram bot tokens |
| `TELEGRAPH_BOTS_FILE` | File holding the token(s) instead, mode 0600 |
| `TELEGRAPH_TOKEN_ENV` | Name of the variable holding the token(s), when several nodes share one environment (default `TELEGRAPH_BOTS`) |
| `TELEGRAPH_CHANNEL_URL` | Public channel link on bot buttons (default `https://t.me/tgraph_net`; invite links are refused) |
| `TELEGRAPH_PEERS` | Hex public keys of the other committee nodes, for `committee` and logs |
| `TELEGRAPH_NODE_NAME` | Label added to every log record |
| `TELEGRAPH_LOG_FORMAT` · `TELEGRAPH_LOG_LEVEL` | `text` (default) or `json` · `debug`, `info`, `warn`, `error` |
| `TELEGRAPH_CHANNELS` | Comma-separated channels, e.g. `@chan1,@chan2` |
| `TELEGRAPH_PIN` | Checkpoint hash to bootstrap from |
| `TELEGRAPH_BUS` | Local file bus path, used instead of Telegram |
| `TELEGRAPH_BOT_REGISTER` | `1` turns on the bot's `/register` (same as `--bot-register`) |
| `NO_COLOR` | Disable colour |
| `CLICOLOR_FORCE` | Force colour when stdout is not a terminal |

## Bot commands

A node running `sync --serve` over Telegram answers people in direct
messages, and in groups when a command is addressed to it
(`/status@botname`). The same list appears in Telegram's command menu.

| Command | Reply |
|---|---|
| `/register name [did]` | Register `name@nym`, signed by the node (custodial, DM only, node needs `--bot-register`). A first registration also shows your handle number and a badge if you are in the first 1000 |
| `/card [name]` | Your bio line `⟁ name@nym · 1a2b3c4d` as tap-to-copy code, attested by the node |
| `/resolve handle@nym` | Owner, DID, status and checkpoint attestation (without a handle, it asks for one) |
| `/revoke name` | Release a handle you hold through the bot |
| `/status` | Entries, handles, latest checkpoint and committee signatures |
| `/stats` | Network counts (active, revoked and ever-registered handles, registrars, nodes online, swaps, checkpoints) and how many Founding 100 / Early adopter spots are left |
| `/pin` | The network's trust pin |
| `/about` | What Telegraph is and how it works |
| `/version` | Node software version |
| `/help` | Every command |
| `/start` | Welcome, with Channel (`t.me/tgraph_net`), GitHub, Wiki and Releases buttons |

A handle sent on its own is looked up too. Details:
[TELEGRAM.md](https://github.com/ao3575911/telegraph/blob/main/docs/TELEGRAM.md).

Exit status is 0 on success and 1 on any error; errors print as
`✗ telegraph: …` on stderr.
